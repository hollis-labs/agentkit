// Package main is an executable example showing how to wire
// agentcontext into go-agent-launch via contexthook.
//
// # Running
//
//	go run ./examples/with-context
//
// The example self-contains its skill fixture in a temp directory and
// writes the planted context artifacts to a temp bootdir. Stdout shows
// the assembled BootPrompt that the preparer would feed to the spawned
// session.
//
// # What this demonstrates
//
//  1. Construct an agentcontext.ContextProvider with the full
//     eight-resolver set (resolvers.WithSkillIndex(resolvers.Default())).
//  2. Adapt it to an agentlaunch.ContextHook via contexthook.New.
//  3. Supply a Config.SlotExtractor that yields the slot list (until
//     BootProfileInline grows a Slots field — see CW-20260515-0010
//     follow-up).
//  4. Run launcher.Compile → launcher.Prepare with
//     launcher.WithContextHook(hook). The hook's rendered output
//     becomes PreparedLaunch.BootPrompt.
//
// # Filesystem side-effects
//
// This example deliberately uses os.MkdirTemp for the skill root and
// for the agentlaunch temp-prefix. Both directories survive after the
// process exits — operators inspecting the output can chase the printed
// paths to see what was planted under <bootDir>/context/.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/agentcontext/resolvers"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/contexthook"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("example failed: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	// 1. Materialise a skill fixture so the skill_index resolver has
	//    something to discover.
	skillsRoot, err := os.MkdirTemp("", "agentlaunch-example-skills-*")
	if err != nil {
		return fmt.Errorf("mkdtemp skills: %w", err)
	}
	skillFile := filepath.Join(skillsRoot, "example-skill.md")
	skillBody := `---
slug: example-skill
description: Demo skill — wires the contexthook example end-to-end.
triggers:
  - example
  - demo
---
Demo skill body.
`
	if err := os.WriteFile(skillFile, []byte(skillBody), 0o644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}

	// 2. Build the LaunchPlan. WorkspaceTemp lets the preparer allocate
	//    a per-launch workspace + bootdir under the system tempdir.
	tempPrefix, err := os.MkdirTemp("", "agentlaunch-example-temp-*")
	if err != nil {
		return fmt.Errorf("mkdtemp tempPrefix: %w", err)
	}

	plan := agentlaunch.LaunchPlan{
		Project:  agentlaunch.ProjectSpec{ID: "example", Name: "Example Project"},
		Agent:    agentlaunch.AgentSpec{ID: "demo-agent", Name: "Demo Agent"},
		Provider: agentlaunch.ProviderSpec{ID: "claude"},
		Runtime:  agentlaunch.RuntimePTY,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:       agentlaunch.WorkspaceTemp,
			TempPrefix: tempPrefix,
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootMode: agentlaunch.BootModePlanted,
			},
		},
		Mode: agentlaunch.LaunchInteractive,
		Metadata: agentlaunch.Metadata{
			Labels: map[string]string{"role": "demo"},
		},
	}

	// 3. Build the context provider with the full eight-resolver set.
	provider, err := agentcontext.NewProvider(
		resolvers.WithSkillIndex(resolvers.Default()),
		agentcontext.DefaultRenderer{},
	)
	if err != nil {
		return fmt.Errorf("new provider: %w", err)
	}

	// 4. Wire the contexthook with a SlotExtractor. The extractor
	//    closes over our skillsRoot so the example is self-contained;
	//    a real caller would read these from
	//    compiled.Plan.BootProfile.Inline.Slots once that field lands
	//    (CW-20260515-0010 follow-up).
	hook := contexthook.New(provider, contexthook.Config{
		PlantArtifacts: true,
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name:    "header",
					Section: "## §1 — agent",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "demo agent — example launch"},
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
		},
	})

	// 5. Compile → Prepare with the context hook registered.
	compiled, err := launcher.Compile(ctx, plan)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	prepared, err := launcher.Prepare(ctx, compiled, launcher.WithContextHook(hook))
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}

	// 6. Display the result.
	fmt.Println("=== PreparedLaunch.BootPrompt ===")
	fmt.Println(prepared.BootPrompt)
	fmt.Println()
	fmt.Println("=== Planted bootdir ===")
	fmt.Println(prepared.PlantedBootDir)
	fmt.Println("Inspect <bootdir>/context/*.txt for planted per-slot artifacts.")

	return nil
}
