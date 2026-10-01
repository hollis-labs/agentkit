//go:build !windows

package agentsessions

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CW-20261001-0176. OpenCode's serve mode reports each step's tokens, cost
// and reason in a step-finish part (message.part.updated), once per step; a
// turn is one or more steps. The session emits one EventUsage per step, as
// run mode does for step_finish, and leaves the turn's end to session.idle.
//
// testdata/opencode_serve_two_steps.jsonl is a live capture of opencode
// 1.18.33 serve on the free opencode/big-pickle model: a prompt that makes
// the model run `echo hi` with the bash tool and then answer, so two steps,
// the first ending "tool-calls" and the second "stop". It keeps the SSE
// events of that turn and drops the server's own housekeeping (plugin and
// catalog notices, diffs, session updates). Scrubbed for this public repo:
// the session, message, part, event and tool-call ids are placeholders, the
// session id is the stand-in server's, the working directory is
// /work/project and the timestamps are rebased. The model is free, so every
// cost in the capture is 0; non-zero costs below use the same shape.

func fixtureEvents(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// ocStepFinish is a step-finish part event in the captured shape.
func ocStepFinish(partID, messageID, reason string, input, output, reasoning, cacheRead, cacheWrite int, cost string) string {
	n := func(v int) string { return strconv.Itoa(v) }
	return ocEvent("message.part.updated", `{"sessionID":"`+errorsSessionID+`","part":{"id":"`+partID+`","reason":"`+reason+`","messageID":"`+messageID+
		`","sessionID":"`+errorsSessionID+`","type":"step-finish","tokens":{"total":`+n(input+output+reasoning+cacheRead+cacheWrite)+`,"input":`+n(input)+
		`,"output":`+n(output)+`,"reasoning":`+n(reasoning)+`,"cache":{"write":`+n(cacheWrite)+`,"read":`+n(cacheRead)+`}},"cost":`+cost+`},"time":1700000000000}`)
}

func usages(seen []llmtypes.StreamEvent) []llmtypes.Usage {
	var out []llmtypes.Usage
	for _, ev := range seen {
		if ev.Type == llmtypes.EventUsage && ev.Usage != nil {
			out = append(out, *ev.Usage)
		}
	}
	return out
}

func sumUsage(us []llmtypes.Usage) llmtypes.Usage {
	var total llmtypes.Usage
	for _, u := range us {
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheCreationTokens += u.CacheCreationTokens
		total.CacheReadTokens += u.CacheReadTokens
		total.CostUSD += u.CostUSD
	}
	return total
}

// The live capture: two steps, so two usage events, each that step's own
// tokens and normalized stop reason, both before the one Done. The
// assistant messages' own tokens, reported twice each on message.updated,
// are not counted again.
func TestServeHTTPSession_StepFinishPartsBecomeUsage(t *testing.T) {
	fanout := startScriptedServe(t, fixtureEvents(t, "opencode_serve_two_steps.jsonl"))
	end, seen := turnEnd(t, fanout)
	if end.Type != llmtypes.EventDone {
		t.Fatalf("turn ended %v (%s); want done", end.Type, end.Error)
	}
	got := usages(seen)
	want := []llmtypes.Usage{
		{InputTokens: 11630, OutputTokens: 28, CacheReadTokens: 1920, StopReason: llmtypes.StopReasonToolUse},
		{InputTokens: 27, OutputTokens: 3, CacheReadTokens: 13568, StopReason: llmtypes.StopReasonEndTurn},
	}
	if len(got) != len(want) {
		t.Fatalf("usage events = %+v, want %d (one per step)", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d usage = %+v, want %+v", i+1, got[i], want[i])
		}
	}
	if reply, _ := splitThought(t, seen); reply != "done" {
		t.Errorf("reply = %q, want the reply unchanged by the usage events", reply)
	}
}

// Cost is each step's own, a delta, never a running total; reasoning tokens
// count as output; cache writes are the cache-creation tokens. The
// compaction summary's step is real spend and counts, though its text does
// not reach the reply.
func TestServeHTTPSession_StepUsageIsPerStepWithCostReasoningAndCache(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy,
		ocMessage("msg_summary", "assistant", "compaction", "compaction", "true"),
		ocPartUpdated("msg_summary", "prt_summary_text", "text"),
		ocPartDelta("msg_summary", "prt_summary_text", "summary"),
		ocStepFinish("prt_summary_step", "msg_summary", "stop", 900, 50, 0, 0, 0, "0.002"),
		ocCompacted,
		ocMessage("msg_one", "assistant", "build", "build", ""),
		ocStepFinish("prt_step_one", "msg_one", "tool-calls", 100, 20, 7, 9, 5, "0.0125"),
		ocMessage("msg_two", "assistant", "build", "build", ""),
		ocPartUpdated("msg_two", "prt_text", "text"),
		ocPartDelta("msg_two", "prt_text", "ok"),
		ocStepFinish("prt_step_two", "msg_two", "length", 40, 8, 0, 100, 0, "0.0075"),
		ocIdleState, ocIdle,
	})
	_, seen := turnEnd(t, fanout)
	got := usages(seen)
	want := []llmtypes.Usage{
		{InputTokens: 900, OutputTokens: 50, StopReason: llmtypes.StopReasonEndTurn, CostUSD: 0.002},
		{InputTokens: 100, OutputTokens: 27, CacheReadTokens: 9, CacheCreationTokens: 5, StopReason: llmtypes.StopReasonToolUse, CostUSD: 0.0125},
		{InputTokens: 40, OutputTokens: 8, CacheReadTokens: 100, StopReason: llmtypes.StopReasonMaxTokens, CostUSD: 0.0075},
	}
	if len(got) != len(want) {
		t.Fatalf("usage events = %+v, want %d", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d usage = %+v, want %+v", i+1, got[i], want[i])
		}
	}
	if total := sumUsage(got); total.CostUSD < 0.0219 || total.CostUSD > 0.0221 {
		t.Errorf("the turn's cost = %v, want the steps' costs summed (0.022)", total.CostUSD)
	}
	if reply, _ := splitThought(t, seen); reply != "ok" {
		t.Errorf("reply = %q; the compaction summary must stay out of it", reply)
	}
}

