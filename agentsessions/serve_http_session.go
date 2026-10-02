//go:build !windows

package agentsessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
)

const serveHTTPReadyTimeout = 10 * time.Second

// serveHTTPRuntime is the agentsessions.Runtime backed by a long-lived CLI
// subprocess exposing an HTTP API. The target shape is opencode
// `serve --port 0 --hostname 127.0.0.1`: stdout/stderr carries the bound URL,
// /event carries server-sent events, and session turns are sent through
// /session/{id}/prompt_async.
type serveHTTPRuntime struct {
	cfg AdapterRuntimeConfig
}

func (r *serveHTTPRuntime) ID() string         { return r.cfg.ID }
func (r *serveHTTPRuntime) Kind() string       { return r.cfg.Kind }
func (r *serveHTTPRuntime) Caps() Capabilities { return r.cfg.Caps }

func (r *serveHTTPRuntime) Prepare(_ context.Context) error {
	if !r.cfg.Caps.BinaryRequired {
		return nil
	}
	if _, ok := r.cfg.Adapter.Detect(); !ok {
		return fmt.Errorf("agentsessions: adapter %q binary not found", r.cfg.Adapter.Name())
	}
	return nil
}

func (r *serveHTTPRuntime) Start(ctx context.Context, opts StartOptions) (Session, error) {
	var err error
	opts, err = normalizeStartOptions(opts)
	if err != nil {
		return nil, err
	}
	if opts.Workdir == "" {
		return nil, errors.New("agentsessions: StartOptions.Workdir is required for serve-http runtime")
	}

	bootDir, planted, sessionAdapter, err := preparePlant(opts, r.cfg.Adapter, r.cfg.ID)
	if err != nil {
		return nil, err
	}
	opts = planted

	logPath, err := resolveServeHTTPLogPath(opts)
	if err != nil {
		cleanupBootDir(bootDir)
		return nil, err
	}
	logF, err := openSessionLog(logPath)
	if err != nil {
		cleanupBootDir(bootDir)
		return nil, fmt.Errorf("agentsessions: open log: %w", err)
	}

	s := &serveHTTPSession{
		runtime:       r,
		adapter:       sessionAdapter,
		bootDir:       bootDir,
		opts:          opts,
		logFile:       logF,
		httpClient:    &http.Client{},
		readyURL:      make(chan string, 1),
		processDone:   make(chan error, 1),
		done:          make(chan error, 1),
		stopRequested: make(chan struct{}),
	}
	s.alive.Store(true)
	s.state.Store(int32(LiveStateIdle))
	s.lastSessionID.Store(opts.SessionIDPreset)

	if err := s.spawn(); err != nil {
		_ = logF.Close()
		cleanupBootDir(bootDir)
		return nil, err
	}
	go s.finishOnProcessExit()

	select {
	case base := <-s.readyURL:
		s.baseURL = strings.TrimRight(base, "/")
	case <-s.done:
		_, err := s.Wait()
		if err == nil {
			err = errors.New("process exited before printing listen URL")
		}
		return nil, fmt.Errorf("agentsessions: serve-http start: %w", err)
	case <-time.After(serveHTTPReadyTimeout):
		_ = s.Stop(context.Background())
		return nil, errors.New("agentsessions: serve-http start: timed out waiting for listen URL")
	case <-ctx.Done():
		_ = s.Stop(context.Background())
		return nil, ctx.Err()
	}

	if err := s.waitHealthy(ctx); err != nil {
		_ = s.Stop(context.Background())
		return nil, err
	}

	if opts.SessionIDPreset != "" {
		s.sessionID = opts.SessionIDPreset
	} else if err := s.createSession(ctx); err != nil {
		_ = s.Stop(context.Background())
		return nil, err
	}

	go s.runEventStream()
	// finishOnProcessExit is intentionally NOT started again here — it
	// was already started once at line ~97, right after spawn(). A
	// second call used to fire here too; both goroutines raced to
	// receive the single value on the buffered, close-once
	// s.processDone channel, so roughly half the time this second
	// (later-started) goroutine instead received the zero value off the
	// already-closed channel and won the s.waitOnce.Do race, silently
	// discarding the real exit error (including a real *ExitError from
	// an abnormal exit — the exact bug this file's finishOnProcessExit
	// fix above targets). One waiter, started once, is sufficient: it
	// observes the process's exit at any point in Start()'s lifetime,
	// not just after this point.

	if opts.AutoFireFirstTurn && len(opts.FirstTurnPayload) > 0 {
		if err := s.SendInput(ctx, opts.FirstTurnPayload); err != nil {
			_ = s.Stop(ctx)
			return nil, fmt.Errorf("agentsessions: auto-fire first turn: %w", err)
		}
	}

	return s, nil
}

