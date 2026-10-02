package agentsessions

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

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

// codexSeenItems is how many item ids a session remembers, to report an item once
// if Codex repeats its item/completed.
const codexSeenItems = 256

// codexToolPreviewBytes bounds the output a tool result carries.
const codexToolPreviewBytes = 256

// reportCodexNotification gives the events a Codex app-server notification
// carries to EventFanout and TypedEventCallback, the surfaces every other
// runtime reports its turns on. The adapter's ParseLine and ParseLineEvents see
// every frame but leave app-server's JSON-RPC to the runtime (go-providers'
// CodexAdapter says so), so without this a host reading either surface saw no
// turn at all: no final message, no end of turn.
//
// It runs on the session's reader goroutine, in frame order. The typed callback
// is called synchronously from it, so a callback must not wait on the reader
// (a Call from a Done handler would deadlock the session).
func (s *jsonRpcStdioSession) reportCodexNotification(method string, params json.RawMessage) {
	if s.adapter.Name() != string(runtimes.Codex) {
		return
	}
	tr := codexNotificationEvents(method, params)
	if tr.itemID != "" && s.codexItemSeen(tr.itemID) {
		return
	}
	for _, ev := range tr.stream {
		tryEventFanout(s.opts.EventFanout, ev)
	}
	if s.opts.TypedEventCallback != nil {
		for _, ev := range tr.typed {
			s.opts.TypedEventCallback(ev)
		}
	}
}

// codexItemSeen records an item id and reports whether it had been seen. Only
// the reader goroutine calls it, so it needs no lock.
func (s *jsonRpcStdioSession) codexItemSeen(id string) bool {
	if _, dup := s.codexSeen[id]; dup {
		return true
	}
	if s.codexSeen == nil {
		s.codexSeen = make(map[string]struct{}, codexSeenItems)
	}
	if len(s.codexSeenOrder) >= codexSeenItems {
		delete(s.codexSeen, s.codexSeenOrder[0])
		s.codexSeenOrder = s.codexSeenOrder[1:]
	}
	s.codexSeen[id] = struct{}{}
	s.codexSeenOrder = append(s.codexSeenOrder, id)
	return false
}

// codexUserInputMethod is the server request Codex sends when its model asks the
// user a question (the request_user_input tool); a host that answers no one
// refuses it.
const codexUserInputMethod = "requestuserinput"

// reportCodexRequest gives the events a Codex server request carries to
// EventFanout and TypedEventCallback, after the session has answered it. Only a
// request for user input is reported: as a call to the request_user_input tool,
// with the request's params as its arguments (the questions) and, on the typed
// surface, a tool result that is an error when the request was refused and not
// when a hook answered it. Approvals are reported by the item they gate instead
// (a declined item is a refusal, see codexNotificationEvents), and every other
// request yields nothing.
//
// Like reportCodexNotification it runs on the reader goroutine.
func (s *jsonRpcStdioSession) reportCodexRequest(frame jsonRpcFrame, refused bool) {
	if s.adapter.Name() != string(runtimes.Codex) || frame.ID == nil ||
		!strings.HasSuffix(strings.ToLower(frame.Method), codexUserInputMethod) {
		return
	}
	id := strings.Trim(strings.TrimSpace(string(*frame.ID)), `"`)
	args := map[string]any{}
	_ = json.Unmarshal(frame.Params, &args)
	const name = "request_user_input"
	tryEventFanout(s.opts.EventFanout, llmtypes.StreamEvent{
		Type:    llmtypes.EventToolUse,
		ToolUse: &llmtypes.ToolUseBlock{ID: id, Name: name, Input: args},
	})
	if s.opts.TypedEventCallback != nil {
		s.opts.TypedEventCallback(pevents.ToolUse{ID: id, Name: name, Args: args})
		s.opts.TypedEventCallback(pevents.ToolResult{ID: id, IsError: refused})
	}
}

