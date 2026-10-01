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
	// session connects to for "self-MCP" surfaces. Prepare copies it to
	// PreparedPlantContext.MCPLoopbackURL, and the go-providers renderers
	// plant it into the runtime's native MCP config. Optional.
	LoopbackURL string `yaml:"loopback_url,omitempty" json:"loopback_url,omitempty"`

	// Servers lists per-session MCP servers the agent's MCP client should
	// reach, stdio or HTTP. The launcher copies them into the plant
	// context, and go-providers renders them into each runtime's native
	// MCP config at the path its layout names (.mcp.json, config.toml,
	// opencode.json, the agy plugin's mcp_config.json).
	Servers []MCPServerSpec `yaml:"servers,omitempty" json:"servers,omitempty"`
}

// MCPServerSpec describes a single MCP server the launched agent reaches:
// a stdio server (Command, Args, Env) or a streamable-HTTP server (URL).
// Set exactly one of Command and URL; go-providers rejects a spec with
// neither or both when it renders the plant.
type MCPServerSpec struct {
	// Name is the MCP server's stable identifier as the agent sees it.
	// Required by the catalog port; this library does not enforce
	// non-emptiness because per-server validation lives in CW-0003.
	Name string `yaml:"name" json:"name"`

	// URL is a streamable-HTTP MCP endpoint. Set this XOR Command.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`

	// Command is the binary the launcher spawns for a stdio server.
	// Resolved against PATH when relative. Set this XOR URL.
	Command string `yaml:"command,omitempty" json:"command,omitempty"`

	// Args is the argv (excluding the command itself) handed to the
	// spawned MCP server.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

	// Env is the environment the MCP server inherits in addition to the
	// session's base env. Sensitive values (API keys) flow through here.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}
