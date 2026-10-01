package agentsessions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-runner/runner"
)

// AdapterRuntimeConfig configures a Runtime backed by a
// go-providers.CLIAdapter. Each SendInput drives a fresh runner.Run that
// invokes the underlying CLI binary; a turn ends when the runner emits
// EventProcessExited / EventProcessTimeout.
type AdapterRuntimeConfig struct {
	// ID is the runtime's stable identifier. Required.
	ID string

	// Kind is the free-form classification token (e.g. "cli"). Required.
	Kind string

	// Adapter is the go-providers CLIAdapter to drive. Required.
	Adapter provider.CLIAdapter

	// Caps declares the static capability set Sessions produced by this
	// Runtime expose. Default zero value declares no capabilities.
	Caps Capabilities

	// BuildArgs, when non-nil, overrides the adapter's BuildArgs for
	// constructing per-turn CLI argv. The default uses adapter.BuildArgs
	// with the prompt = SendInput payload, an empty system prompt, and
	// the StartOptions.SessionIDPreset on the first turn (then the most
	// recent observed session_id). Most consumers leave this nil.
	BuildArgs func(prompt, sessionID string) []string

	// WaitDelay is the grace period between SIGTERM (on context cancel)
	// and SIGKILL passed to runner.Run. Zero uses the runner's default.
	WaitDelay time.Duration
}

// NewFromAdapter constructs a Runtime backed by cfg.Adapter. Runtime
// shape is selected by the lifecycle flag in cfg.Caps (at most one may be
// set):
//
//   - no lifecycle flag (default): subprocess-per-turn runtime.
//     Each SendInput drives a fresh runner.Run that invokes the underlying
//     CLI binary; a turn ends when the runner emits EventProcessExited.
//     Single-turn-in-flight semantics — a second SendInput while a turn is
//     running returns ErrTurnInFlight without queueing.
//
//   - cfg.Caps.StreamingStdio: long-lived child speaking NDJSON over
//     stdin/stdout (streaming-stdio).
//
//   - cfg.Caps.JsonRpcStdio: long-lived child speaking JSON-RPC 2.0 over
//     stdin/stdout (jsonrpc-stdio); its Sessions implement JsonRpcCaller.
//
//   - cfg.Caps.ServeHTTP: long-lived child serving an HTTP API with
//     server-sent events (http-sse).
//
//   - cfg.Caps.PTY: long-lived PTY runtime. The adapter binary is
//     spawned once at Start time under a creack/pty master; SendInput writes
//     bytes to the PTY master. Conversation / MCP / tool-affordance state
//     persists across turns inside the long-lived child. Resize works.
//     SendInput is non-blocking; the consumer is responsible for any
//     turn-boundary discipline at the application layer (the lib does not
//     impose ErrTurnInFlight on PTY because turn boundaries on a PTY are
//     CLI-defined, not lib-defined).
//
// Caps().BinaryRequired is honored on every shape — Prepare returns an
// error if the adapter's Detect() finds no binary.
//
// Capability-driven selection means consumers do not pick a constructor;
// they declare what their adapter supports via Caps and the lib routes to
// the right implementation. The acceptance contract is that flipping
// cfg.Caps.PTY does not silently change other observable behavior beyond
// the lifecycle shape (existing adapters with Caps.PTY=false are
// unaffected).
func NewFromAdapter(cfg AdapterRuntimeConfig) (Runtime, error) {
	if cfg.ID == "" {
		return nil, errors.New("agentsessions: AdapterRuntimeConfig.ID is required")
	}
	if cfg.Adapter == nil {
		return nil, errors.New("agentsessions: AdapterRuntimeConfig.Adapter is required")
	}
	if err := cfg.Caps.validateLifecycle(); err != nil {
		return nil, err
	}
	if cfg.Kind == "" {
		cfg.Kind = "cli"
	}
	switch {
	case cfg.Caps.PTY:
		return &ptyRuntime{cfg: cfg}, nil
	case cfg.Caps.StreamingStdio:
		return &streamingStdioRuntime{cfg: cfg}, nil
	case cfg.Caps.JsonRpcStdio:
		return &jsonRpcStdioRuntime{cfg: cfg}, nil
	case cfg.Caps.ServeHTTP:
		return &serveHTTPRuntime{cfg: cfg}, nil
	default:
		return &adapterRuntime{cfg: cfg}, nil
	}
}

