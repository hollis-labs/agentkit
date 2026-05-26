package launcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/matrix"
)

// PrepareOptions tunes the Prepare entry point. The zero value is the
// production default: no hooks registered, the materialized bootdir is
// created empty.
type PrepareOptions struct {
	// BootDirHook plants files into the materialized bootdir. Optional.
	// See the agentlaunch.BootDirHook type for behaviour.
	BootDirHook agentlaunch.BootDirHook

	// ContextHook assembles mechanical context and may override
	// PreparedLaunch.BootPrompt. Optional. See agentlaunch.ContextHook.
	ContextHook agentlaunch.ContextHook

	// WorkspaceHook seeds the workspace dir after it is resolved.
	// Optional. See agentlaunch.WorkspaceHook.
	WorkspaceHook agentlaunch.WorkspaceHook
}

// PrepareOption mutates a PrepareOptions value.
type PrepareOption func(*PrepareOptions)

// WithBootDirHook registers a BootDirHook to be invoked after the
// bootdir is created but before the prepared launch is returned.
func WithBootDirHook(h agentlaunch.BootDirHook) PrepareOption {
	return func(o *PrepareOptions) { o.BootDirHook = h }
}

// WithContextHook registers a ContextHook to be invoked after the
// BootDirHook completes. A non-empty bootPrompt returned by the hook
// overrides any inline boot prompt the compiled launch carried.
func WithContextHook(h agentlaunch.ContextHook) PrepareOption {
	return func(o *PrepareOptions) { o.ContextHook = h }
}

// WithWorkspaceHook registers a WorkspaceHook to be invoked after the
// workspace dir is resolved, before the bootdir hook runs.
func WithWorkspaceHook(h agentlaunch.WorkspaceHook) PrepareOption {
	return func(o *PrepareOptions) { o.WorkspaceHook = h }
}

