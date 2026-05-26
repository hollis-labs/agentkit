package catalog

import (
	"fmt"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// Resolve looks up a launch profile by ID inside g, resolves the
// referenced project / agent / provider entries, and produces a
// fully-populated agentlaunch.LaunchPlan. The result has already passed
// agentlaunch.LaunchPlan.Validate; callers receive an error wrapped
// with "agentlaunch/catalog: ..." if either resolution or validation
// fails.
func (g *GlobalCatalog) Resolve(launchID string) (*agentlaunch.LaunchPlan, error) {
	if g == nil {
		return nil, fmt.Errorf("agentlaunch/catalog: nil GlobalCatalog")
	}
	for i := range g.Launches {
		if g.Launches[i].ID == launchID {
			return g.Launches[i].ToLaunchPlan(g)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrLaunchNotFound, launchID)
}

// ToLaunchPlan translates one LaunchProfile into a populated
// agentlaunch.LaunchPlan, using g for ID resolution. The resulting
// LaunchPlan is run through agentlaunch.LaunchPlan.Validate before
// return; a validation failure is wrapped with "agentlaunch/catalog: ...".
func (lp *LaunchProfile) ToLaunchPlan(g *GlobalCatalog) (*agentlaunch.LaunchPlan, error) {
	if lp == nil {
		return nil, fmt.Errorf("agentlaunch/catalog: nil LaunchProfile")
	}
	if g == nil {
		return nil, fmt.Errorf("agentlaunch/catalog: nil GlobalCatalog")
	}

	annotations := map[string]string{}

	// Resolve project, agent, provider.
	project, err := findProject(g, lp.Project)
	if err != nil {
		return nil, err
	}
	agent, err := findAgent(g, lp.Agent)
	if err != nil {
		return nil, err
	}
	provider, err := findProvider(g, lp.Provider)
	if err != nil {
		return nil, err
	}

	// Workspace: launch.workspace.mode wins, else defaults block, else
	// project.workspace.default_mode, else WorkspaceShared.
	rawWorkspaceMode := firstNonEmpty(lp.Workspace.Mode, g.Defaults.WorkspaceMode, project.Workspace.DefaultMode)
	if rawWorkspaceMode != "" {
		annotations["tether.workspace_mode_raw"] = rawWorkspaceMode
	}
	wsMode := mapWorkspaceMode(rawWorkspaceMode)

	// Runtime: launch.runtime override wins, else provider.runtime_kind.
	rawRuntime := firstNonEmpty(lp.Runtime, provider.RuntimeKind)
	if rawRuntime != "" {
		annotations["tether.runtime_kind_raw"] = rawRuntime
	}
	runtime, runtimeOK := mapRuntimeKind(rawRuntime)
	if !runtimeOK {
		return nil, fmt.Errorf("%w: %q (launch %s)", ErrUnsupportedRuntime, rawRuntime, lp.ID)
	}

	// Mode.
	rawMode := lp.Mode
	if rawMode != "" {
		annotations["tether.launch_mode_raw"] = rawMode
	}
	mode := mapLaunchMode(rawMode)

	// Project spec.
	projectSpec := agentlaunch.ProjectSpec{
		ID:   project.ID,
		Name: project.Name,
		Root: project.RepoRoot,
	}

	// Agent spec. role_file is best-effort: pick the first roles[]
	// entry if the agent has no explicit role_file. Tether agents do
	// not carry a role_file today, but downstream consumers consult
	// roles[0] for the same purpose — preserved via annotations as
	// well so nothing is lost.
	agentSpec := agentlaunch.AgentSpec{
		ID:     agent.ID,
		Name:   agent.Name,
		Labels: copyStringMap(agent.Labels),
	}
	if len(agent.Roles) > 0 {
		annotations["tether.agent.roles"] = joinSlice(agent.Roles)
	}
	if len(agent.Skills) > 0 {
		annotations["tether.agent.skills"] = joinSlice(agent.Skills)
	}
	if len(agent.BootFragments) > 0 {
		annotations["tether.agent.boot_fragments"] = joinSlice(agent.BootFragments)
	}
	if len(agent.ContextFiles) > 0 {
		annotations["tether.agent.context_files"] = joinSlice(agent.ContextFiles)
	}
	if agent.SystemPrompt != "" {
		annotations["tether.agent.system_prompt"] = agent.SystemPrompt
	}
	if agent.AgentPrompt != "" {
		annotations["tether.agent.agent_prompt"] = agent.AgentPrompt
	}
	if agent.Permissions.DefaultSandbox != "" {
		annotations["tether.agent.permissions.default_sandbox"] = agent.Permissions.DefaultSandbox
	}
	if agent.Permissions.Network {
		annotations["tether.agent.permissions.network"] = "true"
	}

	// Provider spec — fold in any per-agent override + per-launch env
	// override.
	providerSpec := agentlaunch.ProviderSpec{
		ID:     provider.ID,
		Binary: provider.Command,
	}
	providerSpec.Flags = append(providerSpec.Flags, provider.Args...)
	if override, ok := agent.ProviderOverrides[provider.ID]; ok {
		providerSpec.Flags = append(providerSpec.Flags, override.ExtraArgs...)
		if len(override.Env) > 0 {
			if providerSpec.Env == nil {
				providerSpec.Env = map[string]string{}
			}
			for k, v := range override.Env {
				providerSpec.Env[k] = v
			}
		}
	}
	if len(lp.Overrides.Env) > 0 {
		if providerSpec.Env == nil {
			providerSpec.Env = map[string]string{}
		}
		for k, v := range lp.Overrides.Env {
			providerSpec.Env[k] = v
		}
	}
	if provider.Type != "" {
		annotations["tether.provider.type"] = provider.Type
	}
	if provider.Provider != "" {
		annotations["tether.provider.brand"] = provider.Provider
	}
	if provider.Adapter != "" {
		annotations["tether.provider.adapter"] = provider.Adapter
	}
	if provider.Bootstrap.Mode != "" {
		annotations["tether.provider.bootstrap.mode"] = provider.Bootstrap.Mode
	}
	if provider.Bootstrap.PromptPrefix != "" {
		annotations["tether.provider.bootstrap.prompt_prefix"] = provider.Bootstrap.PromptPrefix
	}
	if provider.Env.Mode != "" {
		annotations["tether.provider.env.mode"] = provider.Env.Mode
	}
	if len(provider.Env.Passthrough) > 0 {
		annotations["tether.provider.env.passthrough"] = joinSlice(provider.Env.Passthrough)
	}
	if len(provider.Env.Redact) > 0 {
		annotations["tether.provider.env.redact"] = joinSlice(provider.Env.Redact)
	}

	// Workspace spec — pull session_root / worktree_base from the
	// project's workspace block as best-effort defaults.
	workspaceSpec := agentlaunch.WorkspaceSpec{
		Mode:         wsMode,
		Workdir:      project.RepoRoot,
		WorkspaceDir: project.Workspace.SessionRoot,
	}
	if project.Workspace.WorktreeBase != "" {
		annotations["tether.project.workspace.worktree_base"] = project.Workspace.WorktreeBase
	}
	if lp.Workspace.WorktreeName != "" {
		annotations["tether.launch.workspace.worktree_name"] = lp.Workspace.WorktreeName
	}
	if lp.Workspace.WriteHome != "" {
		annotations["tether.launch.workspace.write_home"] = lp.Workspace.WriteHome
	}
	if project.TrackingRoot != "" {
		annotations["tether.project.tracking_root"] = project.TrackingRoot
	}
	if len(project.BootFragments) > 0 {
		annotations["tether.project.boot_fragments"] = joinSlice(project.BootFragments)
	}
	if len(project.KnowledgeBase) > 0 {
		annotations["tether.project.knowledge_base"] = joinSlice(project.KnowledgeBase)
	}

	// Boot profile reference. lp.BootProfile is either a path or just
	// an ID; we cannot resolve to a concrete absolute file path without
	// catalog-root context, so we emit Name verbatim and leave
	// CatalogPath empty for the caller (or the directory-walking
	// loader) to fill in. When BootProfile is unset, fall back to the
	// agent ID as the boot profile name (Tether's implicit convention)
	// — better than silently dropping the field.
	bootRef := agentlaunch.BootProfileRef{
		Name: lp.BootProfile,
	}
	if bootRef.Name == "" {
		// agentlaunch.LaunchPlan.Validate requires CatalogPath OR Inline.
		// Synthesize a minimal inline boot profile so the plan validates.
		// Downstream callers can replace it.
		bootRef.Inline = &agentlaunch.BootProfileInline{
			BootMode: agentlaunch.BootModePlanted,
		}
		annotations["tether.boot_profile.synthesized"] = "true"
	} else {
		// We still need CatalogPath OR Inline for Validate to pass.
		// Emit an inline marker carrying just BootMode so validation
		// passes; the planter will replace the inline form with the
		// catalog-resolved body once boot-profile resolution lands in
		// CW-0005.
		bootRef.Inline = &agentlaunch.BootProfileInline{
			BootMode: agentlaunch.BootModePlanted,
		}
	}

	// Prompt-composition booleans — propagate as annotations so the
	// downstream planter can find them.
	if lp.Prompt.IncludeProjectBoot {
		annotations["tether.prompt.include_project_boot"] = "true"
	}
	if lp.Prompt.IncludeAgentBoot {
		annotations["tether.prompt.include_agent_boot"] = "true"
	}
	if lp.Prompt.IncludeKnowledgeBase {
		annotations["tether.prompt.include_knowledge_base"] = "true"
	}

	// MCP spec — combine launch.mcp.servers, project.mcp.servers, and
	// the defaults block. Per-launch entries win over project entries;
	// defaults fill in only when both are empty.
	allowlist := mergeMCPAllowlist(lp.MCP.Servers, project.MCP.Servers, g.Defaults.MCPAllowlist)
	mcpSpec := agentlaunch.MCPSpec{
		Allowlist: allowlist,
	}

	// Metadata: launch labels win over defaults, defaults win over
	// nothing. Annotations: launch annotations win over defaults, then
	// the per-translation derived annotations layered on top.
	labels := mergeStringMaps(g.Defaults.Labels, lp.Labels)
	annotationsOut := mergeStringMaps(g.Defaults.Annotations, lp.Annotations)
	annotationsOut = mergeStringMaps(annotationsOut, annotations)

	plan := &agentlaunch.LaunchPlan{
		Project:     projectSpec,
		Agent:       agentSpec,
		Provider:    providerSpec,
		Runtime:     runtime,
		Workspace:   workspaceSpec,
		BootProfile: bootRef,
		MCP:         mcpSpec,
		Mode:        mode,
		Metadata: agentlaunch.Metadata{
			Labels:      labels,
			Annotations: annotationsOut,
		},
	}

	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: %w", err)
	}
	return plan, nil
}

// findProject looks up p by ID in g.Projects. Empty ID is an error.
func findProject(g *GlobalCatalog, id string) (*ProjectEntry, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: (empty)", ErrProjectNotFound)
	}
	for i := range g.Projects {
		if g.Projects[i].ID == id {
			return &g.Projects[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrProjectNotFound, id)
}

// findAgent looks up a by ID in g.Agents.
func findAgent(g *GlobalCatalog, id string) (*AgentEntry, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: (empty)", ErrAgentNotFound)
	}
	for i := range g.Agents {
		if g.Agents[i].ID == id {
			return &g.Agents[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, id)
}

// findProvider looks up p by ID in g.Providers.
func findProvider(g *GlobalCatalog, id string) (*ProviderEntry, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: (empty)", ErrProviderNotFound)
	}
	for i := range g.Providers {
		if g.Providers[i].ID == id {
			return &g.Providers[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
}

// mapWorkspaceMode converts Tether's workspace-mode tokens to
// agentlaunch.WorkspaceMode. Known mappings:
//
//   - "shared"     → WorkspaceShared
//   - "temp"       → WorkspaceTemp
//   - "fresh"      → WorkspaceFresh
//   - "persistent" → WorkspacePersistent
//   - "hybrid"     → WorkspacePersistent  (Tether's "hybrid" = preserved
//     session_root with fresh worktrees; the durable side maps cleanly
//     onto WorkspacePersistent and the worktree-name flows through to
//     annotations.)
//
// Unknown / empty values fall through to WorkspaceShared, which is the
// safest default — the preparer simply asserts the directory exists.
// The original token is preserved on the plan via
// Metadata.Annotations["tether.workspace_mode_raw"].
func mapWorkspaceMode(s string) agentlaunch.WorkspaceMode {
	switch s {
	case "shared":
		return agentlaunch.WorkspaceShared
	case "temp":
		return agentlaunch.WorkspaceTemp
	case "fresh":
		return agentlaunch.WorkspaceFresh
	case "persistent":
		return agentlaunch.WorkspacePersistent
	case "hybrid":
		return agentlaunch.WorkspacePersistent
	default:
		return agentlaunch.WorkspaceShared
	}
}

// mapRuntimeKind converts Tether's runtime_kind tokens to
// agentlaunch.RuntimeKind. Returns (token, true) on a clean mapping,
// (zero, false) when the token is unrecognised.
//
// Tether's "api" runtime is intentionally NOT mapped: agentlaunch does
// not model in-process API runtimes today, and the matrix sibling
// (CW-0004) would reject the (provider, api) pair. Callers that load
// a catalog containing api-typed providers can use those entries for
// metadata but cannot Resolve a launch that depends on them.
func mapRuntimeKind(s string) (agentlaunch.RuntimeKind, bool) {
	switch s {
	case "subprocess":
		return agentlaunch.RuntimeSubprocess, true
	case "pty":
		return agentlaunch.RuntimePTY, true
	case "streaming-stdio":
		return agentlaunch.RuntimeStreamingStdio, true
	case "jsonrpc-stdio":
		return agentlaunch.RuntimeJsonRpcStdio, true
	default:
		return "", false
	}
}

// mapLaunchMode converts the LaunchProfile.Mode string into an
// agentlaunch.LaunchMode. Empty defaults to LaunchInteractive (matches
// Tether's implicit default — launches are interactive unless flagged
// otherwise).
func mapLaunchMode(s string) agentlaunch.LaunchMode {
	switch s {
	case "background":
		return agentlaunch.LaunchBackground
	case "ephemeral":
		return agentlaunch.LaunchEphemeral
	case "interactive", "":
		return agentlaunch.LaunchInteractive
	default:
		return agentlaunch.LaunchInteractive
	}
}

// firstNonEmpty returns the first non-empty string from ss, or "" when
// all entries are empty.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// copyStringMap returns a shallow copy of m, or nil when m is empty.
func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// mergeStringMaps overlays b on top of a. Returns nil only when both
// inputs are empty.
func mergeStringMaps(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// mergeMCPAllowlist returns the first non-empty allowlist in priority
// order. We intentionally do NOT union the lists — Tether's
// allowlist semantics are "this is the complete set" at every layer,
// so the more-specific layer fully replaces the less-specific one.
func mergeMCPAllowlist(layers ...[]string) []string {
	for _, layer := range layers {
		if len(layer) > 0 {
			out := make([]string, len(layer))
			copy(out, layer)
			return out
		}
	}
	return nil
}

// joinSlice renders a string slice as a comma+space-delimited string
// for annotation values. Empty slice returns "".
func joinSlice(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