// adapterRuntime wraps a CLIAdapter as a Runtime.
type adapterRuntime struct {
	cfg AdapterRuntimeConfig
}

func (r *adapterRuntime) ID() string         { return r.cfg.ID }
func (r *adapterRuntime) Kind() string       { return r.cfg.Kind }
func (r *adapterRuntime) Caps() Capabilities { return r.cfg.Caps }

func (r *adapterRuntime) Prepare(ctx context.Context) error {
	if r.cfg.Caps.BinaryRequired {
		if _, ok := r.cfg.Adapter.Detect(); !ok {
			return fmt.Errorf("agentsessions: adapter %q binary not found", r.cfg.Adapter.Name())
		}
	}
	// A preflight refusal (for example, missing credentials that would
	// otherwise send the CLI into an interactive login) keeps the session
	// from starting at all.
	if p, ok := r.cfg.Adapter.(provider.Preflighter); ok {
		if err := p.Preflight(); err != nil {
			return fmt.Errorf("agentsessions: adapter %q preflight: %w", r.cfg.Adapter.Name(), err)
		}
	}
	return nil
}

func (r *adapterRuntime) Start(ctx context.Context, opts StartOptions) (Session, error) {
	var err error
	opts, err = normalizeStartOptions(opts)
	if err != nil {
		return nil, err
	}
	if opts.Workdir == "" {
		return nil, errors.New("agentsessions: StartOptions.Workdir is required for adapter runtime")
	}

	bootDir, planted, sessionAdapter, err := preparePlant(opts, r.cfg.Adapter, r.cfg.ID)
	if err != nil {
		return nil, err
	}
	opts = planted

	s := &adapterSession{
		runtime:   r,
		adapter:   sessionAdapter,
		bootDir:   bootDir,
		opts:      opts,
		buildArgs: r.cfg.BuildArgs,
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}
	s.sessionID.Store(opts.SessionIDPreset)
	s.alive.Store(true)
	s.state.Store(int32(LiveStateIdle))
	if opts.Launch != nil && s.buildArgs != nil {
		return nil, errors.New("agentsessions: AdapterRuntimeConfig.BuildArgs and a launch template (StartOptions.Launch) are mutually exclusive: the template owns every turn's argv")
	}
	launchArgs := false
	if s.buildArgs == nil {
		if opts.Launch != nil {
			// A prepared launch's template gives each turn its own prompt
			// and resume id, with ExtraArgs at its extra-argument slot. Only
			// root arguments can fail to resolve, so one check here covers
			// every turn.
			if _, err := spawnArgs(sessionAdapter, opts, "", "", ""); err != nil {
				return nil, err
			}
			turnOpts := opts
			s.buildArgs = func(prompt, sessionID string) []string {
				args, _ := spawnArgs(sessionAdapter, turnOpts, prompt, "", sessionID)
				return args
			}
			launchArgs = true
		} else {
			s.buildArgs = func(prompt, sessionID string) []string {
				return sessionAdapter.BuildArgs(prompt, "", sessionID)
			}
		}
	}
	// ExtraArgs splice composes over whatever buildArgs is in use (caller-
	// supplied or default). Captures len at Start; opts is value-copied
	// into the session struct so post-Start mutation by the caller does
	// not retroactively rewrite per-turn argv. A launch template has
	// already placed them.
	if len(opts.ExtraArgs) > 0 && !launchArgs {
		inner := s.buildArgs
		extra := append([]string(nil), opts.ExtraArgs...)
		s.buildArgs = func(prompt, sessionID string) []string {
			return withExtraArgs(inner(prompt, sessionID), extra)
		}
	}

	if opts.AutoFireFirstTurn && len(opts.FirstTurnPayload) > 0 {
		// Synchronous first turn: subprocess-per-turn semantics mean the
		// runner.Run completes before SendInput returns. Start blocks until
		// the kickoff turn finishes — eliminates the Launch/SendInput race
		// at the cost of a longer Start. Consumers that don't want this
		// latency leave AutoFireFirstTurn=false and call SendInput on their
		// own schedule.
		if err := s.SendInput(ctx, opts.FirstTurnPayload); err != nil {
			_ = s.Stop(ctx)
			return nil, fmt.Errorf("agentsessions: auto-fire first turn: %w", err)
		}
	}
	return s, nil
}

