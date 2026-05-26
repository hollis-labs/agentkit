package agentlaunch

import "testing"

// validBridgeInput returns a PlanFromLaunchInput that PlanFromLaunch accepts —
// each test mutates one field to exercise a specific path.
func validBridgeInput() PlanFromLaunchInput {
	return PlanFromLaunchInput{
		Spec: LaunchSpec{ID: "tether.launch"},
		Bag:  LaunchBag{Spec: "tether.launch", Name: "torque-codex"},
		Render: RenderResult{
			Body: "boot body — assembled launch material",
			ResolvedInputs: map[string]any{
				"project":            "torque",
				LaunchInputWorkDir:   "/work/torque",
				LaunchInputRunner:    "codex-cli",
				LaunchInputIsolation: "hybrid",
			},
		},
		Runtime: RuntimeBinding{
			Provider:    "codex",
			Model:       "gpt-5.4",
			RuntimeKind: RuntimeSubprocess,
			Args:        []string{"--flag"},
			Timeout:     "3h",
			Permission:  "on-request",
		},
		Agent: AgentSpec{ID: "torque-engineer"},
		Mode:  LaunchBackground,
	}
}

func TestPlanFromLaunch(t *testing.T) {
	plan, err := PlanFromLaunch(validBridgeInput())
	if err != nil {
		t.Fatalf("PlanFromLaunch: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("assembled plan must be Validate()-clean: %v", err)
	}

	if plan.Project.ID != "torque" {
		t.Errorf("Project.ID = %q, want torque", plan.Project.ID)
	}
	if plan.Agent.ID != "torque-engineer" {
		t.Errorf("Agent.ID = %q, want torque-engineer", plan.Agent.ID)
	}
	if plan.Provider.ID != "codex" {
		t.Errorf("Provider.ID = %q, want codex", plan.Provider.ID)
	}
	if plan.Provider.ModelOverride != "gpt-5.4" {
		t.Errorf("Provider.ModelOverride = %q, want gpt-5.4", plan.Provider.ModelOverride)
	}
	if len(plan.Provider.Flags) != 1 || plan.Provider.Flags[0] != "--flag" {
		t.Errorf("Provider.Flags = %v, want [--flag]", plan.Provider.Flags)
	}
	if plan.Provider.Permission != "on-request" {
		t.Errorf("Provider.Permission = %q, want on-request (carried from RuntimeBinding.Permission)", plan.Provider.Permission)
	}
	if plan.Runtime != RuntimeSubprocess {
		t.Errorf("Runtime = %q, want subprocess", plan.Runtime)
	}
	if plan.Workspace.Mode != WorkspacePersistent {
		t.Errorf("Workspace.Mode = %q, want persistent (hybrid)", plan.Workspace.Mode)
	}
	if plan.Workspace.Workdir != "/work/torque" {
		t.Errorf("Workspace.Workdir = %q, want /work/torque", plan.Workspace.Workdir)
	}
	if plan.BootProfile.Inline == nil {
		t.Fatal("BootProfile.Inline must be set")
	}
	if plan.BootProfile.Inline.BootContent != "boot body — assembled launch material" {
		t.Errorf("BootContent = %q", plan.BootProfile.Inline.BootContent)
	}
	if plan.BootProfile.Inline.BootMode != BootModePlanted {
		t.Errorf("BootMode = %q, want %q", plan.BootProfile.Inline.BootMode, BootModePlanted)
	}
	if plan.Mode != LaunchBackground {
		t.Errorf("Mode = %q, want background", plan.Mode)
	}

	// Provenance + consumer-overlay annotations.
	wantAnnos := map[string]string{
		"agentlaunch.isolation":       "hybrid",
		"agentlaunch.runtime.timeout": "3h",
		"agentlaunch.launch_spec":     "tether.launch",
		"agentlaunch.launch_bag":      "torque-codex",
	}
	for k, want := range wantAnnos {
		if got := plan.Metadata.Annotations[k]; got != want {
			t.Errorf("annotation %q = %q, want %q", k, got, want)
		}
	}
}

func TestPlanFromLaunch_WorktreeIsolation(t *testing.T) {
	in := validBridgeInput()
	in.Render.ResolvedInputs[LaunchInputIsolation] = "worktree"

	plan, err := PlanFromLaunch(in)
	if err != nil {
		t.Fatalf("PlanFromLaunch: %v", err)
	}
	// worktree maps to a valid WorkspaceMode; the raw token is preserved
	// for the consumer's worktree machinery.
	if plan.Workspace.Mode != WorkspacePersistent {
		t.Errorf("worktree Workspace.Mode = %q, want persistent", plan.Workspace.Mode)
	}
	if plan.Metadata.Annotations["agentlaunch.isolation"] != "worktree" {
		t.Errorf("raw isolation token must be preserved on annotations")
	}
}

func TestPlanFromLaunch_Errors(t *testing.T) {
	t.Run("invalid runtime", func(t *testing.T) {
		in := validBridgeInput()
		in.Runtime = RuntimeBinding{} // no provider, no runtime kind
		if _, err := PlanFromLaunch(in); err == nil {
			t.Error("empty RuntimeBinding should error")
		}
	})
	t.Run("missing agent", func(t *testing.T) {
		in := validBridgeInput()
		in.Agent = AgentSpec{}
		if _, err := PlanFromLaunch(in); err == nil {
			t.Error("empty AgentSpec should error")
		}
	})
	t.Run("missing work_dir", func(t *testing.T) {
		in := validBridgeInput()
		delete(in.Render.ResolvedInputs, LaunchInputWorkDir)
		if _, err := PlanFromLaunch(in); err == nil {
			t.Error("missing work_dir should error")
		}
	})
	t.Run("unknown isolation", func(t *testing.T) {
		in := validBridgeInput()
		in.Render.ResolvedInputs[LaunchInputIsolation] = "bogus"
		if _, err := PlanFromLaunch(in); err == nil {
			t.Error("unknown isolation token should error")
		}
	})
}

func TestIsolationWorkspaceMode(t *testing.T) {
	cases := map[string]struct {
		mode    WorkspaceMode
		wantErr bool
	}{
		"":           {WorkspaceShared, false},
		"hybrid":     {WorkspacePersistent, false},
		"worktree":   {WorkspacePersistent, false},
		"shared":     {WorkspaceShared, false},
		"temp":       {WorkspaceTemp, false},
		"fresh":      {WorkspaceFresh, false},
		"persistent": {WorkspacePersistent, false},
		"nonsense":   {"", true},
	}
	for token, want := range cases {
		got, err := isolationWorkspaceMode(token)
		if want.wantErr {
			if err == nil {
				t.Errorf("isolation %q: want error, got %q", token, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("isolation %q: unexpected error %v", token, err)
		}
		if got != want.mode {
			t.Errorf("isolation %q: got %q, want %q", token, got, want.mode)
		}
	}
}
