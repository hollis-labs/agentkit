package agentlaunch

import (
	"strings"
	"testing"
)

// validPlanForHash returns a plan whose Validate would pass but
// HashLaunchPlan does not require it to.
func validPlanForHash() LaunchPlan {
	return LaunchPlan{
		Project: ProjectSpec{ID: "proj", Name: "Project", Root: "/abs/proj"},
		Agent:   AgentSpec{ID: "agent", Name: "Agent"},
		Provider: ProviderSpec{
			ID:    "claude",
			Flags: []string{"--quiet", "--strict"},
			Env: map[string]string{
				"ZETA":  "z",
				"ALPHA": "a",
				"MID":   "m",
			},
		},
		Runtime: RuntimePTY,
		Workspace: WorkspaceSpec{
			Mode:         WorkspaceShared,
			WorkspaceDir: "/abs/ws",
		},
		BootProfile: BootProfileRef{
			CatalogPath: "/abs/catalog.yaml",
			Name:        "default",
		},
		Mode: LaunchInteractive,
	}
}

// TestHashLaunchPlanStable confirms identical plans hash identically
// AND that the hash is invariant under repeated calls (no map iteration
// noise leaking into the canonical form).
func TestHashLaunchPlanStable(t *testing.T) {
	plan := validPlanForHash()
	first, err := HashLaunchPlan(plan)
	if err != nil {
		t.Fatalf("HashLaunchPlan first call = %v", err)
	}
	// Run several times to make sure map iteration order does not affect
	// the digest.
	for i := 0; i < 50; i++ {
		got, err := HashLaunchPlan(plan)
		if err != nil {
			t.Fatalf("HashLaunchPlan iter %d = %v", i, err)
		}
		if got != first {
			t.Fatalf("HashLaunchPlan iter %d = %q, want %q", i, got, first)
		}
	}
	if len(first) != 64 {
		t.Fatalf("HashLaunchPlan hex length = %d, want 64", len(first))
	}
	if strings.TrimLeft(first, "0123456789abcdef") != "" {
		t.Fatalf("HashLaunchPlan output %q is not lowercase hex", first)
	}
}

// TestHashLaunchPlanDifferentPlanDifferentHash flips one field at a
// time and confirms the hash changes for each.
func TestHashLaunchPlanDifferentPlanDifferentHash(t *testing.T) {
	base := validPlanForHash()
	baseHash, err := HashLaunchPlan(base)
	if err != nil {
		t.Fatalf("base hash err: %v", err)
	}

	mutations := []struct {
		name   string
		mutate func(*LaunchPlan)
	}{
		{"project.id", func(p *LaunchPlan) { p.Project.ID = "other" }},
		{"agent.id", func(p *LaunchPlan) { p.Agent.ID = "other" }},
		{"provider.id", func(p *LaunchPlan) { p.Provider.ID = "codex" }},
		{"runtime", func(p *LaunchPlan) { p.Runtime = RuntimeSubprocess }},
		{"workspace.mode", func(p *LaunchPlan) { p.Workspace.Mode = WorkspaceTemp }},
		{"workspace.workspace_dir", func(p *LaunchPlan) { p.Workspace.WorkspaceDir = "/abs/other" }},
		{"mode", func(p *LaunchPlan) { p.Mode = LaunchBackground }},
		{"boot_profile.name", func(p *LaunchPlan) { p.BootProfile.Name = "different" }},
		{"provider.flags additional", func(p *LaunchPlan) {
			p.Provider.Flags = append(p.Provider.Flags, "--extra")
		}},
		{"provider.env extra key", func(p *LaunchPlan) {
			p.Provider.Env["NEW"] = "v"
		}},
		{"provider.env changed value", func(p *LaunchPlan) {
			p.Provider.Env["ALPHA"] = "different"
		}},
	}

	for _, mut := range mutations {
		mut := mut
		t.Run(mut.name, func(t *testing.T) {
			p := validPlanForHash()
			mut.mutate(&p)
			got, err := HashLaunchPlan(p)
			if err != nil {
				t.Fatalf("HashLaunchPlan = %v", err)
			}
			if got == baseHash {
				t.Fatalf("HashLaunchPlan after %s = base hash %q, want different", mut.name, baseHash)
			}
		})
	}
}

// TestHashLaunchPlanMapOrderIndependent ensures that constructing the
// "same" plan with map keys inserted in different orders produces the
// same hash. Go's runtime randomises map iteration order, so the loop
// implicitly exercises both insertion orders.
func TestHashLaunchPlanMapOrderIndependent(t *testing.T) {
	p1 := validPlanForHash()
	p2 := validPlanForHash()

	// Rebuild p2's env in a different declaration order.
	p2.Provider.Env = map[string]string{}
	p2.Provider.Env["MID"] = "m"
	p2.Provider.Env["ZETA"] = "z"
	p2.Provider.Env["ALPHA"] = "a"

	h1, err := HashLaunchPlan(p1)
	if err != nil {
		t.Fatalf("h1 err: %v", err)
	}
	h2, err := HashLaunchPlan(p2)
	if err != nil {
		t.Fatalf("h2 err: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("map-order-permuted plans produced different hashes: %q vs %q", h1, h2)
	}
}