// adapterSession is the Session a CLIAdapter produces. SendInput drives
// runner.Run; Stop closes stopCh, which triggers any in-flight runner
// context cancel; Wait blocks on done (closed when Stop has fully
// drained).
type adapterSession struct {
	runtime *adapterRuntime
	// adapter is the per-session CLIAdapter — usually a pointer alias to
	// runtime.cfg.Adapter, but a per-session clone when AutoPlantBootDir
	// fired bare-mode injection. Always non-nil after Start.
	adapter provider.CLIAdapter
	// bootDir is the absolute path of the AutoPlantBootDir-planted tempdir,
	// or "" when no plant happened. Cleaned up exactly once at terminal
	// state (Stop) via cleanupBootDir.
	bootDir   string
	opts      StartOptions
	buildArgs func(prompt, sessionID string) []string

	sessionID atomic.Value // string — last observed provider session_id

	state   atomic.Int32 // LiveState
	alive   atomic.Bool
	pid     atomic.Int32 // live PID — resets to 0 between turns
	lastPID atomic.Int32 // sticky most-recent PID — survives turn boundaries
	turnID  atomic.Value // string

	// turnSawTerminal tracks whether the adapter's own ParseLine emitted a
	// terminal llmtypes.StreamEvent (EventDone / EventError) during the
	// in-flight turn. EventUsage is not terminal: OpenCode reports usage
	// once per step and a turn can have several, so usage alone must not
	// suppress the EventError a later crash would synthesize. Reset at the top of each SendInput;
	// consulted after runner.Run returns to decide whether SendInput must
	// synthesize a terminal event on the adapter's behalf (see
	// synthesizeTerminalEvent). Some adapters — OpenCode's `opencode
	// run`, by design — never emit one of their own because the CLI has
	// no structured completion signal on stdout; others (Codex) do.
	// Reads/writes happen only from the single goroutine that owns the
	// in-flight turn (SendInput holds turnMu for the whole call and
	// go-runner's runOnce invokes cfg.OnEvent synchronously within that
	// same call stack — no separate goroutine parses provider events), so
	// a plain bool is safe; atomic.Bool is used anyway for consistency
	// with the rest of this struct's fields.
	turnSawTerminal atomic.Bool

	// Per-turn resume bookkeeping for provider.SessionResumeVerifier
	// adapters. Written at the top of SendInput and read from
	// handleRunnerEvent, which go-runner calls synchronously on the turn's
	// goroutine, so plain fields are safe.
	turnRequestedID    string
	turnFirstSessionID string
	turnSessionLost    bool

	sandboxOutcome atomic.Value // SandboxOutcome

	stopCh   chan struct{}
	stopOnce sync.Once

	done     chan struct{}
	doneOnce sync.Once
	exitCode atomic.Int32

	// turnMu serializes SendInput at the adapter layer. The Manager
	// also serializes via inputMu, but turnMu lets adapter consumers
	// (compliance harness, integration tests) hit the same guarantee
	// when bypassing the Manager.
	turnMu       sync.Mutex
	turnInFlight atomic.Bool
}

func (s *adapterSession) SandboxOutcome() (SandboxOutcome, bool) {
	out, ok := s.sandboxOutcome.Load().(SandboxOutcome)
	return out, ok
}

func (s *adapterSession) reportSandboxOutcome(out SandboxOutcome) {
	s.sandboxOutcome.Store(out)
	if s.opts.SandboxOutcomeCallback != nil {
		s.opts.SandboxOutcomeCallback(out)
	}
}

func (s *adapterSession) Wait() (int, error) {
	<-s.done
	return int(s.exitCode.Load()), nil
}

