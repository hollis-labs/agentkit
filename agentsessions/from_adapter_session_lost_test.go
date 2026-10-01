package agentsessions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// The session-lost tests run the real Claude print adapter against
// go-providers' live captures (claude 2.1.286): a resume of an id claude no
// longer has fails with "No conversation found with session ID: <id>" on
// stderr and exit 1, which ClaudeAdapter.IsSessionLost recognises.

// lostClaudeID is the id the claude/print_resume_unknown_id capture resumed.
const lostClaudeID = "00000000-0000-4000-8000-0000000000ff"

func startClaudePrint(t *testing.T, fake *providertest.Fake, opts StartOptions) Session {
	t.Helper()
	adapter := provider.NewClaudeAdapter()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "session-lost",
		Kind:    "cli",
		Adapter: adapter,
		Caps:    Capabilities{ProviderSessionID: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	opts.Workdir = t.TempDir()
	sess, err := rt.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	return sess
}

// captureSessionID is the session id a per-turn capture reports.
func captureSessionID(t *testing.T, name string) string {
	t.Helper()
	for _, line := range providertest.FixtureLines(t, name) {
		var ev struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(line, &ev) == nil && ev.SessionID != "" {
			return ev.SessionID
		}
	}
	t.Fatalf("%s reports no session id", name)
	return ""
}

// TestAdapterRuntime_SessionLost_DropsIDAndReturnsTypedError: a stale id
// fails turn N with an error matching provider.ErrProviderSessionLost and
// clears the stored id; turn N+1 runs without --resume, and OnSessionID
// reports the new id. Nothing is retried on the caller's behalf.
func TestAdapterRuntime_SessionLost_DropsIDAndReturnsTypedError(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/print_resume_unknown_id"),
		providertest.Replay("claude/print_turn1"),
	)
	var stderrSeen strings.Builder
	var ids []string
	var lostEvents []events.SessionLost
	var fanout syncBuffer
	eventCh := make(chan llmtypes.StreamEvent, 16)
	sess := startClaudePrint(t, fake, StartOptions{
		SessionIDPreset: lostClaudeID,
		Stderr:          &stderrSeen,
		EventFanout:     eventCh,
		Fanout:          &fanout,
		OnSessionID:     func(id string) { ids = append(ids, id) },
		TypedEventCallback: func(ev events.Event) {
			if lost, ok := ev.(events.SessionLost); ok {
				lostEvents = append(lostEvents, lost)
			}
		},
		OnProviderSessionLost: func(string, string, string) {
			t.Error("OnProviderSessionLost reports a turn that ran on in a new session; this one failed")
		},
	})
	ider := sess.(SessionIDer)

	// Turn N: the stale id.
	err := sess.SendInput(context.Background(), []byte("turn N"))
	if !errors.Is(err, provider.ErrProviderSessionLost) {
		t.Fatalf("turn N err = %v; want ErrProviderSessionLost", err)
	}
	var lost *SessionLostError
	if !errors.As(err, &lost) || lost.RequestedID == "" || lost.Err == nil {
		t.Fatalf("turn N err = %#v; want *SessionLostError carrying RequestedID and Err", err)
	}
	if !strings.Contains(err.Error(), lostClaudeID) {
		t.Errorf("turn N err %q does not name the lost id", err)
	}
	if got := ider.ProviderSessionID(); got != "" {
		t.Errorf("stored id after a lost session = %q; want it cleared", got)
	}
	if !strings.Contains(stderrSeen.String(), "No conversation found") {
		t.Errorf("caller's Stderr did not receive the turn's stderr: %q", stderrSeen.String())
	}
	// Claude ends the turn with its own error result, which says only that
	// it failed; the event stream also carries the lost session, once, as
	// the typed SessionLost and the Fanout marker (CW-20261001-0184).
	if len(lostEvents) != 1 || lostEvents[0].RequestedID != lostClaudeID || lostEvents[0].ActualID != "" || !strings.Contains(lostEvents[0].Reason, "not found") {
		t.Errorf("SessionLost events = %+v; want one for %s, with no actual id", lostEvents, lostClaudeID)
	}
	if !strings.Contains(fanout.String(), "[session_lost] requested="+lostClaudeID) {
		t.Errorf("Fanout has no [session_lost] marker: %q", fanout.String())
	}
	var errorEvents int
	for _, ev := range drainEvents(eventCh) {
		if ev.Type == llmtypes.EventError {
			errorEvents++
		}
	}
	if errorEvents != 1 {
		t.Errorf("turn N: %d error events; want 1", errorEvents)
	}

	// Turn N+1: no --resume, and the fresh id is reported.
	if err := sess.SendInput(context.Background(), []byte("turn N+1")); err != nil {
		t.Fatalf("turn N+1: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("claude ran %d times; want 2", len(calls))
	}
	if got, _ := calls[0].ArgAfter("--resume"); got != lostClaudeID {
		t.Errorf("turn N --resume = %q; want %s", got, lostClaudeID)
	}
	if calls[1].HasArg("--resume") {
		t.Errorf("turn N+1 resumed: %q", calls[1].Args)
	}
	fresh := captureSessionID(t, "claude/print_turn1.jsonl")
	if got := ider.ProviderSessionID(); got != fresh {
		t.Errorf("stored id after turn N+1 = %q; want %s", got, fresh)
	}
	if len(ids) != 1 || ids[0] != fresh {
		t.Errorf("OnSessionID calls = %q; want [%s]", ids, fresh)
	}
	if len(lostEvents) != 1 {
		t.Errorf("turn N+1 reported a lost session too: %+v", lostEvents)
	}
}

// TestAdapterRuntime_SessionLost_OtherFailuresKeepID: a resume turn that
// fails for any other reason keeps the id and returns the plain error. The
// failure is claude's own stderr line for a model it cannot use
// (claude/print_error_unknown_model), with exit 1.
func TestAdapterRuntime_SessionLost_OtherFailuresKeepID(t *testing.T) {
	stderr := strings.TrimSpace(string(providertest.ReadFixture(t, "claude/print_error_unknown_model.stderr")))
	fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.Stderr(stderr), providertest.Exit(1)))
	var lost int
	sess := startClaudePrint(t, fake, StartOptions{
		SessionIDPreset: "ses_live",
		TypedEventCallback: func(ev events.Event) {
			if _, ok := ev.(events.SessionLost); ok {
				lost++
			}
		},
	})

	err := sess.SendInput(context.Background(), []byte("x"))
	if err == nil || errors.Is(err, provider.ErrProviderSessionLost) {
		t.Fatalf("err = %v; want a plain failure", err)
	}
	if got := sess.(SessionIDer).ProviderSessionID(); got != "ses_live" {
		t.Errorf("stored id = %q; want ses_live kept", got)
	}
	if lost != 0 {
		t.Errorf("a failure that is not a lost session reported %d SessionLost events", lost)
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
