package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// validPlanForPrepare returns a plan whose Compile succeeds AND whose
// workspace mode is WorkspaceTemp so Prepare materialises a real dir.
func validPlanForPrepare(t *testing.T) agentlaunch.LaunchPlan {
	t.Helper()
	tempPrefix := t.TempDir()
	return agentlaunch.LaunchPlan{
		Project: agentlaunch.ProjectSpec{ID: "proj", Name: "Project"},
		Agent:   agentlaunch.AgentSpec{ID: "agent", Name: "Agent"},
		Provider: agentlaunch.ProviderSpec{
			ID:    "claude",
			Flags: []string{"--quiet"},
			Env: map[string]string{
				"FOO": "fromprovider",
				"BAZ": "providerbaz",
			},
		},
		Runtime: agentlaunch.RuntimePTY,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:       agentlaunch.WorkspaceTemp,
			TempPrefix: tempPrefix,
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootPrompt:  "persona body",
				BootContent: "kickoff",
				BootMode:    agentlaunch.BootModePlanted,
			},
		},
		Injection: agentlaunch.InjectionSpec{
			Env: map[string]string{
				"FOO": "fromInjection", // wins over provider
				"BAR": "injectiononly",
			},
			Args: []string{"--inj-arg"},
		},
		Mode: agentlaunch.LaunchInteractive,
	}
}

// TestPrepareHappyPathTempWorkspace materialises a temp workspace and
// confirms every PreparedLaunch field is set as expected.
func TestPrepareHappyPathTempWorkspace(t *testing.T) {
	plan := validPlanForPrepare(t)
	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}

	prepared, err := Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	if prepared == nil {
		t.Fatalf("Prepare returned nil")
	}

	// BootDir & WorkspaceDir must exist and be absolute.
	if prepared.PlantedBootDir == "" {
		t.Fatalf("PlantedBootDir empty")
	}
	if !filepath.IsAbs(prepared.PlantedBootDir) {
		t.Fatalf("PlantedBootDir = %q, want absolute", prepared.PlantedBootDir)
	}
	if _, err := os.Stat(prepared.PlantedBootDir); err != nil {
		t.Fatalf("PlantedBootDir %q does not exist: %v", prepared.PlantedBootDir, err)
	}
	if prepared.WorkspaceDir == "" {
		t.Fatalf("WorkspaceDir empty")
	}
	if !filepath.IsAbs(prepared.WorkspaceDir) {
		t.Fatalf("WorkspaceDir = %q, want absolute", prepared.WorkspaceDir)
	}
	if _, err := os.Stat(prepared.WorkspaceDir); err != nil {
		t.Fatalf("WorkspaceDir %q does not exist: %v", prepared.WorkspaceDir, err)
	}

	// Workdir defaults to project.root when set, else WorkspaceDir.
	// In this test plan project.Root is empty, so Workdir==WorkspaceDir.
	if prepared.Workdir != prepared.WorkspaceDir {
		t.Fatalf("Workdir = %q, want %q (= WorkspaceDir)", prepared.Workdir, prepared.WorkspaceDir)
	}

	// Env merging: injection wins on collision, injection-only keys
	// preserved, provider-only keys preserved.
	if prepared.Env["FOO"] != "fromInjection" {
		t.Fatalf("Env[FOO] = %q, want fromInjection", prepared.Env["FOO"])
	}
	if prepared.Env["BAR"] != "injectiononly" {
		t.Fatalf("Env[BAR] = %q, want injectiononly", prepared.Env["BAR"])
	}
	if prepared.Env["BAZ"] != "providerbaz" {
		t.Fatalf("Env[BAZ] = %q, want providerbaz", prepared.Env["BAZ"])
	}

	// Argv composition: [binary, provider.Flags..., injection.Args...]
	wantArgv := []string{"claude", "--quiet", "--inj-arg"}
	if len(prepared.Argv) != len(wantArgv) {
		t.Fatalf("Argv = %v, want %v", prepared.Argv, wantArgv)
	}
	for i, v := range wantArgv {
		if prepared.Argv[i] != v {
			t.Fatalf("Argv[%d] = %q, want %q (full=%v)", i, prepared.Argv[i], v, prepared.Argv)
		}
	}

	// BootMode, BootPrompt, BootContent flow through.
	if prepared.BootMode != agentlaunch.BootModePlanted {
		t.Fatalf("BootMode = %q, want %q", prepared.BootMode, agentlaunch.BootModePlanted)
	}
	if prepared.BootPrompt != "persona body" {
		t.Fatalf("BootPrompt = %q", prepared.BootPrompt)
	}
	if prepared.BootContent != "kickoff" {
		t.Fatalf("BootContent = %q", prepared.BootContent)
	}

	// PlantContext.AgentName comes from plan.Agent.Name.
	if prepared.PlantContext.AgentName != "Agent" {
		t.Fatalf("PlantContext.AgentName = %q, want Agent", prepared.PlantContext.AgentName)
	}

	// Provenance is preserved from compiled.
	if prepared.Compiled.Provenance.PlanHash != compiled.Provenance.PlanHash {
		t.Fatalf("PlanHash drift")
	}
}