func (s *adapterSession) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.alive.Store(false)
		s.state.Store(int32(LiveStateStopped))
		close(s.stopCh)
	})
	// Stop is non-blocking on adapter sessions: there is no long-lived
	// process to drain; turn-in-flight is cancelled via stopCh, which
	// the runner's context observes.
	s.doneOnce.Do(func() {
		close(s.done)
		cleanupBootDir(s.bootDir)
	})
	return nil
}

func (s *adapterSession) SendInput(ctx context.Context, data []byte) error {
	if !s.alive.Load() {
		return ErrNoInputChannel
	}
	if !s.turnInFlight.CompareAndSwap(false, true) {
		return ErrTurnInFlight
	}
	defer s.turnInFlight.Store(false)

	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	prompt := string(data)
	sessionID, _ := s.sessionID.Load().(string)
	args := s.buildArgs(prompt, sessionID)

	// Adapters that recognize a dead resume id or a login failure from
	// stderr get a bounded copy of the turn's stderr to inspect; stderr
	// still reaches StartOptions.Stderr unchanged.
	stderr := s.opts.Stderr
	classifier, canClassify := s.adapter.(provider.SessionLostClassifier)
	authClassifier, canClassifyAuth := s.adapter.(provider.AuthFailureClassifier)
	verifier, canVerify := s.adapter.(provider.SessionResumeVerifier)
	resumeVerified := canVerify && verifier.ResumeKeepsSessionID() && sessionID != ""
	// The run context exists before stderr is wired so a recognized login
	// failure can end the turn early (EndTurnOnAuthFailure).
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stderrTail *tailWriter
	if (canClassify && sessionID != "") || canClassifyAuth {
		stderrTail = &tailWriter{w: stderr, max: sessionLostTailBytes}
		if canClassifyAuth && s.opts.EndTurnOnAuthFailure {
			stderrTail.onTail = func(tail []byte) {
				if authClassifier.IsNotAuthenticated(tail) {
					cancel()
				}
			}
		}
		stderr = stderrTail
	}
	s.turnRequestedID = ""
	if resumeVerified {
		s.turnRequestedID = sessionID
	}
	s.turnFirstSessionID = ""
	s.turnSessionLost = false

	turnID := defaultIDFn()
	s.turnID.Store(turnID)
	s.turnSawTerminal.Store(false)
	s.state.Store(int32(LiveStateProcessing))
	defer s.state.Store(int32(LiveStateIdle))

	// Cancel the run if Stop fires while the runner is mid-flight.
	go func() {
		select {
		case <-s.stopCh:
			cancel()
		case <-runCtx.Done():
		}
	}()

	cfg := runner.Config{
		Provider:      s.turnAdapter(),
		SandboxPolicy: s.opts.SandboxPolicy,
		Profile:       s.opts.Profile,
		Workspace:     s.opts.Workdir,
		Args:          args,
		Env:           s.opts.Env,
		Stderr:        stderr,
		ExtraFiles:    s.opts.ExtraFiles,
		WaitDelay:     s.runtime.cfg.WaitDelay,
		OnEvent:       s.handleRunnerEvent,
	}
	// StartOptions.Supervisor + ResourceLimits are PTY-only in v0.6.0.
	// Forwarding them to runner.Config.Supervisor / .ResourceLimits on
	// the adapter path remains a follow-up increment — see CHANGELOG
	// "Out of scope".
	err := runner.Run(runCtx, cfg)
	if err != nil {
		var sandboxErr *runner.SandboxError
		if errors.As(err, &sandboxErr) {
			s.reportSandboxOutcome(sessionSandboxOutcomeFromRunner(sandboxErr.Outcome))
		}
		var startErr *runner.StartError
		if errors.As(err, &startErr) {
			s.reportSandboxOutcome(sessionSandboxOutcomeFromRunner(startErr.Outcome))
		}
	}

	// A dead resume id fails every turn that passes it, so drop it: the
	// next SendInput starts a fresh provider session. Whether to resend
	// this prompt without the lost history is the caller's decision, so
	// the error says what happened instead of retrying here.
	if err != nil && canClassify && stderrTail != nil && sessionID != "" && classifier.IsSessionLost(stderrTail.Bytes()) {
		s.sessionID.CompareAndSwap(sessionID, "")
		err = &SessionLostError{RequestedID: sessionID, Err: err}
	}
	if err != nil && canClassifyAuth && stderrTail != nil && authClassifier.IsNotAuthenticated(stderrTail.Bytes()) {
		err = fmt.Errorf("agentsessions: %w: %w", provider.ErrProviderNotAuthenticated, err)
		s.reportAuthFailed()
	}
	// Secondary signal for an id-keeping adapter: the turn reported no
	// session id at all, but stderr says the requested session was not
	// found. A turn that reported the requested id kept it, whatever
	// stderr says.
	if resumeVerified && !s.turnSessionLost && s.turnFirstSessionID == "" && canClassify && stderrTail != nil && classifier.IsSessionLost(stderrTail.Bytes()) {
		s.sessionID.CompareAndSwap(sessionID, "")
		s.reportSessionLost(sessionID, s.turnFirstSessionID)
	}

	// runner.Run has fully returned — cfg.OnEvent (handleRunnerEvent) has
	// already observed every EventProviderEvent the adapter emitted for
	// this turn, synchronously, on this same goroutine (go-runner's
	// runOnce calls OnEvent directly from streamProviderEvents/its own
	// terminal switch, never from a separate goroutine). If the adapter
	// never produced its own terminal llmtypes.StreamEvent
	// (EventDone/EventError) — true for adapters whose ParseLine never
	// emits one, such as the ACP pass-throughs, and for any turn whose
	// process dies before its terminal line — synthesize one here so a
	// real subprocess exit always surfaces a terminal event to
	// EventFanout/Fanout. Adapters that
	// already emit their own (Codex's "turn.completed" line, for example)
	// are unaffected: turnSawTerminal short-circuits this before it fires.
	if !s.turnSawTerminal.Load() {
		s.synthesizeTerminalEvent(err)
	}

	if err != nil {
		s.exitCode.Store(1)
		return err
	}
	return nil
}

