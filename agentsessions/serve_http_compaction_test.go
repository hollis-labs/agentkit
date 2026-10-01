//go:build !windows

package agentsessions

import (
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CW-20261001-0198. OpenCode streams its compaction summary as ordinary
// message.part.delta events for the session being compacted. The shapes
// below are from a live capture of OpenCode 1.18.33 compacting a session
// (POST /session/{id}/summarize runs the same SessionCompaction processor
// as the automatic compaction after a ContextOverflowError):
//
//   - message.updated announces each message before its parts. The summary
//     is an assistant message with mode and agent "compaction" and
//     summary true; a user message's summary is an object ({"diffs":[]}).
//   - message.part.delta carries sessionID, messageID, partID, field and
//     delta, for the summary's reasoning and text parts alike.

func ocMessage(id, role, mode, agent, summary string) string {
	info := `{"id":"` + id + `","sessionID":"` + errorsSessionID + `","role":"` + role + `"`
	if mode != "" {
		info += `,"mode":"` + mode + `"`
	}
	if agent != "" {
		info += `,"agent":"` + agent + `"`
	}
	if summary != "" {
		info += `,"summary":` + summary
	}
	return ocEvent("message.updated", `{"sessionID":"`+errorsSessionID+`","info":`+info+`}}`)
}

func ocMessageDelta(messageID, text string) string {
	return ocEvent("message.part.delta", `{"sessionID":"`+errorsSessionID+`","messageID":"`+messageID+`","partID":"prt_`+messageID+`","field":"text","delta":"`+text+`"}`)
}

func replyText(seen []llmtypes.StreamEvent) string {
	var b strings.Builder
	for _, ev := range seen {
		if ev.Type == llmtypes.EventDelta {
			b.WriteString(ev.Content)
		}
	}
	return b.String()
}

// The automatic case: a turn overflows, OpenCode compacts and carries on.
// The turn's reply is what the agent said before and after, not the summary.
func TestServeHTTPSession_CompactionSummaryIsNotTheReply(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy,
		ocMessage("msg_reply1", "assistant", "build", "build", ""),
		ocMessageDelta("msg_reply1", "partial "),
		ocOverflow,
		ocMessage("msg_request", "user", "", "build", `{"diffs":[]}`),
		ocMessage("msg_summary", "assistant", "compaction", "compaction", "true"),
		ocMessageDelta("msg_summary", "We need answer exact template."),
		ocMessageDelta("msg_summary", "## Objective\\n- Respond with exactly ok."),
		ocCompacted,
		ocBusy,
		ocMessage("msg_reply2", "assistant", "build", "build", ""),
		ocMessageDelta("msg_reply2", "after compaction"),
		ocIdleState, ocIdle,
	})
	end, seen := turnEnd(t, fanout)
	if end.Type != llmtypes.EventDone {
		t.Fatalf("turn ended %v (%s); want done", end.Type, end.Error)
	}
	if got, want := replyText(seen), "partial after compaction"; got != want {
		t.Fatalf("reply = %q, want %q (the compaction summary is not the reply)", got, want)
	}
}

// A compaction the user asked for (OpenCode's /compact) is the same
// processor on the same session: none of its text is a reply.
func TestServeHTTPSession_ManualCompactionStreamsNoReply(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy,
		ocMessage("msg_reply", "assistant", "build", "build", ""),
		ocMessageDelta("msg_reply", "ok"),
		ocMessage("msg_request", "user", "", "build", `{"diffs":[]}`),
		ocMessage("msg_summary", "assistant", "compaction", "compaction", "true"),
		ocMessageDelta("msg_summary", "## Objective"),
		ocCompacted,
		ocIdleState, ocIdle,
	})
	end, seen := turnEnd(t, fanout)
	if end.Type != llmtypes.EventDone {
		t.Fatalf("turn ended %v (%s); want done", end.Type, end.Error)
	}
	if got := replyText(seen); got != "ok" {
		t.Fatalf("reply = %q, want %q", got, "ok")
	}
}

// Only the compaction message is held back: a user message's object summary
// does not mark anything, and deltas without a messageID (older OpenCode, or
// the session.next.text.delta form) still reach the reply.
func TestServeHTTPSession_OnlyCompactionMessagesAreHeldBack(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy,
		ocMessage("msg_user", "user", "", "build", `{"diffs":[]}`),
		ocMessageDelta("msg_user", "one "),
		ocDelta("two "),
		ocMessage("msg_reply", "assistant", "build", "build", "false"),
		ocMessageDelta("msg_reply", "three"),
		ocIdleState, ocIdle,
	})
	_, seen := turnEnd(t, fanout)
	if got, want := replyText(seen), "one two three"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

func TestSSEMessageInfoIsCompaction(t *testing.T) {
	for _, tc := range []struct {
		info sseMessageInfo
		want bool
	}{
		{sseMessageInfo{ID: "m", Mode: "compaction", Agent: "compaction", Summary: []byte("true")}, true},
		{sseMessageInfo{ID: "m", Summary: []byte("true")}, true},
		{sseMessageInfo{ID: "m", Mode: "compaction"}, true},
		{sseMessageInfo{ID: "m", Mode: "build", Agent: "build"}, false},
		{sseMessageInfo{ID: "m", Summary: []byte(`{"diffs":[]}`)}, false},
		{sseMessageInfo{ID: "m", Summary: []byte("false")}, false},
		{sseMessageInfo{Mode: "compaction"}, false}, // no id to hold back
	} {
		if got := tc.info.isCompaction(); got != tc.want {
			t.Errorf("%+v.isCompaction() = %v, want %v", tc.info, got, tc.want)
		}
	}
}
