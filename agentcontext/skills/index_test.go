package skills

import (
	"errors"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func makeIndex() *Index {
	skills := []Skill{
		{Name: "alpha", Description: "A", Triggers: []string{"/alpha", "/a"}},
		{Name: "bravo", Description: "B", Triggers: []string{"/bravo"}},
		{Name: "capture", Description: "Cap", Triggers: []string{"/capture", "/cap"}},
	}
	idx := &Index{
		Skills:     skills,
		ByName:     map[string]Skill{},
		Overridden: map[string][]string{},
	}
	for _, s := range skills {
		idx.ByName[s.Name] = s
	}
	return idx
}

func TestIndex_Get_Hit(t *testing.T) {
	t.Parallel()
	idx := makeIndex()
	got, err := idx.Get("alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "A" {
		t.Fatalf("Description = %q", got.Description)
	}
}

func TestIndex_Get_Miss(t *testing.T) {
	t.Parallel()
	idx := makeIndex()
	_, err := idx.Get("nope")
	if !errors.Is(err, agentcontext.ErrSkillNotFound) {
		t.Fatalf("err = %v, want ErrSkillNotFound", err)
	}
}

func TestIndex_Get_NilReceiver(t *testing.T) {
	t.Parallel()
	var idx *Index
	_, err := idx.Get("anything")
	if !errors.Is(err, agentcontext.ErrSkillNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestIndex_List_NoFilter(t *testing.T) {
	t.Parallel()
	idx := makeIndex()
	got := idx.List(Filter{})
	if len(got) != 3 {
		t.Fatalf("len(List()) = %d", len(got))
	}
	// Returned slice is independent of internal storage.
	got[0].Description = "MUTATED"
	if idx.Skills[0].Description == "MUTATED" {
		t.Fatalf("List() returned internal slice — must be a fresh copy")
	}
}

func TestIndex_List_NamePrefix(t *testing.T) {
	t.Parallel()
	idx := makeIndex()
	got := idx.List(Filter{NamePrefix: "b"})
	if len(got) != 1 || got[0].Name != "bravo" {
		t.Fatalf("got = %v", got)
	}
}

func TestIndex_List_HasAnyTrigger(t *testing.T) {
	t.Parallel()
	idx := makeIndex()
	got := idx.List(Filter{HasAnyTrigger: []string{"/cap"}})
	if len(got) != 1 || got[0].Name != "capture" {
		t.Fatalf("got = %v", got)
	}
}

func TestIndex_Lookup_Hit(t *testing.T) {
	t.Parallel()
	idx := makeIndex()
	got := idx.Lookup("/a", "/bravo")
	if len(got) != 2 {
		t.Fatalf("got = %v", got)
	}
	// Ordering: Name-sorted (alpha before bravo).
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Fatalf("ordering = %v", got)
	}
}

func TestIndex_Lookup_Empty(t *testing.T) {
	t.Parallel()
	idx := makeIndex()
	if got := idx.Lookup(); len(got) != 0 {
		t.Fatalf("Lookup() with no triggers should be empty, got %v", got)
	}
	if got := idx.Lookup(""); len(got) != 0 {
		t.Fatalf("Lookup(\"\") should be empty, got %v", got)
	}
}

func TestIndex_LayerAccessors(t *testing.T) {
	t.Parallel()
	idx := &Index{
		Layers: []Layer{
			{Name: "low", Root: "/a"},
			{Name: "high", Root: "/b"},
		},
	}
	names := idx.SortLayerNames()
	if len(names) != 2 || names[0] != "low" || names[1] != "high" {
		t.Fatalf("names = %v", names)
	}
	roots := idx.LayerRoots()
	if len(roots) != 2 || roots[0] != "/a" || roots[1] != "/b" {
		t.Fatalf("roots = %v", roots)
	}
}

func TestSortedTriggers(t *testing.T) {
	t.Parallel()
	s := Skill{Triggers: []string{"/z", "", "  ", "/a", "/m"}}
	got := sortedTriggers(s)
	if len(got) != 3 || got[0] != "/a" || got[1] != "/m" || got[2] != "/z" {
		t.Fatalf("got = %v", got)
	}
}
