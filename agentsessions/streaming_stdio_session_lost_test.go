//go:build !windows

package agentsessions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// These tests run the real Claude streaming adapter against go-providers'
// claude/stream_resume_unknown_id capture: a streaming session started to
// resume an id claude no longer has reads the first user message, answers it
// with an error result, writes "No conversation found with session ID: <id>"
// to stderr and exits 1. lostClaudeID is that id (from_adapter_session_lost_test.go).

const lostStreamTurn = `{"type":"user","message":{"role":"user","content":"say hi"}}`

// lostEvents collects the typed events a session reports.
type lostEvents struct {
	mu  sync.Mutex
	all []events.SessionLost
}

func (l *lostEvents) callback(ev events.Event) {
	if sl, ok := ev.(events.SessionLost); ok {
		l.mu.Lock()
		l.all = append(l.all, sl)
		l.mu.Unlock()
	}
}

func (l *lostEvents) list() []events.SessionLost {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]events.SessionLost(nil), l.all...)
}

type streamLostSession struct {
	sess   Session
	typed  *lostEvents
	fanout *syncBuffer
	stderr *syncBuffer
	log    string
}

func startClaudeStream(t *testing.T, adapter provider.CLIAdapter, opts StartOptions) streamLostSession {
	t.Helper()
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "stream-session-lost",
		Kind:    "cli",
		Adapter: adapter,
		Caps:    Capabilities{StreamingStdio: true, ProviderSessionID: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	out := streamLostSession{typed: &lostEvents{}, fanout: &syncBuffer{}, stderr: &syncBuffer{}}
	dir := t.TempDir()
	out.log = filepath.Join(dir, "session.log")
	opts.Workdir = dir
	opts.LogPath = out.log
	if opts.TypedEventCallback == nil {
		opts.TypedEventCallback = out.typed.callback
	}
	opts.Fanout = out.fanout
	if opts.Stderr == nil {
		opts.Stderr = out.stderr
	}
	out.sess, err = rt.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = out.sess.Stop(context.Background())
		_, _ = out.sess.Wait()
	})
	return out
}

func claudeStreamAdapter(fake *providertest.Fake) provider.CLIAdapter {
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = fake.Path
	return adapter
}

// waitExit waits for the session's child to be gone and reports Wait's code.
func waitExit(t *testing.T, sess Session) int {
	t.Helper()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := sess.Wait()
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		return r.code
	case <-time.After(10 * time.Second):
		t.Fatal("the session never exited")
		return 0
	}
}

// TestStreamingStdioSession_ResumeOfLostSessionIsReported: the resume fails;
// the loss is announced once, with the typed event and the Fanout marker,
// and the caller's stderr writer still gets the CLI's message. The session
// takes no more input, and says why. The dead id is no longer its provider
// session id.
func TestStreamingStdioSession_ResumeOfLostSessionIsReported(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume_unknown_id"))
	s := startClaudeStream(t, claudeStreamAdapter(fake), StartOptions{SessionIDPreset: lostClaudeID})
	if err := s.sess.SendInput(context.Background(), []byte(lostStreamTurn)); err != nil {
		t.Fatalf("SendInput of the turn the CLI answers with the error: %v", err)
	}
	if code := waitExit(t, s.sess); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	got := s.typed.list()
	if len(got) != 1 {
		t.Fatalf("SessionLost events = %+v, want exactly one", got)
	}
	if got[0].RequestedID != lostClaudeID || got[0].ActualID != "" || got[0].Reason != sessionLostFailedReason {
		t.Errorf("SessionLost = %+v", got[0])
	}
	if n := strings.Count(s.fanout.String(), "[session_lost] requested="+lostClaudeID); n != 1 {
		t.Errorf("the Fanout carries %d session_lost markers, want 1:\n%s", n, s.fanout.String())
	}
	if !strings.Contains(s.stderr.String(), "No conversation found with session ID: "+lostClaudeID) {
		t.Errorf("the caller's stderr writer lost the CLI's message: %q", s.stderr.String())
	}
	if id, ok := fake.Call(0).ArgAfter("--resume"); !ok || id != lostClaudeID {
		t.Errorf("the CLI was started with --resume %q, want %q", id, lostClaudeID)
	}

	err := s.sess.SendInput(context.Background(), []byte(lostStreamTurn))
	var lost *SessionLostError
	if !errors.As(err, &lost) || lost.RequestedID != lostClaudeID {
		t.Fatalf("SendInput after the loss = %v, want *SessionLostError for %s", err, lostClaudeID)
	}
	if !errors.Is(err, provider.ErrProviderSessionLost) || !errors.Is(err, ErrNoInputChannel) {
		t.Errorf("SendInput after the loss = %v, want it to match ErrProviderSessionLost and ErrNoInputChannel", err)
	}
	if id := s.sess.(SessionIDer).ProviderSessionID(); id != "" {
		t.Errorf("ProviderSessionID = %q after the loss, want it dropped", id)
	}
	if len(s.typed.list()) != 1 {
		t.Errorf("a later SendInput announced the loss again: %+v", s.typed.list())
	}
}

