package ext

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/proc"
	"github.com/AutumnsGrove/nova/tool"
)

// Instance states.
const (
	stateStarting = "starting"
	stateRunning  = "running"
	stateFailed   = "failed"
	stateDisabled = "disabled"
	stateStopped  = "stopped"
)

// Supervision timings.
const (
	startupTimeout    = 10 * time.Second
	restartBackoff    = time.Second
	healthyAfter      = 60 * time.Second
	shutdownGrace     = 2 * time.Second
	defaultMaxRestart = 3
	// maxBackoffDoublings caps the backoff at 32 seconds, so a large
	// restart.max cannot overflow the shift or wait unreasonably long.
	maxBackoffDoublings = 5
	// groupWaitDelay is how long Wait lingers for a killed command's output
	// pipes.
	groupWaitDelay = 2 * time.Second
)

// errStopping reports that a start lost a race with a stop.
var errStopping = errors.New("extension is stopping")

// instance is one loaded extension and its running process.
type instance struct {
	host     *Host
	manifest *Manifest

	mu         sync.Mutex
	state      string
	restarts   int
	lastErr    string
	startedAt  time.Time
	conn       *conn
	cmd        *exec.Cmd
	procDone   chan struct{}
	reg        registration
	entryKinds []string
	stopped    bool
	supervised bool

	registerCh chan struct{}
	crashCh    chan struct{}
	doneCh     chan struct{}
	// stopCtx ends when stop begins, so a supervisor sleeping out a restart
	// backoff wakes up and does not respawn a process nothing will stop.
	stopCtx    context.Context
	stopCancel context.CancelFunc

	// Bundled extensions run in-process and contribute Go hooks and
	// observers rather than protocol frames.
	bundled     Bundled
	env         *Env
	envHooks    agent.Hooks
	bundleHooks map[string]bool
	cmdRun      map[string]InProcCommand
	events      []eventObserver
}

// registration is what one instance currently contributes.
type registration struct {
	// tools are the tools the process (or a bundled extension) registered; they
	// go away when it stops.
	tools []string
	// decl are the manifest's exec tools, which do not depend on the process.
	decl    []string
	command []RegisterCommand
	hooks   []string
	flags   []FlagSpec
}

// newInstance creates a stopped instance.
func newInstance(h *Host, m *Manifest) *instance {
	ctx, cancel := context.WithCancel(context.Background())
	return &instance{
		host:       h,
		manifest:   m,
		state:      stateStopped,
		registerCh: make(chan struct{}, 1),
		crashCh:    make(chan struct{}, 1),
		doneCh:     make(chan struct{}),
		stopCtx:    ctx,
		stopCancel: cancel,
	}
}

// getConn returns the current connection, or nil when no process has started.
func (inst *instance) getConn() *conn {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.conn
}

// request sends a frame to the current process and waits for its answer.
func (inst *instance) request(ctx context.Context, frame *outFrame, timeout time.Duration) (reply, error) {
	c := inst.getConn()
	if c == nil {
		return reply{}, errExited
	}
	return c.request(ctx, frame, timeout)
}

// launch starts the process and its connection. It refuses once stop has begun,
// and records the process under the same lock stop reads it with, so a stop
// either prevents the start or sees the process and kills it.
func (inst *instance) launch() (*conn, error) {
	m := inst.manifest
	if len(m.Run) == 0 {
		inst.setState(stateDisabled, "no run command")
		return nil, fmt.Errorf("ext: %s has no run command", m.Name)
	}
	argv := slices.Clone(m.Run)
	argv[0] = resolveExec(m.Dir, argv[0])
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.Dir
	cmd.Env = inst.host.childEnv(m)
	proc.SetGroup(cmd)

	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.stopped {
		return nil, errStopping
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ext: %s stdin: %w", m.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ext: %s stdout: %w", m.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ext: %s stderr: %w", m.Name, err)
	}
	// A register token left by the previous incarnation would make the next
	// startup look finished before the new process has said anything.
	select {
	case <-inst.registerCh:
	default:
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ext: start %s: %w", m.Name, err)
	}
	done := make(chan struct{})
	c := newConn(m.Name, stdout, stdin, &procCloser{stdin: stdin, stdout: stdout}, inst.host.clock, inst.handle)
	inst.cmd = cmd
	inst.procDone = done
	inst.conn = c
	inst.startedAt = inst.host.clock.Now()
	inst.state = stateStarting
	inst.lastErr = ""
	go inst.forwardStderr(stderr)
	go inst.waitExit(cmd, c, done)
	return c, nil
}

