// Package mode is the server core and the transports on top of it. A Server
// owns one session, agent, tool registry, permission policy, and compaction
// settings, and implements the protocol commands. Print, JSON, and RPC are
// thin transports over the same server.
package mode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/bundled"
	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/usage"
)

// extShutdownTimeout bounds how long Close waits for extensions to stop.
const extShutdownTimeout = 5 * time.Second

// Clock is the time seam, so tests can drive time with a fake clock.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// realClock is the wall-clock Clock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Options configures a Server.
type Options struct {
	Config *config.Config
	// Reload re-reads the configuration for the reload command, applying the same
	// files, environment and flags as the first load. It returns warnings to pass
	// on. nil means reload leaves the configuration as it is.
	Reload    func() (*config.Config, []string, error)
	CWD       string
	Home      string
	Provider  provider.Provider // nil means build OpenRouter from the config and auth
	Session   string            // path to open or create; "" means a new default
	NoSession bool              // keep the transcript in memory only
	Clock     Clock
	Version   string
	Mode      string   // tui | print | json | rpc | serve, for extensions
	Tools     []string // explicit active tool set; nil means use the config
	NoTools   bool     // register no tools at all

	// ExtraTools are added to the tool pool before the active set is resolved,
	// so a caller (the GitHub Action) can register mode-specific tools. A
	// non-empty SystemPrompt replaces the assembled prompt.
	ExtraTools   []tool.Tool
	SystemPrompt string
	// Extra is passed to bundled extensions through ext.Env.Extra. The TUI
	// puts its widget registry under "tui".
	Extra map[string]any
}

// subscriber is one event consumer.
type subscriber struct {
	ch  chan proto.Event
	seq int64
}

// Server owns the session and implements the protocol commands.
type Server struct {
	opt       Options
	cfg       *config.Config
	cwd, home string
	clock     Clock

	prov      provider.Provider
	usesAuth  bool
	tools     *tool.Registry
	pool      map[string]tool.Tool
	active    []string
	policy    permission.Policy
	agent     *agent.Agent
	store     *session.Store
	compactor *compaction.Compactor
	system    string
	ext       *ext.Host
	ledger    *usage.Ledger

	// loaded is what the last config load read, to tell it from a runtime override.
	loaded knobs

	mu      sync.Mutex
	subs    map[int64]*subscriber
	nextSub int64
	closed  bool
	model   string // wire slug
	think   string
	limit   int
	lastUse provider.Usage
	usage   provider.Usage
	mem     []message.Message // transcript when NoSession
	path    string            // session file path, "" when NoSession
	// attachments holds uploads from POST /v1/attachments, by id, until the
	// server closes.
	attachments map[string]proto.Attachment
	// attachmentBytes is the decoded size of everything in attachments.
	attachmentBytes int

	uiMu      sync.Mutex
	uiHandler func(context.Context, proto.UIRequest) (proto.UIResponse, error)
	uiPending map[string]chan proto.UIResponse
	uiCaps    map[string]bool
	uiNext    atomic.Int64

	// sessionAllow holds the per-server allow rules added by a "remember:
	// session" permission answer, tool name to true. They are never persisted.
	sessionAllow sync.Map

	// logins holds the interactive sign-ins in progress, one per provider id,
	// guarded by mu. loginWG counts their goroutines so Close can wait.
	logins  map[string]*loginFlow
	loginWG sync.WaitGroup

	// extStarted closes when the background extension start has finished.
	extStarted chan struct{}

	// life ends when the server closes. notifyCh feeds the one goroutine that
	// delivers fire-and-forget UI requests in order; notifying (guarded by mu)
	// records that it has started.
	life      context.Context
	stopLife  context.CancelFunc
	notifyCh  chan proto.UIRequest
	notifying bool

	// fallbackConv identifies the conversation when no session store is open
	// (print mode with no session), so a provider still sees one stable id.
	fallbackConv string
}