// TestPrepareBootDirNameContainsShortHash confirms the bootdir name
// embeds the first 8 chars of the plan hash for post-mortem correlation.
func TestPrepareBootDirNameContainsShortHash(t *testing.T) {
	plan := validPlanForPrepare(t)
	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}
	prepared, err := Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	short := compiled.Provenance.PlanHash[:8]
	base := filepath.Base(prepared.PlantedBootDir)
	if !strings.Contains(base, short) {
		t.Fatalf("bootdir name %q does not contain hash prefix %q", base, short)
	}
}

// TestPrepareWorkspaceFreshCreatesDir confirms WorkspaceFresh creates a
// pre-named dir that did not previously exist.
func TestPrepareWorkspaceFreshCreatesDir(t *testing.T) {
	root := t.TempDir()
	wsdir := filepath.Join(root, "fresh-workspace")

	plan := validPlanForPrepare(t)
	plan.Workspace = agentlaunch.WorkspaceSpec{
		Mode:         agentlaunch.WorkspaceFresh,
		WorkspaceDir: wsdir,
		TempPrefix:   root,
	}

	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}

	prepared, err := Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	if prepared.WorkspaceDir != wsdir {
		t.Fatalf("WorkspaceDir = %q, want %q", prepared.WorkspaceDir, wsdir)
	}
	if _, err := os.Stat(wsdir); err != nil {
		t.Fatalf("Fresh workspace dir %q not created: %v", wsdir, err)
	}
}

// TestPrepareSharedWithoutWorkspaceDirErrors confirms WorkspaceShared
// requires WorkspaceDir to be set explicitly.
func TestPrepareSharedWithoutWorkspaceDirErrors(t *testing.T) {
	plan := validPlanForPrepare(t)
	plan.Workspace = agentlaunch.WorkspaceSpec{
		Mode: agentlaunch.WorkspaceShared,
		// WorkspaceDir intentionally empty
	}
	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}
	_, err = Prepare(context.Background(), compiled)
	if err == nil {
		t.Fatalf("Prepare with shared+no-wsdir = nil, want error")
	}
	if !strings.Contains(err.Error(), "requires WorkspaceSpec.WorkspaceDir") {
		t.Fatalf("Prepare err = %v, want message about WorkspaceDir requirement", err)
	}
}

// TestPrepareNilCompiledErrors confirms passing nil compiled returns
// ErrCompiledMissingPlan wrapped.
func TestPrepareNilCompiledErrors(t *testing.T) {
	_, err := Prepare(context.Background(), nil)
	if err == nil {
		t.Fatalf("Prepare(nil) = nil, want error")
	}
	if !errors.Is(err, agentlaunch.ErrCompiledMissingPlan) {
		t.Fatalf("Prepare(nil) err = %v, want errors.Is ErrCompiledMissingPlan", err)
	}
}
