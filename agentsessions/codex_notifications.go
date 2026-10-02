package agentsessions

import (
	"encoding/json"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	pevents "github.com/hollis-labs/go-providers/provider/events"
)

// Codex app-server's own phase words for an agent message. Its final answer is
// phase "final_answer"; the messages it writes while it works are "commentary".
const (
	codexPhaseFinalAnswer = "final_answer"
	codexPhaseCommentary  = "commentary"
)

// reportCodexNotification gives the events a Codex app-server notification
// carries to EventFanout and TypedEventCallback, the surfaces every other
// runtime reports its turns on. The adapter's ParseLine and ParseLineEvents see
// every frame but leave app-server's JSON-RPC to the runtime (go-providers'
// CodexAdapter says so), so without this a host reading either surface saw no
// turn at all: no final message, no end of turn.
func (s *jsonRpcStdioSession) reportCodexNotification(method string, params json.RawMessage) {
	if s.adapter.Name() != string(runtimes.Codex) {
		return
	}
	stream, typed := codexNotificationEvents(method, params)
	for _, ev := range stream {
		tryEventFanout(s.opts.EventFanout, ev)
	}
	if s.opts.TypedEventCallback != nil {
		for _, ev := range typed {
			s.opts.TypedEventCallback(ev)
		}
	}
}

// codexNotificationEvents translates the notifications of Codex app-server that
// carry a turn's output:
//
//   - item/completed for an agentMessage is one whole message: a delta whose
//     block id is the item id and whose phase is final for the final answer and
//     narration for commentary. Codex streams the same text as
//     item/agentMessage/delta notifications first; those are not reported, so a
//     message is never delivered twice. A message with no phase is left
//     unclassified.
//   - turn/completed ends the turn: done with stop reason end_turn, or cancelled
//     for status "interrupted"; status "failed" is an error carrying the turn's
//     own message.
//
// Every other notification, and every item that is not an agent message (user
// messages, commands, file changes), yields nothing.
func codexNotificationEvents(method string, params json.RawMessage) ([]llmtypes.StreamEvent, []pevents.Event) {
	switch method {
	case "item/completed":
		var p struct {
			Item struct {
				Type  string `json:"type"`
				ID    string `json:"id"`
				Text  string `json:"text"`
				Phase string `json:"phase"`
			} `json:"item"`
		}
		if json.Unmarshal(params, &p) != nil || p.Item.Type != "agentMessage" || p.Item.Text == "" {
			return nil, nil
		}
		phase := ""
		switch p.Item.Phase {
		case codexPhaseFinalAnswer:
			phase = llmtypes.PhaseFinal
		case codexPhaseCommentary:
			phase = llmtypes.PhaseNarration
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: p.Item.Text, BlockID: p.Item.ID, Phase: phase}},
			[]pevents.Event{pevents.Delta{Text: p.Item.Text, Phase: phase, BlockID: p.Item.ID}}

	case "turn/completed":
		var p struct {
			Turn struct {
				Status string          `json:"status"`
				Error  json.RawMessage `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &p) != nil {
			return nil, nil
		}
		switch p.Turn.Status {
		case "failed":
			msg := codexTurnError(p.Turn.Error)
			return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: msg}}, []pevents.Event{pevents.Error{Message: msg}}
		case "interrupted":
			return codexTurnDone(llmtypes.StopReasonCancelled)
		default:
			return codexTurnDone(llmtypes.StopReasonEndTurn)
		}
	}
	return nil, nil
}

// codexTurnDone is a turn's end: the stop reason travels on a usage event on the
// legacy stream, which has no stop reason of its own on its done.
func codexTurnDone(stop string) ([]llmtypes.StreamEvent, []pevents.Event) {
	return []llmtypes.StreamEvent{
			{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: stop}},
			{Type: llmtypes.EventDone},
		},
		[]pevents.Event{pevents.Done{StopReason: stop}}
}

// codexTurnError reads a failed turn's message from its error, which Codex
// writes as an object with a message (or, in older builds, as a string).
func codexTurnError(raw json.RawMessage) string {
	const fallback = "codex turn failed"
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	var str string
	if json.Unmarshal(raw, &str) == nil && str != "" {
		return str
	}
	return fallback
}