// TestStreamingStdioSession_LostSessionIsNotRestartedOrReportedTwice: with
// restart-on-crash on, the exit would be restarted with the same dead id, and
// fail the same way. The loss ends the session after one attempt.
func TestStreamingStdioSession_LostSessionIsNotRestartedOrReportedTwice(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/stream_resume_unknown_id"),
		providertest.Replay("claude/stream_resume_unknown_id"),
	)
	var restarts int
	var mu sync.Mutex
	s := startClaudeStream(t, claudeStreamAdapter(fake), StartOptions{
		SessionIDPreset: lostClaudeID,
		Supervisor: &SupervisorOptions{
			RestartOnCrash:    2,
			MaxRestartBackoff: 10 * time.Millisecond,
			OnRestart: func(int, *ExitError) {
				mu.Lock()
				restarts++
				mu.Unlock()
			},
		},
	})
	if err := s.sess.SendInput(context.Background(), []byte(lostStreamTurn)); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	// Wait returns once the supervisor has finished, so a restart, were
	// there one, has already happened.
	waitExit(t, s.sess)

	if got := s.typed.list(); len(got) != 1 {
		t.Errorf("SessionLost events = %+v, want exactly one", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts != 0 || len(fake.Calls()) != 1 {
		t.Errorf("the lost session was restarted: %d restarts, %d CLI starts", restarts, len(fake.Calls()))
	}
}

// plainStreamAdapter hides everything but provider.CLIAdapter, so the
// session sees an adapter with no SessionLostClassifier.
type plainStreamAdapter struct{ provider.CLIAdapter }

// TestStreamingStdioSession_SilentWithoutClassifierOrResumeID: the same
// failed run is not reported for an adapter that cannot classify it, or when
// no resume id was requested. The caller's stderr still gets every byte.
func TestStreamingStdioSession_SilentWithoutClassifierOrResumeID(t *testing.T) {
	cases := []struct {
		name    string
		adapter func(*providertest.Fake) provider.CLIAdapter
		preset  string
	}{
		{"adapter without a classifier", func(f *providertest.Fake) provider.CLIAdapter {
			return plainStreamAdapter{claudeStreamAdapter(f)}
		}, lostClaudeID},
		{"no resume id requested", claudeStreamAdapter, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume_unknown_id"))
			s := startClaudeStream(t, tc.adapter(fake), StartOptions{SessionIDPreset: tc.preset})
			if err := s.sess.SendInput(context.Background(), []byte(lostStreamTurn)); err != nil {
				t.Fatalf("SendInput: %v", err)
			}
			waitExit(t, s.sess)

			if got := s.typed.list(); len(got) != 0 {
				t.Errorf("SessionLost events = %+v, want none", got)
			}
			if strings.Contains(s.fanout.String(), "[session_lost]") {
				t.Errorf("the Fanout carries a session_lost marker:\n%s", s.fanout.String())
			}
			if !strings.Contains(s.stderr.String(), "No conversation found") {
				t.Errorf("the caller's stderr writer lost the CLI's message: %q", s.stderr.String())
			}
			err := s.sess.SendInput(context.Background(), []byte(lostStreamTurn))
			var lost *SessionLostError
			if errors.As(err, &lost) || !errors.Is(err, ErrNoInputChannel) {
				t.Errorf("SendInput after the exit = %v, want plain ErrNoInputChannel", err)
			}
		})
	}
}

