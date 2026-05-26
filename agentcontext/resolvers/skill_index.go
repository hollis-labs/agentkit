package resolvers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/agentcontext/skills"
)

// DefaultSkillIndexLimit caps the number of skills the
// skill_index resolver emits when SlotSource.SkillIndex.Limit is
// zero. The cap is generous (256) — the operator-side budget enforced
// by the Renderer is the real backstop. Operators tighten the
// resolver-side cap via WithSkillIndexDefaultLimit.
const DefaultSkillIndexLimit = 256

// SkillIndexResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindSkillIndex. The resolver walks the
// configured discovery layers, parses every matching skill file,
// and emits a deterministic per-skill index line of the form:
//
//	<primary-trigger> — <description>
//
// where <primary-trigger> is the alphabetically-first entry in
// Skill.Triggers, or "/" + Skill.Name when Triggers is empty.
//
// # Output format (load-bearing — Subagent D consumes this)
//
// The rendered content is plain text: one skill per line, lines
// joined by "\n", with NO trailing newline and NO surrounding
// markdown fence. The line format above is stable; downstream
// consumers MAY string-split on "\n" to recover the per-skill rows.
//
// Skill bodies are NOT included — this is an index, not a content
// dump. Apps that need the body invoke skills.Discover themselves
// and read Skill.Body.
//
// # Layer config
//
// SlotSource.SkillIndex.Roots feeds Discover via a Layer list
// constructed in this resolver. Layer names default to
// "root_<i>" (zero-indexed) — operators who want named layers
// pre-build the DiscoveryConfig themselves and wire the resolver
// through a NewSkillIndexResolverWithLayers option (out of scope
// for this commit; reserved for Subagent D).
//
// # PreferTriggers
//
// SkillIndexSource.PreferTriggers boosts skills whose Triggers list
// contains any string in PreferTriggers, preserving the
// PreferTriggers order as the tie-breaker. Remaining skills sort by
// Name. The deterministic ordering rule:
//
//  1. For each skill, compute its rank = the lowest index of a
//     PreferTriggers entry that matches one of its triggers, or
//     math.MaxInt when no match.
//  2. Sort by (rank, Name) — lower rank first; same-rank ties
//     break alphabetically by Name.
//
// # Limit / truncation
//
// SlotSource.SkillIndex.Limit caps the number of skills emitted. A
// Limit of zero defers to the resolver's defaultLimit
// (configurable via WithSkillIndexDefaultLimit). When the discovered
// count exceeds the cap, SlotResult.Truncated is set true and
// Provenance.Extra["truncated_to"] records the cap.
//
// # Required-and-empty
//
// When the dispatcher routes a Required=true slot with zero
// discovered skills, this resolver returns
// (SlotResult{Content:""}, nil). The default Provider.Assemble
// promotes the empty result to ErrSlotRequiredAndEmpty. The
// resolver itself does not double-wrap that case — it would
// short-circuit the unified handling DefaultProvider already does.
//
// When DiscoveryConfig.StrictMissingRoot is true (set via
// WithSkillIndexStrictMissingRoot) AND any configured Root is
// absent, Discover returns ErrSkillRootMissing which this resolver
// propagates verbatim.
type SkillIndexResolver struct {
	defaultLimit      int
	strictMissingRoot bool
	recursive         bool
	filePattern       string
}

// SkillIndexOption configures a SkillIndexResolver.
type SkillIndexOption func(*SkillIndexResolver)

// WithSkillIndexDefaultLimit overrides the resolver's defaultLimit
// — used when SkillIndexSource.Limit is zero. Non-positive disables
// the default cap (NOT recommended in production).
func WithSkillIndexDefaultLimit(n int) SkillIndexOption {
	return func(r *SkillIndexResolver) {
		r.defaultLimit = n
	}
}

// WithSkillIndexStrictMissingRoot makes the resolver treat a missing
// discovery root as ErrSkillRootMissing instead of silently skipping
// it. Default is silent skip — apps that want fail-loud opt in.
func WithSkillIndexStrictMissingRoot(strict bool) SkillIndexOption {
	return func(r *SkillIndexResolver) {
		r.strictMissingRoot = strict
	}
}

// WithSkillIndexRecursive controls whether the resolver walks
// subdirectories of each Root. Default false (non-recursive) matches
// the existing Nanite / Tether behaviour where skills live directly
// under the root.
func WithSkillIndexRecursive(recursive bool) SkillIndexOption {
	return func(r *SkillIndexResolver) {
		r.recursive = recursive
	}
}

// WithSkillIndexFilePattern overrides the file glob used during
// discovery. Empty string reverts to the package default ("*.md").
func WithSkillIndexFilePattern(pattern string) SkillIndexOption {
	return func(r *SkillIndexResolver) {
		r.filePattern = pattern
	}
}