func resolveServeHTTPLogPath(opts StartOptions) (string, error) {
	if opts.LogPath != "" {
		return opts.LogPath, nil
	}
	if opts.WorkspaceDir == "" {
		return "", errors.New("agentsessions: serve-http runtime requires StartOptions.LogPath or StartOptions.WorkspaceDir")
	}
	logPath := filepath.Join(opts.WorkspaceDir, "logs", "session.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", fmt.Errorf("agentsessions: ensure log dir: %w", err)
	}
	return logPath, nil
}

type serveHTTPSession struct {
	runtime *serveHTTPRuntime
	adapter provider.CLIAdapter
	bootDir string
	opts    StartOptions

	cmd      *exec.Cmd
	logFile  *os.File
	baseURL  string
	clientMu sync.Mutex

	httpClient *http.Client
	sessionID  string

	state      atomic.Int32
	alive      atomic.Bool
	startedPID atomic.Int32
	lastPID    atomic.Int32

	sandboxOutcome atomic.Value // SandboxOutcome

	lastSessionID atomic.Value // string

	readyURL    chan string
	processDone chan error
	done        chan error
	waitOnce    sync.Once
	waitCode    atomic.Int32
	waitErr     atomic.Value // error

	stopOnce      sync.Once
	stopRequested chan struct{}

	turnMu       sync.Mutex
	turnInFlight bool
	// turnBusy is set when OpenCode reports the turn in flight busy.
	// interruptedTurn marks a turn InterruptTurn aborted; once it ends,
	// afterAbort holds until the next turn ends. OpenCode follows an
	// abort with a late second session.idle as the aborted tool cleans up,
	// so after an abort a turn ends only once it has been reported busy.
	turnBusy        bool
	interruptedTurn bool
	afterAbort      bool
	// overflow is the turn's ContextOverflowError, held until OpenCode
	// either compacts and carries on (session.compacted) or goes idle
	// without compacting, which makes it the turn's failure.
	overflow []byte

	// compactionMessages are the ids of OpenCode's compaction summary
	// messages, whose deltas are not the reply; reasoningParts are the ids
	// of reasoning parts, whose deltas are the model thinking. Both hold
	// one turn's ids: SendInput resets them (resetTurnMarks).
	compactionMu       sync.Mutex
	compactionMessages map[string]bool
	reasoningParts     map[string]bool

	streamCancel context.CancelFunc
}

func (s *serveHTTPSession) SandboxOutcome() (SandboxOutcome, bool) {
	out, ok := s.sandboxOutcome.Load().(SandboxOutcome)
	return out, ok
}

func (s *serveHTTPSession) reportSandboxOutcome(out SandboxOutcome) {
	s.sandboxOutcome.Store(out)
	if s.opts.SandboxOutcomeCallback != nil {
		s.opts.SandboxOutcomeCallback(out)
	}
}

