package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// writeSkill writes a minimal skill file with the given slug,
// description, and triggers (one per element). Returns the absolute
// path.
func writeSkill(t *testing.T, dir, fname, slug, desc string, triggers []string) string {
	t.Helper()
	full := filepath.Join(dir, fname)
	body := "---\nslug: " + slug + "\ndescription: " + desc + "\n"
	if len(triggers) > 0 {
		body += "triggers:\n"
		for _, tr := range triggers {
			body += "  - " + tr + "\n"
		}
	}
	body += "---\nbody for " + slug + "\n"
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return full
}

func TestDiscover_SingleLayer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkill(t, dir, "a.md", "alpha", "first", []string{"/alpha"})
	writeSkill(t, dir, "b.md", "bravo", "second", []string{"/bravo"})

	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "test", Root: dir}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 2 {
		t.Fatalf("len(Skills) = %d, want 2", len(idx.Skills))
	}
	// Name-sorted.
	if idx.Skills[0].Name != "alpha" || idx.Skills[1].Name != "bravo" {
		t.Fatalf("ordering wrong: %v", []string{idx.Skills[0].Name, idx.Skills[1].Name})
	}
}

func TestDiscover_LayeredOverride_HigherWins(t *testing.T) {
	t.Parallel()
	low := t.TempDir()
	high := t.TempDir()
	lowPath := writeSkill(t, low, "cap.md", "capture", "low version", []string{"/cap"})
	writeSkill(t, high, "cap.md", "capture", "high version", []string{"/cap"})

	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{
			{Name: "low", Root: low},
			{Name: "high", Root: high},
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(idx.Skills))
	}
	got := idx.Skills[0]
	if got.Description != "high version" {
		t.Fatalf("Description = %q, want high version", got.Description)
	}
	overridden := idx.Overridden["capture"]
	if len(overridden) != 1 || overridden[0] != lowPath {
		t.Fatalf("Overridden[capture] = %v, want [%s]", overridden, lowPath)
	}
}

func TestDiscover_Determinism(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkill(t, dir, "c.md", "charlie", "c", nil)
	writeSkill(t, dir, "a.md", "alpha", "a", nil)
	writeSkill(t, dir, "b.md", "bravo", "b", nil)

	idx1, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "t", Root: dir}},
	})
	if err != nil {
		t.Fatalf("Discover 1: %v", err)
	}
	idx2, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "t", Root: dir}},
	})
	if err != nil {
		t.Fatalf("Discover 2: %v", err)
	}
	for i := range idx1.Skills {
		if idx1.Skills[i].Name != idx2.Skills[i].Name {
			t.Fatalf("non-deterministic order at %d", i)
		}
	}
}

func TestDiscover_ParseErrorsCollected_DiscoveryContinues(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Good one
	writeSkill(t, dir, "good.md", "good", "g", nil)
	// Bad: no frontmatter
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter here"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Bad: missing description
	if err := os.WriteFile(filepath.Join(dir, "incomplete.md"), []byte("---\nslug: x\n---\nbody"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "t", Root: dir}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 1 || idx.Skills[0].Name != "good" {
		t.Fatalf("expected only good skill, got %v", idx.Skills)
	}
	if len(idx.ParseErrors) != 2 {
		t.Fatalf("expected 2 ParseErrors, got %d", len(idx.ParseErrors))
	}
	// ParseErrors are sorted by (Layer, Path) — verify it's stable.
	if idx.ParseErrors[0].Path >= idx.ParseErrors[1].Path {
		t.Fatalf("ParseErrors not sorted: %v", idx.ParseErrors)
	}
}

func TestDiscover_TildeExpansion(t *testing.T) {
	t.Parallel()
	// Use HOME-relative path; verify expandRoot resolves it.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no HOME")
	}
	// Make a temp subdir of HOME so we don't pollute.
	sub, err := os.MkdirTemp(home, "skills-test-")
	if err != nil {
		t.Skip("cannot mkdir under HOME")
	}
	t.Cleanup(func() { _ = os.RemoveAll(sub) })

	writeSkill(t, sub, "x.md", "x", "desc", nil)

	// Build a "~/..." path that points to sub.
	rel, err := filepath.Rel(home, sub)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	tildePath := "~/" + rel

	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "tilde", Root: tildePath}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 1 {
		t.Fatalf("expected 1 skill via tilde, got %d", len(idx.Skills))
	}
}

func TestDiscover_MissingRoot_SilentByDefault(t *testing.T) {
	t.Parallel()
	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "nope", Root: "/no/such/path/at/all"}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(idx.Skills))
	}
}

func TestDiscover_MissingRoot_StrictError(t *testing.T) {
	t.Parallel()
	_, err := Discover(context.Background(), DiscoveryConfig{
		Layers:            []Layer{{Name: "nope", Root: "/no/such/path/at/all"}},
		StrictMissingRoot: true,
	})
	if !errors.Is(err, agentcontext.ErrSkillRootMissing) {
		t.Fatalf("err = %v, want ErrSkillRootMissing", err)
	}
}

func TestDiscover_EmptyRoot_Skipped(t *testing.T) {
	t.Parallel()
	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "", Root: ""}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(idx.Skills))
	}
}

func TestDiscover_NonRecursive_Default(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkill(t, dir, "top.md", "top", "top-desc", nil)
	// Subdir skill should NOT be discovered.
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeSkill(t, sub, "deep.md", "deep", "deep-desc", nil)

	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "t", Root: dir}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 1 || idx.Skills[0].Name != "top" {
		t.Fatalf("expected only top-level, got %v", idx.Skills)
	}
}

func TestDiscover_Recursive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkill(t, dir, "top.md", "top", "top-desc", nil)
	sub := filepath.Join(dir, "sub")
	writeSkill(t, sub, "deep.md", "deep", "deep-desc", nil)

	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers:    []Layer{{Name: "t", Root: dir}},
		Recursive: true,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d (%v)", len(idx.Skills), idx.Skills)
	}
}

func TestDiscover_ContextCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkill(t, dir, "a.md", "a", "a", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Discover(ctx, DiscoveryConfig{
		Layers: []Layer{{Name: "t", Root: dir}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDiscover_DotfilesSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkill(t, dir, "good.md", "good", "g", nil)
	// Dotfile — must be skipped.
	if err := os.WriteFile(filepath.Join(dir, ".hidden.md"), []byte("---\nslug: bad\ndescription: bad\n---\nbody"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "t", Root: dir}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 1 || idx.Skills[0].Name != "good" {
		t.Fatalf("dotfile leaked: %v", idx.Skills)
	}
}

func TestDiscover_NonMatchingPatternSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkill(t, dir, "good.md", "good", "g", nil)
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("not a skill"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	idx, err := Discover(context.Background(), DiscoveryConfig{
		Layers: []Layer{{Name: "t", Root: dir}},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(idx.Skills) != 1 {
		t.Fatalf("len(Skills) = %d", len(idx.Skills))
	}
}

func TestDefaultLayers(t *testing.T) {
	t.Parallel()
	got := DefaultLayers()
	if len(got) < 2 {
		t.Fatalf("DefaultLayers should return >=2 layers, got %d", len(got))
	}
	// Order: tether-user before nanite-user.
	if got[0].Name != "tether-user" || got[1].Name != "nanite-user" {
		t.Fatalf("layer order: %v", got)
	}
}

func TestWithTetherCatalogLayer(t *testing.T) {
	t.Parallel()
	got := WithTetherCatalogLayer("/some/catalog")
	if got.Name != "tether-catalog" || got.Root != "/some/catalog" {
		t.Fatalf("got = %+v", got)
	}
}