// NewSkillIndexResolver returns a SkillIndexResolver configured with
// the supplied options. Default: defaultLimit =
// DefaultSkillIndexLimit, strictMissingRoot = false, recursive =
// false, filePattern = "*.md".
func NewSkillIndexResolver(opts ...SkillIndexOption) agentcontext.Resolver {
	r := &SkillIndexResolver{
		defaultLimit: DefaultSkillIndexLimit,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve implements agentcontext.Resolver.
func (r *SkillIndexResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindSkillIndex {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindSkillIndex)
	}

	src := spec.Source.SkillIndex

	cfg := skills.DiscoveryConfig{
		Layers:            make([]skills.Layer, 0, len(src.Roots)),
		FilePattern:       r.filePattern,
		Recursive:         r.recursive,
		StrictMissingRoot: r.strictMissingRoot,
	}
	for i, root := range src.Roots {
		cfg.Layers = append(cfg.Layers, skills.Layer{
			Name: fmt.Sprintf("root_%d", i),
			Root: root,
		})
	}

	idx, err := skills.Discover(ctx, cfg)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("skill_index: discover: %w", err)
	}

	all := idx.Skills

	// Apply PreferTriggers ordering boost.
	ordered := applyPreferTriggers(all, src.PreferTriggers)

	// Apply Limit (or resolver default).
	limit := src.Limit
	if limit <= 0 {
		limit = r.defaultLimit
	}
	truncated := false
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
		truncated = true
	}

	content := renderSkillLines(ordered)

	extra := map[string]string{
		"skill_count":       fmt.Sprintf("%d", len(all)),
		"emitted_count":     fmt.Sprintf("%d", len(ordered)),
		"layer_count":       fmt.Sprintf("%d", len(cfg.Layers)),
		"parse_error_count": fmt.Sprintf("%d", len(idx.ParseErrors)),
		"layers":            strings.Join(layerNames(cfg.Layers), ","),
	}
	if truncated {
		extra["truncated_to"] = fmt.Sprintf("%d", limit)
	}
	if len(src.PreferTriggers) > 0 {
		extra["prefer_triggers"] = strings.Join(src.PreferTriggers, ",")
	}

	return agentcontext.SlotResult{
		Content:   content,
		Truncated: truncated,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindSkillIndex,
			Source:      strings.Join(layerRoots(cfg.Layers), ","),
			Bytes:       len(content),
			ContentHash: hashContent(content),
			FetchedAt:   nowUTC(),
			Extra:       extra,
		},
	}, nil
}

// applyPreferTriggers returns skills ordered by (preferRank, Name).
// preferRank is the lowest index in prefers of a string that appears
// in the skill's Triggers list, or len(prefers) (max sentinel) when
// no trigger matches.
//
// The returned slice is a new allocation — the input is not mutated.
// Tie-breaking on equal rank is alphabetical by Skill.Name (the input
// is already Name-sorted by skills.Discover, but we resort defensively
// in case a caller passes a hand-built slice).
func applyPreferTriggers(in []skills.Skill, prefers []string) []skills.Skill {
	out := append([]skills.Skill(nil), in...)
	if len(prefers) == 0 {
		// Already Name-sorted from Discover, but defensively resort.
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Name < out[j].Name
		})
		return out
	}
	rank := func(s skills.Skill) int {
		best := len(prefers)
		for _, t := range s.Triggers {
			for i, p := range prefers {
				if t == p && i < best {
					best = i
				}
			}
		}
		return best
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// renderSkillLines emits one "<trigger> — <description>" line per
// skill, joined by "\n", with no trailing newline. <trigger> is the
// alphabetically-first non-empty Triggers entry, or "/" + Name when
// Triggers is empty. Description leading/trailing whitespace is
// trimmed.
//
// Em-dash is used between trigger and description to match the
// rendering style operators see in the standing nanite.backend.main
// boot prompt.
func renderSkillLines(in []skills.Skill) string {
	if len(in) == 0 {
		return ""
	}
	lines := make([]string, 0, len(in))
	for _, s := range in {
		lines = append(lines, renderOneSkill(s))
	}
	return strings.Join(lines, "\n")
}

func renderOneSkill(s skills.Skill) string {
	trigger := primaryTrigger(s)
	desc := strings.TrimSpace(s.Description)
	return fmt.Sprintf("%s — %s", trigger, desc)
}

// primaryTrigger returns the trigger shown in the rendered index
// line. Priority:
//
//  1. The alphabetically-first non-empty entry of Skill.Triggers.
//  2. Fallback: "/" + Skill.Name.
//
// The fallback gives every skill at least one routable token in the
// rendered output, matching how chat-side routers parse the boot
// prompt today.
func primaryTrigger(s skills.Skill) string {
	if len(s.Triggers) > 0 {
		sorted := append([]string(nil), s.Triggers...)
		for i, t := range sorted {
			sorted[i] = strings.TrimSpace(t)
		}
		// drop empties
		out := sorted[:0]
		for _, t := range sorted {
			if t != "" {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			sort.Strings(out)
			return out[0]
		}
	}
	return "/" + s.Name
}

func layerNames(in []skills.Layer) []string {
	out := make([]string, len(in))
	for i, l := range in {
		out[i] = l.Name
	}
	return out
}

func layerRoots(in []skills.Layer) []string {
	out := make([]string, len(in))
	for i, l := range in {
		out[i] = l.Root
	}
	return out
}
