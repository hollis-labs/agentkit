package agentcontext

import "github.com/hollis-labs/agentkit/artifact"

// AuthoredRecipe is the app-neutral composition input for callers that want
// shared base/parts/slot/document assembly. Apps supply definitions and policy;
// this package owns deterministic merge mechanics.
type AuthoredRecipe struct {
	ID          string            `yaml:"id" json:"id"`
	Base        string            `yaml:"base,omitempty" json:"base,omitempty"`
	Parts       []PartRef         `yaml:"parts,omitempty" json:"parts,omitempty"`
	Slots       []SlotSpec        `yaml:"slots,omitempty" json:"slots,omitempty"`
	Documents   []Document        `yaml:"documents,omitempty" json:"documents,omitempty"`
	Artifacts   artifact.Tree     `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Inputs      map[string]any    `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	MergeRules  []MergeRule       `yaml:"merge_rules,omitempty" json:"merge_rules,omitempty"`
	Extensions  map[string]any    `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	Provenance  ProvenanceInput   `yaml:"provenance,omitempty" json:"provenance,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

type PartRef struct {
	ID       string         `yaml:"id" json:"id"`
	Optional bool           `yaml:"optional,omitempty" json:"optional,omitempty"`
	Inputs   map[string]any `yaml:"inputs,omitempty" json:"inputs,omitempty"`
}

type MergeRuleKind string

const (
	MergeReplace MergeRuleKind = "replace"
	MergeAppend  MergeRuleKind = "append"
	MergeKeyed   MergeRuleKind = "keyed"
	MergeOpaque  MergeRuleKind = "opaque"
)

type MergeRule struct {
	Field string        `yaml:"field" json:"field"`
	Kind  MergeRuleKind `yaml:"kind" json:"kind"`
	Key   string        `yaml:"key,omitempty" json:"key,omitempty"`
}

// ResolvedComposition is the source-frozen output of composition. Provider
// projection may consume Documents and Artifacts, while direct artifact callers
// may bypass composition entirely.
type ResolvedComposition struct {
	ID           string                  `yaml:"id" json:"id"`
	RecipeID     string                  `yaml:"recipe_id,omitempty" json:"recipe_id,omitempty"`
	Documents    []Document              `yaml:"documents,omitempty" json:"documents,omitempty"`
	Artifacts    artifact.Tree           `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Extensions   map[string]any          `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	Contributors []Contributor           `yaml:"contributors,omitempty" json:"contributors,omitempty"`
	Digests      map[string]string       `yaml:"digests,omitempty" json:"digests,omitempty"`
	Diagnostics  []CompositionDiagnostic `yaml:"diagnostics,omitempty" json:"diagnostics,omitempty"`
}

type Document struct {
	ID       string    `yaml:"id" json:"id"`
	Path     string    `yaml:"path,omitempty" json:"path,omitempty"`
	Sections []Section `yaml:"sections,omitempty" json:"sections,omitempty"`
	Content  string    `yaml:"content,omitempty" json:"content,omitempty"`
}

type Section struct {
	ID          string   `yaml:"id" json:"id"`
	Title       string   `yaml:"title,omitempty" json:"title,omitempty"`
	Content     string   `yaml:"content" json:"content"`
	Contributor string   `yaml:"contributor,omitempty" json:"contributor,omitempty"`
	DependsOn   []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
}

type Contributor struct {
	ID         string `yaml:"id" json:"id"`
	SourcePath string `yaml:"source_path,omitempty" json:"source_path,omitempty"`
	Revision   string `yaml:"revision,omitempty" json:"revision,omitempty"`
	Dirty      bool   `yaml:"dirty,omitempty" json:"dirty,omitempty"`
}

type CompositionDiagnostic struct {
	Code     string `yaml:"code" json:"code"`
	Severity string `yaml:"severity" json:"severity"`
	Message  string `yaml:"message" json:"message"`
	Source   string `yaml:"source,omitempty" json:"source,omitempty"`
}

type ComposeRequest struct {
	Recipe      AuthoredRecipe   `yaml:"recipe" json:"recipe"`
	Definitions []AuthoredRecipe `yaml:"definitions,omitempty" json:"definitions,omitempty"`
}

type Composer interface {
	Compose(req ComposeRequest) (ResolvedComposition, error)
}
