//go:build !windows

package agentsessions

import (
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CW-20261001-0209. OpenCode streams a reasoning part's text as the same
// message.part.delta event as the reply's. In a live capture of OpenCode
// 1.18.33 (see serve_http_compaction_test.go), message.part.updated announces
// each part first, with part.type "reasoning" or "text", and each delta names
// its partID; that announcement is the only thing telling the model's
// thinking from its reply.

func ocPartUpdated(messageID, partID, typ string) string {
	return ocEvent("message.part.updated", `{"sessionID":"`+errorsSessionID+`","part":{"id":"`+partID+`","messageID":"`+messageID+`","sessionID":"`+errorsSessionID+`","type":"`+typ+`","text":""}}`)
}

func ocPartDelta(messageID, partID, text string) string {
	return ocEvent("message.part.delta", `{"sessionID":"`+errorsSessionID+`","messageID":"`+messageID+`","partID":"`+partID+`","field":"text","delta":"`+text+`"}`)
}

// splitThought returns the reply text and the thought text of a turn's
// deltas, checking every thought delta names its block.
func splitThought(t *testing.T, seen []llmtypes.StreamEvent) (reply, thought string) {
	t.Helper()
	var r, th strings.Builder
	for _, ev := range seen {
		if ev.Type != llmtypes.EventDelta {
			continue
		}
		switch ev.Phase {
		case llmtypes.PhaseThinking:
			if ev.BlockID == "" {
				t.Errorf("thought delta %q has no BlockID", ev.Content)
			}
			th.WriteString(ev.Content)
		case "":
			r.WriteString(ev.Content)
		default:
			t.Errorf("delta %q has phase %q", ev.Content, ev.Phase)
		}
	}
	return r.String(), th.String()
}

func TestServeHTTPSession_ReasoningDeltasAreThought(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy,
		ocMessage("msg_reply", "assistant", "build", "build", ""),
		ocPartUpdated("msg_reply", "prt_step", "step-start"),
		ocPartUpdated("msg_reply", "prt_reason", "reasoning"),
		ocPartDelta("msg_reply", "prt_reason", "We need"),
		ocPartDelta("msg_reply", "prt_reason", " to answer."),
		ocPartUpdated("msg_reply", "prt_text", "text"),
		ocPartDelta("msg_reply", "prt_text", "ok"),
		ocIdleState, ocIdle,
	})
	end, seen := turnEnd(t, fanout)
	if end.Type != llmtypes.EventDone {
		t.Fatalf("turn ended %v (%s); want done", end.Type, end.Error)
	}
	reply, thought := splitThought(t, seen)
	if reply != "ok" {
		t.Errorf("reply = %q, want %q (thinking is not the reply)", reply, "ok")
	}
	if thought != "We need to answer." {
		t.Errorf("thought = %q, want the reasoning part's text", thought)
	}
	for _, ev := range seen {
		if ev.Phase == llmtypes.PhaseThinking && ev.BlockID != "prt_reason" {
			t.Errorf("thought BlockID = %q, want the reasoning part's id", ev.BlockID)
		}
	}
}

// Text parts, parts announced as any other type, and deltas with no partID
// stay reply text.
func TestServeHTTPSession_OnlyReasoningPartsAreThought(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy,
		ocPartUpdated("msg_reply", "prt_text", "text"),
		ocPartDelta("msg_reply", "prt_text", "one "),
		ocPartDelta("msg_reply", "prt_unannounced", "two "),
		ocDelta("three"),
		ocIdleState, ocIdle,
	})
	_, seen := turnEnd(t, fanout)
	reply, thought := splitThought(t, seen)
	if reply != "one two three" || thought != "" {
		t.Errorf("reply = %q, thought = %q; want all reply", reply, thought)
	}
}

// The compaction summary's reasoning part is still held back entirely: it
// is neither the reply nor the turn's thinking.
func TestServeHTTPSession_CompactionReasoningIsHeldBack(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy,
		ocMessage("msg_summary", "assistant", "compaction", "compaction", "true"),
		ocPartUpdated("msg_summary", "prt_sreason", "reasoning"),
		ocPartDelta("msg_summary", "prt_sreason", "We need answer exact template."),
		ocCompacted,
		ocMessage("msg_reply", "assistant", "build", "build", ""),
		ocPartUpdated("msg_reply", "prt_text", "text"),
		ocPartDelta("msg_reply", "prt_text", "ok"),
		ocIdleState, ocIdle,
	})
	_, seen := turnEnd(t, fanout)
	reply, thought := splitThought(t, seen)
	if reply != "ok" || thought != "" {
		t.Errorf("reply = %q, thought = %q; the compaction summary's reasoning must not appear", reply, thought)
	}
}
