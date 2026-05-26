package launcher

import (
	"context"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// TestPlanFromLaunch_Compiles is the runnability gate for the S4→execution
// bridge: a LaunchPlan assembled by agentlaunch.PlanFromLaunch must drive the
// existing Compile pipeline cleanly. The S4.5 parity harness only proves
// launch *identity* (project/work_dir/runner/isolation) — it never builds a
// plan or calls Compile, so this test covers the gap that hid the missing
// bridge for a sprint.
func TestPlanFromLaunch_Compiles(t *testing.T) {
	in := agentlaunch.PlanFromLaunchInput{
		Spec: agentlaunch.LaunchSpec{ID: "tether.launch"},
		Bag:  agentlaunch.LaunchBag{Spec: "tether.launch", Name: "torque-codex"},
		Render: agentlaunch.RenderResult{
			Body: "boot body — assembled launch material",
			ResolvedInputs: map[string]any{
				"project":                        "torque",
				agentlaunch.LaunchInputWorkDir:   "/work/torque",
				agentlaunch.LaunchInputRunner:    "codex-cli",
				agentlaunch.LaunchInputIsolation: "hybrid",
			},
		},
		Runtime: agentlaunch.RuntimeBinding{
			Provider:    "codex",
			RuntimeKind: agentlaunch.RuntimeSubprocess,
		},
		Agent: agentlaunch.AgentSpec{ID: "torque-engineer"},
		Mode:  agentlaunch.LaunchBackground,
	}

	plan, err := agentlaunch.PlanFromLaunch(in)
	if err != nil {
		t.Fatalf("PlanFromLaunch: %v", err)
	}

	compiled, err := Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("Compile(bridged plan): %v", err)
	}
	if compiled == nil || compiled.Plan == nil {
		t.Fatal("Compile returned an empty CompiledLaunch")
	}
	if compiled.Plan.Provider.ID != "codex" {
		t.Errorf("compiled provider = %q, want codex", compiled.Plan.Provider.ID)
	}
}
