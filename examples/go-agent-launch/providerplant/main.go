// Package main is an executable example showing how an app uses
// go-agent-launch to own provider bootdir planting end to end —
// workspace, provider boot files, native skill files, and the injection
// overlay — through a single Prepare/Plant API.
//
// # Running
//
//	go run ./examples/providerplant
//
// The example uses the opencode provider so it has no global filesystem
// side effects (the claude planter seeds a workspace-trust marker in
// ~/.claude.json; opencode does not). It writes everything under the
// system tempdir and prints the planted layout plus the SessionLaunch
// the go-agent-sessions runtime would consume.
//
// # What this demonstrates
//
//  1. Build a LaunchPlan with an InjectionSpec carrying a native skill
//     file and a bootdir overlay entry.
//  2. launcher.Compile the plan.
//  3. providerplant.PrepareAndPlant — one call that prepares the
//     workspace/bootdir AND plants the provider BootDirSpec files,
//     native files, and overlay.
//  4. sessionshim.ToSessionLaunch — convert the PreparedLaunch into the
//     binary + StartOptions go-agent-sessions' Manager.Start consumes.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
	"github.com/hollis-labs/agentkit/agentlaunch/providerplant"
	"github.com/hollis-labs/agentkit/agentlaunch/sessionshim"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("example failed: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	tempPrefix, err := os.MkdirTemp("", "providerplant-example-*")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}
	projectRoot, err := os.MkdirTemp("", "providerplant-example-project-*")
	if err != nil {
		return fmt.Errorf("mkdtemp project: %w", err)
	}

	// 1. A plan whose InjectionSpec carries a native skill file and an
	//    overlay entry — both planted by providerplant alongside the
	//    provider's own BootDirSpec files.
	plan := agentlaunch.LaunchPlan{
		Project:  agentlaunch.ProjectSpec{ID: "example", Name: "Example", Root: projectRoot},
		Agent:    agentlaunch.AgentSpec{ID: "demo-agent", Name: "demo-agent"},
		Provider: agentlaunch.ProviderSpec{ID: "opencode"},
		Runtime:  agentlaunch.RuntimeSubprocess,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:       agentlaunch.WorkspaceTemp,
			TempPrefix: tempPrefix,
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootPrompt:  "You are the demo agent for the providerplant example.",
				BootContent: "Demonstrate end-to-end bootdir planting.",
				BootMode:    agentlaunch.BootModePlanted,
			},
		},
		Injection: agentlaunch.InjectionSpec{
			NativeFiles: []agentlaunch.NativeFile{{
				Kind:    agentlaunch.NativeFileSkill,
				ID:      "demo-skill",
				Content: "# Demo skill\n\nPlanted as a provider-native skill file.\n",
			}},
			BootDirOverlay: map[string]string{
				"NOTES.md": "Overlay file — planted last, wins on path collisions.\n",
			},
		},
		Mode: agentlaunch.LaunchInteractive,
	}

	// 2. Compile.
	compiled, err := launcher.Compile(ctx, plan)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}

	// 3. Prepare + Plant in one call.
	prepared, err := providerplant.PrepareAndPlant(ctx, compiled)
	if err != nil {
		return fmt.Errorf("prepare and plant: %w", err)
	}

	fmt.Println("=== Planted bootdir ===")
	fmt.Println(prepared.PlantedBootDir)
	for _, f := range walkFiles(prepared.PlantedBootDir) {
		fmt.Println("  " + f)
	}

	// 4. Convert to the go-agent-sessions launch handoff.
	sl, err := sessionshim.ToSessionLaunch(prepared)
	if err != nil {
		return fmt.Errorf("to session launch: %w", err)
	}
	fmt.Println()
	fmt.Println("=== SessionLaunch ===")
	fmt.Printf("  Binary:    %s\n", sl.Binary)
	fmt.Printf("  Workdir:   %s\n", sl.Options.Workdir)
	fmt.Printf("  ExtraArgs: %v\n", sl.Options.ExtraArgs)
	fmt.Printf("  Env:       %v\n", sl.Options.Env)
	return nil
}

// walkFiles returns the bootdir-relative paths of every planted file,
// sorted, for a stable printout.
func walkFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out
}
