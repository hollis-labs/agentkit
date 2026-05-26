package agentcontext

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubResolver implements Resolver via a captured function. Used by
// the provider tests so we can exercise the dispatch / render /
// budget logic without depending on Subagent B's concrete resolvers.
type stubResolver struct {
	fn func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error)
}

func (s stubResolver) Resolve(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
	return s.fn(ctx, spec, env)
}

func mustProvider(t *testing.T, resolvers map[SlotSourceKind]Resolver) *DefaultProvider {
	t.Helper()
	p, err := NewProvider(resolvers, DefaultRenderer{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	// Freeze clock for stable AssembledAt across tests that compare results.
	p.withClock(func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) })
	return p
}

func TestNewProviderNilArgs(t *testing.T) {
	t.Parallel()
	if _, err := NewProvider(nil, DefaultRenderer{}); !errors.Is(err, ErrMissingResolver) {
		t.Errorf("NewProvider(nil resolvers): want %v, got %v", ErrMissingResolver, err)
	}
	if _, err := NewProvider(map[SlotSourceKind]Resolver{}, nil); !errors.Is(err, ErrMissingRenderer) {
		t.Errorf("NewProvider(nil renderer): want %v, got %v", ErrMissingRenderer, err)
	}
}

func TestAssembleValidationBubblesUp(t *testing.T) {
	t.Parallel()
	p := mustProvider(t, map[SlotSourceKind]Resolver{})
	_, err := p.Assemble(context.Background(), ContextRequest{
		Slots: []SlotSpec{{Source: SlotSource{Kind: SlotSourceKindInline}}}, // no name
	})
	if !errors.Is(err, ErrMissingSlotName) {
		t.Fatalf("want ErrMissingSlotName, got %v", err)
	}
}

func TestAssembleUnknownKindWhenResolverMissing(t *testing.T) {
	t.Parallel()
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		// no resolvers registered
	})
	_, err := p.Assemble(context.Background(), ContextRequest{
		Slots: []SlotSpec{{Name: "a", Source: SlotSource{Kind: SlotSourceKindInline}}},
	})
	if !errors.Is(err, ErrUnknownSlotKind) {
		t.Fatalf("want ErrUnknownSlotKind, got %v", err)
	}
}

func TestAssembleDispatchesByKindAndComposes(t *testing.T) {
	t.Parallel()
	// Inline resolver returns the captured content; cmd resolver
	// returns a fixed string.
	inlineRes := stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
		return SlotResult{Content: spec.Source.Inline.Content}, nil
	}}
	cmdRes := stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
		return SlotResult{Content: "cmd-output"}, nil
	}}
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: inlineRes,
		SlotSourceKindCmd:    cmdRes,
	})

	req := ContextRequest{
		Slots: []SlotSpec{
			{Name: "intro", Section: "## §1", Source: SlotSource{Kind: SlotSourceKindInline, Inline: InlineSource{Content: "hello"}}},
			{Name: "git", Section: "## §4", Source: SlotSource{Kind: SlotSourceKindCmd, Cmd: CmdSource{Run: "git log"}}},
		},
		Workdir:    "/work",
		Provenance: ProvenanceInput{LineageAlias: "nanite.backend.main"},
	}

	res, err := p.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(res.Slots) != 2 {
		t.Fatalf("want 2 slot results, got %d", len(res.Slots))
	}
	if res.Slots[0].Name != "intro" || res.Slots[1].Name != "git" {
		t.Fatalf("slot order wrong: %+v", res.Slots)
	}
	if res.Slots[0].Content != "hello" || res.Slots[1].Content != "cmd-output" {
		t.Fatalf("contents wrong: %+v", res.Slots)
	}
	// Default Renderer composes section headers + bodies.
	if !strings.Contains(res.Rendered, "## §1") || !strings.Contains(res.Rendered, "hello") {
		t.Fatalf("rendered missing first slot: %q", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "## §4") || !strings.Contains(res.Rendered, "cmd-output") {
		t.Fatalf("rendered missing second slot: %q", res.Rendered)
	}
	// Provenance plumbed through.
	if res.Provenance.Input.LineageAlias != "nanite.backend.main" {
		t.Fatalf("input provenance not threaded: %+v", res.Provenance)
	}
	if res.Provenance.LibraryVersion != Version {
		t.Fatalf("library version not stamped: got %q", res.Provenance.LibraryVersion)
	}
	if res.Provenance.RequestHash == "" {
		t.Fatalf("request hash not stamped")
	}
	// Per-slot provenance kind backfilled.
	if res.Slots[0].Provenance.Kind != SlotSourceKindInline {
		t.Fatalf("provenance kind not backfilled, got %+v", res.Slots[0].Provenance)
	}
}

