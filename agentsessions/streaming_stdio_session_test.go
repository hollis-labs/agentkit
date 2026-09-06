//go:build !windows

package agentsessions

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

type gatedStdoutReader struct {
	io.ReadCloser
	ready <-chan struct{}
}

func (r gatedStdoutReader) Read(p []byte) (int, error) {
	<-r.ready
	return r.ReadCloser.Read(p)
}

func TestStreamingStdioSession_DrainBoundsInheritedPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	readerDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		close(readerDone)
	}()
	finished := make(chan struct{})
	go func() {
		drainStreamingStdout(reader, readerDone)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("drain blocked on inherited stdout writer")
	}
}

// Force the child to exit before its stdout reader gets scheduled. Both
// lifecycle paths must preserve buffered terminal events after process exit.
func TestStreamingStdioSession_DrainAfterFastExit(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		name := "legacy"
		if supervised {
			name = "supervised"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			adapter := &echoAdapter{script: writeTestScript(t, dir, []string{"done"})}
			logFile, err := os.Create(filepath.Join(dir, "session.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = logFile.Close() }()
			fanout := make(chan llmtypes.StreamEvent, 1)
			s := &streamingStdioSession{
				runtime: &streamingStdioRuntime{cfg: AdapterRuntimeConfig{ID: name}},
				adapter: adapter,
				opts:    StartOptions{Workdir: dir, EventFanout: fanout},
				logFile: logFile, done: make(chan error, 1),
				copyDone: make(chan struct{}), stopRequested: make(chan struct{}),
			}
			cmd, stdin, stdout, cleanup, err := s.spawnAttempt(0)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ready := make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(ready) })
			gated := gatedStdoutReader{ReadCloser: stdout, ready: ready}
			finished := make(chan error, 1)
			if supervised {
				s.opts.Supervisor = &SupervisorOptions{}
				go func() {
					exit := s.waitOnceSupervised(context.Background(), cmd, stdin, gated, 0)
					if exit != nil {
						finished <- exit
					} else {
						finished <- nil
					}
				}()
			} else {
				s.spawnReaderLegacy(gated)
				s.spawnWaiterLegacy(cmd, stdin, gated)
				go func() { _, err := s.Wait(); finished <- err }()
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				s.ioLock.Lock()
				reaped := s.stdin == nil
				s.ioLock.Unlock()
				if reaped {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child was not reaped")
				}
				time.Sleep(time.Millisecond)
			}
			release.Do(func() { close(ready) })
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("wait did not finish")
			}
			select {
			case ev := <-fanout:
				if ev.Type != llmtypes.EventDone {
					t.Fatalf("event = %v, want done", ev)
				}
			default:
				t.Fatal("terminal event lost after child exit")
			}
		})
	}
}

