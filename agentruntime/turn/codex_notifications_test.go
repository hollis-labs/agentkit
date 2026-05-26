package turn

import (
	"encoding/json"
	"testing"
)

func TestParseCodexItemCompletedCommandExecution(t *testing.T) {
	item, ok := ParseCodexItemCompleted(json.RawMessage(`{"item":{"type":"commandExecution","id":"call_1","command":"/bin/zsh -lc 'echo hi'","exitCode":0},"threadId":"t","turnId":"u"}`))
	if !ok {
		t.Fatal("expected commandExecution item")
	}
	if item.Type != "commandExecution" {
		t.Fatalf("type = %q, want commandExecution", item.Type)
	}
	if item.ID != "call_1" {
		t.Fatalf("id = %q, want call_1", item.ID)
	}
	if item.Command != "/bin/zsh -lc 'echo hi'" {
		t.Fatalf("command = %q", item.Command)
	}
}

func TestParseCodexItemCompletedFileChange(t *testing.T) {
	item, ok := ParseCodexItemCompleted(json.RawMessage(`{"item":{"type":"fileChange","id":"fc_1"}}`))
	if !ok {
		t.Fatal("expected fileChange item")
	}
	if item.Type != "fileChange" {
		t.Fatalf("type = %q, want fileChange", item.Type)
	}
	if item.ID != "fc_1" {
		t.Fatalf("id = %q, want fc_1", item.ID)
	}
}

func TestParseCodexItemCompletedAgentMessage(t *testing.T) {
	item, ok := ParseCodexItemCompleted(json.RawMessage(`{"item":{"type":"agentMessage","id":"msg_1","text":"working on it","phase":"commentary"}}`))
	if !ok {
		t.Fatal("expected agentMessage item")
	}
	if item.Type != "agentMessage" {
		t.Fatalf("type = %q, want agentMessage", item.Type)
	}
	if item.ID != "msg_1" {
		t.Fatalf("id = %q, want msg_1", item.ID)
	}
	if item.Text != "working on it" {
		t.Fatalf("text = %q", item.Text)
	}
	if item.Phase != "commentary" {
		t.Fatalf("phase = %q, want commentary", item.Phase)
	}
}

func TestParseCodexItemCompletedIgnoredTypes(t *testing.T) {
	for _, raw := range []string{
		`{"item":{"type":"userMessage","content":[{"type":"text","text":"hi"}]}}`,
		`{"item":{"type":"agentMessage","text":""}}`,
		`{"item":{"type":"reasoning"}}`,
		`not json`,
	} {
		if item, ok := ParseCodexItemCompleted(json.RawMessage(raw)); ok {
			t.Fatalf("ParseCodexItemCompleted(%s) = %#v, true; want false", raw, item)
		}
	}
}

func TestParseCodexTokenUsageTotals(t *testing.T) {
	totals, ok := ParseCodexTokenUsageTotals(json.RawMessage(`{"tokenUsage":{"total":{"totalTokens":16445,"inputTokens":16370,"cachedInputTokens":3456,"outputTokens":75}},"threadId":"t"}`))
	if !ok {
		t.Fatal("expected token usage totals")
	}
	want := CodexTokenUsageTotals{
		InputTokens:       16370,
		OutputTokens:      75,
		CachedInputTokens: 3456,
	}
	if totals != want {
		t.Fatalf("totals = %#v, want %#v", totals, want)
	}
}

func TestParseCodexTokenUsageTotalsEmptyOrBad(t *testing.T) {
	for _, raw := range []string{
		`{"tokenUsage":{"total":{"inputTokens":0,"outputTokens":0,"cachedInputTokens":0}}}`,
		`{"tokenUsage":{"total":{}}}`,
		`{}`,
		`garbage`,
	} {
		if totals, ok := ParseCodexTokenUsageTotals(json.RawMessage(raw)); ok {
			t.Fatalf("ParseCodexTokenUsageTotals(%s) = %#v, true; want false", raw, totals)
		}
	}
}

func TestCodexTokenUsageDeltaSemantics(t *testing.T) {
	updates := []json.RawMessage{
		json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":5000,"outputTokens":20,"cachedInputTokens":1000}}}`),
		json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":11000,"outputTokens":50,"cachedInputTokens":2500}}}`),
		json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":16370,"outputTokens":75,"cachedInputTokens":3456}}}`),
	}
	var prev CodexTokenUsageTotals
	var sum CodexTokenUsageTotals
	for _, raw := range updates {
		next, ok := ParseCodexTokenUsageTotals(raw)
		if !ok {
			t.Fatalf("expected totals for %s", raw)
		}
		delta := CodexTokenUsageDelta(prev, next)
		prev = next
		sum.InputTokens += delta.InputTokens
		sum.OutputTokens += delta.OutputTokens
		sum.CachedInputTokens += delta.CachedInputTokens
	}
	want := CodexTokenUsageTotals{
		InputTokens:       16370,
		OutputTokens:      75,
		CachedInputTokens: 3456,
	}
	if sum != want {
		t.Fatalf("summed deltas = %#v, want %#v", sum, want)
	}
}

func TestCodexTokenUsageDeltaClampsNegative(t *testing.T) {
	prev := CodexTokenUsageTotals{
		InputTokens:       20,
		OutputTokens:      20,
		CachedInputTokens: 10,
	}
	next := CodexTokenUsageTotals{
		InputTokens:       10,
		OutputTokens:      30,
		CachedInputTokens: 1,
	}
	want := CodexTokenUsageTotals{
		InputTokens:       0,
		OutputTokens:      10,
		CachedInputTokens: 0,
	}
	if delta := CodexTokenUsageDelta(prev, next); delta != want {
		t.Fatalf("delta = %#v, want %#v", delta, want)
	}
}

func TestIsCodexTurnCompletedExactMatch(t *testing.T) {
	if !IsCodexTurnCompleted("turn/completed") {
		t.Fatal("expected exact turn/completed match")
	}
	for _, method := range []string{"turn/complete", "Turn/completed", "thread/turn/completed", ""} {
		if IsCodexTurnCompleted(method) {
			t.Fatalf("IsCodexTurnCompleted(%q) = true, want false", method)
		}
	}
}
