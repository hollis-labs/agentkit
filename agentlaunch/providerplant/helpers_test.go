package providerplant

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

// compiledFor compiles a minimal valid launch for the given
// provider×runtime pair with no injection.
func compiledFor(t *testing.T, providerID string, runtime agentlaunch.RuntimeKind) *agentlaunch.CompiledLaunch {
	t.Helper()
	return compiledWith(t, providerID, runtime, agentlaunch.InjectionSpec{})
}

// compiledWith is compiledFor with a caller-supplied InjectionSpec.
func compiledWith(t *testing.T, providerID string, runtime agentlaunch.RuntimeKind, inj agentlaunch.InjectionSpec) *agentlaunch.CompiledLaunch {
	t.Helper()
	plan := agentlaunch.LaunchPlan{
		Project:   agentlaunch.ProjectSpec{ID: "proj", Name: "Project", Root: t.TempDir()},
		Agent:     agentlaunch.AgentSpec{ID: "agent-id", Name: "agent-name"},
		Provider:  agentlaunch.ProviderSpec{ID: providerID},
		Runtime:   runtime,
		Workspace: agentlaunch.WorkspaceSpec{Mode: agentlaunch.WorkspaceTemp, TempPrefix: t.TempDir()},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootPrompt:  "PERSONA-PROMPT",
				BootContent: "TASK-KICKOFF",
				BootMode:    agentlaunch.BootModePlanted,
			},
		},
		Injection: inj,
		Mode:      agentlaunch.LaunchInteractive,
	}
	compiled, err := launcher.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("compile %s/%s: %v", providerID, runtime, err)
	}
	return compiled
}

// preparedFor compiles + prepares (no plant) a launch.
func preparedFor(t *testing.T, providerID string, runtime agentlaunch.RuntimeKind) *agentlaunch.PreparedLaunch {
	t.Helper()
	prepared, err := launcher.Prepare(context.Background(), compiledFor(t, providerID, runtime))
	if err != nil {
		t.Fatalf("prepare %s/%s: %v", providerID, runtime, err)
	}
	return prepared
}

// isolateHome points HOME and CODEX_HOME at fresh temp dirs so the
// claude trust-seed side effect and codex auth replication never touch
// the developer's real dotfiles during a test.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
}

// readFile reads a planted file relative to a bootdir, failing the test
// if it is absent.
func readFile(t *testing.T, bootDir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bootDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read planted %s: %v", rel, err)
	}
	return string(b)
}

// assertFileMode fails the test when the file at bootDir/rel does not
// have the expected permission bits.
func assertFileMode(t *testing.T, bootDir, rel string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(filepath.Join(bootDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("stat planted %s: %v", rel, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("planted %s mode = %o, want %o", rel, got, want)
	}
}

// assertExists fails the test when bootDir/rel is missing.
func assertExists(t *testing.T, bootDir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(bootDir, filepath.FromSlash(rel))); err != nil {
		t.Errorf("expected planted file %s: %v", rel, err)
	}
}
