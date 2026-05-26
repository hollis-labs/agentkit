package agentlaunch

// CompiledLaunch is the output of the Compile step (owned by CW-0005)
// — a validated, normalized LaunchPlan plus the resolved fields the
// caller did not need to supply. The original Plan is embedded by
// reference so consumers can introspect the declarative input that
// produced this compilation.
//
// CompiledLaunch is the state Prepare consumes. It is the right level
// to cache (by Provenance.PlanHash) because everything in it is purely
// a function of the source LaunchPlan and the library version — no
// filesystem I/O has happened yet.
type CompiledLaunch struct {
	// Plan is the source LaunchPlan, retained for introspection.
	// Always non-nil for a CompiledLaunch produced by Compile; tests
	// may construct CompiledLaunch values directly without populating
	// Plan, in which case Validate fails with ErrCompiledMissingPlan.
	Plan *LaunchPlan `yaml:"plan" json:"plan"`

	// ResolvedProjectRoot is the absolute path the compiler resolved
	// from Plan.Project.Root (or filled in when Plan.Project.Root was
	// empty). Empty when the compiler had nothing to resolve.
	ResolvedProjectRoot string `yaml:"resolved_project_root,omitempty" json:"resolved_project_root,omitempty"`

	// ResolvedProviderBinary is the absolute path of the binary the
	// adapter will spawn — Plan.Provider.Binary if set, otherwise the
	// adapter default resolved against PATH. Empty for in-process /
	// API-only providers.
	ResolvedProviderBinary string `yaml:"resolved_provider_binary,omitempty" json:"resolved_provider_binary,omitempty"`

	// ResolvedRoleFile is the absolute path of the role file the boot
	// planter renders. Empty when the agent declares identity fully
	// inline via Plan.BootProfile.Inline.BootPrompt.
	ResolvedRoleFile string `yaml:"resolved_role_file,omitempty" json:"resolved_role_file,omitempty"`

	// BootDirIntent describes the layout intent for the bootdir Prepare
	// will materialize. Concrete fields are populated by CW-0005; the
	// type definition lives here so siblings can wire to it without
	// importing the not-yet-written compiler.
	BootDirIntent BootDirIntent `yaml:"boot_dir_intent,omitempty" json:"boot_dir_intent,omitempty"`

	// Provenance records compile-time provenance (compiled-at time,
	// catalog source, library version, plan hash).
	Provenance Provenance `yaml:"provenance,omitempty" json:"provenance,omitempty"`
}

// BootDirIntent describes the planted-bootdir layout the compiler
// resolves from a BootProfile. Prepare consumes this to know what
// files to write and what paths to template into env/argv. The
// concrete field set is owned by CW-0005; the struct ships now (as
// a placeholder with the canonical fields) so the matrix and catalog
// siblings can reference it.
type BootDirIntent struct {
	// PerProviderBootFile is the relative path inside the bootdir that
	// the per-provider boot file is planted at (e.g. "CLAUDE.md",
	// "AGENTS.md", "agents/<name>.md"). Empty for providers that have
	// no boot file convention.
	PerProviderBootFile string `yaml:"per_provider_boot_file,omitempty" json:"per_provider_boot_file,omitempty"`

	// TransientBootFile is the relative path of the transient boot
	// content file (e.g. "boot.md") referenced from the per-provider
	// boot file via an @-include. Empty when BootContent is empty.
	TransientBootFile string `yaml:"transient_boot_file,omitempty" json:"transient_boot_file,omitempty"`

	// MCPDescriptorFile is the relative path of the planted .mcp.json
	// (or equivalent) for the per-session MCP server set. Empty when
	// no MCP descriptor will be planted.
	MCPDescriptorFile string `yaml:"mcp_descriptor_file,omitempty" json:"mcp_descriptor_file,omitempty"`
}

// Validate runs sanity checks on the compiled state. A CompiledLaunch
// is valid only when its Plan is non-nil AND that Plan itself validates.
// Resolved fields (paths, role file) are NOT enforced here because the
// compiler legitimately leaves them empty when nothing needed resolving.
func (c CompiledLaunch) Validate() error {
	if c.Plan == nil {
		return ErrCompiledMissingPlan
	}
	if err := c.Plan.Validate(); err != nil {
		return err
	}
	return nil
}
