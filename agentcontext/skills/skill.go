package skills

import (
	"strings"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// Skill is the provider-neutral on-disk skill model. It captures the
// minimum fields the agentcontext.SlotSourceKindSkillIndex resolver
// needs to render a deterministic index, plus the raw frontmatter
// map for orchestrator-side extension reads.
//
// The struct is intentionally small. Apps that need broker-policy
// metadata (allowed-tools, effort, model, argument-hint, modes,
// broker-hints, tags, …) read those fields off Skill.Frontmatter
// using the orchestrator-side keys. Apps that need the rendered
// markdown body read Skill.Body.
//
// All string fields are normalised on Parse:
//
//   - Skill.Name is trimmed of surrounding whitespace.
//   - Skill.Description is trimmed.
//   - Skill.Triggers entries are trimmed; empties are dropped.
//   - Skill.Body has the trailing newline preserved (so a renderer
//     can concatenate bodies without inserting a missing separator).
type Skill struct {
	// Name is the canonical kebab-case identifier. Populated from
	// frontmatter "slug" if present, otherwise from "name". Required
	// post-Parse — Validate returns ErrSkillMissingName if empty.
	Name string

	// Description is the one-line skill summary. Required post-Parse.
	Description string

	// Triggers is the slash-command / keyword list a chat-side router
	// uses to dispatch the skill. Empty when the frontmatter omits
	// the field; callers MAY fall back to "/" + Name in that case.
	Triggers []string

	// Body is the markdown body that follows the frontmatter. May be
	// empty for terse skills whose entire content is in the
	// frontmatter (rare; legal).
	Body string

	// Source is the absolute filesystem path the Skill was parsed
	// from. Discovery sets this to the canonical absolute path.
	// Empty when Parse is called on raw bytes with no source.
	Source string

	// Frontmatter is the verbatim parsed YAML frontmatter map. Keys
	// that this library promotes to first-class fields (slug, name,
	// description, triggers) ARE preserved here as well so apps that
	// want to read the original key shape (e.g. distinguishing a
	// Tether "name" from a Nanite "slug") can do so without
	// re-parsing the file.
	//
	// Map iteration order is non-deterministic; callers that need
	// stable ordering should sort the keys.
	Frontmatter map[string]any
}

// Validate enforces the contract-required fields: a non-empty Name
// and a non-empty Description. Returns ErrSkillMissingName or
// ErrSkillMissingDescription as appropriate.
//
// Apps that want richer validation (e.g. enforce that Triggers is
// non-empty, or that Frontmatter["effort"] is one of a known set)
// run their own validators on top — this library only enforces what
// the agentcontext contract layer needs.
func (s Skill) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return agentcontext.ErrSkillMissingName
	}
	if strings.TrimSpace(s.Description) == "" {
		return agentcontext.ErrSkillMissingDescription
	}
	return nil
}