// A step-finish part reported again is not counted again; a step-finish with
// no tokens, one for another session, and a message.updated that carries the
// assistant message's tokens and cost are not usage events.
func TestServeHTTPSession_StepUsageIsNotCountedTwiceOrFromElsewhere(t *testing.T) {
	other := `{"type":"message.part.updated","properties":{"sessionID":"ses_someone_else","part":{"id":"prt_other","type":"step-finish","reason":"stop","tokens":{"input":5,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"cost":1}}}`
	noTokens := ocEvent("message.part.updated", `{"sessionID":"`+errorsSessionID+`","part":{"id":"prt_bare","messageID":"msg_one","type":"step-finish","reason":"stop"}}`)
	assistant := ocEvent("message.updated", `{"sessionID":"`+errorsSessionID+`","info":{"id":"msg_one","role":"assistant","mode":"build","agent":"build","cost":0.5,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"finish":"stop"}}`)
	step := ocStepFinish("prt_step_one", "msg_one", "stop", 10, 10, 0, 0, 0, "0.5")
	fanout := startScriptedServe(t, []string{ocBusy, other, noTokens, step, assistant, step, assistant, ocIdleState, ocIdle})
	_, seen := turnEnd(t, fanout)
	got := usages(seen)
	if len(got) != 1 || got[0].InputTokens != 10 || got[0].CostUSD != 0.5 {
		t.Errorf("usage events = %+v, want the one step counted once", got)
	}
}

// Over many turns the usage is reported per turn, the ids of reported steps
// do not accumulate, and each turn's steps are its own.
func TestServeHTTPSession_StepUsageIsPerTurnAndDoesNotAccumulate(t *testing.T) {
	const turnCount = 40
	turns := make([][]string, turnCount)
	for i := range turns {
		n := strconv.Itoa(i)
		turns[i] = []string{
			ocBusy,
			ocStepFinish("prt_a_"+n, "msg_a_"+n, "tool-calls", 10, 1, 0, 0, 0, "0.001"),
			ocStepFinish("prt_b_"+n, "msg_b_"+n, "stop", 20, 2, 0, 0, 0, "0.002"),
			ocIdleState, ocIdle,
		}
	}
	sess, fanout := startScriptedServeTurns(t, turns...)
	s := sess.(*serveHTTPSession)
	for i := range turns {
		if err := sess.SendInput(context.Background(), []byte("hi")); err != nil {
			t.Fatalf("turn %d: SendInput: %v", i, err)
		}
		seen := readUntil(t, fanout, endedWith(llmtypes.EventDone))
		got := usages(seen)
		if total := sumUsage(got); len(got) != 2 || total.InputTokens != 30 || total.OutputTokens != 3 {
			t.Fatalf("turn %d: usage events = %+v, want its two steps", i, got)
		}
	}
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	if got := len(s.usageParts); got > 2 {
		t.Errorf("usageParts holds %d ids after %d turns, want one turn's (2)", got, turnCount)
	}
}
