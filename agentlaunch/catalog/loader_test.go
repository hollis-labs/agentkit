package catalog

import (
	_ "embed"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

//go:embed testdata/global.yaml
var globalRootBytes []byte

//go:embed testdata/codex-launch.yaml
var codexLaunchBytes []byte

//go:embed testdata/nanite.backend.main.yaml
var naniteBootProfileBytes []byte

//go:embed testdata/inline-catalog.yaml
var inlineCatalogBytes []byte

//go:embed testdata/malformed.yaml
var malformedBytes []byte

func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

func TestLoadGlobalFromBytes_Inline(t *testing.T) {
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	if got, want := g.Version, "0.1.0"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if got, want := len(g.Projects), 1; got != want {
		t.Errorf("len(Projects) = %d, want %d", got, want)
	}
	if got, want := len(g.Agents), 1; got != want {
		t.Errorf("len(Agents) = %d, want %d", got, want)
	}
	if got, want := len(g.Providers), 1; got != want {
		t.Errorf("len(Providers) = %d, want %d", got, want)
	}
	if got, want := len(g.Launches), 1; got != want {
		t.Errorf("len(Launches) = %d, want %d", got, want)
	}
	if got, want := g.Projects[0].ID, "demo"; got != want {
		t.Errorf("Projects[0].ID = %q, want %q", got, want)
	}
	if got, want := g.Launches[0].Provider, "codex-cli"; got != want {
		t.Errorf("Launches[0].Provider = %q, want %q", got, want)
	}
	if got, want := g.Defaults.Labels["portfolio"], "hollis-labs"; got != want {
		t.Errorf("Defaults.Labels[portfolio] = %q, want %q", got, want)
	}
}

func TestLoadGlobalFromBytes_TetherGlobalShape(t *testing.T) {
	// The Tether-style global.yaml has no inline projects/agents/etc.
	// — just version + catalog.roots + defaults. Parsing should
	// succeed with empty inline slices.
	g, err := LoadGlobalFromBytes(globalRootBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	if got, want := g.Version, "0.1.0"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if got, want := g.Catalog.Roots.Projects, "projects"; got != want {
		t.Errorf("Catalog.Roots.Projects = %q, want %q", got, want)
	}
	if len(g.Projects) != 0 {
		t.Errorf("expected no inline projects in Tether-shape global.yaml, got %d", len(g.Projects))
	}
}

func TestLoadGlobal_TetherDirectoryTree(t *testing.T) {
	dir := filepath.Join(testdataDir(t), "tree")
	g, err := LoadGlobal(dir)
	if err != nil {
		t.Fatalf("LoadGlobal(dir): %v", err)
	}
	// The directory-walk loader must have populated every list from
	// the sibling subdirs.
	if got, want := len(g.Projects), 1; got != want {
		t.Errorf("len(Projects) = %d, want %d", got, want)
	}
	if got, want := len(g.Agents), 1; got != want {
		t.Errorf("len(Agents) = %d, want %d", got, want)
	}
	if got, want := len(g.Providers), 1; got != want {
		t.Errorf("len(Providers) = %d, want %d", got, want)
	}
	if got, want := len(g.Launches), 1; got != want {
		t.Errorf("len(Launches) = %d, want %d", got, want)
	}
	if g.Projects[0].ID != "demo" {
		t.Errorf("Projects[0].ID = %q, want %q", g.Projects[0].ID, "demo")
	}
}

func TestLoadGlobal_TetherGlobalYamlFile(t *testing.T) {
	// Loading the global.yaml file path directly (when the parent dir
	// contains the sibling subdirectories) should still walk the tree.
	path := filepath.Join(testdataDir(t), "tree", "global.yaml")
	g, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal(file): %v", err)
	}
	if len(g.Launches) != 1 {
		t.Fatalf("len(Launches) = %d, want 1 (parent-dir walk should have populated it)", len(g.Launches))
	}
}

func TestLoadGlobalFromBytes_Malformed(t *testing.T) {
	_, err := LoadGlobalFromBytes(malformedBytes)
	if err == nil {
		t.Fatal("expected parse error for malformed YAML, got nil")
	}
}

func TestLoadLaunch_FromBytes(t *testing.T) {
	lp, err := LoadLaunchFromBytes(codexLaunchBytes)
	if err != nil {
		t.Fatalf("LoadLaunchFromBytes: %v", err)
	}
	if got, want := lp.ID, "codex-launch"; got != want {
		t.Errorf("ID = %q, want %q", got, want)
	}
	if got, want := lp.Project, "demo"; got != want {
		t.Errorf("Project = %q, want %q", got, want)
	}
	if got, want := lp.Provider, "codex-cli"; got != want {
		t.Errorf("Provider = %q, want %q", got, want)
	}
	if got, want := lp.Workspace.Mode, "hybrid"; got != want {
		t.Errorf("Workspace.Mode = %q, want %q", got, want)
	}
	if !lp.Prompt.IncludeProjectBoot {
		t.Error("Prompt.IncludeProjectBoot = false, want true")
	}
	if !lp.Prompt.IncludeAgentBoot {
		t.Error("Prompt.IncludeAgentBoot = false, want true")
	}
}

func TestLoadLaunch_MissingID(t *testing.T) {
	_, err := LoadLaunchFromBytes([]byte("project: demo\n"))
	if !errors.Is(err, ErrMissingLaunchID) {
		t.Fatalf("err = %v, want ErrMissingLaunchID", err)
	}
}

func TestLoadLaunch_MalformedYAML(t *testing.T) {
	_, err := LoadLaunchFromBytes([]byte(":\n\tnope: : :"))
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestLoadLaunch_File(t *testing.T) {
	path := filepath.Join(testdataDir(t), "codex-launch.yaml")
	lp, err := LoadLaunch(path)
	if err != nil {
		t.Fatalf("LoadLaunch: %v", err)
	}
	if lp.ID != "codex-launch" {
		t.Errorf("ID = %q, want %q", lp.ID, "codex-launch")
	}
}

func TestLoadBootProfile_FromBytes(t *testing.T) {
	bp, err := LoadBootProfileFromBytes(naniteBootProfileBytes)
	if err != nil {
		t.Fatalf("LoadBootProfileFromBytes: %v", err)
	}
	if got, want := bp.ID, "nanite.backend.main"; got != want {
		t.Errorf("ID = %q, want %q", got, want)
	}
	if !strings.Contains(bp.DisplayName, "Nanite") {
		t.Errorf("DisplayName = %q, want it to contain %q", bp.DisplayName, "Nanite")
	}
	if got, want := bp.Identity.ProfileID, "nanite-backend"; got != want {
		t.Errorf("Identity.ProfileID = %q, want %q", got, want)
	}
	if got, want := bp.Identity.Role, "backend"; got != want {
		t.Errorf("Identity.Role = %q, want %q", got, want)
	}
	// Slots must round-trip including the "recap" cmd slot.
	recap, ok := bp.Slots["recap"]
	if !ok {
		t.Fatalf("slots missing recap key")
	}
	if recap.Type != "cmd" {
		t.Errorf("slots.recap.type = %q, want cmd", recap.Type)
	}
	if recap.Timeout != "5s" {
		t.Errorf("slots.recap.timeout = %q, want 5s", recap.Timeout)
	}
}

func TestLoadBootProfile_MissingID(t *testing.T) {
	_, err := LoadBootProfileFromBytes([]byte("display_name: foo\n"))
	if !errors.Is(err, ErrMissingBootProfileID) {
		t.Fatalf("err = %v, want ErrMissingBootProfileID", err)
	}
}
