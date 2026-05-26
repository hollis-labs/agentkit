package launcher_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/agentcontext/resolvers"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/contexthook"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

// TestPrepare_ContextHookEndToEnd wires a real agentcontext provider
// through contexthook.New and confirms that:
//
//   - launcher.Compile → launcher.Prepare produces a non-empty BootPrompt
//     sourced from the assembled context.
//   - PlantArtifacts=true plants per-slot files under
//     <bootDir>/context/<name>.txt.
//   - The bootPrompt overrides the inline BootPrompt the LaunchPlan
//     carried (matching agentlaunch.ContextHook godoc).
//   - Re-running the full pipeline with identical inputs yields a
//     byte-identical BootPrompt (determinism).
//
// The test uses only real on-disk paths via t.TempDir — no mocks.
func TestPrepare_ContextHookEndToEnd(t *testing.T) {
	// 1. Materialise a skill on disk so the skill_index resolver has
	// something to discover.
	skillsRoot := t.TempDir()
	skillFile := filepath.Join(skillsRoot, "integration-skill.md")
	skillBody := `---
slug: integration-skill
description: Integration-test skill for contexthook end-to-end coverage.
triggers:
  - integration
  - end-to-end
---
Skill body.
`
	if err := os.WriteFile(skillFile, []byte(skillBody), 0o644); err != nil {
		t.Fatalf("WriteFile skill: %v", err)
	}

	// 2. Build a LaunchPlan with a real workspace + inline boot profile.
	projectRoot := t.TempDir()
	tempPrefix := t.TempDir()
	plan := agentlaunch.LaunchPlan{
		Project: agentlaunch.ProjectSpec{ID: "proj", Name: "Project", Root: projectRoot},
		Agent:   agentlaunch.AgentSpec{ID: "agent", Name: "Agent"},
		Provider: agentlaunch.ProviderSpec{
			ID:    "claude",
			Flags: []string{"--quiet"},
		},
		Runtime: agentlaunch.RuntimePTY,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:       agentlaunch.WorkspaceTemp,
			TempPrefix: tempPrefix,
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootPrompt: "INLINE-PROMPT-THAT-SHOULD-BE-OVERRIDDEN",
				BootMode:   agentlaunch.BootModePlanted,
			},
		},
		Mode: agentlaunch.LaunchInteractive,
		Metadata: agentlaunch.Metadata{
			Labels: map[string]string{"role": "backend"},
		},
	}

	// 3. Build a provider with the full eight-resolver set.
	provider, err := agentcontext.NewProvider(
		resolvers.WithSkillIndex(resolvers.Default()),
		agentcontext.DefaultRenderer{},
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// 4. Build the contexthook with a SlotExtractor that emits a stable
	// fixture set: a static inline header + a skill_index slot.
	extractor := func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
		return []agentcontext.SlotSpec{
			{
				Name:    "agent",
				Section: "## §1 — agent",
				Source: agentcontext.SlotSource{
					Kind:   agentcontext.SlotSourceKindInline,
					Inline: agentcontext.InlineSource{Content: "backend worker"},
				},
			},
			{
				Name:    "skills",
				Section: "## §2 — skills",
				Source: agentcontext.SlotSource{
					Kind: agentcontext.SlotSourceKindSkillIndex,
					SkillIndex: agentcontext.SkillIndexSource{
						Roots: []string{skillsRoot},
						Limit: 8,
					},
				},
			},
		}, nil
	}

	hook := contexthook.New(provider, contexthook.Config{
		SlotExtractor:  extractor,
		PlantArtifacts: true,
	})

	// 5. Run Compile → Prepare.
	compiled, err := launcher.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	prepared, err := launcher.Prepare(context.Background(), compiled,
		launcher.WithContextHook(hook),
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared == nil {
		t.Fatalf("Prepare returned nil")
	}

	// 6. Assertions on the BootPrompt.
	if prepared.BootPrompt == "" {
		t.Fatalf("expected non-empty BootPrompt")
	}
	if prepared.BootPrompt == "INLINE-PROMPT-THAT-SHOULD-BE-OVERRIDDEN" {
		t.Fatalf("ContextHook did not override inline BootPrompt")
	}
	// The skill_index resolver renders "<primary-trigger> — <desc>"
	// where primary-trigger is the alphabetically-first non-empty entry
	// of Skill.Triggers — so "end-to-end" wins over "integration" for
	// our fixture skill.
	for _, want := range []string{
		"## §1 — agent",
		"backend worker",
		"## §2 — skills",
		"end-to-end",
		"Integration-test skill",
	} {
		if !strings.Contains(prepared.BootPrompt, want) {
			t.Fatalf("BootPrompt missing %q\n--- got ---\n%s\n--- end ---", want, prepared.BootPrompt)
		}
	}

	// 7. Assertions on planted per-slot artifacts.
	for name, mustContain := range map[string]string{
		"agent":  "backend worker",
		"skills": "end-to-end",
	} {
		path := filepath.Join(prepared.PlantedBootDir, "context", name+".txt")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		if !strings.Contains(string(got), mustContain) {
			t.Fatalf("planted %s missing %q: got %q", path, mustContain, got)
		}
	}

	// 8. Determinism: re-run the pipeline with identical inputs and
	// confirm the BootPrompt is byte-identical. Compile+Prepare both
	// allocate fresh bootdirs, so the per-launch paths will differ —
	// but the rendered context is purely a function of slot inputs and
	// the request hash, which is deterministic.
	compiled2, err := launcher.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("Compile (2nd): %v", err)
	}
	prepared2, err := launcher.Prepare(context.Background(), compiled2,
		launcher.WithContextHook(hook),
	)
	if err != nil {
		t.Fatalf("Prepare (2nd): %v", err)
	}
	if prepared.BootPrompt != prepared2.BootPrompt {
		t.Fatalf("BootPrompt non-deterministic\n--- first ---\n%s\n--- second ---\n%s\n",
			prepared.BootPrompt, prepared2.BootPrompt)
	}
}
