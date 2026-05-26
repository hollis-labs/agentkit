package contexthook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/agentkit/agentcontext"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// New returns an agentlaunch.ContextHook that assembles mechanical
// context via the supplied agentcontext.ContextProvider.
//
// # Flow
//
//  1. Extract slots from the CompiledLaunch via cfg.SlotExtractor (or
//     return early with empty bootPrompt when no extractor is supplied
//     — the documented no-op behaviour).
//  2. Resolve the request Workdir using the same precedence
//     launcher.Prepare follows for the spawned process's working
//     directory: compiled.Plan.Workspace.Workdir →
//     compiled.ResolvedProjectRoot (the compiler-resolved absolute
//     form of plan.Project.Root) → compiled.Plan.Workspace.WorkspaceDir.
//  3. Build the ContextRequest, hand it to provider.Assemble.
//  4. If cfg.PlantArtifacts, write each non-empty
//     SlotResult.Content to <bootDir>/context/<sanitised-name>.txt.
//  5. Return ContextResult.Rendered as the bootPrompt the preparer
//     injects into PreparedLaunch.BootPrompt. (A non-empty return
//     overrides any inline boot prompt the compiled launch carried —
//     see agentlaunch.ContextHook godoc.)
//
// # Error wrapping
//
// All errors are wrapped under "agentlaunch/contexthook: …" so callers
// can recognise the adapter layer in failure traces.
//
// # Provider nil
//
// A nil provider yields a hook that returns ErrProviderNil on first
// invocation. We deliberately do NOT panic at construction so callers
// constructing the hook lazily (e.g. plumbing through a dependency
// container that nil-checks late) get a sentinel error instead of a
// crash.
func New(provider agentcontext.ContextProvider, cfg Config) agentlaunch.ContextHook {
	return func(ctx context.Context, bootDir string, compiled *agentlaunch.CompiledLaunch) (string, error) {
		if provider == nil {
			return "", fmt.Errorf("agentlaunch/contexthook: %w", ErrProviderNil)
		}
		if compiled == nil {
			return "", fmt.Errorf("agentlaunch/contexthook: %w", ErrCompiledNil)
		}

		// 1. Extract slots. Nil extractor → no-op (documented).
		var (
			slots []agentcontext.SlotSpec
			err   error
		)
		if cfg.SlotExtractor != nil {
			slots, err = cfg.SlotExtractor(compiled)
			if err != nil {
				return "", fmt.Errorf("agentlaunch/contexthook: slot extractor: %w", err)
			}
		}
		if len(slots) == 0 {
			// No work to do — match agentlaunch's "no hook registered"
			// shape: empty bootPrompt, no files planted.
			return "", nil
		}

		// 2. Resolve workdir using the same precedence
		// launcher.Prepare follows for the spawned process's working
		// directory: explicit Workspace.Workdir > ResolvedProjectRoot
		// (the compiler-resolved absolute form of plan.Project.Root) >
		// Workspace.WorkspaceDir. Keeping these in lockstep means slot
		// resolvers see the same cwd the spawned agent process will.
		var workdir string
		if compiled.Plan != nil {
			workdir = compiled.Plan.Workspace.Workdir
		}
		if workdir == "" {
			workdir = compiled.ResolvedProjectRoot
		}
		if workdir == "" && compiled.Plan != nil {
			workdir = compiled.Plan.Workspace.WorkspaceDir
		}

		// 3. Build provenance.
		var prov agentcontext.ProvenanceInput
		if cfg.ProvenanceFor != nil {
			prov = cfg.ProvenanceFor(compiled)
		} else {
			prov = defaultProvenance(compiled)
		}

		req := agentcontext.ContextRequest{
			Slots:      slots,
			Limits:     cfg.Limits,
			Workdir:    workdir,
			Provenance: prov,
		}

		// 4. Assemble.
		result, err := provider.Assemble(ctx, req)
		if err != nil {
			return "", fmt.Errorf("agentlaunch/contexthook: assemble: %w", err)
		}
		if result == nil {
			// Defensive — a custom provider implementation that returns
			// (nil, nil) would otherwise nil-deref below.
			return "", fmt.Errorf("agentlaunch/contexthook: %w", ErrProviderResultNil)
		}

		// 5. Plant per-slot artifacts if requested.
		if cfg.PlantArtifacts {
			if err := plantArtifacts(bootDir, result.Slots); err != nil {
				return "", fmt.Errorf("agentlaunch/contexthook: plant artifacts: %w", err)
			}
		}

		return result.Rendered, nil
	}
}

