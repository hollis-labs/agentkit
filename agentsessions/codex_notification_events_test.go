//go:build !windows

package agentsessions

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	pevents "github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// codexCollector records what a session reports on both event surfaces.
type codexCollector struct {
	mu     sync.Mutex
	typed  []pevents.Event
	fanout chan llmtypes.StreamEvent
	stream []llmtypes.StreamEvent
}

func newCodexCollector() *codexCollector {
	return &codexCollector{fanout: make(chan llmtypes.StreamEvent, 256)}
}

func (c *codexCollector) callback(ev pevents.Event) {
	c.mu.Lock()
	c.typed = append(c.typed, ev)
	c.mu.Unlock()
}

// drain moves what the fanout holds into stream, and with untilDone waits for a
// terminal event.
func (c *codexCollector) drain(t *testing.T, untilDone bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-c.fanout:
			c.stream = append(c.stream, ev)
			if untilDone && llmtypes.IsTurnComplete(ev) {
				return
			}
		case <-deadline:
			if untilDone {
				t.Fatalf("no terminal event on the fanout; stream so far:\n%s", dumpStream(c.stream))
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

func (c *codexCollector) events() ([]llmtypes.StreamEvent, []pevents.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return relevantStream(c.stream), relevantTyped(append([]pevents.Event(nil), c.typed...))
}

// startCodex starts a real jsonrpc-stdio session on the Codex app-server adapter
// against fake, reporting to c.
func startCodex(t *testing.T, fake *providertest.Fake, c *codexCollector, hook func(string, json.RawMessage)) Session {
	t.Helper()
	adapter := provider.NewCodexAdapterAppServer()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "codex-events", Adapter: adapter, Caps: Capabilities{JsonRpcStdio: true, BinaryRequired: true}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:                 dir,
		LogPath:                 filepath.Join(dir, "session.log"),
		EventFanout:             c.fanout,
		JsonRpcNotificationHook: hook,
		TypedEventCallback:      c.callback,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })
	return sess
}

// runCodexTurns drives the handshake and then turns turn/start calls, waiting for
// each turn's end, and returns what the session reported.
func runCodexTurns(t *testing.T, fake *providertest.Fake, turns int) ([]llmtypes.StreamEvent, []pevents.Event) {
	t.Helper()
	c := newCodexCollector()
	sess := startCodex(t, fake, c, nil)
	caller := sess.(JsonRpcCaller)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, method := range []string{"initialize", "thread/start"} {
		if _, err := caller.Call(ctx, method, map[string]any{"clientInfo": map[string]any{"name": "t", "version": "0"}}); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	for range turns {
		if _, err := caller.Call(ctx, "turn/start", map[string]any{"threadId": "x", "input": []any{}}); err != nil {
			t.Fatalf("turn/start: %v", err)
		}
		c.drain(t, true)
	}
	c.drain(t, false)
	return c.events()
}

func requireEvents(t *testing.T, stream []llmtypes.StreamEvent, typed []pevents.Event, wantStream []llmtypes.StreamEvent, wantTyped []pevents.Event) {
	t.Helper()
	if dumpStream(stream) != dumpStream(wantStream) {
		t.Errorf("EventFanout =\n%s\nwant\n%s", dumpStream(stream), dumpStream(wantStream))
	}
	if !reflect.DeepEqual(typed, wantTyped) {
		t.Errorf("TypedEventCallback =\n%+v\nwant\n%+v", typed, wantTyped)
	}
}

func finalDelta(text, block string) (llmtypes.StreamEvent, pevents.Event) {
	return llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: text, BlockID: block, Phase: llmtypes.PhaseFinal},
		pevents.Delta{Text: text, Phase: "final", BlockID: block}
}

func endOfTurn(stop string) ([]llmtypes.StreamEvent, pevents.Event) {
	return []llmtypes.StreamEvent{
		{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: stop}},
		{Type: llmtypes.EventDone},
	}, pevents.Done{StopReason: stop}
}

// A final-answer agent message is one delta marked final with the item id as its
// block id, and the turn ends with a done that names why. Both event surfaces say
// the same, so a host reads a Codex turn the way it reads every other runtime's
// (CW-20261002-0061).
func TestJsonRpcStdioSession_CodexTurnReportsItsFinalMessage(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Replay("codex/app_server_turn"))
	stream, typed := runCodexTurns(t, fake, 2)

	hiS, hiT := finalDelta("Hi!", "msg_fixture0001")
	byeS, byeT := finalDelta("Bye!", "msg_fixture0002")
	endS, endT := endOfTurn(llmtypes.StopReasonEndTurn)
	requireEvents(t, stream, typed,
		[]llmtypes.StreamEvent{hiS, endS[0], endS[1], byeS, endS[0], endS[1]},
		[]pevents.Event{hiT, endT, byeT, endT})
}

