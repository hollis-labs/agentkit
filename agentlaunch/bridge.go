package agentlaunch

import "fmt"

// bridge.go — the LaunchSpec → LaunchPlan bridge (S5 prerequisite).
//
// The S4 parameterized-launch model (LaunchSpec + LaunchBag + var resolution
// + Render) produces a RenderResult — a rendered boot body + resolved inputs.
// It stops there. The execution pipeline (launcher.Compile → Prepare → Plant)
// consumes a LaunchPlan, and before this the ONLY thing that produced a
// LaunchPlan was the legacy catalog path (catalog.GlobalCatalog.Resolve).
//
// PlanFromLaunch is the missing seam: it assembles a runnable LaunchPlan from
// the S4 model so both front-ends (interactive, autonomous) drive the same
// shipped Compile → Prepare → Plant pipeline. See the S5 design note
// "LaunchSpec → LaunchPlan bridge" for the locked decisions this implements.

// PlanFromLaunchInput is the fully-resolved input to PlanFromLaunch.
//
// The caller has already done all resolution and I/O: loaded + validated the
// LaunchSpec and LaunchBag, run S4.2 var resolution, called LaunchSpec.Render,
// resolved the bag's `runner` input to a RuntimeBinding (registry /
// file-backed / literal — see ResolveRuntimeBinding), and resolved the agent
// identity. PlanFromLaunch itself is a pure, deterministic, I/O-free transform.
type PlanFromLaunchInput struct {
	// Spec is the resolved LaunchSpec. Used for provenance only — the
	// bridge never reads Spec's embedded BootSpec.Runtime (see Runtime).
	Spec LaunchSpec

	// Bag is the concrete launch invocation. Used for provenance only.
	Bag LaunchBag

	// Render is the output of Spec.Render(Bag.RenderRequest(frontEnd)) —
	// the rendered boot body plus the resolved input map.
	Render RenderResult

	// Runtime is the resolved runtime binding for the bag's `runner`
	// input. It is the SOLE runtime source: PlanFromLaunch never reads
	// Spec.BootSpec.Runtime (locked decision §4.1 — `runner` is
	// authoritative; BootSpec.Runtime is authoring metadata only). An
	// empty/invalid Runtime is a hard error — there is no spec-baked
	// fallback; any fallback is caller-side policy.
	Runtime RuntimeBinding

	// Agent is the caller-resolved agent identity (locked decision §4.2 —
	// the agent is a consumer-composed handle artifact, never a LaunchSpec
	// or directory-owned entity).
	Agent AgentSpec

	// Mode is the lifecycle stance the front-end supplies (interactive /
	// background / ephemeral).
	Mode LaunchMode
}