// New builds a server from the options. A missing API key is not an error; the
// first prompt reports it.
func New(opt Options) (*Server, error) {
	if opt.Config == nil {
		return nil, fmt.Errorf("mode: Options.Config is required")
	}
	if opt.CWD == "" {
		return nil, fmt.Errorf("mode: Options.CWD is required")
	}
	home := opt.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("mode: home directory: %w", err)
		}
		home = h
	}
	clock := opt.Clock
	if clock == nil {
		clock = realClock{}
	}
	cfg := opt.Config
	if cfg.Model == "" {
		cfg.Model = openrouter.DefaultModel
	}
	diag.SetModel(cfg.Model)
	s := &Server{
		opt:       opt,
		cfg:       cfg,
		cwd:       opt.CWD,
		home:      home,
		clock:     clock,
		subs:      map[int64]*subscriber{},
		model:     modelSlug(cfg.Model),
		think:     cfg.Thinking,
		limit:     cfg.ContextLimit,
		uiPending: map[string]chan proto.UIResponse{},
		ledger:    usage.NewLedger(provider.GetModel),
		logins:    map[string]*loginFlow{},

		extStarted: make(chan struct{}),
		notifyCh:   make(chan proto.UIRequest, notifyQueueSize),
	}
	s.life, s.stopLife = context.WithCancel(context.Background())
	s.fallbackConv = newConversationID()
	s.prov = opt.Provider
	if s.prov == nil {
		s.usesAuth = true
		s.prov = buildProvider(cfg, home, s.conversationID)
	}
	tools, pool, err := buildTools(cfg)
	if err != nil {
		return nil, err
	}
	s.tools = tools
	s.pool = pool
	for _, t := range opt.ExtraTools {
		s.pool[t.Info().Name] = t
	}
	s.active = s.activeTools(cfg)
	s.loaded = knobsOf(cfg)
	if err := registerTools(s.tools, s.pool, s.active); err != nil {
		return nil, err
	}
	s.policy = cfg.Permissions

	if !opt.NoSession {
		path := opt.Session
		if path == "" {
			path = defaultSessionPath(opt.CWD)
		}
		store, err := session.Open(path, session.Options{})
		if err != nil {
			return nil, fmt.Errorf("mode: open session: %w", err)
		}
		s.store = store
		s.path = path
	}
	s.compactor = &compaction.Compactor{
		Provider: s.prov,
		Model:    s.model,
		Settings: compactionSettings(cfg, cfg.Model),
	}
	s.system = s.systemPrompt()

	s.ext = s.buildExtensionHost()
	if !opt.NoTools {
		for _, b := range bundled.Enabled(cfg) {
			if err := s.ext.AddBundled(b); err != nil {
				if s.store != nil {
					_ = s.store.Close()
				}
				return nil, fmt.Errorf("mode: add bundled extension: %w", err)
			}
		}
	}
	extHooks := s.ext.Hooks()

	s.agent = agent.New(agent.Config{
		Provider:  s.prov,
		Model:     s.model,
		System:    s.system,
		Tools:     s.tools,
		Policy:    &s.policy,
		Ask:       s.ask,
		WorkDir:   opt.CWD,
		Roots:     []string{opt.CWD},
		SessionID: s.sessionID(),
		MaxTurns:  cfg.MaxTurns,
		MaxTokens: cfg.MaxTokens,
		// Read per request, so a model switch mid-session is honoured.
		InputModalitiesFunc: func() []string {
			s.mu.Lock()
			defer s.mu.Unlock()
			return modelModalities(s.cfg.Model)
		},
		ToolExecution: agent.Parallel,
		Retry:         retryPolicy(cfg),
		Emit:          s.emit,
		OnMessage:     s.onMessage,
		Complete:      s.complete,
		Hooks: agent.Hooks{
			BeforeProviderRequest: func(ctx context.Context, req *provider.Request) {
				s.beforeProviderRequest(ctx, req)
				if extHooks.BeforeProviderRequest != nil {
					extHooks.BeforeProviderRequest(ctx, req)
				}
			},
			TransformContext: func(ctx context.Context, msgs []message.Message) []message.Message {
				msgs = s.transformContext(ctx, msgs)
				if extHooks.TransformContext != nil {
					msgs = extHooks.TransformContext(ctx, msgs)
				}
				return msgs
			},
			BeforeTurn: s.beforeTurn,
			ToolCall:   extHooks.ToolCall,
			ToolResult: extHooks.ToolResult,
			MessageEnd: extHooks.MessageEnd,
			TurnEnd:    extHooks.TurnEnd,
			AgentEnd:   extHooks.AgentEnd,
		},
	})
	go s.startExtensions()
	return s, nil
}

