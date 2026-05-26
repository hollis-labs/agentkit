package agentlaunch

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAbs covers the helper that filepath.Abs-resolves a single
// path: empty input passes through, relative input becomes absolute,
// already-absolute input is preserved.
func TestResolveAbs(t *testing.T) {
	t.Run("empty pass-through", func(t *testing.T) {
		got, err := resolveAbs("test", "")
		if err != nil {
			t.Fatalf("resolveAbs empty = %v", err)
		}
		if got != "" {
			t.Fatalf("resolveAbs empty = %q, want \"\"", got)
		}
	})

	t.Run("relative becomes absolute", func(t *testing.T) {
		got, err := resolveAbs("test", "relative/path")
		if err != nil {
			t.Fatalf("resolveAbs relative = %v", err)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("resolveAbs relative = %q, want absolute", got)
		}
		if !strings.HasSuffix(got, "/relative/path") {
			t.Fatalf("resolveAbs relative = %q, want suffix /relative/path", got)
		}
	})

	t.Run("absolute is preserved", func(t *testing.T) {
		in := "/abs/already/there"
		got, err := resolveAbs("test", in)
		if err != nil {
			t.Fatalf("resolveAbs abs = %v", err)
		}
		if got != in {
			t.Fatalf("resolveAbs abs = %q, want %q", got, in)
		}
	})
}

// TestResolvePlanPaths walks every field that should be absolutized and
// confirms empty fields stay empty while non-empty fields become abs.
func TestResolvePlanPaths(t *testing.T) {
	t.Run("empty fields stay empty", func(t *testing.T) {
		plan := LaunchPlan{
			Project: ProjectSpec{ID: "p"},
			Agent:   AgentSpec{ID: "a"},
			Provider: ProviderSpec{
				ID: "claude",
			},
			Runtime: RuntimePTY,
			Workspace: WorkspaceSpec{
				Mode: WorkspaceShared,
			},
			BootProfile: BootProfileRef{
				Inline: &BootProfileInline{BootMode: BootModeNone},
			},
			Mode: LaunchInteractive,
		}
		got, err := ResolvePlanPaths(plan)
		if err != nil {
			t.Fatalf("ResolvePlanPaths = %v", err)
		}
		if got.Project.Root != "" {
			t.Fatalf("Project.Root = %q, want empty", got.Project.Root)
		}
		if got.Workspace.Workdir != "" {
			t.Fatalf("Workspace.Workdir = %q, want empty", got.Workspace.Workdir)
		}
		if got.Workspace.WorkspaceDir != "" {
			t.Fatalf("Workspace.WorkspaceDir = %q, want empty", got.Workspace.WorkspaceDir)
		}
		if got.BootProfile.CatalogPath != "" {
			t.Fatalf("BootProfile.CatalogPath = %q, want empty", got.BootProfile.CatalogPath)
		}
	})

	t.Run("relative fields become absolute", func(t *testing.T) {
		plan := LaunchPlan{
			Project: ProjectSpec{ID: "p", Root: "rel/project"},
			Agent:   AgentSpec{ID: "a"},
			Provider: ProviderSpec{
				ID: "claude",
			},
			Runtime: RuntimePTY,
			Workspace: WorkspaceSpec{
				Mode:         WorkspaceFresh,
				Workdir:      "rel/workdir",
				WorkspaceDir: "rel/ws",
			},
			BootProfile: BootProfileRef{
				CatalogPath: "rel/catalog.yaml",
				Name:        "default",
			},
			Mode: LaunchInteractive,
		}
		got, err := ResolvePlanPaths(plan)
		if err != nil {
			t.Fatalf("ResolvePlanPaths = %v", err)
		}
		fields := []struct {
			name string
			got  string
		}{
			{"Project.Root", got.Project.Root},
			{"Workspace.Workdir", got.Workspace.Workdir},
			{"Workspace.WorkspaceDir", got.Workspace.WorkspaceDir},
			{"BootProfile.CatalogPath", got.BootProfile.CatalogPath},
		}
		for _, f := range fields {
			if !filepath.IsAbs(f.got) {
				t.Errorf("%s = %q, want absolute", f.name, f.got)
			}
		}
	})

	t.Run("input not mutated", func(t *testing.T) {
		plan := LaunchPlan{
			Project: ProjectSpec{ID: "p", Root: "rel/project"},
		}
		_, err := ResolvePlanPaths(plan)
		if err != nil {
			t.Fatalf("ResolvePlanPaths = %v", err)
		}
		if plan.Project.Root != "rel/project" {
			t.Fatalf("input Project.Root mutated to %q", plan.Project.Root)
		}
	})
}
