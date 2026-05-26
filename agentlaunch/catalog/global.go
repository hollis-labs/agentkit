package catalog

// GlobalCatalog is the on-disk shape of Tether's `global.yaml` plus the
// aggregated set of project / agent / provider / launch entries the
// loader resolves from sibling YAML files (or that callers supplied
// inline). The loader fills the inline slices regardless of which
// physical layout (single file vs directory tree) the catalog uses, so
// downstream code only ever sees the populated slice form.
//
// The Version, Catalog (roots), Daemon, and Defaults fields are passed
// through from Tether's global.yaml verbatim. The translator does not
// consult Daemon / Catalog.Roots — those describe how to LOCATE catalog
// content, not how to translate it.
type GlobalCatalog struct {
	// Version is the catalog schema version recorded at the top of
	// global.yaml (e.g. "0.1.0"). The translator does not branch on it
	// today; it is preserved so consumers that care about schema drift
	// can read it.
	Version string `yaml:"version,omitempty" json:"version,omitempty"`

	// Catalog carries the directory-pointer shape Tether uses
	// (catalog.roots.{projects,agents,providers,launches,boot}) plus the
	// catalog-level Defaults block.
	Catalog CatalogRoots `yaml:"catalog,omitempty" json:"catalog,omitempty"`

	// Daemon is the daemon-process config block from Tether's global.yaml.
	// Pure passthrough — the translator ignores it. Preserved so callers
	// can read it after a LoadGlobal call without re-parsing.
	Daemon DaemonConfig `yaml:"daemon,omitempty" json:"daemon,omitempty"`

	// Projects holds the list of project entries discovered by the
	// loader (one per file under catalog.roots.projects) or supplied
	// inline by the caller.
	Projects []ProjectEntry `yaml:"projects,omitempty" json:"projects,omitempty"`

	// Agents holds the list of agent entries.
	Agents []AgentEntry `yaml:"agents,omitempty" json:"agents,omitempty"`

	// Providers holds the list of provider entries.
	Providers []ProviderEntry `yaml:"providers,omitempty" json:"providers,omitempty"`

	// Launches holds the list of launch profile entries.
	Launches []LaunchEntry `yaml:"launches,omitempty" json:"launches,omitempty"`

	// Defaults is the catalog-wide defaults block applied to launches that
	// omit a field. Distinct from Catalog.Defaults (which describes
	// workspace roots / state-db locations Tether's daemon cares about)
	// — DefaultsBlock here is for fields the translator merges into
	// translated LaunchPlans.
	Defaults DefaultsBlock `yaml:"defaults_block,omitempty" json:"defaults_block,omitempty"`
}

// CatalogRoots mirrors Tether's `catalog:` block — paths to the
// sibling directories the loader walks to discover per-entry files.
// All paths are interpreted relative to the directory containing
// global.yaml; absolute paths or paths starting with "~" are honoured
// verbatim (the loader does not expand "~", that is the caller's
// concern).
type CatalogRoots struct {
	// Roots names the per-entry-type subdirectories.
	Roots CatalogPaths `yaml:"roots,omitempty" json:"roots,omitempty"`

	// Defaults is Tether's catalog-level defaults (workspace_root,
	// state_db, temp_root). Preserved as-is; the translator does not
	// consult it.
	Defaults CatalogDefaults `yaml:"defaults,omitempty" json:"defaults,omitempty"`
}

// CatalogPaths names the per-entry-type subdirectories the loader walks.
// Empty fields fall back to the well-known defaults
// ("projects", "agents", "providers", "launches", "boot-profiles").
type CatalogPaths struct {
	Projects     string `yaml:"projects,omitempty" json:"projects,omitempty"`
	Agents       string `yaml:"agents,omitempty" json:"agents,omitempty"`
	Providers    string `yaml:"providers,omitempty" json:"providers,omitempty"`
	Launches     string `yaml:"launches,omitempty" json:"launches,omitempty"`
	Boot         string `yaml:"boot,omitempty" json:"boot,omitempty"`
	BootProfiles string `yaml:"boot_profiles,omitempty" json:"boot_profiles,omitempty"`
}

