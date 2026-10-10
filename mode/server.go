// Package mode is the server core and the transports on top of it. A Server
// owns one session, agent, tool registry, permission policy, and compaction
// settings, and implements the protocol commands. Print, JSON, and RPC are
// thin transports over the same server.
package mode

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/tool"
)

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
	Config      *config.Config
	CWD         string
	Home        string
	Provider    provider.Provider // nil means build OpenRouter from the config and auth
	Session     string            // path to open or create; "" means a new default
	NoSession   bool              // keep the transcript in memory only
	Out         io.Writer
	In          io.Reader
	Clock       Clock
	Interactive bool // whether a UI client is attached (affects permission "ask")
	Version     string
	Tools       []string // explicit active tool set; nil means use the config
	NoTools     bool     // register no tools at all
}

// subscriber is one event consumer.
type subscriber struct {
	ch      chan proto.Event
	seq     int64
	dropped bool
}

// Server owns the session and implements the protocol commands.
type Server struct {
	opt       Options
	cfg       *config.Config
	cwd, home string
	clock     Clock

	prov      provider.Provider
	usesAuth  bool
	keyStore  auth.Store
	tools     *tool.Registry
	pool      map[string]tool.Tool
	active    []string
	policy    permission.Policy
	agent     *agent.Agent
	store     *session.Store
	compactor *compaction.Compactor
	system    string

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

	uiMu      sync.Mutex
	uiHandler func(context.Context, proto.UIRequest) (proto.UIResponse, error)
	uiPending map[string]chan proto.UIResponse
	uiNext    int64
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
	}
	s.prov = opt.Provider
	if s.prov == nil {
		s.usesAuth = true
		s.keyStore = auth.NewFileStore(keyPath(home))
		s.prov = buildProvider(cfg, home)
	}
	tools, pool, err := buildTools(cfg)
	if err != nil {
		return nil, err
	}
	s.tools = tools
	s.pool = pool
	s.active = append([]string(nil), cfg.Tools.Active...)
	if len(s.active) == 0 {
		s.active = append([]string(nil), defaultToolNames...)
	}
	if opt.NoTools {
		s.active = nil
	} else if opt.Tools != nil {
		s.active = append([]string(nil), opt.Tools...)
	}
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
	s.system = buildSystemPrompt(cfg, opt.CWD, home, tools, clock.Now())

	s.agent = agent.New(agent.Config{
		Provider:      s.prov,
		Model:         s.model,
		System:        s.system,
		Tools:         s.tools,
		Policy:        &s.policy,
		WorkDir:       opt.CWD,
		Roots:         []string{opt.CWD},
		SessionID:     s.sessionID(),
		MaxTurns:      cfg.MaxTurns,
		ToolExecution: agent.Parallel,
		Retry: agent.RetryPolicy{
			Max:    cfg.Retry.Max,
			Base:   time.Duration(cfg.Retry.BaseMS) * time.Millisecond,
			Cap:    time.Duration(cfg.Retry.CapMS) * time.Millisecond,
			Jitter: 0.2,
		},
		Emit:      s.emit,
		OnMessage: s.onMessage,
		Hooks: agent.Hooks{
			BeforeProviderRequest: s.beforeProviderRequest,
			BeforeTurn:            s.beforeTurn,
		},
	})
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
// It never blocks: a full subscriber is dropped with a final error event.
func (s *Server) emit(ev proto.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sub := range s.subs {
		if sub.dropped {
			continue
		}
		sub.seq++
		out := ev
		out.Seq = sub.seq
		select {
		case sub.ch <- out:
		default:
			sub.dropped = true
			delete(s.subs, id)
			select {
			case sub.ch <- proto.Event{Type: proto.EventError, Seq: sub.seq + 1, Error: &proto.Error{Code: proto.CodeInternal, Message: "subscriber too slow"}}:
			default:
			}
			close(sub.ch)
		}
	}
}

// onMessage persists one message synchronously. A store error aborts the run.
func (s *Server) onMessage(m message.Message, u *provider.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u != nil {
		s.lastUse = *u
		s.usage.Add(*u)
	}
	if s.store == nil {
		s.mem = append(s.mem, m)
		return nil
	}
	_, err := s.store.Append(session.Entry{Type: session.TypeMessage, Message: &m, Usage: u})
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

// sessionID returns the store's id, or an empty string when none is open.
func (s *Server) sessionID() string {
	if s.store == nil {
		return ""
	}
	return s.store.Header().ID
}

// beforeProviderRequest applies the live model and thinking state to each
// request, so set_model and set_thinking take effect without rebuilding.
func (s *Server) beforeProviderRequest(_ context.Context, req *provider.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req.Model = s.model
	req.ReasoningEffort = s.think
}

// beforeTurn runs compaction when the context is over the limit.
func (s *Server) beforeTurn(ctx context.Context, msgs []message.Message) ([]message.Message, error) {
	if s.store == nil || s.compactor == nil {
		return nil, nil
	}
	s.mu.Lock()
	settings := s.compactor.Settings
	settings.ContextLimit = s.limit
	last := s.lastUse
	s.mu.Unlock()
	if !compaction.ShouldCompact(settings, msgs, &last) {
		return nil, nil
	}
	out, err := compaction.Run(ctx, s.store, s.compactor, "")
	if err != nil {
		s.emit(proto.Event{Type: proto.EventError, Error: &proto.Error{Code: proto.CodeInternal, Message: "compaction failed: " + err.Error()}})
		return nil, nil
	}
	s.emit(proto.Event{
		Type:         proto.EventCompaction,
		Entry:        out.Entry.ID,
		TokensBefore: out.TokensBefore,
		TokensAfter:  out.TokensAfter,
	})
	return s.history(), nil
}

// run executes one agent run in its own goroutine.
func (s *Server) run(text string) {
	history := s.history()
	_, _ = s.agent.Run(context.Background(), history, message.User(text))
}

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
	if store != nil {
		return store.Close()
	}
	return nil
}

// SetUIHandler installs the handler used to answer permission and ui requests.
func (s *Server) SetUIHandler(h func(context.Context, proto.UIRequest) (proto.UIResponse, error)) {
	s.uiMu.Lock()
	defer s.uiMu.Unlock()
	s.uiHandler = h
}