func (s *serveHTTPSession) spawn() error {
	binary, ok := s.adapter.Detect()
	if !ok {
		return fmt.Errorf("agentsessions: adapter %q binary not found", s.adapter.Name())
	}

	args, err := spawnArgs(s.adapter, s.opts, "", s.opts.BootPrompt, s.opts.SessionIDPreset)
	if err != nil {
		return err
	}

	cmd := exec.Command(binary, args...) //nolint:gosec // G204: adapter-sourced binary + args
	configureCommandProcessGroup(cmd)
	cmd.Dir = s.opts.Workdir
	if s.opts.Env != nil {
		cmd.Env = s.opts.Env
	}
	if len(s.opts.ExtraFiles) > 0 {
		cmd.ExtraFiles = s.opts.ExtraFiles
	}

	// Own stdout and stderr ourselves: exec.Cmd.Wait closes StdoutPipe and
	// StderrPipe immediately on exit, racing the scanners and discarding
	// the child's final lines. See drainChildOutput.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("agentsessions: stdout pipe: %w", err)
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		return fmt.Errorf("agentsessions: stderr pipe: %w", err)
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	closePipes := func() {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		_ = stderr.Close()
		_ = stderrWriter.Close()
	}

	sandboxOutcome, sandboxCleanup, err := prepareSandboxForCommand(cmd, s.opts)
	if err != nil {
		closePipes()
		return err
	}

	limitCleanup, err := applyResourceLimits(cmd, s.opts.ResourceLimits)
	if err != nil {
		closePipes()
		sandboxCleanup()
		return fmt.Errorf("agentsessions: apply resource limits: %w", err)
	}

	if err := cmd.Start(); err != nil {
		closePipes()
		limitCleanup()
		sandboxCleanup()
		return fmt.Errorf("agentsessions: start: %w", err)
	}
	// Only the child retains the write ends.
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	s.reportSandboxOutcome(sandboxOutcome)

	s.cmd = cmd
	if cmd.Process != nil {
		s.startedPID.Store(int32(cmd.Process.Pid))
		s.lastPID.Store(int32(cmd.Process.Pid))
	}

	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() { defer close(stdoutDone); s.scanProcessOutput(stdout) }()
	go func() { defer close(stderrDone); s.scanProcessOutput(stderr) }()
	go func() {
		err := cmd.Wait()
		drainChildOutput(stdout, stdoutDone)
		drainChildOutput(stderr, stderrDone)
		limitCleanup()
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		s.processDone <- err
		close(s.processDone)
	}()
	return nil
}

func (s *serveHTTPSession) scanProcessOutput(r io.Reader) {
	err := readLines(r, func(raw []byte) {
		line := string(raw)
		s.writeOutput([]byte(line + "\n"))
		if u := parseServeHTTPListenURL(line); u != "" {
			select {
			case s.readyURL <- u:
			default:
			}
		}
	}, func(n int) { noteOversizeLine(nil, "serve-http", s.runtime.cfg.ID, n) })
	if readerFailed(err) {
		// Keep draining so the child never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, r)
	}
}

func parseServeHTTPListenURL(line string) string {
	idx := strings.Index(line, "http://")
	if idx < 0 {
		idx = strings.Index(line, "https://")
	}
	if idx < 0 {
		return ""
	}
	fields := strings.Fields(line[idx:])
	if len(fields) == 0 {
		return ""
	}
	raw := strings.TrimRight(fields[0], ".,;)")
	if _, err := url.ParseRequestURI(raw); err != nil {
		return ""
	}
	return raw
}

func (s *serveHTTPSession) waitHealthy(ctx context.Context) error {
	deadline := time.Now().Add(serveHTTPReadyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/global/health", nil)
		if err != nil {
			return err
		}
		resp, err := s.httpClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("agentsessions: serve-http health: %w", err)
			}
			return errors.New("agentsessions: serve-http health: timeout")
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *serveHTTPSession) createSession(ctx context.Context) error {
	endpoint := s.withWorkdirQuery(s.baseURL + "/session")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agentsessions: create serve-http session: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("agentsessions: create serve-http session: status %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("agentsessions: decode serve-http session: %w", err)
	}
	if out.ID == "" {
		return errors.New("agentsessions: create serve-http session: empty id")
	}
	s.sessionID = out.ID
	s.lastSessionID.Store(out.ID)
	if s.opts.OnSessionID != nil {
		s.opts.OnSessionID(out.ID)
	}
	return nil
}