// Subscribe returns a channel of events and a cancel function. buf is the
// channel capacity; a subscriber that fills it is dropped.
func (s *Server) Subscribe(buf int) (<-chan proto.Event, func()) {
	if buf <= 0 {
		buf = 64
	}
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	sub := &subscriber{ch: make(chan proto.Event, buf)}
	s.subs[id] = sub
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(sub.ch)
		}
	}
	return sub.ch, cancel
}

// emit fans an event out to every subscriber with a per-subscriber sequence.
// It never blocks: a full subscriber is dropped with a final error event. An
// event the agent did not stamp (a server-raised one) gets its time here.
func (s *Server) emit(ev proto.Event) {
	if ev.TS.IsZero() {
		ev.TS = s.clock.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sub := range s.subs {
		sub.seq++
		out := ev
		out.Seq = sub.seq
		select {
		case sub.ch <- out:
		default:
			delete(s.subs, id)
			// Make room for the final error so a small buffer still sees it.
			select {
			case <-sub.ch:
			default:
			}
			sub.ch <- proto.Event{Type: proto.EventError, Seq: sub.seq + 1, Error: &proto.Error{Code: proto.CodeInternal, Message: "subscriber too slow"}}
			close(sub.ch)
		}
	}
}

// onMessage persists one message synchronously. A store error aborts the run.
// The append happens outside the lock so a slow disk never stalls event
// delivery or the state getters.
func (s *Server) onMessage(m message.Message, u *provider.Usage) error {
	s.mu.Lock()
	if u != nil {
		s.lastUse = *u
		s.usage.Add(*u)
	}
	store := s.store
	if store == nil {
		s.mem = append(s.mem, m)
	}
	s.mu.Unlock()
	if store == nil {
		return nil
	}
	_, err := store.Append(session.Entry{Type: session.TypeMessage, Message: &m, Usage: u})
	return err
}

// history returns the current transcript for a new run.
func (s *Server) history() []message.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return append([]message.Message(nil), s.mem...)
	}
	msgs, err := s.store.Messages(s.store.Leaf())
	if err != nil {
		return nil
	}
	return msgs
}

// sessionStore returns the open store, or nil when the transcript lives in
// memory only. session.new, resume, and fork replace the store, so every
// reader outside New takes it through here rather than reading the field.
func (s *Server) sessionStore() *session.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store
}

// storeID returns a store's session id, or "" when there is none.
func storeID(st *session.Store) string {
	if st == nil {
		return ""
	}
	return st.Header().ID
}

// storeLeaf returns a store's active leaf, or "" when there is none.
func storeLeaf(st *session.Store) string {
	if st == nil {
		return ""
	}
	return st.Leaf()
}

// sessionID returns the current session's id, or "" when none is open.
func (s *Server) sessionID() string { return storeID(s.sessionStore()) }

// conversationID is the id providers receive for the current conversation: the
// session's id, which changes with session.new, resume, and fork, or a random
// per-server id when there is no session store. It is read on every request,
// possibly from the run goroutine.
func (s *Server) conversationID() string {
	if id := s.sessionID(); id != "" {
		return id
	}
	return s.fallbackConv
}

// newConversationID returns a random id for a server with no session store. It
// only labels the conversation for a provider, so it is not a secret.
func newConversationID() string { return "nova_" + randomHex(8) }