func TestAssembleNonRequiredSlotErrorRecorded(t *testing.T) {
	t.Parallel()
	resolveErr := errors.New("resolver-broke")
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
			return SlotResult{}, resolveErr
		}},
	})
	req := ContextRequest{
		Slots: []SlotSpec{
			{Name: "soft", Source: SlotSource{Kind: SlotSourceKindInline}, Required: false},
		},
	}
	res, err := p.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("expected non-required slot error NOT to fail Assemble, got %v", err)
	}
	if len(res.Slots) != 1 || !errors.Is(res.Slots[0].Err, resolveErr) {
		t.Fatalf("expected slot.Err to record resolver error, got %+v", res.Slots[0])
	}
}

func TestAssembleRequiredSlotResolverErrorBubbles(t *testing.T) {
	t.Parallel()
	resolveErr := errors.New("resolver-broke")
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
			return SlotResult{}, resolveErr
		}},
	})
	req := ContextRequest{
		Slots: []SlotSpec{
			{Name: "hard", Source: SlotSource{Kind: SlotSourceKindInline}, Required: true},
		},
	}
	_, err := p.Assemble(context.Background(), req)
	if !errors.Is(err, ErrRequiredSlotFailed) {
		t.Fatalf("expected ErrRequiredSlotFailed, got %v", err)
	}
	if !errors.Is(err, resolveErr) {
		t.Fatalf("expected wrapped resolver error to be reachable, got %v", err)
	}
}

func TestAssembleRequiredSlotEmptyContent(t *testing.T) {
	t.Parallel()
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
			// nil error, empty Content — caller said required.
			return SlotResult{}, nil
		}},
	})
	req := ContextRequest{
		Slots: []SlotSpec{
			{Name: "empty-required", Source: SlotSource{Kind: SlotSourceKindInline}, Required: true},
		},
	}
	_, err := p.Assemble(context.Background(), req)
	if !errors.Is(err, ErrSlotRequiredAndEmpty) {
		t.Fatalf("expected ErrSlotRequiredAndEmpty, got %v", err)
	}
}

func TestAssemblePassesEnvToResolver(t *testing.T) {
	t.Parallel()
	var seenWorkdir, seenLineage string
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
			seenWorkdir = env.Workdir
			seenLineage = env.RequestProvenance.LineageAlias
			return SlotResult{Content: "ok"}, nil
		}},
	})
	req := ContextRequest{
		Slots:      []SlotSpec{{Name: "a", Source: SlotSource{Kind: SlotSourceKindInline}}},
		Workdir:    "/Users/test/work",
		Provenance: ProvenanceInput{LineageAlias: "nanite.backend.main"},
	}
	if _, err := p.Assemble(context.Background(), req); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if seenWorkdir != "/Users/test/work" || seenLineage != "nanite.backend.main" {
		t.Fatalf("env not propagated: workdir=%q lineage=%q", seenWorkdir, seenLineage)
	}
}

func TestAssembleHonoursCtxCancel(t *testing.T) {
	t.Parallel()
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
			return SlotResult{Content: "ok"}, nil
		}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Assemble runs
	req := ContextRequest{
		Slots: []SlotSpec{{Name: "a", Source: SlotSource{Kind: SlotSourceKindInline}}},
	}
	_, err := p.Assemble(ctx, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAssembleMarksTruncatedSlots(t *testing.T) {
	t.Parallel()
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
			return SlotResult{Content: spec.Source.Inline.Content}, nil
		}},
	})
	req := ContextRequest{
		Limits: Limits{MaxBytes: 30},
		Slots: []SlotSpec{
			{Name: "big", Section: "H", Source: SlotSource{Kind: SlotSourceKindInline, Inline: InlineSource{Content: strings.Repeat("X", 500)}}},
		},
	}
	res, err := p.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !res.Slots[0].Truncated {
		t.Fatalf("expected slot marked truncated, got %+v", res.Slots[0])
	}
	if int64(len(res.Rendered)) > 30 {
		t.Fatalf("rendered exceeded budget: %d bytes", len(res.Rendered))
	}
}

func TestAssembleByteIdenticalOutputForSameRequest(t *testing.T) {
	t.Parallel()
	p := mustProvider(t, map[SlotSourceKind]Resolver{
		SlotSourceKindInline: stubResolver{fn: func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
			return SlotResult{Content: spec.Source.Inline.Content}, nil
		}},
	})
	req := ContextRequest{
		Slots: []SlotSpec{
			{Name: "a", Section: "## A", Source: SlotSource{Kind: SlotSourceKindInline, Inline: InlineSource{Content: "alpha"}}},
			{Name: "b", Section: "## B", Source: SlotSource{Kind: SlotSourceKindInline, Inline: InlineSource{Content: "beta"}}},
		},
	}
	res1, err := p.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 0; i < 8; i++ {
		got, err := p.Assemble(context.Background(), req)
		if err != nil {
			t.Fatalf("iter %d err: %v", i, err)
		}
		if got.Rendered != res1.Rendered {
			t.Fatalf("iter %d rendered differs", i)
		}
		if got.Provenance.RequestHash != res1.Provenance.RequestHash {
			t.Fatalf("iter %d hash differs", i)
		}
	}
}