// PlanFromLaunch assembles a runnable LaunchPlan from the S4 launch model.
//
// It is pure and deterministic: the caller owns resolution (vars,
// runner→RuntimeBinding, agent), the bridge owns assembly. The returned plan
// is Validate()-clean and ready for launcher.Compile — the entire shipped
// Compile → Prepare → Plant pipeline is reused unchanged.
//
// The runtime always comes from in.Runtime; the bridge never reads Spec's
// embedded BootSpec.Runtime. The MCP / Injection / Metadata on the returned
// plan are a BASE the consumer overlays (e.g. a per-task MCP loopback injected
// post-Prepare). The bag's raw `isolation` token and the RuntimeBinding's
// Timeout are surfaced on Metadata.Annotations for the consumer's preparer /
// scheduler to act on.
func PlanFromLaunch(in PlanFromLaunchInput) (LaunchPlan, error) {
	if err := in.Runtime.Validate(); err != nil {
		return LaunchPlan{}, fmt.Errorf("agentlaunch: PlanFromLaunch: runtime binding: %w", err)
	}
	if in.Agent.ID == "" {
		return LaunchPlan{}, fmt.Errorf("agentlaunch: PlanFromLaunch: %w", ErrMissingAgentID)
	}

	inputs := in.Render.ResolvedInputs
	workDir := resolvedString(inputs, LaunchInputWorkDir)
	if workDir == "" {
		return LaunchPlan{}, fmt.Errorf("agentlaunch: PlanFromLaunch: required input %q is empty", LaunchInputWorkDir)
	}

	wsMode, err := isolationWorkspaceMode(resolvedString(inputs, LaunchInputIsolation))
	if err != nil {
		return LaunchPlan{}, fmt.Errorf("agentlaunch: PlanFromLaunch: %w", err)
	}

	annotations := map[string]string{}
	if iso := resolvedString(inputs, LaunchInputIsolation); iso != "" {
		annotations["agentlaunch.isolation"] = iso
	}
	if in.Runtime.Timeout != "" {
		annotations["agentlaunch.runtime.timeout"] = in.Runtime.Timeout
	}
	if in.Spec.ID != "" {
		annotations["agentlaunch.launch_spec"] = in.Spec.ID
	}
	if in.Bag.Name != "" {
		annotations["agentlaunch.launch_bag"] = in.Bag.Name
	}

	plan := LaunchPlan{
		Project: ProjectSpec{ID: resolvedString(inputs, "project")},
		Agent:   in.Agent,
		Provider: ProviderSpec{
			ID:            in.Runtime.Provider,
			ModelOverride: in.Runtime.Model,
			Flags:         in.Runtime.Args,
			Permission:    in.Runtime.Permission,
		},
		Runtime: in.Runtime.RuntimeKind,
		Workspace: WorkspaceSpec{
			Mode:    wsMode,
			Workdir: workDir,
		},
		BootProfile: BootProfileRef{
			Inline: &BootProfileInline{
				BootContent: in.Render.Body,
				BootMode:    BootModePlanted,
			},
		},
		Mode:     in.Mode,
		Metadata: Metadata{Annotations: annotations},
	}

	if err := plan.Validate(); err != nil {
		return LaunchPlan{}, fmt.Errorf("agentlaunch: PlanFromLaunch: assembled plan invalid: %w", err)
	}
	return plan, nil
}

// isolationWorkspaceMode maps the S4.4 `isolation` input onto an
// agentlaunch.WorkspaceMode (LaunchPlan.Workspace.Mode, which Validate
// requires).
//
//   - ""                     → WorkspaceShared (isolation is optional;
//     shared is the documented default, matching the legacy catalog).
//   - "hybrid"                → WorkspacePersistent (Tether's "hybrid" =
//     preserved session root; same mapping the legacy catalog used).
//   - "worktree"              → WorkspacePersistent. agentlaunch has no
//     "worktree" WorkspaceMode — the git worktree itself is created by the
//     consumer's own machinery (e.g. Torque's per-run worktree subsystem),
//     not by WorkspaceSpec. The raw "worktree" token is preserved on
//     Metadata.Annotations["agentlaunch.isolation"] so the consumer's
//     preparer/scheduler drives the worktree; the WorkspaceMode just needs
//     to be a valid, stable value.
//   - a literal WorkspaceMode → passed through (shared/temp/fresh/persistent).
//   - anything else           → error (a typo'd token fails loudly rather
//     than silently degrading).
//
// NOTE (S5 review follow-up): the "worktree" → WorkspacePersistent mapping is
// a bridge-author call, not a locked §4 decision — the locked design said
// "reuse the catalog mapping", but the legacy catalog never had a "worktree"
// workspace-mode token (worktree was a separate file-twin axis). If the team
// wants different isolation→Mode semantics it is a one-function change here;
// the raw token is preserved on annotations so no information is lost.
func isolationWorkspaceMode(isolation string) (WorkspaceMode, error) {
	switch isolation {
	case "":
		return WorkspaceShared, nil
	case "hybrid", "worktree":
		return WorkspacePersistent, nil
	case string(WorkspaceShared), string(WorkspaceTemp),
		string(WorkspaceFresh), string(WorkspacePersistent):
		return WorkspaceMode(isolation), nil
	default:
		return "", fmt.Errorf("unknown isolation token %q (want hybrid|worktree|shared|temp|fresh|persistent)", isolation)
	}
}

// resolvedString reads key from a resolved-input map and coerces it to a
// string. A missing key or a non-string value yields "".
func resolvedString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
