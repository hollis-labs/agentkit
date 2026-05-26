package resolvers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func TestRoleSummaryResolver_FullBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := "# Role: Backend\n\nMission: ship reliably.\n"
	path := writeTempFile(t, dir, "backend.md", body)

	r := NewRoleSummaryResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "role",
			Source: agentcontext.SlotSource{
				Kind:        agentcontext.SlotSourceKindRoleSummary,
				RoleSummary: agentcontext.RoleSummarySource{Path: path},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != body {
		t.Fatalf("Content: got %q", got.Content)
	}
	if got.Truncated {
		t.Fatalf("Truncated should be false")
	}
	if got.Provenance.Extra["path"] != path {
		t.Fatalf("Extra[path] = %q", got.Provenance.Extra["path"])
	}
}

func TestRoleSummaryResolver_SectionExtraction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := strings.Join([]string{
		"# Role: Backend",
		"",
		"intro paragraph",
		"",
		"## Mission",
		"",
		"ship reliably.",
		"",
		"## Stack",
		"",
		"Go.",
		"",
	}, "\n")
	path := writeTempFile(t, dir, "backend.md", body)

	r := NewRoleSummaryResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "role",
			Source: agentcontext.SlotSource{
				Kind:        agentcontext.SlotSourceKindRoleSummary,
				RoleSummary: agentcontext.RoleSummarySource{Path: path, Section: "Mission"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got.Content, "## Mission") {
		t.Fatalf("Content missing section heading: %q", got.Content)
	}
	if !strings.Contains(got.Content, "ship reliably.") {
		t.Fatalf("Content missing body: %q", got.Content)
	}
	if strings.Contains(got.Content, "## Stack") {
		t.Fatalf("Content leaked into next section: %q", got.Content)
	}
}

func TestRoleSummaryResolver_SectionMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := "# Role: Backend\n\nintro\n"
	path := writeTempFile(t, dir, "backend.md", body)
	r := NewRoleSummaryResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "role",
			Source: agentcontext.SlotSource{
				Kind:        agentcontext.SlotSourceKindRoleSummary,
				RoleSummary: agentcontext.RoleSummarySource{Path: path, Section: "NoSuchSection"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != body {
		t.Fatalf("expected fallback to full body, got %q", got.Content)
	}
}

func TestRoleSummaryResolver_TruncatesAtMaxBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := strings.Repeat("X", 100)
	path := writeTempFile(t, dir, "huge.md", body)

	r := NewRoleSummaryResolver(WithRoleSummaryMaxBytes(20))
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "huge",
			Source: agentcontext.SlotSource{
				Kind:        agentcontext.SlotSourceKindRoleSummary,
				RoleSummary: agentcontext.RoleSummarySource{Path: path},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("expected Truncated=true")
	}
	if len(got.Content) != 20 {
		t.Fatalf("len(Content) = %d, want 20", len(got.Content))
	}
}

func TestRoleSummaryResolver_PathIsDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := NewRoleSummaryResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "dir",
			Source: agentcontext.SlotSource{
				Kind:        agentcontext.SlotSourceKindRoleSummary,
				RoleSummary: agentcontext.RoleSummarySource{Path: dir},
			},
		},
		agentcontext.ResolverEnv{})
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestRoleSummaryResolver_Missing(t *testing.T) {
	t.Parallel()
	r := NewRoleSummaryResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "missing",
			Source: agentcontext.SlotSource{
				Kind:        agentcontext.SlotSourceKindRoleSummary,
				RoleSummary: agentcontext.RoleSummarySource{Path: "/no/such/role.md"},
			},
		},
		agentcontext.ResolverEnv{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRoleSummaryResolver_WrongKind(t *testing.T) {
	t.Parallel()
	r := NewRoleSummaryResolver()
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

func TestRoleSummaryResolver_Determinism(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "r.md", "# r\nbody\n")
	r := NewRoleSummaryResolver()
	spec := agentcontext.SlotSpec{
		Name: "d",
		Source: agentcontext.SlotSource{
			Kind:        agentcontext.SlotSourceKindRoleSummary,
			RoleSummary: agentcontext.RoleSummarySource{Path: path},
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
		t.Fatalf("determinism broken")
	}
}
