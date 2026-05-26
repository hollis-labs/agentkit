package catalog

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

func TestResolve_InlineCatalog(t *testing.T) {
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	plan, err := g.Resolve("codex-launch")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Validate has already been called inside Resolve; calling again
	// should still succeed.
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan.Validate: %v", err)
	}
	if got, want := plan.Project.ID, "demo"; got != want {
		t.Errorf("Project.ID = %q, want %q", got, want)
	}
	if got, want := plan.Project.Name, "Demo Project"; got != want {
		t.Errorf("Project.Name = %q, want %q", got, want)
	}
	if got, want := plan.Project.Root, "/tmp/demo-repo"; got != want {
		t.Errorf("Project.Root = %q, want %q", got, want)
	}
	if got, want := plan.Agent.ID, "demo-agent"; got != want {
		t.Errorf("Agent.ID = %q, want %q", got, want)
	}
	if got, want := plan.Provider.ID, "codex-cli"; got != want {
		t.Errorf("Provider.ID = %q, want %q", got, want)
	}
	if got, want := plan.Runtime, agentlaunch.RuntimeSubprocess; got != want {
		t.Errorf("Runtime = %q, want %q", got, want)
	}
	// "hybrid" is the Tether-only workspace token; the translator maps
	// it to WorkspacePersistent and preserves the raw token in
	// annotations.
	if got, want := plan.Workspace.Mode, agentlaunch.WorkspacePersistent; got != want {
		t.Errorf("Workspace.Mode = %q, want %q", got, want)
	}
	if got, want := plan.Metadata.Annotations["tether.workspace_mode_raw"], "hybrid"; got != want {
		t.Errorf("annotations[tether.workspace_mode_raw] = %q, want %q", got, want)
	}
	// Defaults block label flows through into Metadata.Labels.
	if got, want := plan.Metadata.Labels["portfolio"], "hollis-labs"; got != want {
		t.Errorf("Metadata.Labels[portfolio] = %q, want %q", got, want)
	}
	// Prompt-composition booleans land in annotations.
	if got, want := plan.Metadata.Annotations["tether.prompt.include_project_boot"], "true"; got != want {
		t.Errorf("annotations[include_project_boot] = %q, want %q", got, want)
	}
	if got, want := plan.Metadata.Annotations["tether.prompt.include_agent_boot"], "true"; got != want {
		t.Errorf("annotations[include_agent_boot] = %q, want %q", got, want)
	}
	// Mode defaults to interactive when launch profile omits it.
	if got, want := plan.Mode, agentlaunch.LaunchInteractive; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	// Provider bootstrap mode (agents_md from the fixture) propagates
	// via annotations.
	if got, want := plan.Metadata.Annotations["tether.provider.bootstrap.mode"], "agents_md"; got != want {
		t.Errorf("annotations[provider.bootstrap.mode] = %q, want %q", got, want)
	}
}

func TestResolve_LaunchNotFound(t *testing.T) {
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	_, err = g.Resolve("does-not-exist")
	if !errors.Is(err, ErrLaunchNotFound) {
		t.Fatalf("err = %v, want ErrLaunchNotFound", err)
	}
}

func TestResolve_ProjectNotFound(t *testing.T) {
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	// Mutate the launch so it references a missing project.
	g.Launches[0].Project = "no-such-project"
	_, err = g.Resolve("codex-launch")
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

func TestResolve_AgentNotFound(t *testing.T) {
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	g.Launches[0].Agent = "no-such-agent"
	_, err = g.Resolve("codex-launch")
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("err = %v, want ErrAgentNotFound", err)
	}
}

func TestResolve_ProviderNotFound(t *testing.T) {
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	g.Launches[0].Provider = "no-such-provider"
	_, err = g.Resolve("codex-launch")
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("err = %v, want ErrProviderNotFound", err)
	}
}

func TestResolve_UnsupportedRuntime(t *testing.T) {
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	g.Providers[0].RuntimeKind = "websocket"
	_, err = g.Resolve("codex-launch")
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("err = %v, want ErrUnsupportedRuntime", err)
	}
}

func TestResolve_ApiRuntimeRejected(t *testing.T) {
	// Tether's "api" runtime is intentionally unmapped — it doesn't
	// fit the agentlaunch.RuntimeKind set.
	g, err := LoadGlobalFromBytes(inlineCatalogBytes)
	if err != nil {
		t.Fatalf("LoadGlobalFromBytes: %v", err)
	}
	g.Providers[0].RuntimeKind = "api"
	_, err = g.Resolve("codex-launch")
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("err = %v, want ErrUnsupportedRuntime", err)
	}
}

