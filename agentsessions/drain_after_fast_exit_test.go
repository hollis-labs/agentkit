//go:build !windows

package agentsessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CW-20261001-0046. A child that prints its last line and exits at once must
// still have that line delivered. The long-lived runtimes read the child's
// output from a file they own and drain it to EOF after Wait (see
// drainChildOutput); reading exec.Cmd's StdoutPipe, or closing the PTY master
// before reading it, lost the line whenever the child was reaped before the
// reader got to it. streaming-stdio's equivalent is
// TestStreamingStdioSession_DrainAfterFastExit.

// waitReaped polls until reaped reports true, failing after 5s.
func waitReaped(t *testing.T, reaped func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !reaped() {
		if time.Now().After(deadline) {
			t.Fatal("child was not reaped")
		}
		time.Sleep(time.Millisecond)
	}
}

func writeFastExitScript(t *testing.T, dir, line string) string {
	t.Helper()
	path := filepath.Join(dir, "fast-exit.sh")
	body := "#!/bin/sh\nprintf '%s\\n' " + shellQuote(line) + "\nexit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The reader is held back until the child has been reaped, which is the
// interleaving that used to lose the frame. Both lifecycle paths must still
// deliver it.
func TestJsonRpcStdioSession_DrainAfterFastExit(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		name := "legacy"
		if supervised {
			name = "supervised"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := writeFastExitScript(t, dir, `{"jsonrpc":"2.0","method":"turn/completed","params":{}}`)
			logFile, err := os.Create(filepath.Join(dir, "session.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = logFile.Close() }()
			notified := make(chan string, 1)
			s := &jsonRpcStdioSession{
				runtime: &jsonRpcStdioRuntime{cfg: AdapterRuntimeConfig{ID: name}},
				adapter: &minimalAdapter{binary: script},
				opts: StartOptions{Workdir: dir, JsonRpcNotificationHook: func(method string, _ json.RawMessage) {
					select {
					case notified <- method:
					default:
					}
				}},
				logFile: logFile, done: make(chan error, 1),
				copyDone: make(chan struct{}), stopRequested: make(chan struct{}),
				pending: make(map[int64]chan jsonRpcResponse),
			}
			s.alive.Store(true)
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
					if exit := s.waitOnceSupervised(context.Background(), cmd, stdin, gated, 0); exit != nil {
						finished <- exit
						return
					}
					finished <- nil
				}()
			} else {
				s.spawnReaderLegacy(gated)
				s.spawnWaiterLegacy(cmd, stdin, gated)
				go func() { _, err := s.Wait(); finished <- err }()
			}
			waitReaped(t, func() bool {
				s.ioLock.Lock()
				defer s.ioLock.Unlock()
				return s.stdin == nil
			})
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
			case method := <-notified:
				if method != "turn/completed" {
					t.Fatalf("notification = %q, want turn/completed", method)
				}
			default:
				t.Fatal("final frame lost after child exit")
			}
		})
	}
}

// The PTY reader starts only after the child has been reaped. The waiter
// used to close the master first, discarding what the child wrote; now it
// drains before closing.
func TestPTYSession_DrainAfterFastExit(t *testing.T) {
	dir := t.TempDir()
	script := writeFastExitScript(t, dir, "done")
	logFile, err := os.Create(filepath.Join(dir, "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	fanout := make(chan llmtypes.StreamEvent, 4)
	s := &ptySession{
		runtime: &ptyRuntime{cfg: AdapterRuntimeConfig{ID: "pty-fast-exit"}},
		adapter: &ptyEchoAdapter{scriptPath: script},
		opts:    StartOptions{Workdir: dir, EventFanout: fanout},
		logFile: logFile, done: make(chan error, 1),
		copyDone: make(chan struct{}), stopRequested: make(chan struct{}),
	}
	s.alive.Store(true)
	cmd, ptmx, cleanup, err := s.spawnAttempt(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	s.spawnWaiterLegacy(ptmx, cmd)
	waitReaped(t, func() bool {
		s.ptmxLock.Lock()
		defer s.ptmxLock.Unlock()
		return s.ptmx == nil
	})
	s.spawnReaderLegacy(ptmx, make(chan struct{}))

	finished := make(chan error, 1)
	go func() { _, err := s.Wait(); finished <- err }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not finish")
	}
	for {
		select {
		case ev := <-fanout:
			if ev.Type == llmtypes.EventDone {
				return
			}
		default:
			t.Fatal("final line lost after child exit")
		}
	}
}

// End to end through Start: a JSON-RPC child that prints its last frame and
// exits immediately. Run with -count to repeat it.
func TestJsonRpcStdioRuntime_FastExitDeliversFinalFrame(t *testing.T) {
	dir := t.TempDir()
	script := writeFastExitScript(t, dir, `{"jsonrpc":"2.0","method":"turn/completed","params":{}}`)
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "jsonrpc-fast-exit",
		Kind:    "cli",
		Adapter: &minimalAdapter{binary: script},
		Caps:    Capabilities{JsonRpcStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var methods []string
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: dir,
		LogPath: filepath.Join(dir, "session.log"),
		JsonRpcNotificationHook: func(method string, _ json.RawMessage) {
			mu.Lock()
			methods = append(methods, method)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 1 || methods[0] != "turn/completed" {
		t.Fatalf("notifications = %v, want [turn/completed]", methods)
	}
}

// serve-http's stdout and stderr carry logs and the listen URL rather than
// events, but they are drained the same way: a line printed just before exit
// reaches the session log.
func TestServeHTTPRuntime_FastExitKeepsFinalLines(t *testing.T) {
	dir := t.TempDir()
	quit := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"test"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ses_fast_exit"}`))
		case r.URL.Path == "/event":
			w.Header().Set("content-type", "text/event-stream")
			select {
			case <-r.Context().Done():
			case <-quit:
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer close(quit) // runs first: the event stream must end before Close waits on it

	path := filepath.Join(dir, "fake-opencode-serve.sh")
	body := `#!/bin/sh
printf 'opencode server listening on %s\n' "$TEST_SERVER_URL"
sleep 0.5
printf 'final stdout line\n'
printf 'final stderr line\n' 1>&2
exit 0
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "serve-http-fast-exit",
		Kind:    "cli",
		Adapter: &minimalAdapter{binary: path},
		Caps:    Capabilities{ServeHTTP: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "session.log")
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: dir,
		LogPath: logPath,
		Env:     append(os.Environ(), "TEST_SERVER_URL="+server.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = sess.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = sess.Stop(context.Background())
		t.Fatal("serve-http child did not exit")
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"final stdout line", "final stderr line"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("session log missing %q:\n%s", want, raw)
		}
	}
}
