package skills

import (
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// Index is the deterministic catalog of skills produced by Discover.
// Iteration order on Skills is sorted by Name. Map field iteration
// (ByName, Overridden) is non-deterministic; callers that need
// stable iteration sort the keys.
type Index struct {
	// Skills is the deterministic, Name-sorted list of skills that
	// survived discovery. One entry per Name; later layers overrode
	// earlier ones (the overridden source path is recorded in
	// Overridden).
	Skills []Skill

	// ByName is the canonical Name → Skill lookup. Get returns
	// ErrSkillNotFound when the key is absent.
	ByName map[string]Skill

	// Overridden tracks the override chain per skill Name. Map value
	// is the sorted list of absolute source paths that lost the
	// override war (i.e. were defined in earlier layers and then
	// replaced). Empty for skills that appeared in exactly one
	// layer.
	Overridden map[string][]string

	// ParseErrors captures per-file parse failures during Discover.
	// Sorted by (Layer, Path) for determinism. Empty when every
	// matched file parsed cleanly.
	ParseErrors []ParseError

	// Layers is the verbatim copy of the DiscoveryConfig.Layers
	// slice for provenance. The skill_index resolver renders these
	// into Provenance.Extra["layers"] so operators can attribute a
	// rendered skill table back to the exact discovery topology.
	Layers []Layer
}

// Filter narrows the result set of List and Lookup. Zero-valued
// fields mean "no filter on this axis".
type Filter struct {
	// NamePrefix matches skills whose Name starts with the given
	// kebab-case prefix.
	NamePrefix string

	// HasAnyTrigger matches skills whose Triggers slice contains at
	// least one of the listed strings (case-sensitive, no
	// normalisation — supply both "/foo" and "foo" if you want both
	// the slash-prefix and bare forms to match).
	HasAnyTrigger []string
}

// Get returns the named skill. ErrSkillNotFound is returned when the
// key is absent — the resolver hard-errors on this so callers can
// safely propagate without wrapping.
func (idx *Index) Get(name string) (Skill, error) {
	if idx == nil {
		return Skill{}, agentcontext.ErrSkillNotFound
	}
	s, ok := idx.ByName[name]
	if !ok {
		return Skill{}, agentcontext.ErrSkillNotFound
	}
	return s, nil
}

// List returns the deterministic, Name-sorted list of skills
// matching filter. A zero-valued Filter returns the full Skills
// slice (a fresh copy so callers can mutate freely without
// disturbing the Index).
func (idx *Index) List(filter Filter) []Skill {
	if idx == nil {
		return nil
	}
	out := make([]Skill, 0, len(idx.Skills))
	for _, s := range idx.Skills {
		if !matchesFilter(s, filter) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Lookup returns the deterministic, Name-sorted list of skills
// whose Triggers slice contains any of the supplied keywords. The
// match is case-sensitive — callers that want case-insensitive
// matching normalise the input themselves.
//
// Empty triggers slice returns the empty result (NOT all skills).
func (idx *Index) Lookup(triggers ...string) []Skill {
	if idx == nil || len(triggers) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(triggers))
	for _, t := range triggers {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		set[t] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]Skill, 0, len(idx.Skills))
	for _, s := range idx.Skills {
		for _, t := range s.Triggers {
			if _, ok := set[t]; ok {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// matchesFilter reports whether s passes filter. Empty filter
// returns true (everything passes).
func matchesFilter(s Skill, f Filter) bool {
	if f.NamePrefix != "" && !strings.HasPrefix(s.Name, f.NamePrefix) {
		return false
	}
	if len(f.HasAnyTrigger) > 0 {
		want := make(map[string]struct{}, len(f.HasAnyTrigger))
		for _, t := range f.HasAnyTrigger {
			want[t] = struct{}{}
		}
		hit := false
		for _, t := range s.Triggers {
			if _, ok := want[t]; ok {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// SortLayerNames returns the layer names in input order — handy for
// callers that want a deterministic comma-joined provenance string
// without depending on the Index private layout.
func (idx *Index) SortLayerNames() []string {
	if idx == nil {
		return nil
	}
	out := make([]string, len(idx.Layers))
	for i, l := range idx.Layers {
		out[i] = l.Name
	}
	return out
}

// LayerRoots returns the layer roots in input order. Same rationale
// as SortLayerNames.
func (idx *Index) LayerRoots() []string {
	if idx == nil {
		return nil
	}
	out := make([]string, len(idx.Layers))
	for i, l := range idx.Layers {
		out[i] = l.Root
	}
	return out
}

// sortedTriggers returns the trimmed Triggers list with no empties,
// sorted lexicographically. Used by the skill_index resolver when
// emitting a deterministic per-skill trigger string. Public so
// callers that build their own renderers can match the same output.
func sortedTriggers(s Skill) []string {
	if len(s.Triggers) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Triggers))
	for _, t := range s.Triggers {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
