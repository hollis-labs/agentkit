package agentlaunch

// ProjectSpec identifies the project the launch is scoped to and
// optionally pins the absolute root directory beneath which the
// workspace materializes. Catalog entries set ID + Name; the orchestrator
// supplies Root when it wants per-project workspace isolation.
type ProjectSpec struct {
	// ID is the stable project identifier (slug). Required. Validation
	// fails with ErrMissingProjectID when empty.
	ID string `yaml:"id" json:"id"`

	// Name is the human-readable project name. Optional; consumers fall
	// back to ID for display when empty.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Root is the absolute project root directory. Optional at plan
	// time — the compiler resolves a sensible default when empty.
	Root string `yaml:"root,omitempty" json:"root,omitempty"`
}
