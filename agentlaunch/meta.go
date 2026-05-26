package agentlaunch

// Metadata carries free-form Labels and Annotations the orchestrator
// attaches to a LaunchPlan. The library does not interpret either map;
// they flow through Compile and Prepare unchanged so downstream
// consumers can route, filter, or log against them.
//
// Labels are conventionally short tokens used for indexing (team, tier,
// region). Annotations are longer, human-targeted strings (commit SHAs,
// ticket references, debug notes).
type Metadata struct {
	// Labels is the indexable tag map.
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`

	// Annotations is the descriptive metadata map.
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}