// The sequence a reducer must not lose a turn in: an answered turn, an
// interrupted turn that said nothing, and a turn that ran a command and wrote
// nothing. Codex reports nothing on turn/started, so the interrupted turn is a
// lone turn/completed, and the tool-only turn is one item and a turn/completed.
func TestJsonRpcStdioSession_CodexTurnsWithNoMessageAreStillReported(t *testing.T) {
	const (
		initOK   = `{"jsonrpc":"2.0","id":1,"result":{"userAgent":"fake"}}`
		threadOK = `{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"th_1"}}}`
		final    = `{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg_1","text":"Hi!","phase":"final_answer"}}}`
		done     = `{"jsonrpc":"2.0","method":"turn/completed","params":{"turn":{"id":"t%d","status":"%s","error":null}}}`
		command  = `{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"commandExecution","id":"cmd_1","command":"make test","status":"completed","aggregatedOutput":"ok\n"}}}`
	)
	fake := providertest.New(t, runtimes.Codex, providertest.Script(
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`), providertest.Send(initOK),
		providertest.Recv(`{"jsonrpc":"2.0","id":2,"method":"thread/start"}`), providertest.Send(threadOK),
		providertest.Recv(`{"jsonrpc":"2.0","id":3,"method":"turn/start"}`), providertest.Send(`{"jsonrpc":"2.0","id":3,"result":{}}`),
		providertest.Stdout(final), providertest.Stdout(fmt.Sprintf(done, 1, "completed")),
		providertest.Recv(`{"jsonrpc":"2.0","id":4,"method":"turn/start"}`), providertest.Send(`{"jsonrpc":"2.0","id":4,"result":{}}`),
		providertest.Stdout(fmt.Sprintf(done, 2, "interrupted")),
		providertest.Recv(`{"jsonrpc":"2.0","id":5,"method":"turn/start"}`), providertest.Send(`{"jsonrpc":"2.0","id":5,"result":{}}`),
		providertest.Stdout(command), providertest.Stdout(fmt.Sprintf(done, 3, "completed")),
		providertest.AwaitEOF(),
	))
	stream, typed := runCodexTurns(t, fake, 3)

	hiS, hiT := finalDelta("Hi!", "msg_1")
	endS, endT := endOfTurn(llmtypes.StopReasonEndTurn)
	cutS, cutT := endOfTurn(llmtypes.StopReasonCancelled)
	args := map[string]any{"command": "make test"}
	requireEvents(t, stream, typed,
		[]llmtypes.StreamEvent{
			hiS, endS[0], endS[1],
			cutS[0], cutS[1],
			{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "cmd_1", Name: "commandExecution", Input: args}}, endS[0], endS[1],
		},
		[]pevents.Event{
			hiT, endT,
			cutT,
			pevents.ToolUse{ID: "cmd_1", Name: "commandExecution", Args: args}, pevents.ToolResult{ID: "cmd_1", ContentPreview: "ok\n"}, endT,
		})
}

// Codex repeating an item/completed (same item id) is reported once, so a
// consumer does not concatenate the message with itself.
func TestJsonRpcStdioSession_CodexRepeatedItemIsReportedOnce(t *testing.T) {
	const item = `{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg_1","text":"Hi!","phase":"final_answer"}}}`
	fake := providertest.New(t, runtimes.Codex, providertest.Script(
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`), providertest.Send(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":2,"method":"thread/start"}`), providertest.Send(`{"jsonrpc":"2.0","id":2,"result":{}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":3,"method":"turn/start"}`), providertest.Send(`{"jsonrpc":"2.0","id":3,"result":{}}`),
		providertest.Stdout(item), providertest.Stdout(item),
		providertest.Stdout(`{"jsonrpc":"2.0","method":"turn/completed","params":{"turn":{"status":"completed"}}}`),
		providertest.AwaitEOF(),
	))
	stream, typed := runCodexTurns(t, fake, 1)
	hiS, hiT := finalDelta("Hi!", "msg_1")
	endS, endT := endOfTurn(llmtypes.StopReasonEndTurn)
	requireEvents(t, stream, typed, []llmtypes.StreamEvent{hiS, endS[0], endS[1]}, []pevents.Event{hiT, endT})
}

// The interrupted turn of the live capture ends cancelled on both surfaces, and
// the next turn on the same process reports its own message.
func TestJsonRpcStdioSession_CodexInterruptedTurnEndsCancelled(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Replay("codex/app_server_interrupt"))
	c := newCodexCollector()
	notes := make(chan string, 256)
	sess := startCodex(t, fake, c, func(method string, _ json.RawMessage) { notes <- method })

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
	c.drain(t, false)
	stream, typed := c.events()

	byeS, byeT := finalDelta("Bye!", "msg_fixture0001")
	cutS, cutT := endOfTurn(llmtypes.StopReasonCancelled)
	endS, endT := endOfTurn(llmtypes.StopReasonEndTurn)
	requireEvents(t, stream, typed,
		[]llmtypes.StreamEvent{cutS[0], cutS[1], byeS, endS[0], endS[1]},
		[]pevents.Event{cutT, byeT, endT})
}

func TestCodexNotificationEvents(t *testing.T) {
	const (
		final      = `{"item":{"type":"agentMessage","id":"m1","text":"All done.","phase":"final_answer"}}`
		commentary = `{"item":{"type":"agentMessage","id":"m2","text":"Looking.","phase":"commentary"}}`
		noPhase    = `{"item":{"type":"agentMessage","id":"m3","text":"Hello."}}`
	)
	delta := func(text, block, phase string) ([]llmtypes.StreamEvent, []pevents.Event) {
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: text, BlockID: block, Phase: phase}},
			[]pevents.Event{pevents.Delta{Text: text, Phase: phase, BlockID: block}}
	}
	done := func(stop string) ([]llmtypes.StreamEvent, []pevents.Event) {
		s, ty := endOfTurn(stop)
		return s, []pevents.Event{ty}
	}
	fail := func(msg string) ([]llmtypes.StreamEvent, []pevents.Event) {
		return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: msg}}, []pevents.Event{pevents.Error{Message: msg}}
	}
	tool := func(typ, id string, args map[string]any, isErr bool, preview string) ([]llmtypes.StreamEvent, []pevents.Event) {
		return []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: id, Name: typ, Input: args}}},
			[]pevents.Event{pevents.ToolUse{ID: id, Name: typ, Args: args}, pevents.ToolResult{ID: id, IsError: isErr, ContentPreview: preview}}
	}
	none := func() ([]llmtypes.StreamEvent, []pevents.Event) { return nil, nil }

	type want struct {
		stream []llmtypes.StreamEvent
		typed  []pevents.Event
		itemID string
	}
	w := func(s []llmtypes.StreamEvent, ty []pevents.Event, id string) want { return want{s, ty, id} }
	var tests []struct {
		name, method, params string
		want                 want
	}
	add := func(name, method, params string, wnt want) {
		tests = append(tests, struct {
			name, method, params string
			want                 want
		}{name, method, params, wnt})
	}
	{
		s, ty := delta("All done.", "m1", "final")
		add("final answer", "item/completed", final, w(s, ty, "m1"))
		s, ty = delta("Looking.", "m2", "narration")
		add("commentary is narration", "item/completed", commentary, w(s, ty, "m2"))
		s, ty = delta("Hello.", "m3", "")
		add("no phase stays unclassified", "item/completed", noPhase, w(s, ty, "m3"))
		s, ty = none()
		add("empty agent message", "item/completed", `{"item":{"type":"agentMessage","id":"m4","text":"","phase":"final_answer"}}`, w(s, ty, ""))
		add("user message", "item/completed", `{"item":{"type":"userMessage","id":"u1","text":"hi"}}`, w(s, ty, ""))
		add("reasoning", "item/completed", `{"item":{"type":"reasoning","id":"r1","text":"thinking"}}`, w(s, ty, ""))
		add("streamed delta is not repeated", "item/agentMessage/delta", `{"itemId":"m1","delta":"Hi"}`, w(s, ty, ""))
		add("turn started", "turn/started", `{"turn":{"id":"t1","status":"inProgress"}}`, w(s, ty, ""))
		add("malformed params", "turn/completed", `not json`, w(s, ty, ""))
		add("other notification", "thread/tokenUsage/updated", `{"tokenUsage":{}}`, w(s, ty, ""))

		s, ty = tool("commandExecution", "c1", map[string]any{"command": "ls"}, false, "a\n")
		add("command", "item/completed", `{"item":{"type":"commandExecution","id":"c1","command":"ls","status":"completed","aggregatedOutput":"a\n"}}`, w(s, ty, "c1"))
		s, ty = tool("commandExecution", "c2", map[string]any{"command": "false"}, true, "")
		add("failed command", "item/completed", `{"item":{"type":"commandExecution","id":"c2","command":"false","status":"failed"}}`, w(s, ty, "c2"))
		s, ty = tool("fileChange", "f1", map[string]any{"changes": []any{map[string]any{"path": "a.go"}}}, false, "")
		add("file change", "item/completed", `{"item":{"type":"fileChange","id":"f1","status":"completed","changes":[{"path":"a.go"}]}}`, w(s, ty, "f1"))

		s, ty = done("end_turn")
		add("completed turn", "turn/completed", `{"turn":{"id":"t1","status":"completed","error":null}}`, w(s, ty, ""))
		add("turn with no status", "turn/completed", `{"turn":{"id":"t1"}}`, w(s, ty, ""))
		s, ty = done("cancelled")
		add("interrupted turn", "turn/completed", `{"turn":{"id":"t1","status":"interrupted"}}`, w(s, ty, ""))
		s, ty = fail("rate limited")
		add("failed turn with a message", "turn/completed", `{"turn":{"id":"t1","status":"failed","error":{"message":"rate limited"}}}`, w(s, ty, ""))
		s, ty = fail("boom")
		add("failed turn with a string error", "turn/completed", `{"turn":{"id":"t1","status":"failed","error":"boom"}}`, w(s, ty, ""))
		s, ty = fail("codex turn failed")
		add("failed turn with no error", "turn/completed", `{"turn":{"id":"t1","status":"failed"}}`, w(s, ty, ""))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codexNotificationEvents(tt.method, json.RawMessage(tt.params))
			if dumpStream(got.stream) != dumpStream(tt.want.stream) {
				t.Errorf("stream =\n%s\nwant\n%s", dumpStream(got.stream), dumpStream(tt.want.stream))
			}
			if !reflect.DeepEqual(got.typed, tt.want.typed) {
				t.Errorf("typed = %+v\nwant %+v", got.typed, tt.want.typed)
			}
			if got.itemID != tt.want.itemID {
				t.Errorf("itemID = %q, want %q", got.itemID, tt.want.itemID)
			}
		})
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