// synthesizeTerminalEvent emits a terminal llmtypes.StreamEvent — EventDone
// on a clean turn (runErr == nil), EventError otherwise — through the same
// EventFanout/Fanout surfaces handleRunnerEvent's EventProviderEvent case
// uses. Only called when the adapter's own ParseLine never produced a
// terminal event during the turn (see turnSawTerminal); the resulting
// event is indistinguishable, to any downstream consumer, from one the
// adapter emitted itself — event_translator-style mappers that key off
// llmtypes.EventDone/EventError need no changes to observe it.
func (s *adapterSession) synthesizeTerminalEvent(runErr error) {
	var synth llmtypes.StreamEvent
	if runErr != nil {
		synth = llmtypes.StreamEvent{Type: llmtypes.EventError, Error: runErr.Error()}
	} else {
		synth = llmtypes.StreamEvent{Type: llmtypes.EventDone}
	}
	tryEventFanout(s.opts.EventFanout, synth)
	if s.opts.Fanout != nil {
		if line, ok := encodeStreamEvent(synth); ok {
			_, _ = s.opts.Fanout.Write(line)
		}
	}
}

// handleRunnerEvent forwards runner events to the Fanout writer (which
// the Manager has wired to the attach broker if AttachEnabled). It also
// captures process pid + the first observed session id so Health reports
// the live state and OnSessionID fires once per turn.
func (s *adapterSession) handleRunnerEvent(ev runner.Event) {
	switch ev.Kind {
	case runner.EventProcessStarted:
		if pid, ok := ev.Payload["pid"].(int); ok {
			s.pid.Store(int32(pid))
			s.lastPID.Store(int32(pid))
		}
		if out, ok := ev.Payload["sandbox"].(runner.SandboxOutcome); ok {
			s.reportSandboxOutcome(sessionSandboxOutcomeFromRunner(out))
		}
	case runner.EventProviderEvent:
		if pe, ok := ev.Payload["event"].(llmtypes.StreamEvent); ok {
			firstReplacement := false
			if pe.Type == llmtypes.EventSessionID && pe.SessionID != "" {
				s.sessionID.Store(pe.SessionID)
				if s.opts.OnSessionID != nil {
					s.opts.OnSessionID(pe.SessionID)
				}
				if s.turnFirstSessionID == "" {
					s.turnFirstSessionID = pe.SessionID
					firstReplacement = s.turnRequestedID != "" && pe.SessionID != s.turnRequestedID
				}
			}
			if pe.Type == llmtypes.EventDone || pe.Type == llmtypes.EventError {
				// The adapter emitted its own terminal event this turn
				// (e.g. Codex's "turn.completed") — SendInput's post-Run
				// synthesis must not double-fire once runner.Run returns.
				s.turnSawTerminal.Store(true)
			}
			tryEventFanout(s.opts.EventFanout, pe)
			if s.opts.Fanout != nil {
				if line, ok := encodeStreamEvent(pe); ok {
					_, _ = s.opts.Fanout.Write(line)
				}
			}
			// Reported after the new id itself, and before anything else
			// the turn emits.
			if firstReplacement {
				s.reportSessionLost(s.turnRequestedID, pe.SessionID)
			}
		}
	case runner.EventProcessExited:
		s.pid.Store(0)
	case runner.EventProcessTimeout:
		s.pid.Store(0)
	}
}

