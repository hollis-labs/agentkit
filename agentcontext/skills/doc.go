// Package skills is the on-disk skill model + layered discovery
// layer that backs the agentcontext.SlotSourceKindSkillIndex slot
// kind. It is intentionally narrow: parse a markdown-with-YAML
// frontmatter file into a provider-neutral Skill, walk an ordered
// list of layer roots (e.g. ~/.tether/skills/, ~/.nanite/skills/,
// project-local skills), apply override-by-name on collisions, and
// emit a deterministic Index that the skill_index resolver can render
// into a context slot.
//
// # Scope
//
// This package owns:
//
//   - Skill model — the provider-neutral struct that downstream
//     orchestrators consume.
//   - Frontmatter parsing — YAML-frontmatter / markdown-body split,
//     with unknown fields preserved in Skill.Frontmatter for
//     orchestrator-side extensions.
//   - Layered discovery — walk an ordered list of Layer roots,
//     deterministic file iteration, per-file parse-error capture so
//     one bad file does not poison the whole discovery, and an
//     override chain recorded per skill name.
//   - Index — sorted/deterministic List, ByName lookup that
//     hard-errors on miss, and a trigger-keyword Lookup.
//
// # Non-goals
//
// This package does NOT own:
//
//   - Skill dispatch / execution — that's the app's job (Tether's
//     skill broker, Nanite's skill service, etc.).
//   - Slash-command parsing or argument extraction — the trigger
//     keyword is a string; how an app routes "/foo bar baz" to a
//     skill is out of scope.
//   - Telemetry, RBAC, broker policy — Skills here are pure
//     data; behavior layers live above.
//   - Frontmatter validation beyond the contract-required fields
//     (name + description). Apps that need richer validation read
//     Skill.Frontmatter and run their own validators on top.
//
// # Frontmatter shape
//
// The canonical frontmatter shape this package recognises is the
// union of the Tether catalog and Nanite skill formats — both rely
// on YAML frontmatter delimited by "---" lines. The required fields
// (post-Validate) are:
//
//   - name OR slug — kebab-case identifier (one wins; see below).
//   - description — one-line summary.
//
// The optional fields surfaced as first-class Skill fields are:
//
//   - triggers — array of strings (slash-commands or keywords).
//
// All other frontmatter keys are preserved unchanged in
// Skill.Frontmatter so app-side code can read tags, allowed-tools,
// effort, model, argument-hint, broker-hints, modes, and any future
// extension fields without this library needing a release.
//
// # name / slug precedence
//
// The Tether catalog format uses "name" for the kebab-case
// identifier; the Nanite format uses "slug" for the identifier and
// "name" for a human-readable title. To accept both without
// requiring orchestrator-side normalization, Parse populates
// Skill.Name as follows:
//
//   - If frontmatter declares both "slug" and "name", "slug" wins
//     (it is the more identifier-shaped field in the Nanite world,
//     and the Tether catalog does not emit "slug" so there is no
//     conflict).
//   - If frontmatter declares only one, that value populates
//     Skill.Name.
//   - If neither is declared, Parse returns ErrSkillMissingName.
//
// The original "name" string (when distinct from the slug) is
// preserved in Skill.Frontmatter["name"] for apps that want to
// surface the human-readable title separately.
package skills