// beforeProviderRequest applies the live model and thinking state to each
// request, so set_model and set_thinking take effect without rebuilding.
func (s *Server) beforeProviderRequest(_ context.Context, req *provider.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req.Model = s.model
	req.ModelID = s.cfg.Model
	req.ReasoningEffort = s.think
}

// compactionState returns a private copy of the compactor with the live
// context limit applied, so a caller can use it without holding s.mu while
// set_model or set_context_limit change the originals.
func (s *Server) compactionState() *compaction.Compactor {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *s.compactor
	c.Settings.ContextLimit = s.limit
	return &c
}

// emitCompaction announces a finished compaction. The history it replaced is
// the one the last token count described, so that count goes with it.
func (s *Server) emitCompaction(out *compaction.Outcome) {
	s.forgetLastUsage()
	s.emit(proto.Event{
		Type:         proto.EventCompaction,
		Entry:        out.Entry.ID,
		TokensBefore: out.TokensBefore,
		TokensAfter:  out.TokensAfter,
	})
}

// reportCompactionFailure tells clients a compaction failed. The run goes on
// uncompacted, because the provider may still accept the longer context.
func (s *Server) reportCompactionFailure(err error) {
	s.emit(proto.Event{Type: proto.EventError, Error: &proto.Error{Code: proto.CodeInternal, Message: "compaction failed: " + err.Error()}})
}

// beforeTurn runs compaction when the context is over the limit. It returns no
// error even when compaction fails: see reportCompactionFailure.
func (s *Server) beforeTurn(ctx context.Context, msgs []message.Message) ([]message.Message, error) {
	store := s.sessionStore()
	if store == nil {
		return nil, nil
	}
	c := s.compactionState()
	s.mu.Lock()
	last := s.lastUse
	s.mu.Unlock()
	if !compaction.ShouldCompact(c.Settings, msgs, &last) {
		return nil, nil
	}
	out, err := compaction.Run(ctx, store, c, "")
	if err != nil {
		s.reportCompactionFailure(err)
		return nil, nil
	}
	s.emitCompaction(out)
	return s.history(), nil
}

// startRun registers an agent run and starts it in the background. The run
// exists when this returns, so a following abort always has something to
// cancel; the outcome arrives as events ending in one run_end.
func (s *Server) startRun(m message.Message) error {
	return s.agent.Start(context.Background(), s.history(), m)
}

// startFailure turns a startRun error into a response: a run already in
// progress is busy, anything else is a configuration problem.
func startFailure(cmd proto.Command, err error) proto.Response {
	if errors.Is(err, agent.ErrBusy) {
		return busyResponse(cmd)
	}
	return failResponse(cmd, proto.CodeInternal, err.Error())
}

// Display returns the TUI rendering preferences from the loaded config, so the
// interactive client starts with the user's saved settings. It is a Go accessor,
// not a protocol command: the display is client-side and never crosses the wire.
func (s *Server) Display() (thinkingLines, toolLines int, showImages bool, imageWidthCells int) {
	return s.cfg.Display.ThinkingLines, s.cfg.Display.ToolLines, s.cfg.Display.ShowImages, s.cfg.Display.ImageWidthCells
}

// StatusConfig returns the saved status-region layout.
func (s *Server) StatusConfig() config.StatusConfig { return s.cfg.Display.Status }

// Close releases the session. It is idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	store := s.store
	s.mu.Unlock()
	s.stopLife()
	s.closeLogins()
	var storeErr error
	if store != nil {
		storeErr = store.Close()
	}
	<-s.extStarted
	ctx, cancel := context.WithTimeout(context.Background(), extShutdownTimeout)
	defer cancel()
	return errors.Join(storeErr, s.ext.Shutdown(ctx))
}

// SetUIHandler installs the handler used to answer permission and ui requests.
func (s *Server) SetUIHandler(h func(context.Context, proto.UIRequest) (proto.UIResponse, error)) {
	s.uiMu.Lock()
	defer s.uiMu.Unlock()
	s.uiHandler = h
}