// writeLostCLI writes a stand-in CLI that says the session is lost on
// stderr, closes its stdin, announces that on stdout, and exits with exit
// after a second, so a write to it fails while the process is still alive.
func writeLostCLI(t *testing.T, exit string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-lost.sh")
	body := `#!/bin/sh
printf 'No conversation found with session ID: ` + lostClaudeID + `\n' >&2
exec 0<&-
printf '{"type":"system","subtype":"stdin-closed"}\n'
sleep 1
exit ` + exit + `
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stand-in CLI: %v", err)
	}
	return path
}

func waitForFanout(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the Fanout never carried %q: %q", want, buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStreamingStdioSession_FailedWriteToLostSessionReportsTheLoss: the CLI
// has stopped reading when the turn is sent, so the write fails before the
// exit is classified. The caller still learns the session was lost, and the
// underlying write error stays reachable. With no StartOptions.Stderr the
// CLI's message goes to the session log.
func TestStreamingStdioSession_FailedWriteToLostSessionReportsTheLoss(t *testing.T) {
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = writeLostCLI(t, "1")
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "stream-session-lost-write", Kind: "cli", Adapter: adapter,
		Caps: Capabilities{StreamingStdio: true, ProviderSessionID: true}})
	if err != nil {
		t.Fatal(err)
	}
	typed := &lostEvents{}
	var fanout syncBuffer
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.log")
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: dir, LogPath: logPath, SessionIDPreset: lostClaudeID,
		TypedEventCallback: typed.callback, Fanout: &fanout,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })

	waitForFanout(t, &fanout, "stdin-closed")
	sendErr := sess.SendInput(context.Background(), []byte(lostStreamTurn))
	var lost *SessionLostError
	if !errors.As(sendErr, &lost) || lost.RequestedID != lostClaudeID {
		t.Fatalf("SendInput to the child that stopped reading = %v, want *SessionLostError", sendErr)
	}
	if !errors.Is(sendErr, syscall.EPIPE) {
		t.Errorf("SendInput = %v, want the broken pipe to stay reachable", sendErr)
	}
	if !errors.Is(sendErr, ErrNoInputChannel) || !errors.Is(sendErr, provider.ErrProviderSessionLost) {
		t.Errorf("SendInput = %v, want it to match ErrNoInputChannel and ErrProviderSessionLost, as a SendInput after the loss does", sendErr)
	}
	// The loss is decided before stdout is drained and announced after it, so
	// SendInput can return the error a moment before the event. The event is
	// out before Wait returns.
	waitExit(t, sess)
	if got := typed.list(); len(got) != 1 {
		t.Errorf("SessionLost events = %+v, want exactly one", got)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "No conversation found with session ID: "+lostClaudeID) {
		t.Errorf("the session log lost the CLI's message: %q", logged)
	}
}

// A resume attempt that exits cleanly was not lost, whatever its stderr
// said: only an abnormal exit is classified.
func TestStreamingStdioSession_CleanExitIsNotALostSession(t *testing.T) {
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = writeLostCLI(t, "0")
	s := startClaudeStream(t, adapter, StartOptions{SessionIDPreset: lostClaudeID})
	if code := waitExit(t, s.sess); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := s.typed.list(); len(got) != 0 {
		t.Errorf("SessionLost events = %+v, want none after a clean exit", got)
	}
}

// The loss is announced once per session. A restart does not happen after a
// loss, but if a later attempt's tail were classified too, it must not
// announce again.
func TestStreamingStdioSession_LossIsAnnouncedOncePerSession(t *testing.T) {
	typed := &lostEvents{}
	s := &streamingStdioSession{opts: StartOptions{TypedEventCallback: typed.callback}}
	s.lastSessionID.Store(lostClaudeID)
	attempt := func() *stderrCapture {
		c := &stderrCapture{
			classifier:  provider.NewClaudeAdapterStreamingStdio(),
			requestedID: lostClaudeID,
			tail:        &tailWriter{max: sessionLostTailBytes},
			classified:  make(chan struct{}),
		}
		_, _ = c.tail.Write([]byte("No conversation found with session ID: " + lostClaudeID + "\n"))
		return c
	}
	exit := errors.New("exit status 1")
	s.announceLost(s.classifyExit(attempt(), exit, false))
	s.announceLost(s.classifyExit(attempt(), exit, false))
	if got := typed.list(); len(got) != 1 {
		t.Errorf("SessionLost events = %+v, want exactly one for the session", got)
	}
}

// writeShellCLI writes a stand-in CLI: an sh script that stands for claude.
func writeShellCLI(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-stand-in.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write stand-in CLI: %v", err)
	}
	return path
}

func claudeStreamAdapterAt(binary string) provider.CLIAdapter {
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = binary
	return adapter
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func waitForText(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	waitForFanout(t, buf, want)
}

// stopQuickly stops a session whose child ignores EOF, without the default
// two-second wait for it.
func stopQuickly(t *testing.T, sess Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = sess.Stop(ctx)
}

// TestStreamingStdioSession_HealthyResumeIsNotLostWhenItLaterExits: a resumed
// CLI that got going (it emitted its init) and whose stderr, however late,
// mentions the same words, for instance in a tool's error line, did not fail to
// resume. Its abnormal exit is not a lost session: the id is kept, nothing is
// announced, and a supervisor still restarts it.
func TestStreamingStdioSession_HealthyResumeIsNotLostWhenItLaterExits(t *testing.T) {
	body := func(counter string) string {
		return `echo start >> '` + counter + `'
printf '{"type":"system","subtype":"init","session_id":"healthy-session"}\n'
printf 'tool error: No conversation found with session ID: ` + lostClaudeID + `\n' >&2
sleep 0.3
exit 1
`
	}

	t.Run("unsupervised", func(t *testing.T) {
		counter := filepath.Join(t.TempDir(), "starts")
		s := startClaudeStream(t, claudeStreamAdapterAt(writeShellCLI(t, body(counter))), StartOptions{SessionIDPreset: lostClaudeID})
		if code := waitExit(t, s.sess); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if got := s.typed.list(); len(got) != 0 {
			t.Errorf("SessionLost events = %+v, want none for a session that got going", got)
		}
		if strings.Contains(s.fanout.String(), "[session_lost]") {
			t.Errorf("the Fanout carries a session_lost marker:\n%s", s.fanout.String())
		}
		if id := s.sess.(SessionIDer).ProviderSessionID(); id != "healthy-session" {
			t.Errorf("ProviderSessionID = %q, want the id the CLI reported, kept", id)
		}
		err := s.sess.SendInput(context.Background(), []byte(lostStreamTurn))
		var lost *SessionLostError
		if errors.As(err, &lost) || !errors.Is(err, ErrNoInputChannel) {
			t.Errorf("SendInput after the exit = %v, want plain ErrNoInputChannel", err)
		}
	})

	t.Run("supervised restart is not stopped", func(t *testing.T) {
		counter := filepath.Join(t.TempDir(), "starts")
		var restarts atomic.Int32
		s := startClaudeStream(t, claudeStreamAdapterAt(writeShellCLI(t, body(counter))), StartOptions{
			SessionIDPreset: lostClaudeID,
			Supervisor: &SupervisorOptions{
				RestartOnCrash:    1,
				MaxRestartBackoff: 10 * time.Millisecond,
				OnRestart:         func(int, *ExitError) { restarts.Add(1) },
			},
		})
		waitExit(t, s.sess)
		if n := countLines(t, counter); n != 2 || restarts.Load() != 1 {
			t.Errorf("%d CLI starts and %d restarts, want 2 and 1: the crash was a crash, not a lost session", n, restarts.Load())
		}
		if got := s.typed.list(); len(got) != 0 {
			t.Errorf("SessionLost events = %+v, want none", got)
		}
	})
}

// TestStreamingStdioSession_StopIsNotALostSession: stopping a resumed session
// kills its child, an abnormal exit, and the child's stderr may mention the
// words. The session ended the attempt, so it is not classified, whether or
// not the child had got going.
func TestStreamingStdioSession_StopIsNotALostSession(t *testing.T) {
	cases := map[string]string{
		"before the CLI got going": `printf 'No conversation found with session ID: ` + lostClaudeID + `\n' >&2
sleep 30
`,
		"after the CLI got going": `printf '{"type":"system","subtype":"init","session_id":"healthy-session"}\n'
printf 'No conversation found with session ID: ` + lostClaudeID + `\n' >&2
sleep 30
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := startClaudeStream(t, claudeStreamAdapterAt(writeShellCLI(t, body)), StartOptions{SessionIDPreset: lostClaudeID})
			waitForText(t, s.stderr, "No conversation found")
			stopQuickly(t, s.sess)
			waitExit(t, s.sess)
			if got := s.typed.list(); len(got) != 0 {
				t.Errorf("SessionLost events = %+v after a Stop, want none", got)
			}
			if strings.Contains(s.fanout.String(), "[session_lost]") {
				t.Errorf("the Fanout carries a session_lost marker after a Stop:\n%s", s.fanout.String())
			}
		})
	}
}