// Prepare materializes a CompiledLaunch into a PreparedLaunch: resolves
// the workspace dir, allocates a per-launch bootdir, composes the final
// env and argv, and runs the registered hooks in a documented order.
//
// # Hook invocation order
//
//  1. WorkspaceHook — invoked once the workspace dir is resolved and
//     created (for temp / fresh modes) but before the bootdir exists.
//     Use this to clone source repos or seed scratch dirs.
//  2. BootDirHook — invoked once the bootdir is created (empty) and
//     before context assembly. Plant per-provider boot files here.
//  3. ContextHook — invoked last. May write additional files into the
//     bootdir AND return a boot-prompt string that overrides any inline
//     prompt.
//
// Any hook returning a non-nil error short-circuits Prepare with the
// hook's error wrapped under "agentlaunch/prepare: <hook>: ...".
//
// # Filesystem effects
//
// Prepare does touch the filesystem: it calls os.MkdirTemp for the
// bootdir (and the workspace dir for temp/fresh modes), and may create
// the workspace dir under WorkspaceFresh / WorkspacePersistent. It does
// NOT plant any per-provider boot files itself — that is the
// BootDirHook's job. Without a hook registered, the bootdir is created
// but empty.
//
// # Caller responsibilities
//
// The caller owns cleanup of the returned bootdir (and the workspace
// dir for temp mode). Phase 2 of SP-20260514-0003 wires a tear-down
// helper into go-agent-sessions's session-end handler.
func Prepare(ctx context.Context, compiled *agentlaunch.CompiledLaunch, opts ...PrepareOption) (*agentlaunch.PreparedLaunch, error) {
	if compiled == nil {
		return nil, fmt.Errorf("agentlaunch/prepare: %w", agentlaunch.ErrCompiledMissingPlan)
	}
	if err := compiled.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/prepare: %w", err)
	}

	options := PrepareOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	plan := compiled.Plan

	// Re-lookup the matrix descriptor — Compile already validated the
	// pair, so this should not fail; we surface any error defensively.
	desc, err := matrix.Lookup(plan.Provider, plan.Runtime)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/prepare: %w", err)
	}

	// 1. Resolve workspace dir per workspace mode.
	workspaceDir, err := resolveWorkspaceDir(plan)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/prepare: %w", err)
	}

	// 2. Invoke the workspace hook now that the workspace dir is known
	//    (but before the bootdir exists).
	if options.WorkspaceHook != nil {
		if err := options.WorkspaceHook(ctx, workspaceDir, compiled); err != nil {
			return nil, fmt.Errorf("agentlaunch/prepare: workspace hook: %w", err)
		}
	}

	// 3. Allocate the bootdir as a sibling of the workspace (under the
	//    same temp-prefix rules). Bootdir is always per-launch, even
	//    for persistent workspaces.
	bootDir, err := allocateBootDir(plan, compiled.Provenance.PlanHash)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/prepare: %w", err)
	}

	// 4. Invoke the bootdir hook to plant per-provider boot files.
	if options.BootDirHook != nil {
		if err := options.BootDirHook(ctx, bootDir, compiled); err != nil {
			return nil, fmt.Errorf("agentlaunch/prepare: bootdir hook: %w", err)
		}
	}

	// 5. Workdir defaults to project.root, then workspaceDir.
	workdir := plan.Workspace.Workdir
	if workdir == "" {
		workdir = plan.Project.Root
	}
	if workdir == "" {
		workdir = workspaceDir
	}

	// 6. Compose env: provider env ∪ injection env (injection wins).
	env := composeEnv(plan)

	// 7. Compose argv: [binary, provider.flags..., injection.args...].
	argv := composeArgv(plan, desc)

	// 8. PlantContext — fill in the fields we know at this layer.
	plantCtx := agentlaunch.PreparedPlantContext{
		AgentName: plan.Agent.Name,
	}
	if plantCtx.AgentName == "" {
		plantCtx.AgentName = plan.Agent.ID
	}

	// 9. Resolve boot prompt + content. Inline wins if supplied; the
	//    real catalog-resolution path (Phase 2 work in CW-0003-land) is
	//    not implemented here. Document the TODO.
	var bootPrompt, bootContent, bootMode string
	if plan.BootProfile.Inline != nil {
		bootPrompt = plan.BootProfile.Inline.BootPrompt
		bootContent = plan.BootProfile.Inline.BootContent
		bootMode = plan.BootProfile.Inline.BootMode
	}
	// TODO (Phase 2): when BootProfile.CatalogPath is set and Inline is
	// nil, resolve the catalog entry via the catalog port (CW-0003) and
	// populate bootPrompt / bootContent / bootMode from the resolved
	// profile. Until then, callers that want a planted boot body must
	// either supply Inline or register a ContextHook that synthesises
	// the body.

	// 10. ContextHook may override bootPrompt.
	if options.ContextHook != nil {
		ctxPrompt, err := options.ContextHook(ctx, bootDir, compiled)
		if err != nil {
			return nil, fmt.Errorf("agentlaunch/prepare: context hook: %w", err)
		}
		if ctxPrompt != "" {
			bootPrompt = ctxPrompt
		}
	}

	prepared := &agentlaunch.PreparedLaunch{
		Compiled:       compiled,
		PlantedBootDir: bootDir,
		WorkspaceDir:   workspaceDir,
		Workdir:        workdir,
		Env:            env,
		Argv:           argv,
		BootMode:       bootMode,
		BootPrompt:     bootPrompt,
		BootContent:    bootContent,
		PlantContext:   plantCtx,
	}

	if err := prepared.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/prepare: %w", err)
	}

	return prepared, nil
}

