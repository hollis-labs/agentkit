package agentsessions

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// agyLikeAdapter mimics agy: a resume of an unknown id warns on stderr,
// exits 0 and runs the turn in a new session; it keeps the id on a known
// resume, fails login with a stderr line, and parses typed events.
type agyLikeAdapter struct {
	script    string
	keepsID   bool
	preflight error
}

func (a *agyLikeAdapter) Name() string { return "agy-like-test" }

func (a *agyLikeAdapter) BuildArgs(_, _, sessionID string) []string {
	if sessionID != "" {
		return []string{"--conversation", sessionID}
	}
	return nil
}

func (a *agyLikeAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	s := strings.TrimRight(string(line), "\r\n")
	switch {
	case strings.HasPrefix(s, "session:"):
		return []llmtypes.StreamEvent{{Type: llmtypes.EventSessionID, SessionID: strings.TrimPrefix(s, "session:")}}, nil
	case s == "done", s == "denied":
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDone}}, nil
	}
	return nil, nil
}

func (a *agyLikeAdapter) ParseLineEvents(line []byte) ([]events.Event, error) {
	s := strings.TrimRight(string(line), "\r\n")
	switch {
	case strings.HasPrefix(s, "session:"):
		return []events.Event{events.SessionID{ID: strings.TrimPrefix(s, "session:")}}, nil
	case s == "denied":
		return []events.Event{events.PermissionDenied{Action: "command", DisplayName: "RunCommand"}, events.Done{}}, nil
	case s == "done":
		return []events.Event{events.Done{}}, nil
	}
	return nil, nil
}

func (a *agyLikeAdapter) Detect() (string, bool)     { return a.script, a.script != "" }
func (a *agyLikeAdapter) ResumeKeepsSessionID() bool { return a.keepsID }
func (a *agyLikeAdapter) Preflight() error           { return a.preflight }
func (a *agyLikeAdapter) IsSessionLost(b []byte) bool {
	return bytes.Contains(b, []byte(`" not found`))
}
func (a *agyLikeAdapter) IsNotAuthenticated(b []byte) bool {
	return bytes.Contains(b, []byte("authentication failed or timed out"))
}

var (
	_ provider.SessionResumeVerifier = (*agyLikeAdapter)(nil)
	_ provider.Preflighter           = (*agyLikeAdapter)(nil)
	_ provider.AuthFailureClassifier = (*agyLikeAdapter)(nil)
	_ provider.EventParser           = (*agyLikeAdapter)(nil)
)

