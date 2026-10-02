//go:build !windows

package agentsessions

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	pevents "github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// codexTurnEvents runs a captured Codex app-server transcript through a real
// jsonrpc-stdio session and returns what the session reported on both event
// surfaces, once the given number of turns have ended.
func codexTurnEvents(t *testing.T, fixture string, turns int, hook func(string, json.RawMessage)) ([]llmtypes.StreamEvent, []pevents.Event) {
	t.Helper()
	fake := providertest.New(t, runtimes.Codex, providertest.Replay(fixture))
	adapter := provider.NewCodexAdapterAppServer()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "codex-events", Adapter: adapter, Caps: Capabilities{JsonRpcStdio: true, BinaryRequired: true}})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var typed []pevents.Event
	fanout := make(chan llmtypes.StreamEvent, 256)
	dir := t.TempDir()
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:                 dir,
		LogPath:                 filepath.Join(dir, "session.log"),
		EventFanout:             fanout,
		JsonRpcNotificationHook: hook,
		TypedEventCallback: func(ev pevents.Event) {
			mu.Lock()
			typed = append(typed, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })

	caller := sess.(JsonRpcCaller)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	calls := []struct {
		method string
		params any
	}{
		{"initialize", map[string]any{"clientInfo": map[string]any{"name": "t", "version": "0"}}},
		{"thread/start", map[string]any{}},
	}
	for range turns {
		calls = append(calls, struct {
			method string
			params any
		}{"turn/start", map[string]any{"threadId": "x", "input": []any{}}})
	}
	var stream []llmtypes.StreamEvent
	drain := func(untilDone bool) {
		deadline := time.After(10 * time.Second)
		for {
			select {
			case ev := <-fanout:
				stream = append(stream, ev)
				if untilDone && llmtypes.IsTurnComplete(ev) {
					return
				}
			case <-deadline:
				if untilDone {
					t.Fatalf("no terminal event; stream so far: %+v", stream)
				}
				return
			default:
				if !untilDone {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	for _, call := range calls {
		if _, err := caller.Call(ctx, call.method, call.params); err != nil {
			t.Fatalf("%s: %v", call.method, err)
		}
		if call.method == "turn/start" {
			drain(true)
		}
	}
	drain(false)
	mu.Lock()
	defer mu.Unlock()
	return stream, append([]pevents.Event(nil), typed...)
}

// A final-answer agent message is one delta marked final with the item id as its
// block id, and the turn ends with a done that names why. Both event surfaces say
// the same, so a host reads a Codex turn the way it reads every other runtime's
// (CW-20261002-0061).
func TestJsonRpcStdioSession_CodexTurnReportsItsFinalMessage(t *testing.T) {
	stream, typed := codexTurnEvents(t, "codex/app_server_turn", 2, nil)

	wantStream := []llmtypes.StreamEvent{
		{Type: llmtypes.EventDelta, Content: "Hi!", BlockID: "msg_fixture0001", Phase: llmtypes.PhaseFinal},
		{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: llmtypes.StopReasonEndTurn}},
		{Type: llmtypes.EventDone},
		{Type: llmtypes.EventDelta, Content: "Bye!", BlockID: "msg_fixture0002", Phase: llmtypes.PhaseFinal},
		{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: llmtypes.StopReasonEndTurn}},
		{Type: llmtypes.EventDone},
	}
	if got := relevantStream(stream); !equalStream(got, wantStream) {
		t.Errorf("EventFanout =\n%s\nwant\n%s", dumpStream(got), dumpStream(wantStream))
	}

	wantTyped := []pevents.Event{
		pevents.Delta{Text: "Hi!", Phase: "final", BlockID: "msg_fixture0001"},
		pevents.Done{StopReason: llmtypes.StopReasonEndTurn},
		pevents.Delta{Text: "Bye!", Phase: "final", BlockID: "msg_fixture0002"},
		pevents.Done{StopReason: llmtypes.StopReasonEndTurn},
	}
	if got := relevantTyped(typed); !equalTyped(got, wantTyped) {
		t.Errorf("TypedEventCallback = %+v\nwant %+v", got, wantTyped)
	}
}

// relevantStream drops the events these tests do not assert on (session ids).
func relevantStream(in []llmtypes.StreamEvent) []llmtypes.StreamEvent {
	var out []llmtypes.StreamEvent
	for _, ev := range in {
		if ev.Type != llmtypes.EventSessionID {
			out = append(out, ev)
		}
	}
	return out
}

func relevantTyped(in []pevents.Event) []pevents.Event {
	var out []pevents.Event
	for _, ev := range in {
		switch ev.(type) {
		case pevents.SessionID, pevents.Heartbeat:
		default:
			out = append(out, ev)
		}
	}
	return out
}

func equalStream(a, b []llmtypes.StreamEvent) bool {
	return dumpStream(a) == dumpStream(b)
}

func dumpStream(evs []llmtypes.StreamEvent) string {
	var out string
	for _, ev := range evs {
		stop := ""
		if ev.Usage != nil {
			stop = ev.Usage.StopReason
		}
		out += "  " + string(ev.Type) + " content=" + ev.Content + " block=" + ev.BlockID + " phase=" + ev.Phase + " err=" + ev.Error + " stop=" + stop + "\n"
	}
	return out
}

func equalTyped(a, b []pevents.Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCodexNotificationEvents(t *testing.T) {
	const (
		final      = `{"item":{"type":"agentMessage","id":"m1","text":"All done.","phase":"final_answer"}}`
		commentary = `{"item":{"type":"agentMessage","id":"m2","text":"Looking.","phase":"commentary"}}`
		noPhase    = `{"item":{"type":"agentMessage","id":"m3","text":"Hello."}}`
	)
	delta := func(text, block, phase string) []pevents.Event {
		return []pevents.Event{pevents.Delta{Text: text, Phase: phase, BlockID: block}}
	}
	done := func(stop string) []pevents.Event { return []pevents.Event{pevents.Done{StopReason: stop}} }

	tests := []struct {
		name, method, params string
		wantTyped            []pevents.Event
	}{
		{"final answer", "item/completed", final, delta("All done.", "m1", "final")},
		{"commentary is narration", "item/completed", commentary, delta("Looking.", "m2", "narration")},
		{"no phase stays unclassified", "item/completed", noPhase, delta("Hello.", "m3", "")},
		{"empty agent message", "item/completed", `{"item":{"type":"agentMessage","id":"m4","text":"","phase":"final_answer"}}`, nil},
		{"user message", "item/completed", `{"item":{"type":"userMessage","id":"u1","text":"hi"}}`, nil},
		{"command", "item/completed", `{"item":{"type":"commandExecution","id":"c1","command":"ls"}}`, nil},
		{"file change", "item/completed", `{"item":{"type":"fileChange","id":"f1"}}`, nil},
		{"streamed delta is not repeated", "item/agentMessage/delta", `{"itemId":"m1","delta":"Hi"}`, nil},
		{"turn started", "turn/started", `{"turn":{"id":"t1","status":"inProgress"}}`, nil},
		{"completed turn", "turn/completed", `{"turn":{"id":"t1","status":"completed","error":null}}`, done("end_turn")},
		{"turn with no status", "turn/completed", `{"turn":{"id":"t1"}}`, done("end_turn")},
		{"interrupted turn", "turn/completed", `{"turn":{"id":"t1","status":"interrupted"}}`, done("cancelled")},
		{"failed turn with a message", "turn/completed", `{"turn":{"id":"t1","status":"failed","error":{"message":"rate limited"}}}`, []pevents.Event{pevents.Error{Message: "rate limited"}}},
		{"failed turn with a string error", "turn/completed", `{"turn":{"id":"t1","status":"failed","error":"boom"}}`, []pevents.Event{pevents.Error{Message: "boom"}}},
		{"failed turn with no error", "turn/completed", `{"turn":{"id":"t1","status":"failed"}}`, []pevents.Event{pevents.Error{Message: "codex turn failed"}}},
		{"malformed params", "turn/completed", `not json`, nil},
		{"other notification", "thread/tokenUsage/updated", `{"tokenUsage":{}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, typed := codexNotificationEvents(tt.method, json.RawMessage(tt.params))
			if !equalTyped(typed, tt.wantTyped) {
				t.Fatalf("typed = %+v, want %+v", typed, tt.wantTyped)
			}
			// The legacy stream says the same, with the stop reason on a usage
			// event ahead of the done.
			var terminals, deltas int
			for _, ev := range stream {
				switch ev.Type {
				case llmtypes.EventDone, llmtypes.EventError:
					terminals++
				case llmtypes.EventDelta:
					deltas++
				}
			}
			var wantTerminals, wantDeltas int
			for _, ev := range tt.wantTyped {
				switch ev.(type) {
				case pevents.Done, pevents.Error:
					wantTerminals++
				case pevents.Delta:
					wantDeltas++
				}
			}
			if terminals != wantTerminals || deltas != wantDeltas {
				t.Fatalf("stream = %s, want %d delta(s) and %d terminal(s)", dumpStream(stream), wantDeltas, wantTerminals)
			}
		})
	}
}

// The interrupted turn of the live capture ends with a done that says it was
// cancelled, and the next turn on the same process reports its own message.
func TestJsonRpcStdioSession_CodexInterruptedTurnEndsCancelled(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Replay("codex/app_server_interrupt"))
	adapter := provider.NewCodexAdapterAppServer()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "codex-interrupt-events", Adapter: adapter, Caps: Capabilities{JsonRpcStdio: true, BinaryRequired: true}})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var typed []pevents.Event
	notes := make(chan string, 256)
	dir := t.TempDir()
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: dir,
		LogPath: filepath.Join(dir, "session.log"),
		JsonRpcNotificationHook: func(method string, _ json.RawMessage) {
			notes <- method
		},
		TypedEventCallback: func(ev pevents.Event) {
			mu.Lock()
			typed = append(typed, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })

	caller := sess.(JsonRpcCaller)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, method := range []string{"initialize", "thread/start", "turn/start"} {
		if _, err := caller.Call(ctx, method, map[string]any{"threadId": "x", "input": []any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}}); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	waitNote(t, notes, "item/started")
	if err := sess.(TurnInterrupter).InterruptTurn(ctx); err != nil {
		t.Fatalf("InterruptTurn: %v", err)
	}
	waitNote(t, notes, "turn/completed")
	if _, err := caller.Call(ctx, "turn/start", map[string]any{"threadId": "x", "input": []any{}}); err != nil {
		t.Fatalf("turn/start after the interrupt: %v", err)
	}
	waitNote(t, notes, "turn/completed")

	mu.Lock()
	defer mu.Unlock()
	got := relevantTyped(typed)
	want := []pevents.Event{
		pevents.Done{StopReason: llmtypes.StopReasonCancelled},
		pevents.Delta{Text: "Bye!", Phase: "final", BlockID: "msg_fixture0001"},
		pevents.Done{StopReason: llmtypes.StopReasonEndTurn},
	}
	if !equalTyped(got, want) {
		t.Fatalf("typed = %+v\nwant %+v", got, want)
	}
}

// Only Codex speaks these notifications: a JSON-RPC runtime that is not Codex
// and happens to use the same method names gets no translation.
func TestJsonRpcStdioSession_OtherAdaptersGetNoCodexEvents(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Script(
		providertest.Stdout(`{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"agentMessage","id":"m","text":"hi","phase":"final_answer"}}}`),
		providertest.Stdout(`{"jsonrpc":"2.0","method":"turn/completed","params":{"turn":{"status":"completed"}}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"ping"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		providertest.AwaitEOF(),
	))
	fanout := make(chan llmtypes.StreamEvent, 8)
	var n notes
	sess := startJsonRpcFake(t, fake, StartOptions{EventFanout: fanout, JsonRpcNotificationHook: n.hook})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := sess.(JsonRpcCaller).Call(ctx, "ping", map[string]any{}); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if _, ok := n.has("turn/completed"); !ok {
		t.Fatal("the notification was not routed to the hook")
	}
	select {
	case ev := <-fanout:
		t.Fatalf("a non-Codex adapter's notification produced %+v", ev)
	default:
	}
}
