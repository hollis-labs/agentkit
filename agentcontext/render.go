package agentcontext

import (
	"strings"
)

// Renderer composes a slice of resolved SlotResults into a single
// rendered output string. It is also the budget-enforcement point —
// the Renderer decides which slots to truncate or drop when Limits
// would otherwise be exceeded.
//
// # Determinism contract
//
//   - For identical (slots, limits) inputs, Render MUST return
//     byte-identical (output, LimitsApplied) pairs.
//   - Slots MUST be emitted in input order (the order of the slice).
//     Renderers MUST NOT sort by name or by any other key.
//
// # Budget contract
//
// When limits.MaxBytes > 0, the renderer SHOULD truncate or drop
// trailing slots so the rendered output's byte length is <=
// MaxBytes. The default renderer policy is: emit slots in order;
// when emitting the next slot's section header + body would exceed
// MaxBytes, attempt to truncate the slot body to fit; if even an
// empty body (header only) would overflow, drop the slot and every
// trailing slot.
//
// When limits.MaxTokens > 0, the same policy applies using the
// EstimateTokens char/4 heuristic.
type Renderer interface {
	Render(slots []SlotResult, limits Limits) (string, LimitsApplied)
}

// DefaultRenderer is the package's section-headered, input-order
// Renderer. It emits each slot as:
//
//	<Section or Name>
//	<Content>
//
// separated by a single blank line. Empty Content slots emit only
// the header. Budget enforcement follows the policy documented on
// Renderer.
//
// A zero-value DefaultRenderer{} is usable; callers do not need to
// construct it through a helper.
type DefaultRenderer struct{}

// Render implements Renderer.
func (DefaultRenderer) Render(slots []SlotResult, limits Limits) (string, LimitsApplied) {
	applied := LimitsApplied{
		MaxBytes:  limits.MaxBytes,
		MaxTokens: limits.MaxTokens,
	}

	var (
		buf              strings.Builder
		isFirst          = true
		droppedFromIndex = -1
	)

	for i := range slots {
		s := slots[i]

		// Build this slot's full section (header + blank + body).
		header := s.Section
		if header == "" {
			header = s.Name
		}

		var slotBuf strings.Builder
		if !isFirst {
			slotBuf.WriteString("\n\n")
		}
		slotBuf.WriteString(header)
		if s.Content != "" {
			slotBuf.WriteString("\n")
			slotBuf.WriteString(s.Content)
		}

		// Compute prospective totals.
		prospectiveBytes := int64(buf.Len() + slotBuf.Len())
		prospectiveTokens := EstimateTokens(int(prospectiveBytes))

		overByByte := limits.MaxBytes > 0 && prospectiveBytes > limits.MaxBytes
		overByTok := limits.MaxTokens > 0 && prospectiveTokens > limits.MaxTokens

		if !overByByte && !overByTok {
			// Fits — emit verbatim. Per-slot bookkeeping
			// (SlotResult.Truncated) is reconciled by the
			// dispatcher using applied.TruncatedSlots /
			// applied.DroppedSlots after Render returns.
			buf.WriteString(slotBuf.String())
			isFirst = false
			continue
		}

		// Doesn't fit. Try truncation if the slot has content and
		// the header alone would fit.
		headerOnlyLen := slotBuf.Len() - len(s.Content)
		if s.Content == "" {
			// Header-only slot can't be truncated further — must drop.
			droppedFromIndex = i
			break
		}
		prospectiveHeaderOnly := int64(buf.Len() + headerOnlyLen)
		headerOverByte := limits.MaxBytes > 0 && prospectiveHeaderOnly > limits.MaxBytes
		headerOverTok := limits.MaxTokens > 0 && EstimateTokens(int(prospectiveHeaderOnly)) > limits.MaxTokens
		if headerOverByte || headerOverTok {
			// Even the header overflows — drop this slot and every
			// trailing slot.
			droppedFromIndex = i
			break
		}

		// Truncate body to fit. Pick the tighter of the two budgets.
		availBytes := int64(-1)
		if limits.MaxBytes > 0 {
			availBytes = limits.MaxBytes - prospectiveHeaderOnly
		}
		if limits.MaxTokens > 0 {
			tokenBudget := int64(limits.MaxTokens*4) - prospectiveHeaderOnly
			if availBytes < 0 || tokenBudget < availBytes {
				availBytes = tokenBudget
			}
		}
		if availBytes < 0 {
			availBytes = 0
		}
		if availBytes > int64(len(s.Content)) {
			availBytes = int64(len(s.Content))
		}

		buf.WriteString(slotBuf.String()[:headerOnlyLen])
		buf.WriteString(s.Content[:availBytes])
		applied.TruncatedSlots = append(applied.TruncatedSlots, s.Name)
		isFirst = false
		// After a truncation, any further slot would overflow; drop
		// the rest.
		if i+1 < len(slots) {
			droppedFromIndex = i + 1
		}
		break
	}

	if droppedFromIndex >= 0 {
		for _, s := range slots[droppedFromIndex:] {
			applied.DroppedSlots = append(applied.DroppedSlots, s.Name)
		}
	}

	rendered := buf.String()
	applied.RenderedBytes = int64(len(rendered))
	applied.EstimatedTokens = EstimateTokens(len(rendered))
	return rendered, applied
}