func TestResolve_TetherDirectoryTree(t *testing.T) {
	dir := filepath.Join(testdataDir(t), "tree")
	g, err := LoadGlobal(dir)
	if err != nil {
		t.Fatalf("LoadGlobal(dir): %v", err)
	}
	plan, err := g.Resolve("codex-launch")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan.Validate: %v", err)
	}
	if got, want := plan.Project.ID, "demo"; got != want {
		t.Errorf("Project.ID = %q, want %q", got, want)
	}
	if got, want := plan.Provider.ID, "codex-cli"; got != want {
		t.Errorf("Provider.ID = %q, want %q", got, want)
	}
	if got, want := plan.Runtime, agentlaunch.RuntimeSubprocess; got != want {
		t.Errorf("Runtime = %q, want %q", got, want)
	}
}

func TestToLaunchPlan_MergesAgentProviderOverride(t *testing.T) {
	g := &GlobalCatalog{
		Projects: []ProjectEntry{{
			ID: "demo", Name: "Demo", RepoRoot: "/tmp/x",
			Workspace: ProjectWorkspace{DefaultMode: "shared"},
		}},
		Agents: []AgentEntry{{
			ID: "a", ProviderOverrides: map[string]ProviderOverride{
				"p": {ExtraArgs: []string{"--extra"}, Env: map[string]string{"X": "y"}},
			},
		}},
		Providers: []ProviderEntry{{
			ID: "p", RuntimeKind: "subprocess", Command: "bin", Args: []string{"--first"},
		}},
		Launches: []LaunchEntry{{
			ID: "lp", Project: "demo", Agent: "a", Provider: "p",
			Workspace: LaunchWorkspace{Mode: "shared"},
			Overrides: LaunchOverrides{Env: map[string]string{"X": "z", "Q": "1"}},
		}},
	}
	plan, err := g.Resolve("lp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got, want := plan.Provider.Binary, "bin"; got != want {
		t.Errorf("Provider.Binary = %q, want %q", got, want)
	}
	if got, want := len(plan.Provider.Flags), 2; got != want {
		t.Fatalf("len(Provider.Flags) = %d, want %d (provider args + agent ExtraArgs)", got, want)
	}
	if got, want := plan.Provider.Flags[0], "--first"; got != want {
		t.Errorf("Provider.Flags[0] = %q, want %q", got, want)
	}
	if got, want := plan.Provider.Flags[1], "--extra"; got != want {
		t.Errorf("Provider.Flags[1] = %q, want %q", got, want)
	}
	// Launch override wins over agent override on key collision.
	if got, want := plan.Provider.Env["X"], "z"; got != want {
		t.Errorf("Provider.Env[X] = %q, want %q (launch override should win)", got, want)
	}
	if got, want := plan.Provider.Env["Q"], "1"; got != want {
		t.Errorf("Provider.Env[Q] = %q, want %q", got, want)
	}
}

