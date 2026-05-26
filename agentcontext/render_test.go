package agentcontext

import (
	"strings"
	"testing"
)

func TestDefaultRendererInputOrder(t *testing.T) {
	t.Parallel()
	// Names chosen so alphabetical and input order differ.
	slots := []SlotResult{
		{Name: "zebra", Section: "## §1 Z", Content: "z-body"},
		{Name: "alpha", Section: "## §2 A", Content: "a-body"},
		{Name: "mike", Section: "## §3 M", Content: "m-body"},
	}
	out, _ := DefaultRenderer{}.Render(slots, Limits{})
	zIdx := strings.Index(out, "z-body")
	aIdx := strings.Index(out, "a-body")
	mIdx := strings.Index(out, "m-body")
	if zIdx < 0 || aIdx < 0 || mIdx < 0 {
		t.Fatalf("missing body in output: %q", out)
	}
	if !(zIdx < aIdx && aIdx < mIdx) {
		t.Fatalf("expected input order (z, a, m); got positions z=%d a=%d m=%d in %q", zIdx, aIdx, mIdx, out)
	}
}

func TestDefaultRendererFallbackHeaderUsesName(t *testing.T) {
	t.Parallel()
	slots := []SlotResult{{Name: "headers-falls-back", Content: "body"}}
	out, _ := DefaultRenderer{}.Render(slots, Limits{})
	if !strings.Contains(out, "headers-falls-back") {
		t.Fatalf("expected name as fallback header, got %q", out)
	}
}

func TestDefaultRendererEmptyContentEmitsHeaderOnly(t *testing.T) {
	t.Parallel()
	slots := []SlotResult{
		{Name: "a", Section: "Sec A", Content: ""},
		{Name: "b", Section: "Sec B", Content: "body"},
	}
	out, _ := DefaultRenderer{}.Render(slots, Limits{})
	// Should contain "Sec A" but not be followed immediately by content.
	if !strings.Contains(out, "Sec A") || !strings.Contains(out, "Sec B\nbody") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestDefaultRendererDeterministicByteIdentical(t *testing.T) {
	t.Parallel()
	slots := []SlotResult{
		{Name: "one", Section: "## A", Content: "alpha"},
		{Name: "two", Section: "## B", Content: "beta"},
		{Name: "three", Section: "## C", Content: "gamma"},
	}
	first, firstApplied := DefaultRenderer{}.Render(slots, Limits{MaxBytes: 4096})
	for i := 0; i < 16; i++ {
		got, gotApplied := DefaultRenderer{}.Render(slots, Limits{MaxBytes: 4096})
		if got != first {
			t.Fatalf("iter %d differed: %q vs %q", i, got, first)
		}
		if gotApplied.RenderedBytes != firstApplied.RenderedBytes {
			t.Fatalf("iter %d applied differs: %+v vs %+v", i, gotApplied, firstApplied)
		}
	}
}

func TestDefaultRendererByteBudgetDropsTrailingSlots(t *testing.T) {
	t.Parallel()
	// Each slot is well-defined; budget is below the third slot's
	// header start, so the third slot must be dropped wholesale.
	slots := []SlotResult{
		{Name: "a", Section: "Sec A", Content: "AAAAAAAAAA"}, // ~16 bytes
		{Name: "b", Section: "Sec B", Content: "BBBBBBBBBB"}, // ~16 bytes; gap+header+body
		{Name: "c", Section: "Sec C", Content: "CCCCCCCCCC"}, // dropped
	}
	out, applied := DefaultRenderer{}.Render(slots, Limits{MaxBytes: 36})
	if strings.Contains(out, "Sec C") || strings.Contains(out, "CCCC") {
		t.Fatalf("expected slot C dropped, got %q (applied=%+v)", out, applied)
	}
	if len(applied.DroppedSlots) == 0 || applied.DroppedSlots[len(applied.DroppedSlots)-1] != "c" {
		t.Fatalf("expected c in DroppedSlots, got %+v", applied)
	}
	if applied.RenderedBytes > 36 {
		t.Fatalf("rendered bytes %d exceeds budget 36", applied.RenderedBytes)
	}
}

func TestDefaultRendererByteBudgetTruncatesBody(t *testing.T) {
	t.Parallel()
	slots := []SlotResult{
		{Name: "a", Section: "Header", Content: strings.Repeat("X", 1000)},
	}
	out, applied := DefaultRenderer{}.Render(slots, Limits{MaxBytes: 50})
	if int64(len(out)) > 50 {
		t.Fatalf("rendered bytes %d exceeds budget 50", len(out))
	}
	if len(applied.TruncatedSlots) != 1 || applied.TruncatedSlots[0] != "a" {
		t.Fatalf("expected TruncatedSlots=[a], got %+v", applied.TruncatedSlots)
	}
	if !strings.Contains(out, "Header") {
		t.Fatalf("expected header to survive truncation, got %q", out)
	}
}

func TestDefaultRendererTokenBudgetEnforced(t *testing.T) {
	t.Parallel()
	// MaxTokens=10 -> ~40 bytes budget via char/4 heuristic.
	slots := []SlotResult{
		{Name: "a", Section: "H", Content: strings.Repeat("X", 200)},
	}
	out, applied := DefaultRenderer{}.Render(slots, Limits{MaxTokens: 10})
	if EstimateTokens(len(out)) > 10 {
		t.Fatalf("rendered token estimate %d exceeds budget 10 (rendered=%q)", EstimateTokens(len(out)), out)
	}
	if applied.EstimatedTokens > 10 {
		t.Fatalf("applied.EstimatedTokens=%d exceeds budget", applied.EstimatedTokens)
	}
}

func TestDefaultRendererHeaderOverflowDropsSlot(t *testing.T) {
	t.Parallel()
	// Budget is smaller than just the first header — entire slot must drop.
	slots := []SlotResult{
		{Name: "verylongheader", Section: strings.Repeat("H", 100), Content: "tiny"},
	}
	out, applied := DefaultRenderer{}.Render(slots, Limits{MaxBytes: 10})
	if out != "" {
		t.Fatalf("expected empty output when budget < header, got %q", out)
	}
	if len(applied.DroppedSlots) != 1 || applied.DroppedSlots[0] != "verylongheader" {
		t.Fatalf("expected slot dropped, got %+v", applied)
	}
}

func TestDefaultRendererUnlimitedBudget(t *testing.T) {
	t.Parallel()
	slots := []SlotResult{
		{Name: "a", Section: "A", Content: strings.Repeat("X", 1<<14)},
	}
	out, applied := DefaultRenderer{}.Render(slots, Limits{})
	if len(applied.DroppedSlots) != 0 || len(applied.TruncatedSlots) != 0 {
		t.Fatalf("expected nothing dropped/truncated under unlimited budget, got %+v", applied)
	}
	if !strings.Contains(out, strings.Repeat("X", 1<<14)) {
		t.Fatalf("expected full content under unlimited budget")
	}
}
