package agentsessions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A host may keep its own O_APPEND writer on the session log (Torque tees its
// first-turn error capture, redacted stderr and permission decisions into
// logs/session.log). Every line it writes, before Start or during the
// session, must survive the session's own writes: the log is opened for
// append and never truncated.
func TestSessionLog_HostAppendWriterSurvivesSession(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.log")
	if err := os.WriteFile(logPath, []byte("host: before start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close() }()

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "session-log-append",
		Kind:    "cli",
		Adapter: &echoAdapter{script: writeStreamingEchoScript(t, dir, "ses_log")},
		Caps:    Capabilities{StreamingStdio: true, BinaryRequired: true, ProviderSessionID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var fanout syncBuffer
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, LogPath: logPath, Fanout: &fanout})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	const hostLines, turns = 300, 30
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < hostLines; i++ {
			if _, err := fmt.Fprintf(host, "host: line %03d\n", i); err != nil {
				t.Errorf("host write: %v", err)
				return
			}
		}
	}()
	for i := 0; i < turns; i++ {
		if err := sess.SendInput(context.Background(), []byte(fmt.Sprintf("turn-%02d", i))); err != nil {
			t.Fatalf("SendInput: %v", err)
		}
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(fanout.String(), fmt.Sprintf("delta:turn-%02d", turns-1)) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := sess.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	_, _ = sess.Wait()

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		lines[line] = true
	}
	var missing []string
	if !lines["host: before start"] {
		missing = append(missing, "host: before start (the log was truncated at Start)")
	}
	for i := 0; i < hostLines; i++ {
		if want := fmt.Sprintf("host: line %03d", i); !lines[want] {
			missing = append(missing, want)
		}
	}
	for i := 0; i < turns; i++ {
		if want := fmt.Sprintf("delta:turn-%02d", i); !lines[want] {
			missing = append(missing, want+" (session line)")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d line(s) missing or torn in the session log, e.g. %q", len(missing), missing[:min(5, len(missing))])
	}
}