// CatalogDefaults mirrors Tether's `catalog.defaults` block. Preserved
// verbatim; the translator does not interpret these fields.
type CatalogDefaults struct {
	WorkspaceRoot string `yaml:"workspace_root,omitempty" json:"workspace_root,omitempty"`
	StateDB       string `yaml:"state_db,omitempty" json:"state_db,omitempty"`
	TempRoot      string `yaml:"temp_root,omitempty" json:"temp_root,omitempty"`
}

// DaemonConfig mirrors Tether's `daemon:` block. Pure passthrough — the
// translator does not consult this struct. Preserved so consumers can
// re-emit the catalog without losing daemon settings.
type DaemonConfig struct {
	ListenAddr      string `yaml:"listen_addr,omitempty" json:"listen_addr,omitempty"`
	PIDFile         string `yaml:"pid_file,omitempty" json:"pid_file,omitempty"`
	ShutdownTimeout string `yaml:"shutdown_timeout,omitempty" json:"shutdown_timeout,omitempty"`
}

// DefaultsBlock carries catalog-wide defaults the translator merges
// into LaunchPlans when a launch profile omits a field. None of these
// fields are required — an empty DefaultsBlock is legal.
type DefaultsBlock struct {
	// WorkspaceMode is the default workspace mode applied when a
	// LaunchProfile.Workspace.Mode is empty.
	WorkspaceMode string `yaml:"workspace_mode,omitempty" json:"workspace_mode,omitempty"`

	// MCPAllowlist is the catalog-wide MCP allowlist applied when a
	// LaunchProfile.MCP.Allowlist is empty.
	MCPAllowlist []string `yaml:"mcp_allowlist,omitempty" json:"mcp_allowlist,omitempty"`

	// Labels is the catalog-wide label set merged into the translated
	// LaunchPlan's Metadata.Labels (LaunchProfile labels win on conflict).
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`

	// Annotations is the catalog-wide annotation set merged into the
	// translated LaunchPlan's Metadata.Annotations.
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// ProjectEntry mirrors Tether's per-project YAML. Fields that map
// cleanly onto agentlaunch.ProjectSpec are translated directly; the
// remainder (tracking_root, knowledge_base, boot_fragments, mcp) flow
// through to LaunchPlan.Metadata.Annotations under documented keys so
// data is never lost.
type ProjectEntry struct {
	ID            string           `yaml:"id" json:"id"`
	Name          string           `yaml:"name,omitempty" json:"name,omitempty"`
	RepoRoot      string           `yaml:"repo_root,omitempty" json:"repo_root,omitempty"`
	TrackingRoot  string           `yaml:"tracking_root,omitempty" json:"tracking_root,omitempty"`
	KnowledgeBase []string         `yaml:"knowledge_base,omitempty" json:"knowledge_base,omitempty"`
	BootFragments []string         `yaml:"boot_fragments,omitempty" json:"boot_fragments,omitempty"`
	Workspace     ProjectWorkspace `yaml:"workspace,omitempty" json:"workspace,omitempty"`
	MCP           CatalogMCPConfig `yaml:"mcp,omitempty" json:"mcp,omitempty"`
}

// ProjectWorkspace mirrors Tether's per-project workspace block.
// Preserved verbatim so consumers can introspect Tether-specific fields
// like worktree_base and session_root.
type ProjectWorkspace struct {
	DefaultMode  string `yaml:"default_mode,omitempty" json:"default_mode,omitempty"`
	WorktreeBase string `yaml:"worktree_base,omitempty" json:"worktree_base,omitempty"`
	SessionRoot  string `yaml:"session_root,omitempty" json:"session_root,omitempty"`
}

// CatalogMCPConfig mirrors Tether's `mcp:` block on Projects and
// Launches — a list of upstream MCP server IDs the proxy should
// expose. Translated into agentlaunch.MCPSpec.Allowlist.
type CatalogMCPConfig struct {
	Servers []string `yaml:"servers,omitempty" json:"servers,omitempty"`
}

// AgentEntry mirrors Tether's per-agent YAML.
type AgentEntry struct {
	ID                string                      `yaml:"id" json:"id"`
	Name              string                      `yaml:"name,omitempty" json:"name,omitempty"`
	Roles             []string                    `yaml:"roles,omitempty" json:"roles,omitempty"`
	Skills            []string                    `yaml:"skills,omitempty" json:"skills,omitempty"`
	ContextFiles      []string                    `yaml:"context_files,omitempty" json:"context_files,omitempty"`
	BootFragments     []string                    `yaml:"boot_fragments,omitempty" json:"boot_fragments,omitempty"`
	Permissions       AgentPermissions            `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	SystemPrompt      string                      `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	AgentPrompt       string                      `yaml:"agent_prompt,omitempty" json:"agent_prompt,omitempty"`
	ProviderOverrides map[string]ProviderOverride `yaml:"provider_overrides,omitempty" json:"provider_overrides,omitempty"`
	// Labels is the per-agent label map. Forwarded to agentlaunch.AgentSpec.Labels.
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// AgentPermissions mirrors Tether's per-agent permissions block.
type AgentPermissions struct {
	Network        bool   `yaml:"network,omitempty" json:"network,omitempty"`
	DefaultSandbox string `yaml:"default_sandbox,omitempty" json:"default_sandbox,omitempty"`
}

// ProviderOverride is Tether's per-agent per-provider override block.
// When the launch's provider has a matching entry the translator
// folds ExtraArgs into ProviderSpec.Flags and Env into ProviderSpec.Env.
type ProviderOverride struct {
	ExtraArgs []string          `yaml:"extra_args,omitempty" json:"extra_args,omitempty"`
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// ProviderEntry mirrors Tether's per-provider YAML.
type ProviderEntry struct {
	ID          string        `yaml:"id" json:"id"`
	Type        string        `yaml:"type,omitempty" json:"type,omitempty"`
	Provider    string        `yaml:"provider,omitempty" json:"provider,omitempty"`
	RuntimeKind string        `yaml:"runtime_kind,omitempty" json:"runtime_kind,omitempty"`
	Command     string        `yaml:"command,omitempty" json:"command,omitempty"`
	Args        []string      `yaml:"args,omitempty" json:"args,omitempty"`
	Adapter     string        `yaml:"adapter,omitempty" json:"adapter,omitempty"`
	Bootstrap   BootstrapSpec `yaml:"bootstrap,omitempty" json:"bootstrap,omitempty"`
	Env         ProviderEnv   `yaml:"env,omitempty" json:"env,omitempty"`
}

// BootstrapSpec mirrors Tether's per-provider bootstrap block. Preserved
// verbatim — the translator uses Mode as a hint for BootProfileInline.BootMode
// when no boot profile reference is supplied, otherwise it flows through
// to LaunchPlan.Metadata.Annotations.
type BootstrapSpec struct {
	Mode         string `yaml:"mode,omitempty" json:"mode,omitempty"`
	PromptPrefix string `yaml:"prompt_prefix,omitempty" json:"prompt_prefix,omitempty"`
}

// ProviderEnv mirrors Tether's per-provider env block. Mode is "merge"
// or "whitelist"; Passthrough is consulted in whitelist mode; Redact
// applies in merge mode. The translator preserves the full struct via
// annotations because agentlaunch.ProviderSpec.Env is a flat string map.
type ProviderEnv struct {
	Mode        string   `yaml:"mode,omitempty" json:"mode,omitempty"`
	Passthrough []string `yaml:"passthrough,omitempty" json:"passthrough,omitempty"`
	Redact      []string `yaml:"redact,omitempty" json:"redact,omitempty"`
}

// LaunchEntry mirrors a launch entry as it appears either inline on
// GlobalCatalog.Launches or in its own launches/<id>.yaml file. It is
// identical in shape to LaunchProfile and is provided as a separate
// name so callers can distinguish "the inline-listed shape" from "the
// per-file shape" without forcing them to do type assertions.
type LaunchEntry = LaunchProfile
