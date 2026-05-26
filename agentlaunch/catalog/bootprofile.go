package catalog

// BootProfile is the on-disk shape of one boot-profiles/<id>.yaml.
// Mirrors Tether's boot-profile schema verbatim. Most fields flow
// through to agentlaunch.BootProfileRef.CatalogPath + Name; the
// translator does NOT inline these into BootProfileInline by default
// because the rich slot / identity payload is interpreted by a separate
// planter step downstream (CW-0005 and beyond).
//
// Callers that need an inline form can construct an
// agentlaunch.BootProfileInline directly from the fields they care
// about; this struct is the parse target, not a translation target.
type BootProfile struct {
	// ID is the profile name within the catalog (e.g.
	// "nanite.backend.main"). Required.
	ID string `yaml:"id" json:"id"`

	// DisplayName is the human-readable display name.
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`

	// Launch optionally pins the launch profile this boot profile is
	// scoped to (one launch may reference one boot profile, but the
	// reverse mapping is one-to-one in Tether's current schema).
	Launch string `yaml:"launch,omitempty" json:"launch,omitempty"`

	// MCPServers is the per-profile MCP server allowlist. Empty or omitted
	// means "proxy default" (i.e. all configured servers). When non-empty
	// the translator folds this into LaunchPlan.MCP.Allowlist after the
	// launch profile's own MCP.Servers is applied.
	MCPServers []string `yaml:"mcp_servers,omitempty" json:"mcp_servers,omitempty"`

	// Identity carries the lineage / project / role headers Tether uses
	// to render the top of the planted boot file.
	Identity Identity `yaml:"identity,omitempty" json:"identity,omitempty"`

	// Slots holds the per-slot definitions (recap, history, memory,
	// skills, etc.). Each slot is a free-form map; the keys vary by slot
	// type and are interpreted by the downstream planter. Preserved
	// verbatim so no slot data is lost in translation.
	Slots map[string]Slot `yaml:"slots,omitempty" json:"slots,omitempty"`

	// BootMode optionally selects a non-default boot mode for the
	// agentlaunch BootProfileInline form. When the translator builds an
	// inline boot profile this maps to agentlaunch.BootModeNone /
	// BootModeStdin / BootModePlanted. Empty defaults to
	// agentlaunch.BootModePlanted.
	BootMode string `yaml:"boot_mode,omitempty" json:"boot_mode,omitempty"`

	// SystemPrompt is the optional inline system / persona prompt template
	// the translator forwards to agentlaunch.BootProfileInline.BootPrompt
	// when constructing an inline profile. Tether's native fixtures do
	// not set this — the canonical Tether boot-profile shape composes the
	// boot prompt out of Slots — but the field is honoured when present
	// for use with non-Tether catalogs.
	SystemPrompt string `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`

	// BootContent is the optional inline per-task kickoff body. Maps to
	// agentlaunch.BootProfileInline.BootContent when constructing an
	// inline profile.
	BootContent string `yaml:"boot_content,omitempty" json:"boot_content,omitempty"`
}

// Identity mirrors the `identity:` block on a Tether boot profile.
// All fields are optional; the translator preserves them under
// LaunchPlan.Metadata.Annotations with the key prefix
// "tether.boot_profile.identity.".
type Identity struct {
	LineageAlias   string `yaml:"lineage_alias,omitempty" json:"lineage_alias,omitempty"`
	LineageID      string `yaml:"lineage_id,omitempty" json:"lineage_id,omitempty"`
	ProfileID      string `yaml:"profile_id,omitempty" json:"profile_id,omitempty"`
	ProfileVersion int    `yaml:"profile_version,omitempty" json:"profile_version,omitempty"`
	Role           string `yaml:"role,omitempty" json:"role,omitempty"`
	Project        string `yaml:"project,omitempty" json:"project,omitempty"`
	WorkRoot       string `yaml:"work_root,omitempty" json:"work_root,omitempty"`
	TrackingRoot   string `yaml:"tracking_root,omitempty" json:"tracking_root,omitempty"`
	VantaPrimary   string `yaml:"vanta_primary,omitempty" json:"vanta_primary,omitempty"`
}

// Slot is the free-form payload for a single boot-profile slot
// (recap, history, memory, skills, etc.). The shape varies by slot
// type, so the value is decoded as a map[string]any to avoid forcing
// every slot variant into one struct. The downstream planter
// interprets the type field and dispatches on it.
type Slot struct {
	// Type identifies the slot kind. Known values include
	// "role_summary", "cmd", "skill_index", "vanta_recall". The
	// translator does not interpret Type — it only preserves it.
	Type string `yaml:"type,omitempty" json:"type,omitempty"`

	// Path is consulted by slot kinds that read a file (role_summary).
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// Run is consulted by slot kinds that execute a command (cmd).
	Run string `yaml:"run,omitempty" json:"run,omitempty"`

	// Timeout is the optional execution timeout for Run-based slots
	// (Go duration string, e.g. "5s").
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Limit is consulted by slot kinds that page a list (skill_index).
	Limit int `yaml:"limit,omitempty" json:"limit,omitempty"`

	// PreferTriggers names trigger tokens slot implementations should
	// rank higher when ordering output.
	PreferTriggers []string `yaml:"prefer_triggers,omitempty" json:"prefer_triggers,omitempty"`

	// Namespace, Tags, Ranking, and Format are consulted by Vanta-style
	// recall slot kinds. Preserved verbatim; the translator does not
	// interpret them.
	Namespace string   `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Tags      []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Ranking   string   `yaml:"ranking,omitempty" json:"ranking,omitempty"`
	Format    string   `yaml:"format,omitempty" json:"format,omitempty"`
}
