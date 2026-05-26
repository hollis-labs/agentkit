package resolvers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// writeSkillFile is a thin helper that writes a skill file with
// frontmatter. Mirrors the shape used in skills/discover_test but
// kept local so the resolvers test package stays self-contained.
func writeSkillFile(t *testing.T, dir, fname, slug, desc string, triggers []string) {
	t.Helper()
	body := "---\nslug: " + slug + "\ndescription: " + desc + "\n"
	if len(triggers) > 0 {
		body += "triggers:\n"
		for _, tr := range triggers {
			body += "  - " + tr + "\n"
		}
	}
	body += "---\nbody for " + slug + "\n"
	if err := os.WriteFile(filepath.Join(dir, fname), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestSkillIndexResolver_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkillFile(t, dir, "a.md", "alpha", "Alpha skill", []string{"/alpha"})
	writeSkillFile(t, dir, "b.md", "bravo", "Bravo skill", []string{"/bravo"})

	r := NewSkillIndexResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{Roots: []string{dir}},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	lines := strings.Split(got.Content, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), got.Content)
	}
	if !strings.Contains(lines[0], "/alpha") || !strings.Contains(lines[0], "Alpha skill") {
		t.Fatalf("line[0] = %q", lines[0])
	}
	if !strings.Contains(lines[1], "/bravo") || !strings.Contains(lines[1], "Bravo skill") {
		t.Fatalf("line[1] = %q", lines[1])
	}
	if got.Provenance.Extra["skill_count"] != "2" {
		t.Fatalf("skill_count = %q", got.Provenance.Extra["skill_count"])
	}
	if got.Provenance.Extra["emitted_count"] != "2" {
		t.Fatalf("emitted_count = %q", got.Provenance.Extra["emitted_count"])
	}
}

func TestSkillIndexResolver_WrongKind(t *testing.T) {
	t.Parallel()
	r := NewSkillIndexResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name:   "x",
			Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindInline},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrResolverNotApplicable) {
		t.Fatalf("err = %v, want ErrResolverNotApplicable", err)
	}
}

func TestSkillIndexResolver_FallbackTriggerFromName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkillFile(t, dir, "x.md", "lonely", "No triggers", nil)
	r := NewSkillIndexResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{Roots: []string{dir}},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got.Content, "/lonely") {
		t.Fatalf("Content missing fallback trigger: %q", got.Content)
	}
}

func TestSkillIndexResolver_LimitTruncation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		writeSkillFile(t, dir, n+".md", n, n+"-desc", []string{"/" + n})
	}
	r := NewSkillIndexResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{
					Roots: []string{dir},
					Limit: 2,
				},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("expected Truncated=true")
	}
	lines := strings.Split(got.Content, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), got.Content)
	}
	if got.Provenance.Extra["truncated_to"] != "2" {
		t.Fatalf("truncated_to = %q", got.Provenance.Extra["truncated_to"])
	}
}

func TestSkillIndexResolver_PreferTriggers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkillFile(t, dir, "a.md", "alpha", "alpha-desc", []string{"/alpha"})
	writeSkillFile(t, dir, "b.md", "bravo", "bravo-desc", []string{"/bravo", "vanta"})
	writeSkillFile(t, dir, "c.md", "charlie", "charlie-desc", []string{"/charlie"})

	r := NewSkillIndexResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{
					Roots:          []string{dir},
					PreferTriggers: []string{"vanta"},
				},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	lines := strings.Split(got.Content, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines: %q", got.Content)
	}
	// bravo should be first because it matches PreferTrigger "vanta".
	if !strings.Contains(lines[0], "bravo") {
		t.Fatalf("expected bravo first, got %q", lines[0])
	}
	if got.Provenance.Extra["prefer_triggers"] != "vanta" {
		t.Fatalf("prefer_triggers extra = %q", got.Provenance.Extra["prefer_triggers"])
	}
}

func TestSkillIndexResolver_EmptyRoots_EmptyContent(t *testing.T) {
	t.Parallel()
	r := NewSkillIndexResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{Roots: nil},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "" {
		t.Fatalf("Content should be empty: %q", got.Content)
	}
	if got.Provenance.Extra["skill_count"] != "0" {
		t.Fatalf("skill_count = %q", got.Provenance.Extra["skill_count"])
	}
}