// codexTranslation is what one notification reports: the same events on both
// surfaces, and the id of the item they describe (empty for a turn's end), so a
// repeated item is reported once.
type codexTranslation struct {
	stream []llmtypes.StreamEvent
	typed  []pevents.Event
	itemID string
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
//   - item/completed for a commandExecution or fileChange is a tool use, with a
//     tool result on the typed surface. A turn that only runs tools is a turn
//     with events, not one a consumer would take for a repeat of the last.
//   - turn/completed ends the turn: done with stop reason end_turn, or cancelled
//     for status "interrupted"; status "failed" is an error carrying the turn's
//     own message.
//
// Every other notification, and every other item (user messages, reasoning),
// yields nothing.
func codexNotificationEvents(method string, params json.RawMessage) codexTranslation {
	switch method {
	case "item/completed":
		var p struct {
			Item struct {
				Type             string          `json:"type"`
				ID               string          `json:"id"`
				Text             string          `json:"text"`
				Phase            string          `json:"phase"`
				Command          string          `json:"command"`
				Status           string          `json:"status"`
				AggregatedOutput string          `json:"aggregatedOutput"`
				Changes          json.RawMessage `json:"changes"`
			} `json:"item"`
		}
		if json.Unmarshal(params, &p) != nil {
			return codexTranslation{}
		}
		item := p.Item
		switch item.Type {
		case "agentMessage":
			if item.Text == "" {
				return codexTranslation{}
			}
			phase := ""
			switch item.Phase {
			case codexPhaseFinalAnswer:
				phase = llmtypes.PhaseFinal
			case codexPhaseCommentary:
				phase = llmtypes.PhaseNarration
			}
			return codexTranslation{
				stream: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: item.Text, BlockID: item.ID, Phase: phase}},
				typed:  []pevents.Event{pevents.Delta{Text: item.Text, Phase: phase, BlockID: item.ID}},
				itemID: item.ID,
			}
		case "commandExecution", "fileChange":
			args := map[string]any{}
			if item.Type == "commandExecution" {
				args["command"] = item.Command
			} else if len(item.Changes) > 0 {
				var changes any
				if json.Unmarshal(item.Changes, &changes) == nil {
					args["changes"] = changes
				}
			}
			typed := []pevents.Event{
				pevents.ToolUse{ID: item.ID, Name: item.Type, Args: args},
				pevents.ToolResult{ID: item.ID, IsError: item.Status == "failed" || item.Status == "declined", ContentPreview: cutPreview(item.AggregatedOutput)},
			}
			// A declined item is the posture refusing the action Codex asked
			// about: Codex's own record that it did not happen, which a consumer
			// would otherwise read as a tool call that merely did nothing.
			if item.Status == "declined" {
				typed = append(typed, pevents.PermissionDenied{Action: item.Type, DisplayName: codexItemLabel(item.Type, item.Command, item.Changes), ToolUseID: item.ID})
			}
			return codexTranslation{
				stream: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: item.ID, Name: item.Type, Input: args}}},
				typed:  typed,
				itemID: item.ID,
			}
		}
		return codexTranslation{}

	case "turn/completed":
		var p struct {
			Turn struct {
				Status string          `json:"status"`
				Error  json.RawMessage `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &p) != nil {
			return codexTranslation{}
		}
		switch p.Turn.Status {
		case "failed":
			msg := codexTurnError(p.Turn.Error)
			return codexTranslation{
				stream: []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: msg}},
				typed:  []pevents.Event{pevents.Error{Message: msg}},
			}
		case "interrupted":
			return codexTurnDone(llmtypes.StopReasonCancelled)
		default:
			return codexTurnDone(llmtypes.StopReasonEndTurn)
		}
	}
	return codexTranslation{}
}

// codexItemLabel is what a command or file-change item was about to act on: the
// command, or the paths it would have changed.
func codexItemLabel(typ, command string, changes json.RawMessage) string {
	if typ == "commandExecution" && command != "" {
		return cutPreview(command)
	}
	var list []struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(changes, &list) == nil {
		var paths []string
		for _, c := range list {
			if c.Path != "" {
				paths = append(paths, c.Path)
			}
		}
		if len(paths) > 0 {
			return cutPreview(strings.Join(paths, ", "))
		}
	}
	return typ
}

// codexTurnDone is a turn's end: the stop reason travels on a usage event on the
// legacy stream, which has no stop reason of its own on its done.
func codexTurnDone(stop string) codexTranslation {
	return codexTranslation{
		stream: []llmtypes.StreamEvent{
			{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: stop}},
			{Type: llmtypes.EventDone},
		},
		typed: []pevents.Event{pevents.Done{StopReason: stop}},
	}
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

// cutPreview bounds a tool's output to codexToolPreviewBytes on a rune boundary.
func cutPreview(s string) string {
	if len(s) <= codexToolPreviewBytes {
		return s
	}
	cut := codexToolPreviewBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
