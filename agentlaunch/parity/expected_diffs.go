package parity

// expected_diffs.go is the annotated registry of intentional old-vs-new
// divergences. Per the S4.5 acceptance contract a non-zero field diff is
// acceptable ONLY when it is a documented, intentional difference; this
// file is that documentation, in code, so the harness can classify each
// diff mechanically.
//
// A diff that matches no entry here is an UNEXPLAINED diff and fails the
// harness. Adding an entry is a deliberate act: it records that a divergence
// was reviewed and judged correct, with the rationale attached.

// ExpectedDiff is one registered intentional divergence. It is also the
// shape a caller passes to WithExpectedDiffs when running RunParity over a
// corpus wider than the built-in one.
type ExpectedDiff struct {
	// Launch is the corpus launch name (the testdata bag stem) the diff
	// applies to. Empty matches any launch.
	Launch string

	// Field is the NormalizedPlan field the diff is on (project / work_dir
	// / runner / isolation).
	Field string

	// Old / New pin the exact values. An empty string is a wildcard for
	// that side — useful when only one side's value is stable.
	Old string
	New string

	// Rationale is the documented reason this divergence is correct and
	// intentional. It is surfaced verbatim in the harness Report.
	Rationale string
}

// expectedDiffs enumerates every reviewed, intentional old-vs-new
// divergence in the S4.4 corpus.
//
// # THE agent-mux PROJECT-REPOINT FINDING
//
// All five legacy launches/agent-mux-*.yaml files carry `project: tether`
// — they were cloned from the tether launch and never re-pointed at an
// agent-mux project (and ~/.tether/catalog/projects/agent-mux.yaml does
// not exist). The S4.4 re-expression CORRECTED this: every agent-mux
// LaunchBag declares `project: agent-mux` and the agent-mux work_dir. That
// correction is the right call — an agent-mux launch operating on the
// tether repo is a latent bug in the legacy catalog — so the resulting
// project + work_dir divergence is an intentional, documented difference,
// not a parity failure. (The legacy catalog is read-only here and is left
// untouched; the fix lives in the new model and, eventually, in a catalog
// data correction outside this lib.)
var expectedDiffs = []ExpectedDiff{
	// --- agent-mux: project re-pointed (5 launches) ---
	{
		Launch:    "agent-mux-claude",
		Field:     "project",
		Old:       "tether",
		New:       "agent-mux",
		Rationale: "agent-mux-project-repoint: legacy launches/agent-mux-claude.yaml carries project:tether (cloned, never re-pointed); S4.4 bag correctly scopes to agent-mux",
	},
	{
		Launch:    "agent-mux-claude",
		Field:     "work_dir",
		Old:       "~/dev/hollis-labs/apps/tether",
		New:       "~/dev/hollis-labs/apps/agent-mux",
		Rationale: "agent-mux-project-repoint: work_dir follows the corrected project (legacy resolved tether's repo_root)",
	},
	{
		Launch:    "agent-mux-codex-launch",
		Field:     "project",
		Old:       "tether",
		New:       "agent-mux",
		Rationale: "agent-mux-project-repoint: legacy launches/agent-mux-codex-launch.yaml carries project:tether; S4.4 bag correctly scopes to agent-mux",
	},
	{
		Launch:    "agent-mux-codex-launch",
		Field:     "work_dir",
		Old:       "~/dev/hollis-labs/apps/tether",
		New:       "~/dev/hollis-labs/apps/agent-mux",
		Rationale: "agent-mux-project-repoint: work_dir follows the corrected project",
	},
	{
		Launch:    "agent-mux-codex-app-server",
		Field:     "project",
		Old:       "tether",
		New:       "agent-mux",
		Rationale: "agent-mux-project-repoint: legacy launches/agent-mux-codex-app-server.yaml carries project:tether; S4.4 bag correctly scopes to agent-mux",
	},
	{
		Launch:    "agent-mux-codex-app-server",
		Field:     "work_dir",
		Old:       "~/dev/hollis-labs/apps/tether",
		New:       "~/dev/hollis-labs/apps/agent-mux",
		Rationale: "agent-mux-project-repoint: work_dir follows the corrected project",
	},
	{
		Launch:    "agent-mux-opencode",
		Field:     "project",
		Old:       "tether",
		New:       "agent-mux",
		Rationale: "agent-mux-project-repoint: legacy launches/agent-mux-opencode.yaml carries project:tether; S4.4 bag correctly scopes to agent-mux",
	},
	{
		Launch:    "agent-mux-opencode",
		Field:     "work_dir",
		Old:       "~/dev/hollis-labs/apps/tether",
		New:       "~/dev/hollis-labs/apps/agent-mux",
		Rationale: "agent-mux-project-repoint: work_dir follows the corrected project",
	},
	{
		Launch:    "agent-mux-claude-stream-worktree",
		Field:     "project",
		Old:       "tether",
		New:       "agent-mux",
		Rationale: "agent-mux-project-repoint: legacy launches/agent-mux-claude-stream-worktree.yaml carries project:tether; S4.4 bag correctly scopes to agent-mux",
	},
	{
		Launch:    "agent-mux-claude-stream-worktree",
		Field:     "work_dir",
		Old:       "~/dev/hollis-labs/apps/tether",
		New:       "~/dev/hollis-labs/apps/agent-mux",
		Rationale: "agent-mux-project-repoint: work_dir follows the corrected project",
	},
}

