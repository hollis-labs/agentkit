package agentlaunch

// LaunchPlan is the declarative input to the launch pipeline. A
// LaunchPlan carries everything the catalog port (CW-0003), the
// provider × runtime matrix (CW-0004), and the compiler/preparer
// (CW-0005) need to turn a catalog entry plus a runtime selection into
// a ready-to-spawn session.
//
// LaunchPlan remains the stable LaunchSpec-equivalent integration view
// for existing consumers even after the BootSpec / RuntimeBinding split:
// BootSpec defines the frozen boot-assembly blueprint, while LaunchPlan
// continues to be the compile/prepare handoff where consumer runtime
// overlays take precedence.
//
// A LaunchPlan is value-typed and shareable across goroutines. Maps
// and slices inside it are NOT defensively copied — callers must not
// mutate them after the plan is handed to Compile.
//
// Validate enforces field-shape correctness only: required identifiers,
// known enum values, path-safety on injection overlays, and that some
// boot profile source is supplied. Validate deliberately does NOT
// enforce provider × runtime compatibility — the matrix sibling package
// owns that boundary.
type LaunchPlan struct {
	// Project identifies the project the launch is scoped to.
	Project ProjectSpec `yaml:"project" json:"project"`

	// Agent identifies the agent persona to boot.
	Agent AgentSpec `yaml:"agent" json:"agent"`

	// Provider names the provider adapter (claude / codex / opencode).
	Provider ProviderSpec `yaml:"provider" json:"provider"`

	// Runtime names the lifecycle shape (subprocess / pty /
	// streaming-stdio / jsonrpc-stdio). The matrix sibling validates
	// that the (Provider.ID, Runtime) pair is legal.
	Runtime RuntimeKind `yaml:"runtime" json:"runtime"`

	// Workspace declares the workspace reservation policy.
	Workspace WorkspaceSpec `yaml:"workspace" json:"workspace"`

	// BootProfile references the boot profile to apply (catalog entry
	// or inline body).
	BootProfile BootProfileRef `yaml:"boot_profile" json:"boot_profile"`

	// MCP declares the MCP allow/deny/server surface for the session.
	MCP MCPSpec `yaml:"mcp,omitempty" json:"mcp,omitempty"`

	// Injection carries caller-supplied env / argv / bootdir overlay
	// overrides spliced in at prepare time.
	Injection InjectionSpec `yaml:"injection,omitempty" json:"injection,omitempty"`

	// Mode names the lifecycle stance (interactive / background /
	// ephemeral).
	Mode LaunchMode `yaml:"mode" json:"mode"`

	// Metadata carries orchestrator-defined labels and annotations.
	Metadata Metadata `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// Validate runs field-shape correctness checks on the plan. Returns
// one of the package-level sentinel errors on first failure — use
// errors.Is to branch on the failure mode.
//
// The order of checks below is stable so consumers writing tests
// against the sentinel-first-returned can rely on it; reorder with
// care.
//
// Validate does NOT enforce provider × runtime compatibility. That
// boundary lives in the matrix sibling subpackage (CW-0004).
func (p LaunchPlan) Validate() error {
	if p.Project.ID == "" {
		return ErrMissingProjectID
	}
	if p.Agent.ID == "" {
		return ErrMissingAgentID
	}
	if p.Provider.ID == "" {
		return ErrMissingProviderID
	}
	if !p.Runtime.Valid() {
		return ErrUnknownRuntime
	}
	if !p.Workspace.Mode.Valid() {
		return ErrUnsupportedWorkspaceMode
	}
	if !p.Mode.Valid() {
		return ErrUnsupportedLaunchMode
	}
	if p.BootProfile.CatalogPath == "" && p.BootProfile.Inline == nil {
		return ErrMissingBootProfile
	}
	if p.BootProfile.Inline != nil {
		if !validBootMode(p.BootProfile.Inline.BootMode) {
			return ErrUnsupportedBootMode
		}
	}
	for key := range p.Injection.BootDirOverlay {
		if err := validateOverlayKey(key); err != nil {
			return err
		}
	}
	for i := range p.Injection.NativeFiles {
		if err := p.Injection.NativeFiles[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}
