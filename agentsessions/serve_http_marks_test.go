//go:build !windows

package agentsessions

import (
	"context"
	"strconv"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CW-20261001-0224. The ids of reasoning parts and compaction summary
// messages are unique and matter only for the deltas of their own turn; the
// session forgets them when the next turn starts.

// readUntil collects fanout events until done says the turn's events are in.
func readUntil(t *testing.T, fanout <-chan llmtypes.StreamEvent, done func([]llmtypes.StreamEvent) bool) []llmtypes.StreamEvent {
	t.Helper()
	var seen []llmtypes.StreamEvent
	deadline := time.After(5 * time.Second)
	for !done(seen) {
		select {
		case ev := <-fanout:
			seen = append(seen, ev)
		case <-deadline:
			t.Fatalf("the events never arrived; saw %v", seen)
		}
	}
	return seen
}

func endedWith(typ llmtypes.EventType) func([]llmtypes.StreamEvent) bool {
	return func(seen []llmtypes.StreamEvent) bool {
		return len(seen) > 0 && seen[len(seen)-1].Type == typ
	}
}

// Each turn announces a compaction summary with its own reasoning part, and
// a reply with a reasoning part and a text part, all under fresh ids. Over
// many turns the maps keep one turn's ids, and every turn is still told
// apart: the thinking is thought, the summary is held back, the reply is the
// reply.
func TestServeHTTPSession_TurnMarksDoNotAccumulate(t *testing.T) {
	const turnCount = 60
	turns := make([][]string, turnCount)
	for i := range turns {
		n := strconv.Itoa(i)
		turns[i] = []string{
			ocBusy,
			ocMessage("msg_summary_"+n, "assistant", "compaction", "compaction", "true"),
			ocPartUpdated("msg_summary_"+n, "prt_sreason_"+n, "reasoning"),
			ocPartDelta("msg_summary_"+n, "prt_sreason_"+n, "summary thinking"),
			ocCompacted,
			ocMessage("msg_reply_"+n, "assistant", "build", "build", ""),
			ocPartUpdated("msg_reply_"+n, "prt_reason_"+n, "reasoning"),
			ocPartDelta("msg_reply_"+n, "prt_reason_"+n, "think"),
			ocPartUpdated("msg_reply_"+n, "prt_text_"+n, "text"),
			ocPartDelta("msg_reply_"+n, "prt_text_"+n, "ok"),
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
		reply, thought := splitThought(t, seen)
		if reply != "ok" || thought != "think" {
			t.Fatalf("turn %d: reply = %q, thought = %q; want ok and think", i, reply, thought)
		}
	}
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	// One turn's worth: the summary's reasoning part and the reply's.
	if got := len(s.reasoningParts); got > 2 {
		t.Errorf("reasoningParts holds %d ids after %d turns, want one turn's (2)", got, turnCount)
	}
	if got := len(s.compactionMessages); got > 1 {
		t.Errorf("compactionMessages holds %d ids after %d turns, want one turn's (1)", got, turnCount)
	}
}

// A reasoning delta that follows its turn's ending event, as OpenCode sends
// after an abort, is still thought: the ids are forgotten when the next turn
// starts, not when one ends. The next turn then starts clean.
func TestServeHTTPSession_LateReasoningDeltaAfterTurnEndIsStillThought(t *testing.T) {
	sess, fanout := startScriptedServeTurns(t,
		[]string{
			ocBusy,
			ocMessage("msg_reply", "assistant", "build", "build", ""),
			ocPartUpdated("msg_reply", "prt_reason", "reasoning"),
			ocPartDelta("msg_reply", "prt_reason", "think"),
			ocIdleState, ocIdle,
			ocPartDelta("msg_reply", "prt_reason", " late"),
		},
		[]string{
			ocBusy,
			ocPartUpdated("msg_next", "prt_next", "text"),
			ocPartDelta("msg_next", "prt_next", "second"),
			ocIdleState, ocIdle,
		},
	)
	s := sess.(*serveHTTPSession)
	if err := sess.SendInput(context.Background(), []byte("one")); err != nil {
		t.Fatal(err)
	}
	seen := readUntil(t, fanout, func(seen []llmtypes.StreamEvent) bool {
		var done, late bool
		for _, ev := range seen {
			done = done || ev.Type == llmtypes.EventDone
			late = late || (ev.Type == llmtypes.EventDelta && ev.Content == " late")
		}
		return done && late
	})
	for _, ev := range seen {
		if ev.Content == " late" && (ev.Phase != llmtypes.PhaseThinking || ev.BlockID != "prt_reason") {
			t.Errorf("the late delta = %+v, want thought for prt_reason", ev)
		}
	}

	if err := sess.SendInput(context.Background(), []byte("two")); err != nil {
		t.Fatal(err)
	}
	seen = readUntil(t, fanout, endedWith(llmtypes.EventDone))
	if reply, thought := splitThought(t, seen); reply != "second" || thought != "" {
		t.Errorf("second turn: reply = %q, thought = %q", reply, thought)
	}
	if s.isReasoningPart("prt_reason") {
		t.Error("the first turn's reasoning part is still marked after the second turn started")
	}
}