// syncBuffer is a thread-safe bytes.Buffer wrapper for fanout in tests.
// The reader goroutine writes to it concurrently with the test goroutine
// polling String(); the bare bytes.Buffer is unsafe under -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// writeStreamingEchoScript drops a tiny shell script that emits a session
// init line, then reads stdin line by line and emits one delta + done per
// line. Used by streaming-stdio tests to drive a real long-lived child.
func writeStreamingEchoScript(t *testing.T, dir, sessionID string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-streaming.sh")
	body := "#!/bin/sh\n"
	if sessionID != "" {
		body += "printf 'session:%s\\n' " + shellQuote(sessionID) + "\n"
	}
	body += `while IFS= read -r line; do
    printf 'delta:%s\n' "$line"
    printf 'done\n'
done
exit 0
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestStreamingStdioSession_HappyPath_BootSendInputStop(t *testing.T) {
	dir := t.TempDir()
	script := writeStreamingEchoScript(t, dir, "ses_streaming")

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "streaming-happy",
		Kind:    "cli",
		Adapter: &echoAdapter{script: script},
		Caps: Capabilities{
			StreamingStdio:    true,
			BinaryRequired:    true,
			ProviderSessionID: true,
			CheckpointResume:  true,
		},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	if err := rt.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	var fanout syncBuffer
	var observedSessionID atomic.Value
	observedSessionID.Store("")
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		LogPath:     filepath.Join(dir, "session.log"),
		Fanout:      &fanout,
		OnSessionID: func(id string) { observedSessionID.Store(id) },
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	if err := sess.SendInput(context.Background(), []byte("hello")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	// Wait briefly for the reader goroutine to drain the echo + session id.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sid, _ := observedSessionID.Load().(string)
		if sid != "" && strings.Contains(fanout.String(), "delta:hello") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	gotSID, _ := observedSessionID.Load().(string)
	if gotSID != "ses_streaming" {
		t.Errorf("OnSessionID got %q, want ses_streaming", gotSID)
	}
	out := fanout.String()
	if !strings.Contains(out, "session:ses_streaming") {
		t.Errorf("fanout missing session line: %q", out)
	}
	if !strings.Contains(out, "delta:hello") {
		t.Errorf("fanout missing delta:hello: %q", out)
	}

	sider, ok := sess.(SessionIDer)
	if !ok {
		t.Fatal("session does not implement SessionIDer")
	}
	if sider.ProviderSessionID() != "ses_streaming" {
		t.Errorf("ProviderSessionID = %q", sider.ProviderSessionID())
	}

	hint, ok := sess.CheckpointHints()
	if !ok || string(hint) != "ses_streaming" {
		t.Errorf("CheckpointHints = (%q, %v); want (ses_streaming, true)", string(hint), ok)
	}

	if err := sess.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if _, err := sess.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestStreamingStdioSession_MultiTurnSameProcess(t *testing.T) {
	dir := t.TempDir()
	script := writeStreamingEchoScript(t, dir, "")

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "streaming-multiturn",
		Kind:    "cli",
		Adapter: &echoAdapter{script: script},
		Caps:    Capabilities{StreamingStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	var fanout syncBuffer
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: dir,
		LogPath: filepath.Join(dir, "session.log"),
		Fanout:  &fanout,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	reporter, ok := sess.(PIDReporter)
	if !ok {
		t.Fatal("session does not implement PIDReporter")
	}
	pid1 := reporter.LivePID()
	if pid1 == 0 {
		t.Fatal("LivePID = 0 after Start")
	}

	if err := sess.SendInput(context.Background(), []byte("first")); err != nil {
		t.Fatalf("SendInput first: %v", err)
	}
	if err := sess.SendInput(context.Background(), []byte("second")); err != nil {
		t.Fatalf("SendInput second: %v", err)
	}

	// Wait for both echoes to flow through the reader goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(fanout.String(), "delta:first") && strings.Contains(fanout.String(), "delta:second") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	out := fanout.String()
	if !strings.Contains(out, "delta:first") || !strings.Contains(out, "delta:second") {
		t.Errorf("fanout = %q, want both turns", out)
	}

	pid2 := reporter.LivePID()
	if pid2 != pid1 {
		t.Errorf("PID changed across turns: %d -> %d (expected same long-lived child)", pid1, pid2)
	}
}

func TestStreamingStdioSession_AutoFireFirstTurn(t *testing.T) {
	dir := t.TempDir()
	script := writeStreamingEchoScript(t, dir, "")

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "streaming-autofire",
		Kind:    "cli",
		Adapter: &echoAdapter{script: script},
		Caps:    Capabilities{StreamingStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	var fanout syncBuffer
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:           dir,
		LogPath:           filepath.Join(dir, "session.log"),
		Fanout:            &fanout,
		AutoFireFirstTurn: true,
		FirstTurnPayload:  []byte("auto-kickoff"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(fanout.String(), "delta:auto-kickoff") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("autofire kickoff never observed in fanout: %q", fanout.String())
}

func TestStreamingStdioSession_StopClosesStdinAndExits(t *testing.T) {
	dir := t.TempDir()
	script := writeStreamingEchoScript(t, dir, "")

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "streaming-stop",
		Kind:    "cli",
		Adapter: &echoAdapter{script: script},
		Caps:    Capabilities{StreamingStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: dir,
		LogPath: filepath.Join(dir, "session.log"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := sess.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	code, werr := sess.Wait()
	if werr != nil {
		t.Errorf("Wait error after Stop: %v", werr)
	}
	// sh script exits 0 on stdin EOF.
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (clean stdin-EOF exit)", code)
	}

	// SendInput after Stop is a write to a closed channel.
	if err := sess.SendInput(context.Background(), []byte("after-stop")); err != ErrNoInputChannel {
		t.Errorf("SendInput after Stop = %v, want ErrNoInputChannel", err)
	}
}

// TestStreamingStdioSession_ExternalSigkill_SurfacesExitError is the
// real-subprocess regression test for agentkit's unsupervised
// legacy-waiter bug: spawnWaiterLegacy never constructed a real
// *ExitError for cmd.Wait()'s *exec.ExitError branch (any abnormal exit,
// signal deaths included), so Wait() returned a nil error for a
// genuinely SIGKILL'd process — indistinguishable from a clean exit to a
// caller classifying via errors.As(err, &xe) (e.g.
// internal/recovery/broker in the consuming app). No Supervisor is
// configured here — this is exactly the "no Nanite CLI session
// configures a Supervisor today" path the finding is about.
//
// A cmd.Wait()-level fake would hide this specific bug (it's about how a
// real *exec.ExitError from a real killed child gets translated by the
// errors.As branching), so this spawns a real child via the real
// streaming-stdio runtime and kills it externally with a real SIGKILL —
// matching the finding's own `kill -9` repro against a real process,
// never a mock.
func TestStreamingStdioSession_ExternalSigkill_SurfacesExitError(t *testing.T) {
	dir := t.TempDir()
	script := writeStreamingEchoScript(t, dir, "")

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "streaming-sigkill",
		Kind:    "cli",
		Adapter: &echoAdapter{script: script},
		Caps:    Capabilities{StreamingStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: dir,
		LogPath: filepath.Join(dir, "session.log"),
		// StartOptions.Supervisor intentionally left nil — this test
		// targets the unsupervised legacy waiter (spawnWaiterLegacy),
		// the only waiter path a real Nanite CLI session exercises
		// today.
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	reporter, ok := sess.(PIDReporter)
	if !ok {
		t.Fatal("session does not implement PIDReporter")
	}
	pid := reporter.LivePID()
	if pid == 0 {
		t.Fatal("LivePID = 0 after Start")
	}

	// Real external SIGKILL against the real live child — not a fake,
	// not a Stop()-driven graceful shutdown. The echo script is already
	// blocked reading stdin at this point, so no input needs to be sent
	// first; this mirrors "kill -9 a real, live, healthy claude CLI
	// subprocess" from the finding.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("syscall.Kill(%d, SIGKILL): %v", pid, err)
	}

	code, waitErr := sess.Wait()
	if waitErr == nil {
		t.Fatal("Wait() returned a nil error for a SIGKILL'd process — legacy-waiter regression")
	}

	var xe *ExitError
	if !errors.As(waitErr, &xe) {
		t.Fatalf("Wait() error = %v (%T), want errors.As-extractable *ExitError", waitErr, waitErr)
	}
	if xe.Signal != int(syscall.SIGKILL) {
		t.Errorf("ExitError.Signal = %d, want %d (SIGKILL)", xe.Signal, syscall.SIGKILL)
	}
	if !xe.Killed {
		t.Error("ExitError.Killed = false, want true for a SIGKILL death")
	}
	if xe.Code != -1 {
		t.Errorf("ExitError.Code = %d, want -1 (signal death, matching exec.ExitError.ExitCode() convention)", xe.Code)
	}
	if xe.Cause != "" {
		t.Errorf("ExitError.Cause = %q, want empty (no Supervisor attached on this path)", xe.Cause)
	}
	if code != -1 {
		t.Errorf("Wait() code = %d, want -1", code)
	}
}

func TestStreamingStdioSession_RequiresWorkdir(t *testing.T) {
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "streaming-no-workdir",
		Kind:    "cli",
		Adapter: &echoAdapter{script: "/bin/true"},
		Caps:    Capabilities{StreamingStdio: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	_, err = rt.Start(context.Background(), StartOptions{})
	if err == nil {
		t.Fatal("Start without Workdir = nil, want error")
	}
	if !strings.Contains(err.Error(), "Workdir") {
		t.Errorf("error = %v, want one mentioning Workdir", err)
	}
}
