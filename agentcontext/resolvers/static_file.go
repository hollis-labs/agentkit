package resolvers

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// StaticFileResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindStaticFile.
//
// The resolver reads the entire file at SlotSource.StaticFile.Path,
// expanding a leading ~ via os.UserHomeDir and resolving relative
// paths against ResolverEnv.Workdir. The file is read verbatim — no
// truncation or summarization happens here. Budget enforcement is the
// Renderer / Limits layer's job; size-bound concerns for role-style
// prompts go through RoleSummaryResolver.
//
// # Error model
//
//   - os.IsNotExist(err) propagates so callers can branch on missing
//     files.
//   - Other I/O errors propagate wrapped with context.
type StaticFileResolver struct{}

// NewStaticFileResolver returns a ready-to-use StaticFileResolver.
// The resolver has no configurable behaviour.
func NewStaticFileResolver() agentcontext.Resolver {
	return StaticFileResolver{}
}

// Resolve implements agentcontext.Resolver.
func (StaticFileResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindStaticFile {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindStaticFile)
	}

	path, err := resolveFilePath(spec.Source.StaticFile.Path, env.Workdir)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("static_file: resolve path: %w", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path; SlotSpec.Validate guards ..
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("static_file: read %s: %w", path, err)
	}
	content := string(data)
	return agentcontext.SlotResult{
		Content: content,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindStaticFile,
			Source:      path,
			Bytes:       len(content),
			ContentHash: hashContent(content),
			FetchedAt:   nowUTC(),
		},
	}, nil
}