func (s *serveHTTPSession) withWorkdirQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("directory", s.opts.Workdir)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *serveHTTPSession) runEventStream() {
	ctx, cancel := context.WithCancel(context.Background())
	s.clientMu.Lock()
	s.streamCancel = cancel
	s.clientMu.Unlock()
	endpoint := s.withWorkdirQuery(s.baseURL + "/event")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("accept", "text/event-stream")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	readSSEData(resp.Body, s.handleSSEData)
}

// readSSEData calls emit with the data of each event in an SSE stream.
// Per the WHATWG event-stream format, an event's data lines join with
// "\n"; an event whose data is empty is not dispatched, and one the
// stream ends before terminating is dropped.
func readSSEData(r io.Reader, emit func([]byte)) {
	var (
		data    bytes.Buffer
		hasData bool
		// broken marks an event one of whose lines was too long to
		// read; it is dropped rather than dispatched incomplete.
		broken bool
	)
	_ = readLines(r, func(raw []byte) {
		line := string(raw)
		if line == "" {
			if data.Len() > 0 && !broken {
				emit(data.Bytes())
			}
			data.Reset()
			hasData, broken = false, false
			return
		}
		chunk, ok := strings.CutPrefix(line, "data")
		if !ok || (chunk != "" && chunk[0] != ':') {
			return // another field, or a comment
		}
		chunk = strings.TrimPrefix(strings.TrimPrefix(chunk, ":"), " ")
		if hasData {
			data.WriteByte('\n')
		}
		data.WriteString(chunk)
		hasData = true
	}, func(n int) {
		broken = true
		noteOversizeLine(nil, "serve-http", "event-stream", n)
	})
}