// dumpStream renders every field a test asserts on, so comparing two dumps
// compares the events whole.
func dumpStream(evs []llmtypes.StreamEvent) string {
	var sb strings.Builder
	for _, ev := range evs {
		stop := ""
		if ev.Usage != nil {
			stop = ev.Usage.StopReason
		}
		tool := ""
		if ev.ToolUse != nil {
			args, _ := json.Marshal(ev.ToolUse.Input)
			tool = ev.ToolUse.ID + "/" + ev.ToolUse.Name + "/" + string(args)
		}
		fmt.Fprintf(&sb, "  %s content=%q block=%q phase=%q err=%q stop=%q tool=%q\n", ev.Type, ev.Content, ev.BlockID, ev.Phase, ev.Error, stop, tool)
	}
	return sb.String()
}

// EventFanout drops events when its channel is full, whichever they are; the
// typed callback never does. A host that must see a turn's final message and its
// end uses the callback, which is why StartOptions says so.
func TestJsonRpcStdioSession_CodexFullFanoutDropsEventsButTheCallbackDoesNot(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Replay("codex/app_server_turn"))
	c := newCodexCollector()
	c.fanout = make(chan llmtypes.StreamEvent, 1) // never read: a stalled consumer
	notes := make(chan string, 256)
	sess := startCodex(t, fake, c, func(method string, _ json.RawMessage) { notes <- method })
	caller := sess.(JsonRpcCaller)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, method := range []string{"initialize", "thread/start", "turn/start"} {
		if _, err := caller.Call(ctx, method, map[string]any{"threadId": "x", "input": []any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}}); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	waitNote(t, notes, "turn/completed")
	if _, err := caller.Call(ctx, "turn/start", map[string]any{"threadId": "x", "input": []any{}}); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	waitNote(t, notes, "turn/completed")

	_, typed := c.events()
	end := pevents.Done{StopReason: llmtypes.StopReasonEndTurn}
	want := []pevents.Event{
		pevents.Delta{Text: "Hi!", Phase: "final", BlockID: "msg_fixture0001"}, end,
		pevents.Delta{Text: "Bye!", Phase: "final", BlockID: "msg_fixture0002"}, end,
	}
	if !reflect.DeepEqual(typed, want) {
		t.Fatalf("the callback lost events: %+v, want %+v", typed, want)
	}
	if n := len(c.fanout); n != 1 {
		t.Fatalf("a capacity-1 fanout holds %d events; want exactly 1 (the rest dropped)", n)
	}
	if first := <-c.fanout; first.Type != llmtypes.EventDelta {
		t.Fatalf("the one event kept was %+v, want the first (the delta)", first)
	}
}