// writeAgyLikeScript: `--conversation live` (or ses_new) keeps the id, any other id is
// replaced by ses_new with the agy warning; no id starts ses_first. A
// prompt file named "auth" makes it fail login, "deny" ends in a denial.
func writeAgyLikeScript(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test script needs sh")
	}
	path := filepath.Join(dir, "fake-agy.sh")
	body := `#!/bin/sh
if [ -f "` + filepath.Join(dir, "guiprobe") + `" ]; then
  out=$(/usr/bin/open -h 2>&1)
  printf '%s' "$out" > "` + filepath.Join(dir, "probe.out") + `"
  printf 'done\n'; exit 0
fi
if [ -f "` + filepath.Join(dir, "hangauth") + `" ]; then
  printf 'error: authentication failed or timed out\n' 1>&2
  sleep 30
  exit 0
fi
if [ -f "` + filepath.Join(dir, "auth") + `" ]; then
  printf 'error: authentication failed or timed out\n' 1>&2
  exit 1
fi
if [ -f "` + filepath.Join(dir, "noid") + `" ]; then
  printf 'warning: conversation "%s" not found\n' "$2" 1>&2
  printf 'done\n'; exit 0
fi
end=done
if [ -f "` + filepath.Join(dir, "deny") + `" ]; then end=denied; fi
if [ "$1" = "--conversation" ]; then
  if [ "$2" = "live" ] || [ "$2" = "ses_new" ]; then printf 'session:%s\n' "$2"; printf '%s\n' "$end"; exit 0; fi
  printf 'warning: conversation "%s" not found\n' "$2" 1>&2
  printf 'session:ses_new\n'; printf '%s\n' "$end"; exit 0
fi
printf 'session:ses_first\n'; printf '%s\n' "$end"
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type lostCall struct{ requested, actual, reason string }

func startAgyLike(t *testing.T, a *agyLikeAdapter, preset string) (Session, *bytes.Buffer, *[]lostCall, *[]events.Event, string) {
	t.Helper()
	dir := t.TempDir()
	a.script = writeAgyLikeScript(t, dir)
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "agy-like", Kind: "cli", Adapter: a, Caps: Capabilities{ProviderSessionID: true}})
	if err != nil {
		t.Fatal(err)
	}
	var fanout bytes.Buffer
	var lost []lostCall
	var typed []events.Event
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:            dir,
		SessionIDPreset:    preset,
		Fanout:             &fanout,
		TypedEventCallback: func(ev events.Event) { typed = append(typed, ev) },
		OnProviderSessionLost: func(r, act, reason string) {
			lost = append(lost, lostCall{r, act, reason})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	return sess, &fanout, &lost, &typed, dir
}

// startAgy runs the real provider.AntigravityAdapter on providertest runs of
// go-providers' live agy captures.
func startAgy(t *testing.T, preset string, runs ...providertest.Run) (Session, *syncBuffer, *[]lostCall, *[]events.Event) {
	t.Helper()
	fake := providertest.New(t, runtimes.Antigravity, runs...)
	adapter := provider.NewAntigravityAdapter()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "agy", Kind: "cli", Adapter: adapter, Caps: Capabilities{ProviderSessionID: true}})
	if err != nil {
		t.Fatal(err)
	}
	var fanout syncBuffer
	var lost []lostCall
	var typed []events.Event
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:            t.TempDir(),
		SessionIDPreset:    preset,
		Fanout:             &fanout,
		TypedEventCallback: func(ev events.Event) { typed = append(typed, ev) },
		OnProviderSessionLost: func(r, act, reason string) {
			lost = append(lost, lostCall{r, act, reason})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	return sess, &fanout, &lost, &typed
}

// agySessionID is the session id an agy capture reports.
func agySessionID(t *testing.T, name string) string {
	t.Helper()
	for _, line := range providertest.FixtureLines(t, name) {
		evs, _ := provider.NewAntigravityAdapter().ParseLine(line)
		for _, ev := range evs {
			if ev.Type == llmtypes.EventSessionID && ev.SessionID != "" {
				return ev.SessionID
			}
		}
	}
	t.Fatalf("%s reports no session id", name)
	return ""
}

// A resume that the provider answers with a different id reports the lost
// session once, alongside a turn that still succeeds.
func TestAdapterRuntime_ResumeReplacedSessionIsReported(t *testing.T) {
	// agy 1.2.7 resuming an id it does not have: it warns on stderr, exits
	// 0 and runs the turn in a new conversation (antigravity/
	// print_resume_unknown_id). The next turn resumes that new conversation,
	// which keeps its id: the same capture's stdout, without the warning.
	var resumed []providertest.Step
	for _, line := range providertest.FixtureLines(t, "antigravity/print_resume_unknown_id.jsonl") {
		resumed = append(resumed, providertest.Stdout(string(line)))
	}
	sess, fanout, lost, typed := startAgy(t, "ses_gone",
		providertest.Replay("antigravity/print_resume_unknown_id"),
		providertest.Script(resumed...),
	)
	actual := agySessionID(t, "antigravity/print_resume_unknown_id.jsonl")

	if err := sess.SendInput(context.Background(), []byte("turn N")); err != nil {
		t.Fatalf("turn N: %v (the turn itself succeeded)", err)
	}
	want := lostCall{"ses_gone", actual, sessionLostReason}
	if len(*lost) != 1 || (*lost)[0] != want {
		t.Fatalf("OnProviderSessionLost calls = %+v; want [%+v]", *lost, want)
	}
	var sawLost bool
	for _, ev := range *typed {
		if sl, ok := ev.(events.SessionLost); ok {
			sawLost = sl == events.SessionLost{RequestedID: "ses_gone", ActualID: actual, Reason: sessionLostReason}
		}
	}
	if !sawLost {
		t.Errorf("typed events = %#v; want an events.SessionLost", *typed)
	}
	out := fanout.String()
	marker := "[session_lost] requested=ses_gone actual=" + actual + ": " + sessionLostReason
	if !strings.Contains(out, marker) || strings.Index(out, marker) > strings.Index(out, "[turn_done]") {
		t.Errorf("fanout = %q; want the session_lost marker before [turn_done]", out)
	}
	if got := sess.(SessionIDer).ProviderSessionID(); got != actual {
		t.Errorf("stored id = %q; want %s", got, actual)
	}

	// The next turn resumes the new session and reports nothing.
	if err := sess.SendInput(context.Background(), []byte("turn N+1")); err != nil {
		t.Fatal(err)
	}
	if len(*lost) != 1 {
		t.Errorf("a clean resume reported a lost session: %+v", *lost)
	}
}

// agy resuming a conversation it has keeps its id (antigravity/
// print_turn2_resume resumes print_turn1's).
func TestAdapterRuntime_ResumeKeptSessionReportsNothing(t *testing.T) {
	sess, fanout, lost, _ := startAgy(t, agySessionID(t, "antigravity/print_turn1.jsonl"), providertest.Replay("antigravity/print_turn2_resume"))
	if err := sess.SendInput(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(*lost) != 0 || strings.Contains(fanout.String(), "[session_lost]") {
		t.Errorf("lost=%+v fanout=%q", *lost, fanout.String())
	}
}

// Without SessionResumeVerifier a changed id is not a loss: some CLIs may
// legitimately hand out a new id on resume.
func TestAdapterRuntime_ChangedIDWithoutVerifierIsNotALoss(t *testing.T) {
	sess, _, lost, _, _ := startAgyLike(t, &agyLikeAdapter{keepsID: false}, "ses_gone")
	if err := sess.SendInput(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(*lost) != 0 {
		t.Errorf("lost = %+v; want none", *lost)
	}
}

func TestAdapterRuntime_PreflightRefusesStart(t *testing.T) {
	a := &agyLikeAdapter{preflight: provider.ErrProviderNotAuthenticated, script: "/bin/sh"}
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "agy-like", Kind: "cli", Adapter: a, Caps: Capabilities{BinaryRequired: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Prepare(context.Background()); !errors.Is(err, provider.ErrProviderNotAuthenticated) {
		t.Fatalf("Prepare = %v; want ErrProviderNotAuthenticated", err)
	}
}

func TestAdapterRuntime_AuthFailureIsTyped(t *testing.T) {
	sess, fanout, _, typed, dir := startAgyLike(t, &agyLikeAdapter{keepsID: true}, "")
	if err := os.WriteFile(filepath.Join(dir, "auth"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sess.SendInput(context.Background(), []byte("x")); !errors.Is(err, provider.ErrProviderNotAuthenticated) {
		t.Fatalf("err = %v; want ErrProviderNotAuthenticated", err)
	}
	// CW-20260930-0137: the failure is also a typed event and a Fanout
	// marker, so a consumer reading events (not the SendInput error) sees it.
	var auth []events.AuthFailed
	for _, ev := range *typed {
		if a, ok := ev.(events.AuthFailed); ok {
			auth = append(auth, a)
		}
	}
	if len(auth) != 1 || auth[0].Message == "" {
		t.Errorf("typed AuthFailed = %#v; want one with a message", auth)
	}
	if !strings.Contains(fanout.String(), "[auth_failed]") {
		t.Errorf("fanout = %q; want an [auth_failed] marker", fanout.String())
	}
}

// Typed events are tapped on the subprocess path even without a
// TypedEventCallback, so a permission denial still reaches the byte Fanout.
func TestAdapterRuntime_TypedTapIsOnWithoutACallback(t *testing.T) {
	a := &agyLikeAdapter{keepsID: true}
	dir := t.TempDir()
	a.script = writeAgyLikeScript(t, dir)
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "agy-like", Kind: "cli", Adapter: a, Caps: Capabilities{ProviderSessionID: true}})
	if err != nil {
		t.Fatal(err)
	}
	var fanout bytes.Buffer
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, Fanout: &fanout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	if err := os.WriteFile(filepath.Join(dir, "deny"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sess.SendInput(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fanout.String(), "[permission_denied:command] RunCommand") {
		t.Errorf("fanout = %q; want the permission-denied marker without a callback", fanout.String())
	}
}

// agy's own denial of a command it was not allowed to run (antigravity/
// print_tool_denied) reaches the typed callback and the Fanout before the
// turn ends.
func TestAdapterRuntime_TypedTapSurfacesPermissionDenied(t *testing.T) {
	sess, fanout, _, typed := startAgy(t, "", providertest.Replay("antigravity/print_tool_denied"))
	if err := sess.SendInput(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	var denied []events.PermissionDenied
	for _, ev := range *typed {
		if d, ok := ev.(events.PermissionDenied); ok {
			denied = append(denied, d)
		}
	}
	if len(denied) == 0 {
		t.Fatalf("typed = %#v; want agy's permission denial", *typed)
	}
	out := fanout.String()
	marker := "[permission_denied:" + denied[0].Action + "]"
	if !strings.Contains(out, marker) || strings.Index(out, marker) > strings.Index(out, "[turn_done]") {
		t.Errorf("fanout = %q; want %s before [turn_done]", out, marker)
	}
}

// Secondary signal: the turn reported no id, but stderr says the requested
// session was not found.
func TestAdapterRuntime_ResumeLostByStderrOnly(t *testing.T) {
	sess, _, lost, _, dir := startAgyLike(t, &agyLikeAdapter{keepsID: true}, "ses_gone")
	if err := os.WriteFile(filepath.Join(dir, "noid"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sess.SendInput(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(*lost) != 1 || (*lost)[0].requested != "ses_gone" || (*lost)[0].actual != "" {
		t.Fatalf("lost = %+v", *lost)
	}
	if got := sess.(SessionIDer).ProviderSessionID(); got != "" {
		t.Errorf("stored id = %q; want it cleared", got)
	}
}

func TestAdapterRuntime_EndTurnOnAuthFailureDoesNotWaitOutTheCLI(t *testing.T) {
	a := &agyLikeAdapter{keepsID: true}
	dir := t.TempDir()
	a.script = writeAgyLikeScript(t, dir)
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "agy-like", Kind: "cli", Adapter: a, Caps: Capabilities{ProviderSessionID: true}})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, EndTurnOnAuthFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	if err := os.WriteFile(filepath.Join(dir, "hangauth"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = sess.SendInput(context.Background(), []byte("x"))
	if !errors.Is(err, provider.ErrProviderNotAuthenticated) {
		t.Fatalf("err = %v; want ErrProviderNotAuthenticated", err)
	}
	// The fake CLI sleeps 30s after printing the marker; ending early must not
	// wait that out.
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("turn took %v; the marker should have ended it immediately", d)
	}
}

// Through the real adapter runtime (go-runner spawns the child), DenyGUILaunch
// must make open(1) unexecutable inside the launched process.
func TestAdapterRuntime_DenyGUILaunchReachesTheChild(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not found")
	}
	for _, tc := range []struct {
		name string
		deny bool
	}{{"denied", true}, {"control", false}} {
		t.Run(tc.name, func(t *testing.T) {
			a := &agyLikeAdapter{keepsID: true}
			dir := t.TempDir()
			a.script = writeAgyLikeScript(t, dir)
			rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "agy-like", Kind: "cli", Adapter: a, Caps: Capabilities{ProviderSessionID: true}})
			if err != nil {
				t.Fatal(err)
			}
			sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, DenyGUILaunch: tc.deny})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sess.Stop(context.Background()) })
			if err := os.WriteFile(filepath.Join(dir, "guiprobe"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := sess.SendInput(context.Background(), []byte("x")); err != nil {
				t.Fatal(err)
			}
			probe, err := os.ReadFile(filepath.Join(dir, "probe.out"))
			if err != nil {
				t.Fatalf("the child did not run the probe: %v", err)
			}
			denied := strings.Contains(string(probe), "Operation not permitted")
			if denied != tc.deny {
				t.Fatalf("open denied = %v, want %v; probe output: %q", denied, tc.deny, probe)
			}
		})
	}
}
