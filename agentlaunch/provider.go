package agentlaunch

// ProviderSpec names the provider adapter (claude / codex / opencode /
// future) and carries the configuration the launcher needs to spawn it.
// The ID drives adapter selection through go-providers; everything else
// is optional and overrides defaults the adapter would otherwise pick.
//
// The library does NOT validate which provider IDs exist — that is the
// catalog port's responsibility (CW-0003) at parse time, and the
// matrix's responsibility (CW-0004) when pairing with RuntimeKind.
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

	// Permission is the spawned agent's permission/approval posture in the
	// provider's own vocabulary (claude permission_mode / codex
	// approval_policy — see RuntimeBinding.Permission). providerplant's
	// DefaultResolver applies it to the resolved go-providers adapter
	// (ClaudeAdapter.PermissionMode / CodexAdapter.ApprovalPolicy) so the
	// planted boot dir carries the non-interactive approval contract.
	// PlanFromLaunch sets it from RuntimeBinding.Permission. Optional.
	Permission string `yaml:"permission,omitempty" json:"permission,omitempty"`
}
