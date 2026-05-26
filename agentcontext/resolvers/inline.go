package resolvers

import (
	"context"
	"fmt"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// InlineResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindInline. It copies SlotSource.Inline.Content
// verbatim onto SlotResult.Content.
//
// Useful as a fallback for slots whose content has been pre-resolved by
// the orchestrator (e.g. a Vanta memory_recall result stuffed inline)
// and for test fixtures.
type InlineResolver struct{}

// NewInlineResolver returns a ready-to-use InlineResolver. The
// resolver has no configurable behaviour.
func NewInlineResolver() agentcontext.Resolver {
	return InlineResolver{}
}

// Resolve implements agentcontext.Resolver.
func (InlineResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindInline {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindInline)
	}
	content := spec.Source.Inline.Content
	return agentcontext.SlotResult{
		Content: content,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindInline,
			Source:      "inline",
			Bytes:       len(content),
			ContentHash: hashContent(content),
			FetchedAt:   nowUTC(),
		},
	}, nil
}
