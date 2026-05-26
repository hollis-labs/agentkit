package agentcontext

// Limits caps the size of the rendered ContextResult. Both fields are
// optional — a zero value means "unlimited along that axis".
//
// The library enforces Limits inside the default Renderer. Custom
// Renderers MAY ignore Limits, but SHOULD honour them for consistency.
type Limits struct {
	// MaxBytes caps the rendered output length in bytes. Zero means
	// unlimited. When non-zero, the default Renderer truncates the
	// trailing slots that would overflow the budget — the order is
	// input-order from the request, so callers control which slots
	// survive truncation by placing load-bearing slots first.
	MaxBytes int64 `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`

	// MaxTokens caps the rendered output length in tokens, estimated
	// via the documented char/4 heuristic (len(rendered) / 4). The
	// estimate is intentionally approximate — Phase 2 only needs the
	// budget plumbing, not a real tokenizer. Callers that need
	// precise tokenization SHOULD precompute against their tokenizer
	// of choice and pass the result via a tighter MaxBytes.
	//
	// Zero means unlimited.
	MaxTokens int `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
}

// EstimateTokens applies the library's char/4 heuristic to a byte
// length and returns the estimated token count. Exposed as a helper
// for symmetry with MaxTokens — callers and custom Renderers can use
// the same approximation the default Renderer uses.
//
// The heuristic is deliberately conservative: it overestimates short
// strings (where the per-token overhead is meaningful) and
// underestimates very long strings (where some tokens span more than
// 4 characters). It is NOT a substitute for the consumer's
// tokenizer; it exists to give the default Renderer a reasonable
// budget knob.
func EstimateTokens(byteLen int) int {
	if byteLen <= 0 {
		return 0
	}
	return (byteLen + 3) / 4
}

// LimitsApplied records what the Renderer did to honour Limits.
// Populated by the Renderer and copied onto ContextResult.Limits.
type LimitsApplied struct {
	// MaxBytes echoes the configured Limits.MaxBytes (or 0 for
	// unlimited). Provided so downstream consumers can audit the
	// applied policy without re-reading the request.
	MaxBytes int64 `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`

	// MaxTokens echoes the configured Limits.MaxTokens.
	MaxTokens int `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`

	// RenderedBytes is the byte length of the final rendered output.
	RenderedBytes int64 `yaml:"rendered_bytes" json:"rendered_bytes"`

	// EstimatedTokens is the char/4 estimate of the final rendered
	// output. Provided for symmetry with MaxTokens.
	EstimatedTokens int `yaml:"estimated_tokens" json:"estimated_tokens"`

	// DroppedSlots names slots that were skipped entirely because
	// they would have overflowed the budget. Names appear in input
	// order.
	DroppedSlots []string `yaml:"dropped_slots,omitempty" json:"dropped_slots,omitempty"`

	// TruncatedSlots names slots whose Content was shortened to fit
	// the budget. Names appear in input order. SlotResult.Truncated
	// is also set true on each truncated slot for downstream
	// inspection.
	TruncatedSlots []string `yaml:"truncated_slots,omitempty" json:"truncated_slots,omitempty"`
}