// TestStreamingStdioSession_CrashOfAResumedSessionIsRestartedAsBefore: a
// resumed session that crashes for a reason unrelated to the provider having
// lost the session is restarted by its supervisor as it always was: two
// restarts, three starts, and no lost session. (Treating any abnormal exit of
// a resume as a loss would stop the first restart.)
func TestStreamingStdioSession_CrashOfAResumedSessionIsRestartedAsBefore(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "starts")
	cli := writeShellCLI(t, `echo start >> '`+counter+`'
printf 'fatal: out of memory\n' >&2
exit 1
`)
	var restarts atomic.Int32
	s := startClaudeStream(t, claudeStreamAdapterAt(cli), StartOptions{
		SessionIDPreset: lostClaudeID,
		Supervisor: &SupervisorOptions{
			RestartOnCrash:    2,
			MaxRestartBackoff: 10 * time.Millisecond,
			OnRestart:         func(int, *ExitError) { restarts.Add(1) },
		},
	})
	waitExit(t, s.sess)
	if n := countLines(t, counter); n != 3 || restarts.Load() != 2 {
		t.Errorf("%d CLI starts and %d restarts, want 3 and 2", n, restarts.Load())
	}
	if got := s.typed.list(); len(got) != 0 {
		t.Errorf("SessionLost events = %+v, want none for an unrelated crash", got)
	}
	if strings.Contains(s.fanout.String(), "[session_lost]") {
		t.Errorf("the Fanout carries a session_lost marker:\n%s", s.fanout.String())
	}
	if !strings.Contains(s.stderr.String(), "fatal: out of memory") {
		t.Errorf("the caller's stderr writer lost the CLI's message: %q", s.stderr.String())
	}
}