func TestSkillIndexResolver_MissingRoot_StrictPropagates(t *testing.T) {
	t.Parallel()
	r := NewSkillIndexResolver(WithSkillIndexStrictMissingRoot(true))
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{Roots: []string{"/no/such/dir"}},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrSkillRootMissing) {
		t.Fatalf("err = %v, want ErrSkillRootMissing", err)
	}
}

func TestSkillIndexResolver_MissingRoot_SilentByDefault(t *testing.T) {
	t.Parallel()
	r := NewSkillIndexResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{Roots: []string{"/no/such/dir"}},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "" {
		t.Fatalf("Content: %q", got.Content)
	}
}

func TestSkillIndexResolver_Determinism(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkillFile(t, dir, "c.md", "charlie", "c", []string{"/c"})
	writeSkillFile(t, dir, "a.md", "alpha", "a", []string{"/a"})
	writeSkillFile(t, dir, "b.md", "bravo", "b", []string{"/b"})

	r := NewSkillIndexResolver()
	spec := agentcontext.SlotSpec{
		Name: "skill_index",
		Source: agentcontext.SlotSource{
			Kind:       agentcontext.SlotSourceKindSkillIndex,
			SkillIndex: agentcontext.SkillIndexSource{Roots: []string{dir}},
		},
	}
	a, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a.Content != b.Content || a.Provenance.ContentHash != b.Provenance.ContentHash {
		t.Fatalf("determinism broken\na=%q\nb=%q", a.Content, b.Content)
	}
}

func TestSkillIndexResolver_LayeredOverride(t *testing.T) {
	t.Parallel()
	low := t.TempDir()
	high := t.TempDir()
	writeSkillFile(t, low, "x.md", "shared", "low version", []string{"/shared"})
	writeSkillFile(t, high, "x.md", "shared", "high version", []string{"/shared"})

	r := NewSkillIndexResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{
					Roots: []string{low, high},
				},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got.Content, "high version") {
		t.Fatalf("Content should contain high version: %q", got.Content)
	}
	if strings.Contains(got.Content, "low version") {
		t.Fatalf("Content leaked low version: %q", got.Content)
	}
}

func TestSkillIndexResolver_DefaultLimitOption(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		writeSkillFile(t, dir, n+".md", n, n+"-desc", nil)
	}
	r := NewSkillIndexResolver(WithSkillIndexDefaultLimit(2))
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "skill_index",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindSkillIndex,
				SkillIndex: agentcontext.SkillIndexSource{Roots: []string{dir}},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("expected Truncated=true via default limit")
	}
}

func TestWithSkillIndex_RegistersIntoMap(t *testing.T) {
	t.Parallel()
	res := WithSkillIndex(Default())
	if _, ok := res[agentcontext.SlotSourceKindSkillIndex]; !ok {
		t.Fatalf("WithSkillIndex did not register skill_index")
	}
	// Default's seven kinds still present.
	for _, k := range []agentcontext.SlotSourceKind{
		agentcontext.SlotSourceKindStaticFile,
		agentcontext.SlotSourceKindStaticDir,
		agentcontext.SlotSourceKindInline,
		agentcontext.SlotSourceKindCmd,
		agentcontext.SlotSourceKindHTTPText,
		agentcontext.SlotSourceKindHTTPJSON,
		agentcontext.SlotSourceKindRoleSummary,
	} {
		if _, ok := res[k]; !ok {
			t.Errorf("WithSkillIndex dropped Default kind %q", k)
		}
	}
	if len(res) != 8 {
		t.Fatalf("len(res) = %d, want 8", len(res))
	}
}

func TestWithSkillIndex_NilMap(t *testing.T) {
	t.Parallel()
	res := WithSkillIndex(nil, WithSkillIndexDefaultLimit(10))
	if _, ok := res[agentcontext.SlotSourceKindSkillIndex]; !ok {
		t.Fatalf("WithSkillIndex(nil) failed")
	}
	if len(res) != 1 {
		t.Fatalf("len = %d, want 1", len(res))
	}
}

func TestWithSkillIndex_PluggableIntoProvider(t *testing.T) {
	t.Parallel()
	res := WithSkillIndex(Default())
	if _, err := agentcontext.NewProvider(res, agentcontext.DefaultRenderer{}); err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
}
