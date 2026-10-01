package agentlaunch

import permission "github.com/hollis-labs/go-permission"

// ProviderSpec names the provider adapter (claude / codex / opencode /
// future) and carries the configuration the launcher needs to spawn it.
// The ID drives adapter selection through go-providers; everything else
// is optional and overrides defaults the adapter would otherwise pick.
//
// The library does NOT validate which provider IDs exist — that is the
// catalog port's responsibility at parse time, and the matrix's (which reads
// the go-providers runtime registry) when pairing with a runtimes.Mode.
// Validate only enforces non-empty ID.
type ProviderSpec struct {
	// ID is the provider's stable identifier (e.g. "claude", "codex",
	// "opencode"). Required.
	ID string `yaml:"id" json:"id"`

	// Binary is an absolute or PATH-resolvable binary name that overrides
	// the adapter's default executable. Optional. The preparer resolves
	// this against PATH when relative.
	Binary string `yaml:"binary,omitempty" json:"binary,omitempty"`

	// Version is the version selector the adapter should pin to (e.g.
	// "1.4.2"). The semantics — strict equality, semver range, "latest"
	// — are adapter-defined; this library treats the value as opaque.
	// Optional.
	Version string `yaml:"version,omitempty" json:"version,omitempty"`

	// Flags is the slice of CLI flags spliced into argv after the
	// adapter's own BuildArgs output. The preparer appends these in
	// order; no template substitution is performed.
	Flags []string `yaml:"flags,omitempty" json:"flags,omitempty"`

	// Env is the map of environment variables forwarded to the spawned
	// process. Merged with the runtime's base env and with
	// InjectionSpec.Env at prepare time; InjectionSpec wins on conflict.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// ModelOverride pins the model the provider uses for this session,
	// overriding the catalog default and any per-agent preference.
	// Adapter-defined string (e.g. "claude-sonnet-4.5"). Optional.
	ModelOverride string `yaml:"model_override,omitempty" json:"model_override,omitempty"`

	// Permission is the spawned agent's permission posture: go-permission's
	// Mode (default, accept-edits, plan or yolo; D-72), the same for every
	// provider. providerplant maps it onto the provider's own launch flags or
	// environment through the go-providers registry's Posture hook
	// (registry.Descriptor.PostureFor). Empty sets no posture: the launch
	// carries exactly the argv and environment it would without one, and the
	// provider keeps its own default. PlanFromLaunch sets it from
	// RuntimeBinding.Permission. Optional.
	Permission permission.Mode `yaml:"permission,omitempty" json:"permission,omitempty"`

	// MCPExclusive keeps the launch to the MCP servers the launch itself
	// plants (Injection.MCPServers, the boot profile's), so the user's own
	// servers, from the config the runtime reads outside the launch, are not
	// loaded next to them. Without it a runtime may load both, and a host that
	// allow-lists the planted servers has not limited the user-level ones.
	//
	// go-providers owns the mechanism per runtime and mode
	// (registry.MCPExclusivity): Claude's --strict-mcp-config, which the
	// projection adds to the argv; Codex's planted CODEX_HOME, which the launch
	// always sets. Where none was measured (OpenCode, Antigravity today) the
	// launch is refused, not run non-exclusive: Compile returns
	// ErrMCPExclusiveUnsupported, and so does the preparer should a plan reach
	// it uncompiled or through an adapter that cannot honor the request. The
	// user's servers are left out on purpose: a host that wants one passes it
	// as a planted server. It does not narrow which tools of the planted
	// servers may run; that stays the host's allow-list. Default false: the
	// launch is exactly what it was. Optional.
	MCPExclusive bool `yaml:"mcp_exclusive,omitempty" json:"mcp_exclusive,omitempty"`
}