func (s *adapterSession) Resize(ctx context.Context, rows, cols uint16) error {
	// Subprocess adapters have no PTY to resize.
	return nil
}

func (s *adapterSession) Health() HealthStatus {
	turnID, _ := s.turnID.Load().(string)
	return HealthStatus{
		Alive:  s.alive.Load(),
		PID:    int(s.pid.Load()),
		State:  LiveState(s.state.Load()),
		TurnID: turnID,
	}
}

func (s *adapterSession) CheckpointHints() (CheckpointHint, bool) {
	if !s.runtime.cfg.Caps.CheckpointResume {
		return nil, false
	}
	id, _ := s.sessionID.Load().(string)
	if id == "" {
		return nil, false
	}
	return CheckpointHint(id), true
}

// ProviderSessionID implements SessionIDer when the runtime declares
// Caps().ProviderSessionID == true. Returns the empty string before the
// first turn observes a session id.
func (s *adapterSession) ProviderSessionID() string {
	id, _ := s.sessionID.Load().(string)
	return id
}

// sessionLostReason is the reason reported with a replaced session.
const sessionLostReason = "requested provider session not found; the provider started a new session"

// reportSessionLost announces, once per turn, that a resume turn is running
// in a new provider session: to OnProviderSessionLost, the typed-event
// callback (events.SessionLost) and the byte Fanout, as a
// "[session_lost] ..." marker in the style of its other markers. It does
// not fail the turn.
func (s *adapterSession) reportSessionLost(requested, actual string) {
	if s.turnSessionLost {
		return
	}
	s.turnSessionLost = true
	if s.opts.OnProviderSessionLost != nil {
		s.opts.OnProviderSessionLost(requested, actual, sessionLostReason)
	}
	if s.opts.TypedEventCallback != nil {
		s.opts.TypedEventCallback(events.SessionLost{RequestedID: requested, ActualID: actual, Reason: sessionLostReason})
	}
	if s.opts.Fanout != nil {
		_, _ = fmt.Fprintf(s.opts.Fanout, "\n[session_lost] requested=%s actual=%s: %s\n", requested, actual, sessionLostReason)
	}
}

// reportAuthFailed announces a turn the adapter's AuthFailureClassifier
// recognised as a sign-in failure: to the typed-event callback
// (events.AuthFailed) and the byte Fanout, as an "[auth_failed]" marker. The
// turn's error already wraps provider.ErrProviderNotAuthenticated.
func (s *adapterSession) reportAuthFailed() {
	msg := provider.ErrProviderNotAuthenticated.Error()
	if s.opts.TypedEventCallback != nil {
		s.opts.TypedEventCallback(events.AuthFailed{Message: msg})
	}
	if s.opts.Fanout != nil {
		_, _ = fmt.Fprintf(s.opts.Fanout, "\n[auth_failed] %s\n", msg)
	}
}