// spawn starts the process and sends init and the replayed entries. It does
// not wait for registration.
func (inst *instance) spawn() error {
	c, err := inst.launch()
	if err != nil {
		return err
	}
	m := inst.manifest
	flags := map[string]string{}
	for _, f := range m.Flags {
		flags[f.Name] = f.Default
	}
	for k, v := range inst.host.opt.Flags {
		flags[k] = v
	}
	init := &outFrame{
		Type:         "init",
		Version:      1,
		Extension:    m.Name,
		Session:      inst.host.opt.SessionID,
		CWD:          inst.host.opt.CWD,
		Mode:         inst.host.opt.Mode,
		Capabilities: m.Capabilities,
		Flags:        flags,
	}
	if err := c.notify(init); err != nil {
		return fmt.Errorf("ext: init %s: %w", m.Name, err)
	}
	inst.replay(c)
	return nil
}

// forwardStderr forwards the child's stderr line by line to the host log.
func (inst *instance) forwardStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		inst.host.logf("info", inst.manifest.Name+": "+sc.Text())
	}
	// A line over the limit stops the scanner; keep draining so the child
	// never blocks writing to a pipe nobody reads.
	_, _ = io.Copy(io.Discard, r)
}

// replay sends the instance's prior session entries for the kinds it declared.
func (inst *instance) replay(c *conn) {
	if inst.host.opt.Session == nil {
		return
	}
	inst.mu.Lock()
	kinds := slices.Clone(inst.entryKinds)
	inst.mu.Unlock()
	for _, kind := range kinds {
		for _, data := range inst.host.opt.Session.Entries(kind) {
			_ = c.notify(&outFrame{Type: "replay", Kind: kind, Data: data})
		}
	}
}

// waitExit reaps one process, closes its done channel, and signals the
// supervisor. done is closed exactly once, by this goroutine.
func (inst *instance) waitExit(cmd *exec.Cmd, c *conn, done chan struct{}) {
	err := cmd.Wait()
	inst.mu.Lock()
	stopped := inst.stopped
	inst.mu.Unlock()
	if !stopped && err != nil {
		inst.host.logf("warn", inst.manifest.Name+" exited: "+err.Error())
	}
	c.close()
	close(done)
	select {
	case inst.crashCh <- struct{}{}:
	default:
	}
}

// handle receives every frame the extension sends.
func (inst *instance) handle(c *conn, typ string, line []byte, r reply) {
	inst.host.handleFrame(inst, c, typ, line, r)
}

// markRegistered signals a successful registration.
func (inst *instance) markRegistered() {
	select {
	case inst.registerCh <- struct{}{}:
	default:
	}
}

// setState records a state and, when given, the last error.
func (inst *instance) setState(state, lastErr string) {
	inst.mu.Lock()
	inst.state = state
	if lastErr != "" {
		inst.lastErr = lastErr
	}
	inst.mu.Unlock()
}

// status snapshots the instance for Status().
func (inst *instance) status() Status {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return Status{
		Name:      inst.manifest.Name,
		Version:   inst.manifest.Version,
		State:     inst.state,
		Restarts:  inst.restarts,
		LastError: inst.lastErr,
	}
}

