package sessionshim

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

// validCompiled compiles a minimal launch so PreparedLaunch.Validate
// (which re-runs CompiledLaunch.Validate) is satisfied.
func validCompiled(t *testing.T) *agentlaunch.CompiledLaunch {
	t.Helper()
	plan := agentlaunch.LaunchPlan{
		Project:   agentlaunch.ProjectSpec{ID: "proj", Root: t.TempDir()},
		Agent:     agentlaunch.AgentSpec{ID: "agent", Name: "agent"},
		Provider:  agentlaunch.ProviderSpec{ID: "claude"},
		Runtime:   agentlaunch.RuntimePTY,
		Workspace: agentlaunch.WorkspaceSpec{Mode: agentlaunch.WorkspaceTemp, TempPrefix: t.TempDir()},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{BootMode: agentlaunch.BootModePlanted},
		},
		Mode: agentlaunch.LaunchInteractive,
	}
	compiled, err := launcher.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return compiled
}

func TestToSessionLaunch(t *testing.T) {
	prepared := &agentlaunch.PreparedLaunch{
		Compiled:       validCompiled(t),
		PlantedBootDir: t.TempDir(),
		WorkspaceDir:   t.TempDir(),
		Workdir:        "/proj",
		Env:            map[string]string{"BETA": "2", "ALPHA": "1"},
		Argv:           []string{"claude", "--add-dir", "/proj"},
		BootMode:       agentlaunch.BootModePlanted,
		BootPrompt:     "SYS",
		BootContent:    "BODY",
		PlantContext:   agentlaunch.PreparedPlantContext{AgentName: "Rev"},
	}

	sl, err := ToSessionLaunch(prepared)
	if err != nil {
		t.Fatalf("ToSessionLaunch: %v", err)
	}

	if sl.Binary != "claude" {
		t.Errorf("Binary = %q, want claude (Argv[0])", sl.Binary)
	}
	if want := []string{"--add-dir", "/proj"}; !slices.Equal(sl.Options.ExtraArgs, want) {
		t.Errorf("ExtraArgs = %v, want %v", sl.Options.ExtraArgs, want)
	}
	if want := []string{"ALPHA=1", "BETA=2"}; !slices.Equal(sl.Options.Env, want) {
		t.Errorf("Env = %v, want sorted %v", sl.Options.Env, want)
	}
	if sl.Options.Workdir != "/proj" {
		t.Errorf("Workdir = %q", sl.Options.Workdir)
	}
	if sl.Options.BootPrompt != "SYS" || sl.Options.BootContent != "BODY" {
		t.Errorf("boot fields = %q/%q, want SYS/BODY", sl.Options.BootPrompt, sl.Options.BootContent)
	}
	if sl.Options.BootMode != agentlaunch.BootModePlanted {
		t.Errorf("BootMode = %q", sl.Options.BootMode)
	}
	if sl.Options.PlantContext.SystemPrompt != "SYS" || sl.Options.PlantContext.AgentName != "Rev" {
		t.Errorf("PlantContext not translated: %+v", sl.Options.PlantContext)
	}
	// Bootdir is already planted by providerplant — the runtime planter
	// must not run again.
	if sl.Options.AutoPlantBootDir {
		t.Error("AutoPlantBootDir = true, want false (bootdir already planted)")
	}
}

func TestToSessionLaunch_Nil(t *testing.T) {
	if _, err := ToSessionLaunch(nil); !errors.Is(err, ErrNilPrepared) {
		t.Fatalf("ToSessionLaunch(nil) err = %v, want ErrNilPrepared", err)
	}
}

func TestToSessionLaunch_Invalid(t *testing.T) {
	// Missing bootdir / workspace / argv → PreparedLaunch.Validate fails.
	_, err := ToSessionLaunch(&agentlaunch.PreparedLaunch{Compiled: validCompiled(t)})
	if err == nil {
		t.Fatal("expected validation error for incomplete PreparedLaunch")
	}
}

func TestToSessionLaunchFromPreparedExecution(t *testing.T) {
	prepared := &agentlaunch.PreparedExecution{
		InputKind: agentlaunch.PrepareInputArtifacts,
		Bindings: agentlaunch.ExecutionBindings{
			Argv: []string{"codex", "exec", "--json"},
			Env: map[string]agentlaunch.EnvVar{
				"BETA":  {Value: "2"},
				"ALPHA": {Value: "1"},
			},
			CWD: "/proj",
		},
		Roots: agentlaunch.ExecutionRoots{ProjectRoot: "/proj", StateRoot: "/state", CWD: "/proj"},
		Access: agentlaunch.AccessRequirements{
			Mode: agentlaunch.AccessRequired,
			Host: agentlaunch.ExecutionHostLocal,
		},
	}

	sl, err := ToSessionLaunchFromPreparedExecution(prepared)
	if err != nil {
		t.Fatalf("ToSessionLaunchFromPreparedExecution: %v", err)
	}
	if sl.Binary != "codex" {
		t.Fatalf("Binary = %q, want codex", sl.Binary)
	}
	if want := []string{"exec", "--json"}; !slices.Equal(sl.Options.ExtraArgs, want) {
		t.Fatalf("ExtraArgs = %v, want %v", sl.Options.ExtraArgs, want)
	}
	if want := []string{"ALPHA=1", "BETA=2"}; !slices.Equal(sl.Options.Env, want) {
		t.Fatalf("Env = %v, want %v", sl.Options.Env, want)
	}
	if sl.Options.Workdir != "/proj" || sl.Options.WorkspaceDir != "/state" {
		t.Fatalf("Options dirs = %q/%q, want /proj//state", sl.Options.Workdir, sl.Options.WorkspaceDir)
	}
	if sl.Options.PreparedExecution != prepared {
		t.Fatalf("PreparedExecution was not carried through")
	}
	if sl.Options.AutoPlantBootDir {
		t.Fatal("AutoPlantBootDir = true, want false for prepared execution handoff")
	}
}

func TestToSessionLaunchFromPreparedExecution_Invalid(t *testing.T) {
	_, err := ToSessionLaunchFromPreparedExecution(&agentlaunch.PreparedExecution{})
	if err == nil {
		t.Fatal("expected validation error for invalid PreparedExecution")
	}
}