// defaultProvenance copies the compiled plan's Metadata.Labels into
// ProvenanceInput.Extra and lifts the lineage / role / project fields
// from agentlaunch identity-adjacent fields where they exist. Phase 1's
// LaunchPlan does NOT model an identity block yet (no
// lineage_alias / lineage_id / profile_id / profile_version), so those
// fields stay zero until Phase 1 gains them or a caller overrides via
// Config.ProvenanceFor.
func defaultProvenance(compiled *agentlaunch.CompiledLaunch) agentcontext.ProvenanceInput {
	in := agentcontext.ProvenanceInput{}
	if compiled == nil || compiled.Plan == nil {
		return in
	}
	plan := compiled.Plan
	in.Project = plan.Project.ID
	// agentlaunch has no Role field on AgentSpec today; leave Role
	// empty unless a Metadata label carries it.
	if plan.Metadata.Labels != nil {
		// Promote a "role" label to ProvenanceInput.Role if present,
		// matching how Tether catalog entries thread role identity.
		if v, ok := plan.Metadata.Labels["role"]; ok {
			in.Role = v
		}
		in.Extra = make(map[string]string, len(plan.Metadata.Labels))
		for k, v := range plan.Metadata.Labels {
			in.Extra[k] = v
		}
	}
	return in
}

// plantArtifacts writes each non-empty SlotResult.Content into
// <bootDir>/context/<sanitised-slot-name>.txt. The directory is created
// if absent. Empty-content slots are skipped — they would yield empty
// files with no signal.
//
// Slot names are sanitised to A-Za-z0-9_ which means two distinct slot
// names can collapse to the same filename (e.g. "foo-bar" and "foo_bar"
// both sanitise to "foo_bar"). Rather than silently overwriting the
// earlier artifact, we hard-fail with ErrArtifactNameCollision so the
// caller fixes the slot naming.
func plantArtifacts(bootDir string, results []agentcontext.SlotResult) error {
	if bootDir == "" {
		return fmt.Errorf("%w: bootDir empty", ErrPlantArtifacts)
	}
	dir := filepath.Join(bootDir, "context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%w: mkdir %q: %v", ErrPlantArtifacts, dir, err)
	}
	seen := make(map[string]string, len(results))
	for _, slot := range results {
		if slot.Content == "" {
			continue
		}
		name := sanitiseFilename(slot.Name)
		if prev, dup := seen[name]; dup && prev != slot.Name {
			return fmt.Errorf("%w: slot names %q and %q sanitise to the same filename %q.txt",
				ErrArtifactNameCollision, prev, slot.Name, name)
		}
		seen[name] = slot.Name
		path := filepath.Join(dir, name+".txt")
		if err := os.WriteFile(path, []byte(slot.Content), 0o644); err != nil {
			return fmt.Errorf("%w: write %q: %v", ErrPlantArtifacts, path, err)
		}
	}
	return nil
}

// sanitiseFilename collapses a slot name to A-Z a-z 0-9 _ — other runes
// become underscores. Empty input yields "_unnamed" so the planted file
// is still discoverable.
func sanitiseFilename(in string) string {
	if in == "" {
		return "_unnamed"
	}
	var b strings.Builder
	b.Grow(len(in))
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_unnamed"
	}
	return out
}
