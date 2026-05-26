package resolvers

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func TestInlineResolver_Happy(t *testing.T) {
	t.Parallel()
	r := NewInlineResolver()
	spec := agentcontext.SlotSpec{
		Name: "intro",
		Source: agentcontext.SlotSource{
			Kind:   agentcontext.SlotSourceKindInline,
			Inline: agentcontext.InlineSource{Content: "hello world"},
		},
	}
	got, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "hello world" {
		t.Fatalf("Content: got %q, want %q", got.Content, "hello world")
	}
	if got.Provenance.Source != "inline" {
		t.Fatalf("Source: got %q", got.Provenance.Source)
	}
	if got.Provenance.ContentHash == "" {
		t.Fatalf("ContentHash empty")
	}
	if got.Provenance.FetchedAt.IsZero() {
		t.Fatalf("FetchedAt zero")
	}
}

func TestInlineResolver_Empty(t *testing.T) {
	t.Parallel()
	r := NewInlineResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{Name: "e", Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindInline}},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "" {
		t.Fatalf("expected empty content, got %q", got.Content)
	}
}

func TestInlineResolver_WrongKind(t *testing.T) {
	t.Parallel()
	r := NewInlineResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{Name: "x", Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindCmd}},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrResolverNotApplicable) {
		t.Fatalf("expected ErrResolverNotApplicable, got %v", err)
	}
}

func TestInlineResolver_Determinism(t *testing.T) {
	t.Parallel()
	r := NewInlineResolver()
	spec := agentcontext.SlotSpec{
		Name: "d",
		Source: agentcontext.SlotSource{
			Kind:   agentcontext.SlotSourceKindInline,
			Inline: agentcontext.InlineSource{Content: "deterministic"},
		},
	}
	a, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	b, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if a.Content != b.Content || a.Provenance.ContentHash != b.Provenance.ContentHash {
		t.Fatalf("determinism broken: %+v vs %+v", a, b)
	}
}

func TestInlineResolver_CtxCancel(t *testing.T) {
	t.Parallel()
	r := NewInlineResolver()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Resolve(ctx, agentcontext.SlotSpec{
		Name:   "c",
		Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindInline},
	}, agentcontext.ResolverEnv{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
