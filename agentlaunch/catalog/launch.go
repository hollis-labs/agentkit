package catalog

// LaunchProfile is the on-disk shape of one launches/<id>.yaml. Fields
// mirror Tether's launch entry verbatim — ID + Project + Agent +
// Provider IDs identifying the catalog entries to glue together, plus
// the per-launch workspace / prompt / overrides / mcp overrides.
//
// LaunchProfile alone is not enough to build a LaunchPlan; the
// translator (ToLaunchPlan) needs a GlobalCatalog so it can resolve
// the Project / Agent / Provider IDs into the concrete entry data.
type LaunchProfile struct {
	// ID is the launch profile identifier (e.g. "codex-launch"). Required.
	ID string `yaml:"id" json:"id"`

	// Project is the ID of the ProjectEntry this launch is scoped to.
	Project string `yaml:"project,omitempty" json:"project,omitempty"`

	// Agent is the ID of the AgentEntry this launch boots.
	Agent string `yaml:"agent,omitempty" json:"agent,omitempty"`

	// Provider is the ID of the ProviderEntry this launch spawns.
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`

	// Workspace is the per-launch workspace block.
	Workspace LaunchWorkspace `yaml:"workspace,omitempty" json:"workspace,omitempty"`

	// Prompt is Tether's per-launch prompt-composition block. The
	// translator folds the booleans into LaunchPlan.Metadata.Annotations
	// because agentlaunch doesn't model boot-fragment composition (the
	// concrete planter that consumes a CompiledLaunch interprets these).
	Prompt PromptSpec `yaml:"prompt,omitempty" json:"prompt,omitempty"`

	// Overrides carries the per-launch env overlay applied to the
	// resolved ProviderSpec.Env before the LaunchPlan is built.
	Overrides LaunchOverrides `yaml:"overrides,omitempty" json:"overrides,omitempty"`

	// MCP is the per-launch MCP allowlist (set of upstream MCP server
	// IDs). Translated into agentlaunch.MCPSpec.Allowlist.
	MCP CatalogMCPConfig `yaml:"mcp,omitempty" json:"mcp,omitempty"`

	// BootProfile optionally names the boot-profile catalog entry the
	// launch should boot with. Either a relative path under the catalog
	// boot directory or just the boot-profile ID; the translator emits
	// it as BootProfileRef.Name with CatalogPath populated when the
	// loader knows the absolute path.
	BootProfile string `yaml:"boot_profile,omitempty" json:"boot_profile,omitempty"`

	// Mode is the lifecycle stance: "interactive" (default), "background",
	// or "ephemeral". Empty maps to "interactive" — matches Tether's
	// implicit default.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`

	// Runtime, when set, overrides the runtime_kind declared on the
	// referenced ProviderEntry. Catalog authors rarely set this — most
	// launches inherit the provider's declared runtime.
	Runtime string `yaml:"runtime,omitempty" json:"runtime,omitempty"`

	// Labels and Annotations populate agentlaunch.LaunchPlan.Metadata.
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// LaunchWorkspace mirrors Tether's per-launch workspace block.
// Mode is the workspace-mode token (Tether uses "hybrid"; agentlaunch
// uses shared/temp/fresh/persistent — the translator maps known values
// and surfaces the original in annotations under "tether.workspace_mode_raw").
type LaunchWorkspace struct {
	Mode         string `yaml:"mode,omitempty" json:"mode,omitempty"`
	WorktreeName string `yaml:"worktree_name,omitempty" json:"worktree_name,omitempty"`
	WriteHome    string `yaml:"write_home,omitempty" json:"write_home,omitempty"`
}

// PromptSpec mirrors Tether's per-launch prompt composition block.
// The agentlaunch LaunchPlan doesn't have a direct field for these
// booleans — the planter that consumes the CompiledLaunch (CW-0005 and
// beyond) interprets them. The translator preserves them in
// annotations as "tether.prompt.include_project_boot" etc. so the
// downstream planter can find them.
type PromptSpec struct {
	IncludeProjectBoot   bool `yaml:"include_project_boot,omitempty" json:"include_project_boot,omitempty"`
	IncludeAgentBoot     bool `yaml:"include_agent_boot,omitempty" json:"include_agent_boot,omitempty"`
	IncludeKnowledgeBase bool `yaml:"include_knowledge_base,omitempty" json:"include_knowledge_base,omitempty"`
}

// LaunchOverrides mirrors Tether's per-launch overrides block. The Env
// map is merged into the resolved ProviderSpec.Env with launch-side
// values winning on conflict.
type LaunchOverrides struct {
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}
