package agentlaunch

// MCPSpec declares the Model Context Protocol surface exposed to a
// launched session. The shape mirrors Tether's allowlist-driven model
// loosely; the catalog port (CW-0003) adapts the YAML shape into this
// struct so all consumers share a single canonical type.
//
// Validation here is intentionally light: empty lists are legal, and
// per-server fields are caller-defined. The matrix and the catalog port
// each enforce richer rules at their own boundaries.
type MCPSpec struct {
	// Allowlist names MCP tools (or globs) the session is permitted to
	// see. Empty means "no allowlist filter" — every registered tool is
	// surfaced. The semantics of glob matching are caller-defined.
	Allowlist []string `yaml:"allowlist,omitempty" json:"allowlist,omitempty"`

	// Denylist names MCP tools the session is explicitly forbidden from
	// using, applied after Allowlist when both are present.
	Denylist []string `yaml:"denylist,omitempty" json:"denylist,omitempty"`

	// LoopbackURL is the URL of the in-process MCP loopback server the
	// session connects to for "self-MCP" surfaces (planted into the
	// bootdir's .mcp.json by go-agent-sessions). Optional.
	LoopbackURL string `yaml:"loopback_url,omitempty" json:"loopback_url,omitempty"`

	// Servers lists per-session MCP server commands the launcher should
	// register (subprocess-spawn entries the agent's MCP client probes
	// at startup). Each entry is rendered into the bootdir .mcp.json.
	Servers []MCPServerSpec `yaml:"servers,omitempty" json:"servers,omitempty"`
}

// MCPServerSpec describes a single MCP server entry rendered into the
// session's planted .mcp.json. Mirrors the canonical MCP "stdio server"
// descriptor — name + argv + env. URL-based MCP servers are out of
// scope for this struct; the LoopbackURL field on MCPSpec covers the
// single URL case the library cares about today.
type MCPServerSpec struct {
	// Name is the MCP server's stable identifier as the agent sees it.
	// Required by the catalog port; this library does not enforce
	// non-emptiness because per-server validation lives in CW-0003.
	Name string `yaml:"name" json:"name"`

	// Command is the binary the launcher spawns. Resolved against PATH
	// when relative.
	Command string `yaml:"command,omitempty" json:"command,omitempty"`

	// Args is the argv (excluding the command itself) handed to the
	// spawned MCP server.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

	// Env is the environment the MCP server inherits in addition to the
	// session's base env. Sensitive values (API keys) flow through here.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}
