//go:build !windows

package agentsessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	opts.TypedEventCallback = out.typed.callback
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
	waitExit(t, s.sess)
	// A restart, were there one, comes after the 10 ms backoff.
	time.Sleep(300 * time.Millisecond)

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
	if got := typed.list(); len(got) != 1 {
		t.Errorf("SessionLost events = %+v, want exactly one", got)
	}
	waitExit(t, sess)
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
	s.classifyExit(attempt(), exit)
	s.classifyExit(attempt(), exit)
	if got := typed.list(); len(got) != 1 {
		t.Errorf("SessionLost events = %+v, want exactly one for the session", got)
	}
}