// start launches the instance and waits for it to register or fail.
func (inst *instance) start(ctx context.Context) error {
	if err := inst.spawn(); err != nil {
		inst.setState(stateFailed, err.Error())
		return err
	}
	if err := inst.awaitRegister(ctx); err != nil {
		inst.setState(stateFailed, err.Error())
		inst.abort()
		return err
	}
	inst.mu.Lock()
	inst.state = stateRunning
	inst.startedAt = inst.host.clock.Now()
	inst.supervised = true
	inst.mu.Unlock()
	go inst.supervise()
	return nil
}

// awaitRegister waits for the first register frame within the startup timeout.
func (inst *instance) awaitRegister(ctx context.Context) error {
	inst.mu.Lock()
	done := inst.procDone
	inst.mu.Unlock()
	select {
	case <-inst.registerCh:
		return nil
	case <-done:
		return fmt.Errorf("ext: %s exited during startup", inst.manifest.Name)
	case <-inst.host.clock.After(startupTimeout):
		return fmt.Errorf("ext: %s did not register within %s", inst.manifest.Name, startupTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// abort ends a process that never finished starting, so a failed extension
// does not linger until shutdown.
func (inst *instance) abort() {
	inst.mu.Lock()
	c, cmd, done := inst.conn, inst.cmd, inst.procDone
	inst.mu.Unlock()
	if c != nil {
		c.close()
	}
	select {
	case <-done:
		// Already reaped: its pid may belong to someone else by now.
	default:
		if cmd != nil && cmd.Process != nil {
			proc.Kill(cmd.Process.Pid)
			_ = cmd.Process.Kill()
		}
	}
}

// supervise restarts the process after an unexpected exit.
func (inst *instance) supervise() {
	defer close(inst.doneCh)
	limit := inst.manifest.Restart.Max
	if limit <= 0 {
		limit = defaultMaxRestart
	}
	for {
		select {
		case <-inst.crashCh:
		case <-inst.stopCtx.Done():
			return
		}
		if !inst.recoverFromCrash(limit) {
			return
		}
	}
}

// recoverFromCrash handles one unexpected exit: it restarts the process after a
// backoff, or gives up once the restart limit is spent. It reports whether
// supervision should continue.
func (inst *instance) recoverFromCrash(limit int) bool {
	inst.mu.Lock()
	if inst.stopped {
		inst.mu.Unlock()
		return false
	}
	if inst.host.clock.Now().Sub(inst.startedAt) >= healthyAfter {
		inst.restarts = 0
	}
	restarts := inst.restarts
	inst.mu.Unlock()
	if restarts >= limit {
		inst.host.disable(inst, fmt.Sprintf("crashed %d times", restarts))
		return false
	}
	inst.mu.Lock()
	inst.restarts++
	inst.mu.Unlock()
	inst.host.clearProcessRegistrations(inst)
	inst.setState(stateStarting, "")
	if inst.host.clock.Sleep(inst.stopCtx, restartBackoff<<min(restarts, maxBackoffDoublings)) != nil {
		return false
	}
	if err := inst.restart(); err != nil {
		if !errors.Is(err, errStopping) {
			inst.setState(stateFailed, err.Error())
		}
		return false
	}
	return true
}

// restart respawns the process and waits for registration.
func (inst *instance) restart() error {
	if err := inst.spawn(); err != nil {
		return err
	}
	if err := inst.awaitRegister(inst.stopCtx); err != nil {
		inst.abort()
		return err
	}
	inst.mu.Lock()
	inst.state = stateRunning
	inst.startedAt = inst.host.clock.Now()
	inst.mu.Unlock()
	return nil
}

// stop sends shutdown, waits, then signals the group. It waits for the
// supervisor to finish so no goroutine outlives the call.
func (inst *instance) stop(ctx context.Context) {
	inst.mu.Lock()
	if inst.stopped {
		inst.mu.Unlock()
		return
	}
	inst.stopped = true
	c, cmd, done, supervised := inst.conn, inst.cmd, inst.procDone, inst.supervised
	inst.mu.Unlock()
	inst.stopCancel()

	if c != nil {
		c.tryNotify(&outFrame{Type: "shutdown"})
	}
	if cmd != nil && cmd.Process != nil && done != nil {
		pid := cmd.Process.Pid
		if !inst.waitDone(done, shutdownGrace) {
			proc.Terminate(pid)
			if !inst.waitDone(done, shutdownGrace) {
				proc.Kill(pid)
			}
		}
		// Always kill the group, so a forked child cannot outlive the call.
		proc.Kill(pid)
	}
	if c != nil {
		c.close()
	}
	if supervised {
		select {
		case <-inst.doneCh:
		case <-ctx.Done():
		}
	}
	inst.setState(stateStopped, "")
}

// waitDone reports whether done closes within d of the host clock.
func (inst *instance) waitDone(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return true
	case <-inst.host.clock.After(d):
		return false
	}
}

// exitCode is a command's exit code: 128 plus the signal number when it died
// from a signal, the way a shell reports it, and -1 when it never ran.
func exitCode(err error) int {
	code, sig := proc.ExitStatus(err)
	if sig != 0 {
		return 128 + sig
	}
	return code
}

// groupCommand builds a command that runs in its own process group and is
// killed as a whole group when ctx ends. Killing only the leader would leave a
// script's children holding its output pipe open, so Wait would block until
// they finished on their own.
func groupCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	proc.SetGroup(cmd)
	cmd.Cancel = func() error {
		proc.Kill(cmd.Process.Pid)
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = groupWaitDelay
	return cmd
}

// procCloser closes the child's stdin and stdout; the process is killed
// separately.
type procCloser struct {
	stdin  io.WriteCloser
	stdout io.Closer
	once   sync.Once
	err    error
}

func (p *procCloser) Close() error {
	p.once.Do(func() {
		p.err = p.stdin.Close()
		_ = p.stdout.Close()
	})
	return p.err
}

// baseEnv is the environment the host starts from: the process's own, or the
// one the embedder gave.
func (h *Host) baseEnv() []string {
	if h.opt.Env != nil {
		return h.opt.Env()
	}
	return os.Environ()
}

// filteredEnv is baseEnv minus the variables matching patterns, except the ones
// the user passes through by name.
func (h *Host) filteredEnv(patterns []string) []string {
	return tool.FilterEnv(h.baseEnv(), func(name string) bool {
		return isScrubbed(name, patterns) && !slices.Contains(h.opt.PassEnv, name)
	})
}

// commandEnv is the environment for the commands the host runs on an
// extension's behalf (declarative tools, command hooks, build steps): the host
// base minus the variables the embedder asked to hide, which include Nova's own
// credentials. An extension process gets the stricter childEnv.
func (h *Host) commandEnv() []string { return h.filteredEnv(h.opt.ScrubEnv) }

// buildRunner is the runner for build steps: the embedder's, or a direct one
// that uses commandEnv.
func (h *Host) buildRunner() CommandRunner {
	if h.opt.Run != nil {
		return h.opt.Run
	}
	return ExecRunner{Env: h.commandEnv()}
}

// childEnv builds the environment for an extension process: the host base,
// minus secrets, plus the NOVA_* variables.
func (h *Host) childEnv(m *Manifest) []string {
	out := h.filteredEnv(h.scrubPatterns())
	out = append(out,
		"NOVA_EXTENSION="+m.Name,
		"NOVA_VERSION="+h.opt.NovaVersion,
		"NOVA_CWD="+h.opt.CWD,
		"NOVA_MODE="+h.opt.Mode,
		"NOVA_EXTENSION_DIR="+m.Dir,
	)
	return out
}

// scrubPatterns is the fixed scrub list plus the host's extras.
func (h *Host) scrubPatterns() []string {
	return append([]string{"OPENROUTER_API_KEY", "_API_KEY", "_TOKEN", "_SECRET"}, h.opt.ScrubEnv...)
}

// isScrubbed reports whether name matches a scrub pattern: an exact name, or a
// suffix when the pattern starts with an underscore.
func isScrubbed(name string, patterns []string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool {
		return name == p || (strings.HasPrefix(p, "_") && strings.HasSuffix(name, p))
	})
}