// resolveWorkspaceDir returns the absolute workspace directory for the
// plan, allocating a fresh one for temp / fresh modes when the plan
// did not pre-supply one. Shared / persistent modes require an
// explicit WorkspaceDir.
func resolveWorkspaceDir(plan *agentlaunch.LaunchPlan) (string, error) {
	if plan.Workspace.WorkspaceDir != "" {
		abs, err := filepath.Abs(plan.Workspace.WorkspaceDir)
		if err != nil {
			return "", fmt.Errorf("resolve workspace dir %q: %w", plan.Workspace.WorkspaceDir, err)
		}
		// For Fresh / Persistent, create if absent (matches docstring).
		switch plan.Workspace.Mode {
		case agentlaunch.WorkspaceFresh, agentlaunch.WorkspacePersistent:
			if err := os.MkdirAll(abs, 0o755); err != nil {
				return "", fmt.Errorf("create workspace dir %q: %w", abs, err)
			}
		}
		return abs, nil
	}

	switch plan.Workspace.Mode {
	case agentlaunch.WorkspaceTemp, agentlaunch.WorkspaceFresh:
		prefix := plan.Workspace.TempPrefix
		if prefix == "" {
			prefix = os.TempDir()
		}
		// Pattern names the consumer + project for easier post-mortem.
		dir, err := os.MkdirTemp(prefix, "agentlaunch-workspace-*")
		if err != nil {
			return "", fmt.Errorf("allocate workspace tempdir: %w", err)
		}
		return dir, nil
	case agentlaunch.WorkspaceShared, agentlaunch.WorkspacePersistent:
		return "", fmt.Errorf("workspace mode %q requires WorkspaceSpec.WorkspaceDir to be set", plan.Workspace.Mode)
	default:
		// LaunchPlan.Validate guards against unknown modes; defensive
		// branch for completeness.
		return "", fmt.Errorf("unknown workspace mode %q", plan.Workspace.Mode)
	}
}

// allocateBootDir returns an absolute path to a freshly created
// per-launch bootdir. Even WorkspacePersistent gets a per-launch
// bootdir — the bootdir is ephemeral lib-state, distinct from the
// workspace's user-state.
//
// The name pattern includes the first 8 chars of the plan hash so
// operators inspecting /tmp can correlate dirs to launches.
func allocateBootDir(plan *agentlaunch.LaunchPlan, planHash string) (string, error) {
	prefix := plan.Workspace.TempPrefix
	if prefix == "" {
		prefix = os.TempDir()
	}
	short := planHash
	if len(short) > 8 {
		short = short[:8]
	}
	pattern := fmt.Sprintf("agentlaunch-bootdir-%s-*", short)
	dir, err := os.MkdirTemp(prefix, pattern)
	if err != nil {
		return "", fmt.Errorf("allocate bootdir: %w", err)
	}
	return dir, nil
}

// composeEnv merges provider.Env and injection.Env into a single map.
// Injection wins on collision. Empty maps are tolerated; the returned
// map is always non-nil (may be empty) so callers can range over it
// without nil-checks.
func composeEnv(plan *agentlaunch.LaunchPlan) map[string]string {
	env := make(map[string]string, len(plan.Provider.Env)+len(plan.Injection.Env))
	for k, v := range plan.Provider.Env {
		env[k] = v
	}
	for k, v := range plan.Injection.Env {
		env[k] = v
	}
	return env
}

// composeArgv builds the full argv for the spawned process:
// [binary, provider.flags..., injection.args...]. The binary name
// defaults to the matrix descriptor's BinaryName and is overridden by
// plan.Provider.Binary when non-empty.
func composeArgv(plan *agentlaunch.LaunchPlan, desc matrix.Descriptor) []string {
	binary := desc.BinaryName
	if plan.Provider.Binary != "" {
		binary = plan.Provider.Binary
	}
	argv := make([]string, 0, 1+len(plan.Provider.Flags)+len(plan.Injection.Args))
	argv = append(argv, binary)
	argv = append(argv, plan.Provider.Flags...)
	argv = append(argv, plan.Injection.Args...)
	return argv
}
