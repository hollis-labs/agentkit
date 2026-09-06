package agentlaunch

import (
	"context"
	"path"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/artifact"
	"github.com/hollis-labs/agentkit/materialize"
)

// SharedPrepareOptions configure ResolvePreparation.
type SharedPrepareOptions struct {
	Composer    agentcontext.Composer
	Definitions []agentcontext.AuthoredRecipe
	Engine      materialize.Engine
}

// SharedPrepareOption mutates SharedPrepareOptions.
type SharedPrepareOption func(*SharedPrepareOptions)

func WithComposer(c agentcontext.Composer) SharedPrepareOption {
	return func(o *SharedPrepareOptions) { o.Composer = c }
}

func WithCompositionDefinitions(defs []agentcontext.AuthoredRecipe) SharedPrepareOption {
	return func(o *SharedPrepareOptions) { o.Definitions = append([]agentcontext.AuthoredRecipe(nil), defs...) }
}

func WithMaterializationEngine(e materialize.Engine) SharedPrepareOption {
	return func(o *SharedPrepareOptions) { o.Engine = e }
}

// ResolvePreparation is the shared preparation path for raw artifact,
// already-resolved composition and authored-recipe callers. It normalizes the
// input into one artifact tree, optionally installs it when Roots.BootRoot is
// set, and carries provider projection bindings/effects/diagnostics/access
// through without requiring a process launch.
func ResolvePreparation(ctx context.Context, req PrepareRequest, opts ...SharedPrepareOption) (*PreparedExecution, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	cfg := SharedPrepareOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	composition, tree, err := resolvePreparationArtifacts(ctx, req, cfg)
	if err != nil {
		return nil, err
	}
	if len(req.Projection.Artifacts.Entries) > 0 {
		tree, err = mergeArtifactTrees(req.Projection.Artifacts, tree)
		if err != nil {
			return nil, err
		}
	}
	prepared := &PreparedExecution{
		InputKind:   req.Kind,
		Composition: composition,
		Artifacts:   tree,
		Bindings:    req.Projection.Bindings,
		Roots:       req.Roots,
		Access:      withAccessRoots(req.Access, req.Roots),
		Effects:     append([]RuntimeEffect(nil), req.Projection.Effects...),
		Diagnostics: append([]CapabilityDiagnostic(nil), req.Projection.Diagnostics...),
		Legacy:      req.Legacy,
	}
	if prepared.Bindings.CWD == "" {
		prepared.Bindings.CWD = req.Roots.CWD
	}
	if req.Roots.BootRoot != "" && len(tree.Entries) > 0 {
		handle, err := MaterializeArtifacts(ctx, ArtifactMaterializationRequest{
			TargetRoot: req.Roots.BootRoot,
			Roots:      req.Roots,
			Artifacts:  tree,
			Operation:  materialize.OperationReconcile,
			Engine:     cfg.Engine,
			Reconcile:  materialize.ReconcilePolicy{Conflict: materialize.ConflictOverwrite},
		})
		if err != nil {
			return nil, err
		}
		prepared.Materialization = handle
	}
	return prepared, nil
}

func resolvePreparationArtifacts(ctx context.Context, req PrepareRequest, cfg SharedPrepareOptions) (*agentcontext.ResolvedComposition, artifact.Tree, error) {
	switch req.Kind {
	case PrepareInputArtifacts:
		entries, err := artifact.Normalize(req.Artifacts.Entries)
		if err != nil {
			return nil, artifact.Tree{}, err
		}
		return nil, artifact.Tree{Entries: entries, Provenance: req.Artifacts.Provenance}, nil
	case PrepareInputResolvedComposition:
		comp, err := agentcontext.NormalizeResolvedComposition(*req.Composition)
		if err != nil {
			return nil, artifact.Tree{}, err
		}
		tree, err := compositionArtifactTree(comp)
		if err != nil {
			return nil, artifact.Tree{}, err
		}
		return &comp, tree, nil
	case PrepareInputAuthoredRecipe:
		composer := cfg.Composer
		if composer == nil {
			composer = agentcontext.NewComposer(agentcontext.ComposerOptions{})
		}
		comp, err := composer.Compose(agentcontext.ComposeRequest{Recipe: *req.Recipe, Definitions: cfg.Definitions})
		if err != nil {
			return nil, artifact.Tree{}, err
		}
		tree, err := compositionArtifactTree(comp)
		if err != nil {
			return nil, artifact.Tree{}, err
		}
		return &comp, tree, nil
	default:
		return nil, artifact.Tree{}, ErrPrepareInputUnknown
	}
}

func compositionArtifactTree(comp agentcontext.ResolvedComposition) (artifact.Tree, error) {
	entries := append([]artifact.Entry(nil), comp.Artifacts.Entries...)
	for _, doc := range comp.Documents {
		rel := doc.Path
		if rel == "" {
			rel = path.Clean(doc.ID + ".md")
		}
		entries = upsertPreparationEntry(entries, artifact.Entry{
			Path:  rel,
			Kind:  artifact.EntryFile,
			Mode:  0o644,
			Bytes: []byte(doc.Content),
			Ownership: artifact.Ownership{
				EntryID: "composition:document:" + doc.ID,
				GroupID: "composition:documents",
			},
			Provenance: artifact.Provenance{Source: "agentcontext.ResolvedComposition", SourcePath: doc.ID},
		})
	}
	normalized, err := artifact.Normalize(entries)
	if err != nil {
		return artifact.Tree{}, err
	}
	return artifact.Tree{Entries: normalized, Provenance: comp.Artifacts.Provenance}, nil
}

func mergeArtifactTrees(base, overlay artifact.Tree) (artifact.Tree, error) {
	entries := append([]artifact.Entry(nil), base.Entries...)
	for _, entry := range overlay.Entries {
		entries = upsertPreparationEntry(entries, entry)
	}
	normalized, err := artifact.Normalize(entries)
	if err != nil {
		return artifact.Tree{}, err
	}
	return artifact.Tree{Entries: normalized, Provenance: overlay.Provenance}, nil
}

func upsertPreparationEntry(entries []artifact.Entry, next artifact.Entry) []artifact.Entry {
	for i := range entries {
		if entries[i].Path == next.Path {
			entries[i] = next
			return entries
		}
	}
	return append(entries, next)
}

func withAccessRoots(access AccessRequirements, roots ExecutionRoots) AccessRequirements {
	if access.Roots == (ExecutionRoots{}) {
		access.Roots = roots
	}
	if access.Mode == "" {
		access.Mode = AccessOptional
	}
	if access.Host == "" {
		access.Host = ExecutionHostLocal
	}
	return access
}
