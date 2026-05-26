package agentcontext

import "testing"

// helper to build a representative request used across the hash tests.
func sampleRequest() ContextRequest {
	return ContextRequest{
		Slots: []SlotSpec{
			{
				Name:    "role",
				Section: "## §1",
				Source: SlotSource{
					Kind:        SlotSourceKindRoleSummary,
					RoleSummary: RoleSummarySource{Path: "~/.nanite/roles/domain/backend/worker.md"},
				},
			},
			{
				Name:    "history",
				Section: "## §4 — Git",
				Source: SlotSource{
					Kind: SlotSourceKindCmd,
					Cmd:  CmdSource{Run: "git log --oneline -20"},
				},
			},
		},
		Limits:  Limits{MaxBytes: 32_000},
		Workdir: "/work",
		Provenance: ProvenanceInput{
			LineageAlias: "nanite.backend.main",
			ProfileID:    "nanite-backend",
			Project:      "nanite",
			Role:         "backend",
			Extra: map[string]string{
				"z_extra": "value-z",
				"a_extra": "value-a",
			},
		},
	}
}

func TestHashRequestDeterministic(t *testing.T) {
	t.Parallel()
	req := sampleRequest()
	first, err := HashRequest(req)
	if err != nil {
		t.Fatalf("HashRequest err: %v", err)
	}
	for i := 0; i < 16; i++ {
		got, err := HashRequest(req)
		if err != nil {
			t.Fatalf("HashRequest iter %d err: %v", i, err)
		}
		if got != first {
			t.Fatalf("HashRequest iter %d differs: got %s, first %s", i, got, first)
		}
	}
	// 64-char hex digest.
	if len(first) != 64 {
		t.Fatalf("expected 64-char hex digest, got %d (%q)", len(first), first)
	}
}

func TestHashRequestMapOrderInvariant(t *testing.T) {
	t.Parallel()
	req := sampleRequest()
	a, err := HashRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Reassign Extra with same key/value pairs in a fresh map literal —
	// Go does not guarantee map iteration order across runs, but JSON
	// marshaling does sort keys; the test still proves canonicalization
	// is stable across map identity.
	req2 := sampleRequest()
	req2.Provenance.Extra = map[string]string{
		"a_extra": "value-a",
		"z_extra": "value-z",
	}
	b, err := HashRequest(req2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a != b {
		t.Fatalf("hash differed across equal-content maps: %s vs %s", a, b)
	}
}

func TestHashRequestDifferentInputsDiffer(t *testing.T) {
	t.Parallel()
	base, err := HashRequest(sampleRequest())
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Mutating different fields should yield different hashes.
	mutations := []struct {
		name   string
		mutate func(*ContextRequest)
	}{
		{
			name: "added slot",
			mutate: func(r *ContextRequest) {
				r.Slots = append(r.Slots, SlotSpec{
					Name:   "extra",
					Source: SlotSource{Kind: SlotSourceKindInline, Inline: InlineSource{Content: "x"}},
				})
			},
		},
		{
			name:   "reordered slots",
			mutate: func(r *ContextRequest) { r.Slots[0], r.Slots[1] = r.Slots[1], r.Slots[0] },
		},
		{
			name:   "changed limits",
			mutate: func(r *ContextRequest) { r.Limits.MaxBytes = 64_000 },
		},
		{
			name:   "changed workdir",
			mutate: func(r *ContextRequest) { r.Workdir = "/different" },
		},
		{
			name:   "changed lineage",
			mutate: func(r *ContextRequest) { r.Provenance.LineageAlias = "nanite.backend.shadow" },
		},
		{
			name:   "extra key changed",
			mutate: func(r *ContextRequest) { r.Provenance.Extra["a_extra"] = "different" },
		},
	}
	for _, m := range mutations {
		m := m
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			r := sampleRequest()
			m.mutate(&r)
			got, err := HashRequest(r)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got == base {
				t.Fatalf("hash unchanged after mutation %q (both = %s)", m.name, base)
			}
		})
	}
}