func TestToLaunchPlan_MCPAllowlistLayering(t *testing.T) {
	g := &GlobalCatalog{
		Projects: []ProjectEntry{{
			ID: "demo", RepoRoot: "/tmp/x",
			Workspace: ProjectWorkspace{DefaultMode: "shared"},
			MCP:       CatalogMCPConfig{Servers: []string{"proj-srv"}},
		}},
		Agents:    []AgentEntry{{ID: "a"}},
		Providers: []ProviderEntry{{ID: "p", RuntimeKind: "subprocess"}},
		Launches: []LaunchEntry{{
			ID: "lp", Project: "demo", Agent: "a", Provider: "p",
			Workspace: LaunchWorkspace{Mode: "shared"},
			MCP:       CatalogMCPConfig{Servers: []string{"launch-srv"}},
		}},
		Defaults: DefaultsBlock{MCPAllowlist: []string{"default-srv"}},
	}
	plan, err := g.Resolve("lp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Launch allowlist wins fully — no merging across layers.
	if got, want := len(plan.MCP.Allowlist), 1; got != want {
		t.Fatalf("len(MCP.Allowlist) = %d, want %d", got, want)
	}
	if got, want := plan.MCP.Allowlist[0], "launch-srv"; got != want {
		t.Errorf("MCP.Allowlist[0] = %q, want %q", got, want)
	}

	// With the launch MCP servers removed, the project layer kicks in.
	g.Launches[0].MCP.Servers = nil
	plan2, err := g.Resolve("lp")
	if err != nil {
		t.Fatalf("Resolve (no launch mcp): %v", err)
	}
	if got, want := plan2.MCP.Allowlist[0], "proj-srv"; got != want {
		t.Errorf("MCP.Allowlist[0] (project layer) = %q, want %q", got, want)
	}

	// With both launch and project removed, the defaults block takes over.
	g.Projects[0].MCP.Servers = nil
	plan3, err := g.Resolve("lp")
	if err != nil {
		t.Fatalf("Resolve (defaults only): %v", err)
	}
	if got, want := plan3.MCP.Allowlist[0], "default-srv"; got != want {
		t.Errorf("MCP.Allowlist[0] (defaults layer) = %q, want %q", got, want)
	}
}

func TestToLaunchPlan_RuntimeOverride(t *testing.T) {
	g := &GlobalCatalog{
		Projects:  []ProjectEntry{{ID: "demo", RepoRoot: "/tmp/x", Workspace: ProjectWorkspace{DefaultMode: "shared"}}},
		Agents:    []AgentEntry{{ID: "a"}},
		Providers: []ProviderEntry{{ID: "p", RuntimeKind: "subprocess"}},
		Launches: []LaunchEntry{{
			ID: "lp", Project: "demo", Agent: "a", Provider: "p",
			Workspace: LaunchWorkspace{Mode: "shared"},
			Runtime:   "pty",
		}},
	}
	plan, err := g.Resolve("lp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Per-launch runtime override wins over provider.runtime_kind.
	if got, want := plan.Runtime, agentlaunch.RuntimePTY; got != want {
		t.Errorf("Runtime = %q, want %q (launch override should win)", got, want)
	}
}

func TestToLaunchPlan_BootProfileSynthesized(t *testing.T) {
	g := &GlobalCatalog{
		Projects:  []ProjectEntry{{ID: "demo", RepoRoot: "/tmp/x", Workspace: ProjectWorkspace{DefaultMode: "shared"}}},
		Agents:    []AgentEntry{{ID: "a"}},
		Providers: []ProviderEntry{{ID: "p", RuntimeKind: "subprocess"}},
		Launches: []LaunchEntry{{
			ID: "lp", Project: "demo", Agent: "a", Provider: "p",
			Workspace: LaunchWorkspace{Mode: "shared"},
		}},
	}
	plan, err := g.Resolve("lp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Validate requires either CatalogPath or Inline; we synthesize
	// inline so the LaunchPlan passes Validate.
	if plan.BootProfile.Inline == nil {
		t.Fatal("BootProfile.Inline should have been synthesized")
	}
	if plan.BootProfile.Inline.BootMode != agentlaunch.BootModePlanted {
		t.Errorf("BootProfile.Inline.BootMode = %q, want %q", plan.BootProfile.Inline.BootMode, agentlaunch.BootModePlanted)
	}
	if plan.Metadata.Annotations["tether.boot_profile.synthesized"] != "true" {
		t.Errorf("annotations[tether.boot_profile.synthesized] = %q, want true", plan.Metadata.Annotations["tether.boot_profile.synthesized"])
	}
}

func TestToLaunchPlan_BootProfileNamed(t *testing.T) {
	g := &GlobalCatalog{
		Projects:  []ProjectEntry{{ID: "demo", RepoRoot: "/tmp/x", Workspace: ProjectWorkspace{DefaultMode: "shared"}}},
		Agents:    []AgentEntry{{ID: "a"}},
		Providers: []ProviderEntry{{ID: "p", RuntimeKind: "subprocess"}},
		Launches: []LaunchEntry{{
			ID: "lp", Project: "demo", Agent: "a", Provider: "p",
			Workspace:   LaunchWorkspace{Mode: "shared"},
			BootProfile: "demo.engineer.main",
		}},
	}
	plan, err := g.Resolve("lp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got, want := plan.BootProfile.Name, "demo.engineer.main"; got != want {
		t.Errorf("BootProfile.Name = %q, want %q", got, want)
	}
}

func TestNilReceivers(t *testing.T) {
	var g *GlobalCatalog
	_, err := g.Resolve("anything")
	if err == nil {
		t.Fatal("expected error from nil GlobalCatalog.Resolve, got nil")
	}
	var lp *LaunchProfile
	_, err = lp.ToLaunchPlan(&GlobalCatalog{})
	if err == nil {
		t.Fatal("expected error from nil LaunchProfile.ToLaunchPlan, got nil")
	}
}
