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

func TestStaticDirResolver_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "a.md", "alpha")
	writeTempFile(t, dir, "b.md", "bravo")
	writeTempFile(t, dir, "c.md", "charlie")
	// Dotfile — must be skipped.
	writeTempFile(t, dir, ".hidden.md", "ignored")
	// Subdirectory — must be skipped (non-recursive).
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writeTempFile(t, dir, "sub/d.md", "skipped")

	r := NewStaticDirResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "dir",
			Source: agentcontext.SlotSource{
				Kind:      agentcontext.SlotSourceKindStaticDir,
				StaticDir: agentcontext.StaticDirSource{Path: dir, Glob: "*.md"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "alpha\n\nbravo\n\ncharlie"
	if got.Content != want {
		t.Fatalf("Content: got %q, want %q", got.Content, want)
	}
	if got.Truncated {
		t.Fatalf("Truncated should be false")
	}
	files := got.Provenance.Extra["files"]
	if files != "a.md,b.md,c.md" {
		t.Fatalf("Extra[files] = %q", files)
	}
}

func TestStaticDirResolver_EmptyMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "thing.txt", "irrelevant")
	r := NewStaticDirResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "none",
			Source: agentcontext.SlotSource{
				Kind:      agentcontext.SlotSourceKindStaticDir,
				StaticDir: agentcontext.StaticDirSource{Path: dir, Glob: "*.md"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "" {
		t.Fatalf("expected empty Content, got %q", got.Content)
	}
	if got.Provenance.Extra["match_count"] != "0" {
		t.Fatalf("match_count = %q", got.Provenance.Extra["match_count"])
	}
}

func TestStaticDirResolver_NoGlobMatchesAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "a.md", "A")
	writeTempFile(t, dir, "b.txt", "B")
	r := NewStaticDirResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "all",
			Source: agentcontext.SlotSource{
				Kind:      agentcontext.SlotSourceKindStaticDir,
				StaticDir: agentcontext.StaticDirSource{Path: dir},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "A\n\nB" {
		t.Fatalf("Content: got %q", got.Content)
	}
}

func TestStaticDirResolver_MaxFilesTruncates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, n := range []string{"01.md", "02.md", "03.md", "04.md"} {
		writeTempFile(t, dir, n, strings.TrimSuffix(n, ".md"))
	}
	r := NewStaticDirResolver(WithMaxFiles(2))
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "cap",
			Source: agentcontext.SlotSource{
				Kind:      agentcontext.SlotSourceKindStaticDir,
				StaticDir: agentcontext.StaticDirSource{Path: dir, Glob: "*.md"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("expected Truncated=true")
	}
	if got.Content != "01\n\n02" {
		t.Fatalf("Content: got %q", got.Content)
	}
	if got.Provenance.Extra["files"] != "01.md,02.md" {
		t.Fatalf("files: %q", got.Provenance.Extra["files"])
	}
}

func TestStaticDirResolver_BadGlob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "x.md", "x")
	r := NewStaticDirResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "bad",
			Source: agentcontext.SlotSource{
				Kind:      agentcontext.SlotSourceKindStaticDir,
				StaticDir: agentcontext.StaticDirSource{Path: dir, Glob: "["},
			},
		},
		agentcontext.ResolverEnv{})
	if err == nil {
		t.Fatalf("expected error for bad glob")
	}
}

func TestStaticDirResolver_MissingDir(t *testing.T) {
	t.Parallel()
	r := NewStaticDirResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "missing",
			Source: agentcontext.SlotSource{
				Kind:      agentcontext.SlotSourceKindStaticDir,
				StaticDir: agentcontext.StaticDirSource{Path: "/definitely/does/not/exist/agentcontext"},
			},
		},
		agentcontext.ResolverEnv{})
	if err == nil {
		t.Fatalf("expected error for missing dir")
	}
}

func TestStaticDirResolver_PathIsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "thing.txt", "body")
	r := NewStaticDirResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "not-dir",
			Source: agentcontext.SlotSource{
				Kind:      agentcontext.SlotSourceKindStaticDir,
				StaticDir: agentcontext.StaticDirSource{Path: path},
			},
		},
		agentcontext.ResolverEnv{})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want not-a-directory error, got %v", err)
	}
}

func TestStaticDirResolver_WrongKind(t *testing.T) {
	t.Parallel()
	r := NewStaticDirResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name:   "x",
			Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindInline},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrResolverNotApplicable) {
		t.Fatalf("expected ErrResolverNotApplicable, got %v", err)
	}
}
