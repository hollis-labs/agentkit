package agentlaunch

import "time"

// Version is the go-agent-launch library version embedded in
// Provenance.CompilerVersion. Bumped manually when the public surface
// changes; consumers should NOT depend on the exact value at runtime.
//
// Held in a typed string constant (rather than the conventional pkg-level
// Version variable) so it cannot be reassigned by a caller.
const Version = "v0.3.5"

// Provenance records compile-time provenance on a CompiledLaunch so
// downstream consumers can attribute a launch back to the catalog
// version and library version that produced it. The compiler (CW-0005)
// populates these fields; this package only owns the type shape.
type Provenance struct {
	// CompiledAt is the wall-clock time at which Compile returned. UTC
	// is conventional but not enforced. Zero value indicates "compiler
	// did not stamp" (e.g. hand-constructed CompiledLaunch in tests).
	CompiledAt time.Time `yaml:"compiled_at,omitempty" json:"compiled_at,omitempty"`

	// SourceCatalog is the absolute path of the catalog YAML the
	// LaunchPlan originated from. Empty for inline / programmatic
	// LaunchPlans that did not flow through the catalog port.
	SourceCatalog string `yaml:"source_catalog,omitempty" json:"source_catalog,omitempty"`

	// SourceCatalogVersion is the catalog's self-reported version
	// (typically a git SHA or semver string written into the catalog
	// front-matter). Opaque to this library.
	SourceCatalogVersion string `yaml:"source_catalog_version,omitempty" json:"source_catalog_version,omitempty"`

	// CompilerVersion is the go-agent-launch library version that ran
	// Compile. Set to the package-level Version constant by CW-0005's
	// Compile entry point.
	CompilerVersion string `yaml:"compiler_version,omitempty" json:"compiler_version,omitempty"`

	// PlanHash is a stable digest of the source LaunchPlan, useful for
	// caching CompiledLaunch outputs across identical inputs. Format and
	// canonicalization rules are owned by CW-0005's Compile implementation;
	// this package treats the field as an opaque string and only ships
	// the field shape so siblings can wire to it.
	PlanHash string `yaml:"plan_hash,omitempty" json:"plan_hash,omitempty"`
}
