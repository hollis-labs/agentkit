package agentlaunch

// PreparedLaunch is the output of the Prepare step (owned by CW-0005)
// — a CompiledLaunch plus the materialization artifacts the caller
// needs to hand to go-agent-sessions's Manager.Start: the planted
// bootdir, the finalized workspace dir, the finalized environment, and
// the final argv.
//
// The PreparedLaunch fields below intentionally mirror the shape of
// go-agent-sessions's StartOptions (Workdir, WorkspaceDir, Env, Args,
// BootMode, BootPrompt, BootContent, PlantContext) but are modelled as
// native types so this package does not import go-agent-sessions. A
// thin conversion shim (CW-0005's responsibility, possibly behind a
// build tag or in a separate subpackage) translates PreparedLaunch to
// go-agent-sessions's types at the integration boundary.
//
// Native modelling decisions worth flagging:
//   - Env is a map[string]string (not the []string "K=V" form
//     StartOptions uses). The shim joins on conversion. Map form is
//     easier to reason about at the contract level — duplicates collapse
//     to the last-write, and merging InjectionSpec.Env over
//     ProviderSpec.Env is a single-line operation.
//   - Argv is the FULL argv including argv[0] (the binary name). The
//     StartOptions form splits argv[0] into Binary and the rest into
//     Args; the shim performs that split at the boundary.
//   - PlantContext is a sibling struct (PreparedPlantContext) carrying
//     the caller-owned plant context fields. It mirrors
//     provider.PlantContext loosely; CW-0005 widens it as the surface
//     stabilises.
type PreparedLaunch struct {
	// Compiled is the upstream CompiledLaunch from which this prepared
	// state was derived. Always non-nil for a PreparedLaunch produced
	// by Prepare.
	Compiled *CompiledLaunch `yaml:"compiled" json:"compiled"`

	// PlantedBootDir is the absolute path of the materialised bootdir.
	// Required for a valid PreparedLaunch. The preparer removes this
	// directory on session termination unless caller policy says
	// otherwise (e.g. WorkspacePersistent).
	PlantedBootDir string `yaml:"planted_boot_dir" json:"planted_boot_dir"`

	// WorkspaceDir is the absolute path of the finalized per-session
	// workspace root. Required for a valid PreparedLaunch. Maps to
	// go-agent-sessions StartOptions.WorkspaceDir.
	WorkspaceDir string `yaml:"workspace_dir" json:"workspace_dir"`

	// Workdir is the absolute path used as the spawned process's cwd.
	// May equal WorkspaceDir or live underneath it depending on the
	// per-provider plant convention. Maps to StartOptions.Workdir.
	Workdir string `yaml:"workdir,omitempty" json:"workdir,omitempty"`

	// Env is the finalized environment for the spawned process,
	// expressed as a map. The shim that hands off to go-agent-sessions
	// joins this into the []string "K=V" form StartOptions expects.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Argv is the finalized argv for the spawn, INCLUDING argv[0] (the
	// binary path). Required: a PreparedLaunch with empty Argv fails
	// Validate via ErrPreparedMissingArgv. The shim splits Argv[0] into
	// the binary path and Argv[1:] into StartOptions.Args (the runtime
	// determines binary vs. args from the adapter's BuildArgs convention).
	Argv []string `yaml:"argv" json:"argv"`

	// BootMode is the boot-mode token forwarded to
	// StartOptions.BootMode. Empty when the boot profile selected the
	// "none" mode. The library does not validate the value here —
	// LaunchPlan.Validate already did when an inline profile was used,
	// and the compiler is trusted to produce a sane token otherwise.
	BootMode string `yaml:"boot_mode,omitempty" json:"boot_mode,omitempty"`

	// BootPrompt is the durable system / persona prompt the preparer
	// resolved (read from the role file or supplied inline). Maps to
	// StartOptions.BootPrompt.
	BootPrompt string `yaml:"boot_prompt,omitempty" json:"boot_prompt,omitempty"`

	// BootContent is the per-task kickoff body. Maps to
	// StartOptions.BootContent.
	BootContent string `yaml:"boot_content,omitempty" json:"boot_content,omitempty"`

	// PlantContext carries the caller-owned plant context fields the
	// preparer threads through to the bootdir planter. Mirrors
	// provider.PlantContext at the contract level.
	PlantContext PreparedPlantContext `yaml:"plant_context,omitempty" json:"plant_context,omitempty"`
}

// PreparedPlantContext mirrors the caller-owned portion of
// go-providers/provider.PlantContext at the contract level. The shim
// (CW-0005) translates between this and provider.PlantContext at the
// integration boundary so this package stays import-free of
// go-providers.
//
// Lib-managed fields (SystemPrompt, BootContent, ProjectDir, BootDir)
// are NOT carried here — go-agent-sessions overrides them from the
// other StartOptions fields, so duplicating them on this struct would
// create two sources of truth. AgentName, MCPLoopbackURL, SelfMCPCommand,
// SelfMCPArgs, and SelfMCPEnv do flow through verbatim.
type PreparedPlantContext struct {
	// AgentName is the agent display name the per-provider boot-file
	// renderer references (e.g. "{{.AgentName}}" in CLAUDE.md). Empty
	// falls back to AgentSpec.ID at render time.
	AgentName string `yaml:"agent_name,omitempty" json:"agent_name,omitempty"`

	// MCPLoopbackURL mirrors MCPSpec.LoopbackURL — the URL the planted
	// .mcp.json points the agent at for the in-process loopback server.
	MCPLoopbackURL string `yaml:"mcp_loopback_url,omitempty" json:"mcp_loopback_url,omitempty"`

	// SelfMCPCommand is the absolute path of the helper binary the
	// planted .mcp.json descriptor invokes for self-MCP entries. For
	// Tether this is usually the `mux` binary, but the field is kept
	// neutral because agentkit is not Tether-specific.
	SelfMCPCommand string `yaml:"self_mcp_command,omitempty" json:"self_mcp_command,omitempty"`

	// SelfMCPArgs is the argv (excluding command) for SelfMCPCommand.
	SelfMCPArgs []string `yaml:"self_mcp_args,omitempty" json:"self_mcp_args,omitempty"`

	// SelfMCPEnv is the environment forwarded to the helper subprocess.
	SelfMCPEnv map[string]string `yaml:"self_mcp_env,omitempty" json:"self_mcp_env,omitempty"`
}

// Validate runs sanity checks on the materialized state. A
// PreparedLaunch is valid only when:
//
//   - the embedded Compiled launch is non-nil and itself valid,
//   - PlantedBootDir is non-empty,
//   - WorkspaceDir is non-empty,
//   - Argv has at least one element (the binary path).
//
// Env / Workdir / boot-prompt fields may legitimately be empty — they
// fall back to runtime defaults at the StartOptions boundary.
func (p PreparedLaunch) Validate() error {
	if p.Compiled == nil {
		return ErrCompiledMissingPlan
	}
	if err := p.Compiled.Validate(); err != nil {
		return err
	}
	if p.PlantedBootDir == "" {
		return ErrPreparedMissingBootDir
	}
	if p.WorkspaceDir == "" {
		return ErrPreparedMissingWorkspaceDir
	}
	if len(p.Argv) == 0 {
		return ErrPreparedMissingArgv
	}
	return nil
}
