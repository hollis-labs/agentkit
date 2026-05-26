package agentlaunch

// AgentSpec identifies the agent persona to boot — name, identifier,
// the role file the bootdir planter renders into CLAUDE.md / AGENTS.md /
// agents/<name>.md, and free-form labels for downstream routing.
type AgentSpec struct {
	// ID is the stable agent identifier (slug). Required. Validation
	// fails with ErrMissingAgentID when empty.
	ID string `yaml:"id" json:"id"`

	// Name is the display name for the agent. Optional; consumers fall
	// back to ID when empty.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// RoleFile names the role definition the bootdir planter should
	// render into the per-provider boot file. May be an absolute path
	// or a path relative to a catalog-defined role root; resolution
	// happens at compile time. Optional — agents may declare identity
	// fully inline via BootProfileRef.Inline.BootPrompt.
	RoleFile string `yaml:"role_file,omitempty" json:"role_file,omitempty"`

	// Labels is a free-form map for orchestrator-side routing tags
	// (team, capability tier, etc.). The library does not interpret
	// these — they flow through to Metadata for downstream consumers.
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}
