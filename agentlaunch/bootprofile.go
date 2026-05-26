package agentlaunch

// BootMode tokens recognised by BootProfileInline.BootMode. Declared as
// string literals (not a typed enum) because go-agent-sessions's
// StartOptions.BootMode field is a free-form string today; widening the
// set later is a string-literal addition. Validation only checks for
// these three values when an inline boot profile is supplied.
const (
	BootModeNone    = "none"
	BootModeStdin   = "stdin"
	BootModePlanted = "planted"
)

// BootProfileRef references the boot profile a session boots with —
// either a catalog entry resolved by the catalog port (CW-0003) or an
// inline override the orchestrator constructs directly. At least one of
// CatalogPath or Inline must be set; LaunchPlan.Validate rejects an
// empty BootProfileRef via ErrMissingBootProfile.
type BootProfileRef struct {
	// CatalogPath is the absolute path of the source catalog YAML the
	// profile lives in. Empty when the caller uses Inline.
	CatalogPath string `yaml:"catalog_path,omitempty" json:"catalog_path,omitempty"`

	// Name is the profile name within the catalog. Empty when the
	// caller uses Inline.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Inline, when non-nil, supplies the boot profile body directly,
	// bypassing the catalog. Tests and ad-hoc launches use this path;
	// production launches typically resolve through the catalog.
	Inline *BootProfileInline `yaml:"inline,omitempty" json:"inline,omitempty"`
}

// BootProfileInline is the minimal inline form of a boot profile —
// enough fields to drive go-agent-sessions's StartOptions.BootPrompt /
// BootContent / BootMode without round-tripping through the catalog
// schema. The catalog port (CW-0003) defines the richer catalog form
// and converts to / from this struct at compile time when consumers
// hand it inline material.
type BootProfileInline struct {
	// BootPrompt is the durable system / persona prompt rendered into
	// the per-provider boot file (CLAUDE.md / AGENTS.md / etc.). Maps
	// to go-agent-sessions StartOptions.BootPrompt.
	BootPrompt string `yaml:"boot_prompt,omitempty" json:"boot_prompt,omitempty"`

	// BootContent is the per-task kickoff body planted into the
	// transient boot file (boot.md / equivalent). Maps to
	// go-agent-sessions StartOptions.BootContent. Empty falls back to
	// BootPrompt at the consumer end (see go-agent-sessions docs).
	BootContent string `yaml:"boot_content,omitempty" json:"boot_content,omitempty"`

	// BootMode is the boot-mode token: one of BootModeNone, BootModeStdin,
	// or BootModePlanted. Validation rejects any other value via
	// ErrUnsupportedBootMode when Inline is set on the LaunchPlan.
	BootMode string `yaml:"boot_mode,omitempty" json:"boot_mode,omitempty"`
}

// validBootMode reports whether s is one of the three recognised inline
// boot-mode tokens. The zero value ("") is NOT valid when Inline is
// supplied — callers must explicitly choose a mode.
func validBootMode(s string) bool {
	switch s {
	case BootModeNone, BootModeStdin, BootModePlanted:
		return true
	default:
		return false
	}
}