// expectedOldErrors registers launches whose OLD-side static resolution is
// EXPECTED to fail because the legacy catalog entry is itself defective.
// An expected old-side error is a documented intentional divergence, not a
// parity failure: it records that the new model deliberately departs from a
// broken legacy launch.
//
// # THE hollislabs-web-writer DANGLING-AGENT FINDING
//
// legacy launches/hollislabs-web-writer-claude.yaml references
// `agent: web-writer`, but ~/.tether/catalog/agents/ contains no
// web-writer.yaml (only frontend / general / launchpad-*). The legacy
// launch is therefore unresolvable in-process — a dangling agent reference,
// a latent defect in the legacy catalog. The S4.4 re-expression folds the
// agent into the `agent_role` input (web-writer), so the new side resolves
// cleanly. The old-side failure is expected and documented here.
//
// NOTE: this built-in registry is scoped to the harness's own Corpus (the
// 11-entry S4.4 set). A consumer running a WIDER corpus (e.g. Tether's full
// 64-launch catalog, which surfaces more dangling-agent launches) does NOT
// add entries here — it passes its rationales to RunParity via
// WithExpectedOldErrors / WithExpectedDiffs, which merge with this registry
// for that run. Report.StaleExpected then staleness-checks the merged set
// against the run's corpus.
var expectedOldErrors = map[string]string{
	"hollislabs-web-writer-claude": "hollislabs-web-writer-dangling-agent: legacy launch references agent:web-writer with no agents/web-writer.yaml in the catalog; new bag folds it into the agent_role input",
}

// lookupExpectedOldErr reports whether launch has a documented, intentional
// old-side resolution failure in registry m, returning the rationale when
// it does. m is the run's effective expected-old-error registry (the
// built-in expectedOldErrors merged with any WithExpectedOldErrors entries).
func lookupExpectedOldErr(m map[string]string, launch string) (string, bool) {
	rat, ok := m[launch]
	return rat, ok
}

// lookupExpectedDiff reports whether (launch, field, old, new) matches a
// registered intentional divergence in the registry slice, returning the
// rationale when it does. An empty Old or New on a registry entry is a
// wildcard for that side. registry is the run's effective expected-diff set
// (the built-in expectedDiffs merged with any WithExpectedDiffs entries).
func lookupExpectedDiff(registry []ExpectedDiff, launch, field, old, new string) (string, bool) {
	for _, e := range registry {
		if e.Launch != "" && e.Launch != launch {
			continue
		}
		if e.Field != field {
			continue
		}
		if e.Old != "" && e.Old != old {
			continue
		}
		if e.New != "" && e.New != new {
			continue
		}
		return e.Rationale, true
	}
	return "", false
}