func (s *serveHTTPSession) handleSSEData(data []byte) {
	line := append(append([]byte(nil), data...), '\n')
	s.writeOutput(line)

	var ev struct {
		Type       string          `json:"type"`
		Directory  string          `json:"directory"`
		Payload    json.RawMessage `json:"payload"`
		Properties sseProperties   `json:"properties"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}

	payload := ev.Payload
	if len(payload) > 0 {
		var wrapped struct {
			Type       string        `json:"type"`
			Properties sseProperties `json:"properties"`
		}
		if err := json.Unmarshal(payload, &wrapped); err == nil && wrapped.Type != "" {
			ev.Type = wrapped.Type
			ev.Properties = wrapped.Properties
		}
	}

	if ev.Properties.SessionID != "" && ev.Properties.SessionID != s.sessionID {
		return
	}
	// An event that can end a turn counts only when it names this session.
	// OpenCode also reports errors that belong to no session (a plugin or
	// skill that failed to load); those are diagnostics, never a failure of
	// the turn in flight (CW-20261001-0193).
	if ev.Properties.SessionID == "" && endsTurn(ev.Type) {
		if ev.Type == "session.error" || ev.Type == "session.next.step.failed" {
			log.Printf("agentsessions: serve-http session %s: OpenCode reported an error outside any session, not a turn failure: %s", s.sessionID, data)
		}
		return
	}

	switch ev.Type {
	case "session.created":
		if ev.Properties.SessionID != "" {
			s.lastSessionID.Store(ev.Properties.SessionID)
			if s.opts.OnSessionID != nil {
				s.opts.OnSessionID(ev.Properties.SessionID)
			}
			tryEventFanout(s.opts.EventFanout, llmtypes.StreamEvent{Type: llmtypes.EventSessionID, SessionID: ev.Properties.SessionID})
		}
	case "message.updated":
		if ev.Properties.Info.isCompaction() {
			s.markCompactionMessage(ev.Properties.Info.ID)
		}
	case "message.part.updated":
		if ev.Properties.Part.Type == "reasoning" && ev.Properties.Part.ID != "" {
			s.markReasoningPart(ev.Properties.Part.ID)
		}
	case "message.part.delta", "session.next.text.delta":
		// A compaction summary streams as this session's deltas too, but
		// it is OpenCode condensing the context, not the reply; it stays
		// in the raw event stream above (CW-20261001-0198).
		if ev.Properties.Delta == "" || s.isCompactionMessage(ev.Properties.MessageID) {
			break
		}
		delta := llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: ev.Properties.Delta}
		// A reasoning part's text streams as the same delta event; only
		// its message.part.updated says it is the model thinking, not the
		// reply (CW-20261001-0209).
		if s.isReasoningPart(ev.Properties.PartID) {
			delta.Phase = llmtypes.PhaseThinking
			delta.BlockID = ev.Properties.PartID
		}
		tryEventFanout(s.opts.EventFanout, delta)
	case "session.status":
		if ev.Properties.Status.Type == "busy" {
			s.turnMu.Lock()
			s.turnBusy = s.turnInFlight
			s.turnMu.Unlock()
		}
	case "session.compacted":
		// OpenCode compacted the context after an overflow and carries on
		// with the turn.
		s.turnMu.Lock()
		s.overflow = nil
		s.turnMu.Unlock()
	case "session.idle", "session.next.step.ended":
		// Read before endTurn, which frees SendInput to start the next
		// turn and clear it. An idle that ends nothing leaves it held.
		s.turnMu.Lock()
		overflow := s.overflow
		s.turnMu.Unlock()
		if !s.endTurn() {
			return
		}
		if overflow != nil {
			// Idle without compacting: the overflow ended the turn
			// (compaction is off, or the session is too large to
			// compact).
			tryEventFanout(s.opts.EventFanout, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: string(overflow)})
			return
		}
		tryEventFanout(s.opts.EventFanout, llmtypes.StreamEvent{Type: llmtypes.EventDone})
	case "session.error", "session.next.step.failed":
		if ev.Properties.Error.Name == "ContextOverflowError" {
			// OpenCode publishes this before it compacts and carries on;
			// it is the turn's failure only if no compaction follows.
			s.turnMu.Lock()
			if s.turnInFlight {
				s.overflow = append([]byte(nil), data...)
			}
			s.turnMu.Unlock()
			return
		}
		if s.endTurn() {
			tryEventFanout(s.opts.EventFanout, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: string(data)})
		}
	}
}

// sseProperties are the fields of an OpenCode event's properties the session
// reads.
type sseProperties struct {
	SessionID string         `json:"sessionID"`
	MessageID string         `json:"messageID"`
	PartID    string         `json:"partID"`
	Delta     string         `json:"delta"`
	Info      sseMessageInfo `json:"info"`
	Part      ssePart        `json:"part"`
	Status    struct {
		Type string `json:"type"`
	} `json:"status"`
	Error struct {
		Name string `json:"name"`
	} `json:"error"`
}

// sseMessageInfo is the message a message.updated event describes.
type sseMessageInfo struct {
	ID      string          `json:"id"`
	Mode    string          `json:"mode"`
	Agent   string          `json:"agent"`
	Summary json.RawMessage `json:"summary"`
}

// ssePart is the part a message.part.updated event describes: its id and
// type ("text", "reasoning", "tool", "step-start", ...).
type ssePart struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// isCompaction reports whether the message is OpenCode's compaction summary:
// the assistant message SessionCompaction streams, with mode and agent
// "compaction" and summary true. A user message's summary is an object
// ({"diffs": [...]}), which is not this (OpenCode 1.18.33, captured live).
func (m sseMessageInfo) isCompaction() bool {
	return m.ID != "" && (m.Mode == "compaction" || m.Agent == "compaction" || string(m.Summary) == "true")
}

func (s *serveHTTPSession) markCompactionMessage(id string) {
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	if s.compactionMessages == nil {
		s.compactionMessages = map[string]bool{}
	}
	s.compactionMessages[id] = true
}

func (s *serveHTTPSession) markReasoningPart(id string) {
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	if s.reasoningParts == nil {
		s.reasoningParts = map[string]bool{}
	}
	s.reasoningParts[id] = true
}

// resetTurnMarks forgets the previous turn's reasoning parts and compaction
// messages (CW-20261001-0224). Their ids are unique and are consulted only
// for the deltas of the turn they belong to, so keeping them grew the maps
// for the life of the session. It runs when the next turn starts, not when
// one ends: a delta that follows its turn's ending event, as OpenCode sends
// after an abort, must still be told from the reply.
func (s *serveHTTPSession) resetTurnMarks() {
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	s.compactionMessages = nil
	s.reasoningParts = nil
}

func (s *serveHTTPSession) isReasoningPart(id string) bool {
	if id == "" {
		return false
	}
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	return s.reasoningParts[id]
}

func (s *serveHTTPSession) isCompactionMessage(id string) bool {
	if id == "" {
		return false
	}
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	return s.compactionMessages[id]
}

// endsTurn reports whether an OpenCode event type can end or reshape the
// turn in flight.
func endsTurn(typ string) bool {
	switch typ {
	case "session.idle", "session.next.step.ended", "session.error", "session.next.step.failed", "session.status", "session.compacted":
		return true
	}
	return false
}

func (s *serveHTTPSession) writeOutput(p []byte) {
	var sink io.Writer = s.logFile
	if s.opts.Fanout != nil {
		sink = io.MultiWriter(s.logFile, s.opts.Fanout)
	}
	_, _ = sink.Write(p)
}

func (s *serveHTTPSession) finishOnProcessExit() {
	err := <-s.processDone
	s.alive.Store(false)
	s.state.Store(int32(LiveStateStopped))
	s.waitOnce.Do(func() {
		// This runtime has no supervised variant at all (StartOptions.
		// Supervisor is never consulted here) — finishOnProcessExit is
		// its ONLY waiter path. See streamingStdioSession.
		// spawnWaiterLegacy for the full rationale: reuse buildExitError
		// so an abnormal exit — non-zero code or signal death — always
		// surfaces through Wait() as a real *agentsessions.ExitError
		// instead of a nil error. Cause is left empty; no Supervisor is
		// attached on this path. cmd.Wait() (called in spawn()'s own
		// goroutine, which sent err on s.processDone above) always
		// populates s.cmd.ProcessState before returning, so it's safe
		// to read here without additional synchronization.
		var ps *os.ProcessState
		if s.cmd != nil {
			ps = s.cmd.ProcessState
		}
		exitErr := buildExitError(ps, err, "")

		if exitErr == nil {
			s.waitCode.Store(0)
		} else {
			s.waitCode.Store(int32(exitErr.Code))
			s.waitErr.Store(exitErr)
		}
		_ = s.logFile.Close()
		cleanupBootDir(s.bootDir)
		if exitErr == nil {
			s.done <- nil
		} else {
			s.done <- exitErr
		}
		close(s.done)
	})
}

// endTurn ends the turn in flight and reports whether there was one to end.
// An idle or error with no turn in flight ends nothing: OpenCode follows an
// error with an idle, and an abort with a late second idle, and neither is a
// turn of its own. After an abort, a turn ends only once OpenCode has
// reported it busy, so that late idle cannot end the next turn.
func (s *serveHTTPSession) endTurn() bool {
	s.turnMu.Lock()
	if !s.turnInFlight || (s.afterAbort && !s.turnBusy) {
		s.turnMu.Unlock()
		return false
	}
	s.afterAbort, s.interruptedTurn = s.interruptedTurn, false
	s.turnMu.Unlock()
	s.markTurnDone()
	return true
}

func (s *serveHTTPSession) markTurnDone() {
	s.turnMu.Lock()
	s.turnInFlight = false
	s.turnMu.Unlock()
	if s.alive.Load() {
		s.state.Store(int32(LiveStateIdle))
	}
}

func (s *serveHTTPSession) Wait() (int, error) {
	<-s.done
	code := int(s.waitCode.Load())
	var err error
	if v := s.waitErr.Load(); v != nil {
		err, _ = v.(error)
	}
	return code, err
}

func (s *serveHTTPSession) Stop(ctx context.Context) error {
	var killErr error
	s.stopOnce.Do(func() {
		s.alive.Store(false)
		s.state.Store(int32(LiveStateStopped))
		close(s.stopRequested)
		s.clientMu.Lock()
		cancel := s.streamCancel
		s.clientMu.Unlock()
		if cancel != nil {
			cancel()
		}

		_ = s.postNoBody(ctx, s.baseURL+"/global/dispose")
		if s.sessionID != "" {
			_ = s.postNoBody(ctx, s.withWorkdirQuery(s.baseURL+"/session/"+url.PathEscape(s.sessionID)+"/abort"))
		}

		cmd := s.cmd
		if cmd == nil || cmd.Process == nil {
			return
		}
		select {
		case <-s.done:
			return
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
		}
		if err := signalProcessGroup(cmd, syscall.SIGTERM); err != nil {
			return
		}
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			killErr = signalProcessGroup(cmd, syscall.SIGKILL)
		case <-ctx.Done():
			killErr = signalProcessGroup(cmd, syscall.SIGKILL)
		}
	})
	return killErr
}

func (s *serveHTTPSession) postNoBody(ctx context.Context, endpoint string) error {
	if endpoint == "" || s.baseURL == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

func (s *serveHTTPSession) SendInput(ctx context.Context, data []byte) error {
	if !s.alive.Load() {
		return ErrNoInputChannel
	}
	s.turnMu.Lock()
	if s.turnInFlight {
		s.turnMu.Unlock()
		return ErrTurnInFlight
	}
	s.turnInFlight = true
	s.turnBusy = false
	s.overflow = nil
	s.turnMu.Unlock()
	s.resetTurnMarks()

	body, err := json.Marshal(map[string]any{
		"parts": []map[string]any{{
			"type": "text",
			"text": string(data),
		}},
	})
	if err != nil {
		s.markTurnDone()
		return err
	}
	endpoint := s.withWorkdirQuery(s.baseURL + "/session/" + url.PathEscape(s.sessionID) + "/prompt_async")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		s.markTurnDone()
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.markTurnDone()
		return fmt.Errorf("agentsessions: serve-http send input: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.markTurnDone()
		return fmt.Errorf("agentsessions: serve-http send input: status %d: %s", resp.StatusCode, string(respBody))
	}
	s.state.Store(int32(LiveStateProcessing))
	return nil
}

func (s *serveHTTPSession) Resize(_ context.Context, _ uint16, _ uint16) error {
	return nil
}

func (s *serveHTTPSession) Health() HealthStatus {
	pid := 0
	if s.alive.Load() {
		pid = int(s.startedPID.Load())
	}
	return HealthStatus{
		Alive: s.alive.Load(),
		PID:   pid,
		State: LiveState(s.state.Load()),
	}
}

func (s *serveHTTPSession) CheckpointHints() (CheckpointHint, bool) {
	if !s.runtime.cfg.Caps.CheckpointResume {
		return nil, false
	}
	id, _ := s.lastSessionID.Load().(string)
	if id == "" {
		return nil, false
	}
	return CheckpointHint(id), true
}

func (s *serveHTTPSession) LivePID() int {
	if !s.alive.Load() {
		return 0
	}
	return int(s.startedPID.Load())
}

func (s *serveHTTPSession) LastPID() int {
	return int(s.lastPID.Load())
}

func (s *serveHTTPSession) ProviderSessionID() string {
	id, _ := s.lastSessionID.Load().(string)
	return id
}

var (
	_ Runtime     = (*serveHTTPRuntime)(nil)
	_ Session     = (*serveHTTPSession)(nil)
	_ PIDReporter = (*serveHTTPSession)(nil)
)
