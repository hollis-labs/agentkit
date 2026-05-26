package turn

import "encoding/json"

// CodexNotificationKind names Codex app-server JSON-RPC notification methods
// that shared runtime callers commonly need to observe.
type CodexNotificationKind string

const (
	// CodexTurnCompleted is emitted when a turn has finished.
	CodexTurnCompleted CodexNotificationKind = "turn/completed"
	// CodexItemCompleted is emitted for completed tool, file, user, reasoning,
	// and assistant-message items.
	CodexItemCompleted CodexNotificationKind = "item/completed"
	// CodexTokenUsageUpdated is emitted with cumulative thread token totals.
	CodexTokenUsageUpdated CodexNotificationKind = "thread/tokenUsage/updated"
)

// CodexItem contains the provider-neutral fields apps need from supported
// Codex item/completed notifications.
type CodexItem struct {
	Type    string
	ID      string
	Command string
	Text    string
	Phase   string
}

// CodexTokenUsageTotals contains Codex cumulative thread token totals.
type CodexTokenUsageTotals struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

// IsCodexTurnCompleted reports whether method exactly matches Codex's
// turn-completion notification method.
func IsCodexTurnCompleted(method string) bool {
	return method == string(CodexTurnCompleted)
}

// ParseCodexItemCompleted extracts downstream-relevant fields from Codex
// app-server item/completed notification params. It returns ok=false for input
// echoes, unknown item types, empty assistant text, and malformed JSON.
func ParseCodexItemCompleted(params json.RawMessage) (CodexItem, bool) {
	var p struct {
		Item struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Command string `json:"command"`
			Text    string `json:"text"`
			Phase   string `json:"phase"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return CodexItem{}, false
	}
	item := CodexItem{
		Type:    p.Item.Type,
		ID:      p.Item.ID,
		Command: p.Item.Command,
		Text:    p.Item.Text,
		Phase:   p.Item.Phase,
	}
	switch item.Type {
	case "commandExecution":
		return item, true
	case "fileChange":
		return item, true
	case "agentMessage":
		if item.Text == "" {
			return CodexItem{}, false
		}
		return item, true
	default:
		return CodexItem{}, false
	}
}

// ParseCodexTokenUsageTotals extracts cumulative token counts from Codex
// thread/tokenUsage/updated notification params.
func ParseCodexTokenUsageTotals(params json.RawMessage) (CodexTokenUsageTotals, bool) {
	var p struct {
		TokenUsage struct {
			Total struct {
				InputTokens       int `json:"inputTokens"`
				OutputTokens      int `json:"outputTokens"`
				CachedInputTokens int `json:"cachedInputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return CodexTokenUsageTotals{}, false
	}
	totals := CodexTokenUsageTotals{
		InputTokens:       p.TokenUsage.Total.InputTokens,
		OutputTokens:      p.TokenUsage.Total.OutputTokens,
		CachedInputTokens: p.TokenUsage.Total.CachedInputTokens,
	}
	if totals.InputTokens == 0 && totals.OutputTokens == 0 && totals.CachedInputTokens == 0 {
		return CodexTokenUsageTotals{}, false
	}
	return totals, true
}

// CodexTokenUsageDelta converts cumulative Codex token totals into a per-update
// delta, clamping negative field deltas to zero.
func CodexTokenUsageDelta(prev, next CodexTokenUsageTotals) CodexTokenUsageTotals {
	return CodexTokenUsageTotals{
		InputTokens:       nonNegativeDelta(prev.InputTokens, next.InputTokens),
		OutputTokens:      nonNegativeDelta(prev.OutputTokens, next.OutputTokens),
		CachedInputTokens: nonNegativeDelta(prev.CachedInputTokens, next.CachedInputTokens),
	}
}

func nonNegativeDelta(prev, next int) int {
	if next <= prev {
		return 0
	}
	return next - prev
}
