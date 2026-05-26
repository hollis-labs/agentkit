package resolvers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func writeTempFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", full, err)
	}
	return full
}

func TestStaticFileResolver_AbsolutePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	full := writeTempFile(t, dir, "hello.md", "# Hello\n")
	r := NewStaticFileResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "role",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindStaticFile,
				StaticFile: agentcontext.StaticFileSource{Path: full},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "# Hello\n" {
		t.Fatalf("Content: %q", got.Content)
	}
	if got.Provenance.Source != full {
		t.Fatalf("Source: %q != %q", got.Provenance.Source, full)
	}
	if got.Provenance.ContentHash == "" {
		t.Fatalf("ContentHash empty")
	}
}

func TestStaticFileResolver_WorkdirRelative(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "rel.txt", "relative-body")
	r := NewStaticFileResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "rel",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindStaticFile,
				StaticFile: agentcontext.StaticFileSource{Path: "rel.txt"},
			},
		},
		agentcontext.ResolverEnv{Workdir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "relative-body" {
		t.Fatalf("Content: %q", got.Content)
	}
	if !strings.HasPrefix(got.Provenance.Source, dir) {
		t.Fatalf("Source not workdir-anchored: %q", got.Provenance.Source)
	}
}

func TestStaticFileResolver_TildeExpansion(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no HOME on this system")
	}
	tmp, err := os.CreateTemp(home, "agentcontext-static-file-*.txt")
	if err != nil {
		t.Skipf("cannot create temp in HOME (%s): %v", home, err)
	}
	t.Cleanup(func() { os.Remove(tmp.Name()) })
	if _, err := tmp.WriteString("tilde-body"); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	tmp.Close()

	rel := strings.TrimPrefix(tmp.Name(), home)
	if !strings.HasPrefix(rel, string(filepath.Separator)) {
		t.Skip("temp file not under HOME")
	}
	tilde := "~" + rel

	r := NewStaticFileResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "tilde",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindStaticFile,
				StaticFile: agentcontext.StaticFileSource{Path: tilde},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "tilde-body" {
		t.Fatalf("Content: %q", got.Content)
	}
}

func TestStaticFileResolver_MissingFile(t *testing.T) {
	t.Parallel()
	r := NewStaticFileResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "missing",
			Source: agentcontext.SlotSource{
				Kind:       agentcontext.SlotSourceKindStaticFile,
				StaticFile: agentcontext.StaticFileSource{Path: "/nonexistent/agentcontext/file.md"},
			},
		},
		agentcontext.ResolverEnv{})
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		// os.IsNotExist may not unwrap through fmt.Errorf %w on all
		// platforms — fall back to substring check.
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestStaticFileResolver_Determinism(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "d.txt", "deterministic")
	r := NewStaticFileResolver()
	spec := agentcontext.SlotSpec{
		Name: "d",
		Source: agentcontext.SlotSource{
			Kind:       agentcontext.SlotSourceKindStaticFile,
			StaticFile: agentcontext.StaticFileSource{Path: path},
		},
	}
	a, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve a: %v", err)
	}
	b, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve b: %v", err)
	}
	if a.Content != b.Content || a.Provenance.ContentHash != b.Provenance.ContentHash {
		t.Fatalf("determinism broken")
	}
}
