package ext

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
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
)

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
}

// registration is what one instance currently contributes.
type registration struct {
	tools   []string
	command []RegisterCommand
	hooks   []string
	flags   []FlagSpec
}

// newInstance creates a stopped instance.
func newInstance(h *Host, m *Manifest) *instance {
	return &instance{
		host:       h,
		manifest:   m,
		state:      stateStopped,
		registerCh: make(chan struct{}, 1),
		crashCh:    make(chan struct{}, 1),
		doneCh:     make(chan struct{}),
	}
}

// spawn starts the process and its connection and sends init. It does not wait
// for registration.
func (inst *instance) spawn(_ context.Context) error {
	m := inst.manifest
	if len(m.Run) == 0 {
		inst.setState(stateDisabled, "no run command")
		return fmt.Errorf("ext: %s has no run command", m.Name)
	}
	argv := append([]string(nil), m.Run...)
	argv[0] = resolveExec(m.Dir, argv[0])
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.Dir
	cmd.Env = inst.host.childEnv(m)
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ext: %s stdin: %w", m.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ext: %s stdout: %w", m.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ext: %s stderr: %w", m.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ext: start %s: %w", m.Name, err)
	}
	done := make(chan struct{})
	inst.mu.Lock()
	inst.cmd = cmd
	inst.procDone = done
	inst.startedAt = inst.host.clock.Now()
	inst.state = stateStarting
	inst.lastErr = ""
	inst.mu.Unlock()

	go inst.forwardStderr(stderr)
	inst.conn = newConn(m.Name, stdout, stdin, &procCloser{stdin: stdin, stdout: stdout}, inst.host.clock, inst.handle)
	go inst.waitExit(cmd, done)

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
	if err := inst.conn.notify(init); err != nil {
		return fmt.Errorf("ext: init %s: %w", m.Name, err)
	}
	inst.replay()
	return nil
}

// forwardStderr forwards the child's stderr line by line to the host log.
func (inst *instance) forwardStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		inst.host.logf("info", inst.manifest.Name+": "+sc.Text())
	}
}

// replay sends the instance's prior session entries for the kinds it declared.
func (inst *instance) replay() {
	if inst.host.opt.Session == nil {
		return
	}
	inst.mu.Lock()
	kinds := append([]string(nil), inst.entryKinds...)
	inst.mu.Unlock()
	for _, kind := range kinds {
		for _, data := range inst.host.opt.Session.Entries(kind) {
			_ = inst.conn.notify(&outFrame{Type: "replay", Kind: kind, Data: data})
		}
	}
}

// waitExit reaps one process, closes its done channel, and signals the
// supervisor. done is closed exactly once, by this goroutine.
func (inst *instance) waitExit(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	inst.mu.Lock()
	stopped := inst.stopped
	inst.mu.Unlock()
	if !stopped && err != nil {
		inst.host.logf("warn", inst.manifest.Name+" exited: "+err.Error())
	}
	if inst.conn != nil {
		inst.conn.closeWith(err)
	}
	close(done)
	select {
	case inst.crashCh <- struct{}{}:
	default:
	}
}

// handle receives every frame the extension sends.
func (inst *instance) handle(typ string, line []byte, r reply) {
	inst.host.handleFrame(inst, typ, line, r)
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
	if err := inst.spawn(ctx); err != nil {
		inst.setState(stateFailed, err.Error())
		return err
	}
	if err := inst.awaitRegister(ctx); err != nil {
		inst.setState(stateFailed, err.Error())
		inst.closeConn(err)
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

// closeConn closes the current connection, ignoring a nil one.
func (inst *instance) closeConn(err error) {
	if inst.conn != nil {
		inst.conn.closeWith(err)
	}
}

// supervise restarts the process after an unexpected exit.
func (inst *instance) supervise() {
	defer close(inst.doneCh)
	max := inst.manifest.Restart.Max
	if max <= 0 {
		max = defaultMaxRestart
	}
	for range inst.crashCh {
		inst.mu.Lock()
		if inst.stopped {
			inst.mu.Unlock()
			return
		}
		healthy := inst.host.clock.Now().Sub(inst.startedAt) >= healthyAfter
		if healthy {
			inst.restarts = 0
		}
		restarts := inst.restarts
		inst.mu.Unlock()
		if healthy {
			inst.host.clearRegistrations(inst)
		}
		if restarts >= max {
			inst.host.disable(inst, fmt.Sprintf("crashed %d times", restarts))
			return
		}
		inst.mu.Lock()
		inst.restarts++
		inst.mu.Unlock()
		inst.host.clearRegistrations(inst)
		inst.setState(stateStarting, "")
		if err := inst.host.clock.Sleep(context.Background(), restartBackoff<<restarts); err != nil {
			return
		}
		if err := inst.restart(); err != nil {
			inst.setState(stateFailed, err.Error())
			return
		}
	}
}

// restart respawns the process and waits for registration.
func (inst *instance) restart() error {
	if err := inst.spawn(context.Background()); err != nil {
		return err
	}
	if err := inst.awaitRegister(context.Background()); err != nil {
		inst.closeConn(err)
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
	c := inst.conn
	cmd := inst.cmd
	done := inst.procDone
	inst.mu.Unlock()

	if c != nil {
		_ = c.notify(&outFrame{Type: "shutdown"})
	}
	if cmd != nil && cmd.Process != nil && done != nil {
		pid := cmd.Process.Pid
		if !inst.waitDone(done, shutdownGrace) {
			terminateGroup(pid)
			if !inst.waitDone(done, shutdownGrace) {
				forceKillGroup(pid)
			}
		}
		// Always kill the group, so a forked child cannot outlive the call.
		forceKillGroup(pid)
	}
	inst.closeConn(errExited)
	inst.mu.Lock()
	supervised := inst.supervised
	inst.mu.Unlock()
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

// procCloser closes the child's stdin and stdout; the process is killed
// separately.
type procCloser struct {
	stdin  io.WriteCloser
	stdout io.Closer
	closed bool
	mu     sync.Mutex
}

func (p *procCloser) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	err := p.stdin.Close()
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	return err
}

// childEnv builds the environment for an extension process: the host base,
// minus secrets, plus the NOVA_* variables.
func (h *Host) childEnv(m *Manifest) []string {
	base := os.Environ()
	if h.opt.Env != nil {
		base = h.opt.Env()
	}
	scrub := h.scrubPatterns()
	out := make([]string, 0, len(base)+5)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if h.scrubbed(name, scrub) && !h.passed(name) {
			continue
		}
		out = append(out, kv)
	}
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

// scrubbed reports whether name matches a scrub pattern or suffix.
func (h *Host) scrubbed(name string, patterns []string) bool {
	for _, p := range patterns {
		if name == p || (strings.HasPrefix(p, "_") && strings.HasSuffix(name, p)) {
			return true
		}
	}
	return false
}

// passed reports whether name is explicitly passed through.
func (h *Host) passed(name string) bool {
	for _, p := range h.opt.PassEnv {
		if p == name {
			return true
		}
	}
	return false
}
