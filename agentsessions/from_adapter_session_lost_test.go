package agentsessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
)

// sessionLostAdapter mimics `opencode run`: a resume turn passes
// "--session <id>" and a dead id fails with "Session not found" on stderr,
// no stdout and exit 1. The script hands out a fresh id on a turn that
// passes none.
type sessionLostAdapter struct {
	script string
	argv   [][]string
}

func (a *sessionLostAdapter) Name() string { return "session-lost-test" }

func (a *sessionLostAdapter) BuildArgs(_, _, sessionID string) []string {
	var args []string
	if sessionID != "" {
		args = []string{"--session", sessionID}
	}
	a.argv = append(a.argv, args)
	return args
}

func (a *sessionLostAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	s := strings.TrimRight(string(line), "\r\n")
	if id, ok := strings.CutPrefix(s, "session:"); ok {
		return []llmtypes.StreamEvent{{Type: llmtypes.EventSessionID, SessionID: id}}, nil
	}
	if s == "done" {
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDone}}, nil
	}
	return nil, nil
}

func (a *sessionLostAdapter) Detect() (string, bool) { return a.script, a.script != "" }

func (a *sessionLostAdapter) IsSessionLost(stderrTail []byte) bool {
	return strings.Contains(string(stderrTail), "Session not found")
}

var _ provider.SessionLostClassifier = (*sessionLostAdapter)(nil)

func writeSessionLostScript(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test script needs sh; not running on Windows")
	}
	path := filepath.Join(dir, "fake-opencode.sh")
	body := `#!/bin/sh
if [ "$1" = "--session" ]; then
  if [ "$2" = "ses_dead" ]; then
    printf 'Error: Session not found\n' 1>&2
    exit 1
  fi
  printf 'session:%s\n' "$2"
  printf 'done\n'
  exit 0
fi
printf 'session:ses_fresh\n'
printf 'done\n'
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// TestAdapterRuntime_SessionLost_DropsIDAndReturnsTypedError: a stale id
// fails turn N with an error matching provider.ErrProviderSessionLost and
// clears the stored id; turn N+1 runs without --session, and OnSessionID
// reports the new id. Nothing is retried on the caller's behalf.
func TestAdapterRuntime_SessionLost_DropsIDAndReturnsTypedError(t *testing.T) {
	dir := t.TempDir()
	adapter := &sessionLostAdapter{script: writeSessionLostScript(t, dir)}
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "session-lost",
		Kind:    "cli",
		Adapter: adapter,
		Caps:    Capabilities{ProviderSessionID: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	var stderrSeen strings.Builder
	var ids []string
	eventCh := make(chan llmtypes.StreamEvent, 16)
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:         dir,
		SessionIDPreset: "ses_dead",
		Stderr:          &stderrSeen,
		EventFanout:     eventCh,
		OnSessionID:     func(id string) { ids = append(ids, id) },
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()
	ider := sess.(SessionIDer)

	// Turn N: the stale id.
	err = sess.SendInput(context.Background(), []byte("turn N"))
	if !errors.Is(err, provider.ErrProviderSessionLost) {
		t.Fatalf("turn N err = %v; want ErrProviderSessionLost", err)
	}
	var lost *SessionLostError
	if !errors.As(err, &lost) || lost.RequestedID == "" || lost.Err == nil {
		t.Fatalf("turn N err = %#v; want *SessionLostError carrying RequestedID and Err", err)
	}
	if !strings.Contains(err.Error(), "ses_dead") {
		t.Errorf("turn N err %q does not name the lost id", err)
	}
	if got := ider.ProviderSessionID(); got != "" {
		t.Errorf("stored id after a lost session = %q; want it cleared", got)
	}
	if !strings.Contains(stderrSeen.String(), "Session not found") {
		t.Errorf("caller's Stderr did not receive the turn's stderr: %q", stderrSeen.String())
	}
	evs := drainEvents(eventCh)
	if len(evs) != 1 || evs[0].Type != llmtypes.EventError || !strings.Contains(evs[0].Error, provider.ErrProviderSessionLost.Error()) {
		t.Errorf("turn N events = %#v; want one EventError naming the lost session", evs)
	}

	// Turn N+1: no --session, and the fresh id is reported.
	if err := sess.SendInput(context.Background(), []byte("turn N+1")); err != nil {
		t.Fatalf("turn N+1: %v", err)
	}
	if len(adapter.argv) != 2 || adapter.argv[0][1] != "ses_dead" || len(adapter.argv[1]) != 0 {
		t.Errorf("argv per turn = %q; want --session ses_dead, then no --session", adapter.argv)
	}
	if got := ider.ProviderSessionID(); got != "ses_fresh" {
		t.Errorf("stored id after turn N+1 = %q; want ses_fresh", got)
	}
	if len(ids) != 1 || ids[0] != "ses_fresh" {
		t.Errorf("OnSessionID calls = %q; want [ses_fresh]", ids)
	}
}

// TestAdapterRuntime_SessionLost_OtherFailuresKeepID: a resume turn that
// fails for any other reason keeps the id and returns the plain error.
func TestAdapterRuntime_SessionLost_OtherFailuresKeepID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test script needs sh; not running on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'Error: rate limited\\n' 1>&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "session-kept",
		Kind:    "cli",
		Adapter: &sessionLostAdapter{script: path},
		Caps:    Capabilities{ProviderSessionID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, SessionIDPreset: "ses_live"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	err = sess.SendInput(context.Background(), []byte("x"))
	if err == nil || errors.Is(err, provider.ErrProviderSessionLost) {
		t.Fatalf("err = %v; want a plain failure", err)
	}
	if got := sess.(SessionIDer).ProviderSessionID(); got != "ses_live" {
		t.Errorf("stored id = %q; want ses_live kept", got)
	}
}

func TestTailWriter_KeepsLastBytes(t *testing.T) {
	var fwd strings.Builder
	tw := &tailWriter{w: &fwd, max: 8}
	for _, p := range []string{"abcdef", "ghij", "kl"} {
		if _, err := tw.Write([]byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(tw.Bytes()); got != "efghijkl" {
		t.Errorf("tail = %q; want efghijkl", got)
	}
	if fwd.String() != "abcdefghijkl" {
		t.Errorf("forwarded = %q", fwd.String())
	}
}