// turnAdapter is the adapter handed to go-runner for one turn. An adapter
// that can produce typed events is always tapped, so each stdout line also
// goes through ParseLineEvents: typed-only events (a permission denial has
// no StreamEvent form) reach the byte Fanout even when no
// TypedEventCallback is set, and the callback when one is. go-runner parses
// lines on the turn's goroutine, so typed and legacy events stay in line
// order.
func (s *adapterSession) turnAdapter() provider.CLIAdapter {
	parser, ok := s.adapter.(provider.EventParser)
	if !ok {
		return s.adapter
	}
	return &typedEventTap{CLIAdapter: s.adapter, parser: parser, cb: s.opts.TypedEventCallback, fanout: s.opts.Fanout}
}

type typedEventTap struct {
	provider.CLIAdapter
	parser provider.EventParser
	cb     provider.EventsCallback // nil when the caller did not ask for typed events
	fanout io.Writer
}

func (t *typedEventTap) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if evs, err := t.parser.ParseLineEvents(line); err == nil {
		for _, ev := range evs {
			if t.cb != nil {
				t.cb(ev)
			}
			// Permission denials have no StreamEvent form; mark them on
			// the byte Fanout so an attached reader sees the no-op.
			if d, ok := ev.(events.PermissionDenied); ok && t.fanout != nil {
				_, _ = fmt.Fprintf(t.fanout, "\n[permission_denied:%s] %s\n", d.Action, d.DisplayName)
			}
		}
	}
	return t.CLIAdapter.ParseLine(line)
}

// sessionLostTailBytes bounds the stderr kept per resume turn for a
// SessionLostClassifier. The message it looks for is the last thing the
// CLI writes before exiting.
const sessionLostTailBytes = 4096

// tailWriter forwards every write to w (when non-nil) and keeps only the
// last max bytes written. go-runner writes a process's stderr from one
// goroutine, but Bytes is read after runner.Run returns, so the buffer is
// still guarded.
type tailWriter struct {
	w   io.Writer
	max int
	// onTail, when set, is called after each write with a copy of the current
	// tail. It runs on the writer's goroutine without the lock held.
	onTail func(tail []byte)

	mu  sync.Mutex
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	t.mu.Unlock()
	if t.onTail != nil {
		t.onTail(t.Bytes())
	}
	if t.w == nil {
		return len(p), nil
	}
	return t.w.Write(p)
}

func (t *tailWriter) Bytes() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.buf...)
}

// LivePID — PIDReporter. Subprocess-per-turn semantics: 0 between turns,
// non-zero only while a turn's runner.Run is active.
func (s *adapterSession) LivePID() int { return int(s.pid.Load()) }

// LastPID — PIDReporter. Sticky most-recent PID across turn boundaries;
// useful for log correlation post-exit.
func (s *adapterSession) LastPID() int { return int(s.lastPID.Load()) }

var _ PIDReporter = (*adapterSession)(nil)

// encodeStreamEvent renders ev for the attach broker. Plain-text
// EventDelta is forwarded as-is; everything else is rendered as a short
// labelled line so subscribers can see turn boundaries / errors / usage
// without the lib having to define a wire format. Adapters that want a
// richer wire format (jsonl, etc.) substitute their own Fanout-shaped
// writer at the consumer layer — this is just the default tee.
func encodeStreamEvent(ev llmtypes.StreamEvent) ([]byte, bool) {
	switch ev.Type {
	case llmtypes.EventDelta:
		if ev.Content == "" {
			return nil, false
		}
		return []byte(ev.Content), true
	case llmtypes.EventToolUse:
		if ev.ToolUse == nil {
			return nil, false
		}
		return []byte(fmt.Sprintf("\n[tool_use:%s]\n", ev.ToolUse.Name)), true
	case llmtypes.EventError:
		return []byte(fmt.Sprintf("\n[error] %s\n", ev.Error)), true
	case llmtypes.EventDone:
		return []byte("\n[turn_done]\n"), true
	}
	return nil, false
}