// TestStreamingStdioSession_SendInputFromAReaderCallbackSeesTheLoss: a
// callback runs on the goroutine that reads the child's stdout, and the
// waiter's drain of that stdout waits for it. A SendInput from such a callback
// after the child died must not wait on a classification that is itself
// waiting for the callback, and must report the loss, not a plain missing
// input channel. The loss is decided before stdout is drained.
func TestStreamingStdioSession_SendInputFromAReaderCallbackSeesTheLoss(t *testing.T) {
	cli := writeShellCLI(t, `printf 'No conversation found with session ID: `+lostClaudeID+`\n' >&2
printf '{"type":"error","message":"boom"}\n'
exit 1
`)
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "stream-session-lost-callback", Kind: "cli", Adapter: claudeStreamAdapterAt(cli),
		Caps: Capabilities{StreamingStdio: true, ProviderSessionID: true}})
	if err != nil {
		t.Fatal(err)
	}
	type sendResult struct {
		err     error
		elapsed time.Duration
	}
	var (
		once     sync.Once
		sess     Session
		ready    = make(chan struct{})
		result   = make(chan sendResult, 1)
		typed    = &lostEvents{}
		fanout   syncBuffer
		callback = func(ev events.Event) {
			typed.callback(ev)
			if _, ok := ev.(events.Error); !ok {
				return
			}
			once.Do(func() {
				<-ready
				// Hold the reader until the child is gone and its exit has
				// been classified, as it is after the stderr drain.
				impl := sess.(*streamingStdioSession)
				for deadline := time.Now().Add(2 * time.Second); impl.lost.Load() == nil && time.Now().Before(deadline); {
					time.Sleep(5 * time.Millisecond)
				}
				start := time.Now()
				err := sess.SendInput(context.Background(), []byte(lostStreamTurn))
				result <- sendResult{err, time.Since(start)}
			})
		}
	)
	dir := t.TempDir()
	sess, err = rt.Start(context.Background(), StartOptions{
		Workdir: dir, LogPath: filepath.Join(dir, "session.log"), SessionIDPreset: lostClaudeID,
		TypedEventCallback: callback, Fanout: &fanout,
	})
	close(ready)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { stopQuickly(t, sess); _, _ = sess.Wait() })

	select {
	case r := <-result:
		var lost *SessionLostError
		if !errors.As(r.err, &lost) || lost.RequestedID != lostClaudeID {
			t.Errorf("SendInput from the callback = %v, want *SessionLostError", r.err)
		}
		if !errors.Is(r.err, ErrNoInputChannel) {
			t.Errorf("SendInput from the callback = %v, want it to match ErrNoInputChannel", r.err)
		}
		if r.elapsed > time.Second {
			t.Errorf("SendInput from the callback took %v: it waited on a classification that waits on the callback", r.elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the callback's SendInput never returned")
	}
	waitExit(t, sess)
	if got := typed.list(); len(got) != 1 {
		t.Errorf("SessionLost events = %+v, want exactly one", got)
	}
}

// A CLI that has closed its stdin but is still running: a write fails with a
// broken pipe while the exit is far from classified.
func writeStdinClosedCLI(t *testing.T, ignoreTerm bool) string {
	t.Helper()
	trap := ""
	if ignoreTerm {
		trap = "trap '' TERM\n"
	}
	return writeShellCLI(t, trap+`printf 'No conversation found with session ID: `+lostClaudeID+`\n' >&2
exec 0<&-
printf '{"type":"system","subtype":"stdin-closed"}\n'
sleep 30
`)
}

// TestStreamingStdioSession_FailedWriteWaitEndsWithTheCallersCtx: the wait for
// a classification that may take long (the child is still running) is the
// caller's to cut short, and it is not a loss yet: the broken pipe is
// returned as it was.
func TestStreamingStdioSession_FailedWriteWaitEndsWithTheCallersCtx(t *testing.T) {
	s := startClaudeStream(t, claudeStreamAdapterAt(writeStdinClosedCLI(t, false)), StartOptions{SessionIDPreset: lostClaudeID})
	waitForFanout(t, s.fanout, "stdin-closed")
	t.Cleanup(func() { stopQuickly(t, s.sess) })

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.sess.SendInput(ctx, []byte(lostStreamTurn))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("SendInput took %v with a 150ms ctx", elapsed)
	}
	var lost *SessionLostError
	if err == nil || errors.As(err, &lost) || !errors.Is(err, syscall.EPIPE) {
		t.Errorf("SendInput = %v, want the broken pipe, unclassified", err)
	}
}

// TestStreamingStdioSession_FailedWriteWaitEndsWithStop: Stop ends the wait at
// once, though the child outlives it (it ignores SIGTERM until Stop's ctx ends).
func TestStreamingStdioSession_FailedWriteWaitEndsWithStop(t *testing.T) {
	s := startClaudeStream(t, claudeStreamAdapterAt(writeStdinClosedCLI(t, true)), StartOptions{SessionIDPreset: lostClaudeID})
	waitForFanout(t, s.fanout, "stdin-closed")

	returned := make(chan time.Time, 1)
	go func() {
		_ = s.sess.SendInput(context.Background(), []byte(lostStreamTurn))
		returned <- time.Now()
	}()
	time.Sleep(100 * time.Millisecond) // let the write fail and the wait begin
	stopCtx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	stopped := time.Now()
	go func() { _ = s.sess.Stop(stopCtx) }()
	select {
	case at := <-returned:
		if wait := at.Sub(stopped); wait > 1500*time.Millisecond {
			t.Errorf("SendInput returned %v after Stop: it kept waiting for the child's exit", wait)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendInput never returned after Stop")
	}
	waitExit(t, s.sess)
}

// TestIsDeadChildWrite: only a write that failed because the child is gone is
// worth waiting for a classification.
func TestIsDeadChildWrite(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{syscall.EPIPE, true},
		{fmt.Errorf("write |1: %w", syscall.EPIPE), true},
		{io.ErrClosedPipe, true},
		{os.ErrClosed, true},
		{ErrNoInputChannel, true},
		{errors.New("disk on fire"), false},
		{syscall.ENOSPC, false},
		{context.Canceled, false},
	} {
		if got := isDeadChildWrite(tc.err); got != tc.want {
			t.Errorf("isDeadChildWrite(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// A write that failed for another reason is returned at once, not after the
// classification wait, and is never turned into a loss.
func TestStreamingStdioSession_OtherWriteFailureDoesNotWait(t *testing.T) {
	s := &streamingStdioSession{stopRequested: make(chan struct{})}
	s.stderrCap = &stderrCapture{classified: make(chan struct{})} // never classified
	want := errors.New("disk on fire")
	start := time.Now()
	got := s.inputFailure(context.Background(), want)
	if got != want {
		t.Errorf("inputFailure = %v, want the error as it was", got)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("inputFailure waited %v for a classification it did not need", elapsed)
	}
}
