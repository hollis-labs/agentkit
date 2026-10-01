package providerplant

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

func TestPrepareAndPlant(t *testing.T) {
	isolateHome(t)
	prepared, err := PrepareAndPlant(context.Background(), compiledFor(t, "claude", runtimes.ModePTY))
	if err != nil {
		t.Fatalf("PrepareAndPlant: %v", err)
	}
	assertExists(t, prepared.PlantedBootDir, "CLAUDE.md")
	if err := prepared.Validate(); err != nil {
		t.Fatalf("prepared invalid: %v", err)
	}
}

// TestPrepareAndPlant_WithContextHook proves a prepare-stage context
// hook's boot prompt reaches the planted CLAUDE.md — the hook overrides
// BootPrompt, which Plant feeds into PlantContext.SystemPrompt.
func TestPrepareAndPlant_WithContextHook(t *testing.T) {
	isolateHome(t)
	hook := func(_ context.Context, _ string, _ *agentlaunch.CompiledLaunch) (string, error) {
		return "CONTEXT-HOOK-PROMPT", nil
	}
	prepared, err := PrepareAndPlant(
		context.Background(),
		compiledFor(t, "claude", runtimes.ModePTY),
		WithPrepareOption(launcher.WithContextHook(hook)),
	)
	if err != nil {
		t.Fatalf("PrepareAndPlant: %v", err)
	}
	if got := readFile(t, prepared.PlantedBootDir, "CLAUDE.md"); !strings.Contains(got, "CONTEXT-HOOK-PROMPT") {
		t.Errorf("CLAUDE.md = %q, want context-hook prompt", got)
	}
}

// TestPrepareAndPlant_WithPlantOption proves plant-stage options thread
// through PrepareAndPlant.
func TestPrepareAndPlant_WithPlantOption(t *testing.T) {
	isolateHome(t)
	prepared, err := PrepareAndPlant(
		context.Background(),
		compiledFor(t, "codex", runtimes.ModeSubprocessPerTurn),
		WithPlantOption(WithAdapter(provider.NewCodexAdapter())),
	)
	if err != nil {
		t.Fatalf("PrepareAndPlant: %v", err)
	}
	assertExists(t, prepared.PlantedBootDir, "config.toml")
}

// The projected Claude argv carries --add-dir <project> itself
// (go-providers v0.31.0); providerplant appends nothing, so it appears
// exactly once. Note: DefaultResolver builds a print-mode ClaudeAdapter for
// every Claude mode today, so all three modes below project the print argv;
// what this pins is the absence of a second --add-dir, not per-mode argv.
// That resolver gap is CW-20260930-0134's.
func TestPrepareExecution_ClaudeProjectDirOnce(t *testing.T) {
	isolateHome(t)
	for _, mode := range []runtimes.Mode{runtimes.ModeStreamingStdio, runtimes.ModeSubprocessPerTurn, runtimes.ModePTY} {
		compiled := compiledFor(t, "claude", mode)
		prepared, err := launcher.Prepare(context.Background(), compiled)
		if err != nil {
			t.Fatalf("%s: prepare: %v", mode, err)
		}
		exec, err := PrepareExecution(context.Background(), prepared)
		if err != nil {
			t.Fatalf("%s: PrepareExecution: %v", mode, err)
		}
		argv := exec.Bindings.Argv
		n := 0
		for i, a := range argv {
			if a == "--add-dir" {
				n++
				if i+1 >= len(argv) || argv[i+1] != compiled.Plan.Project.Root {
					t.Errorf("%s: --add-dir not followed by the project root: %v", mode, argv)
				}
			}
		}
		if n != 1 {
			t.Errorf("%s: --add-dir appears %d times, want 1: %v", mode, n, argv)
		}
	}
}

// Provider.Flags and Injection.Args follow the projected argv, which can end
// in a variadic flag (Claude's --add-dir): a positional first would be
// swallowed, so it is refused. A leading option is fine.
func TestPrepareExecution_NoPositionalAfterProjection(t *testing.T) {
	isolateHome(t)
	positional := compiledWith(t, "claude", runtimes.ModeStreamingStdio, agentlaunch.InjectionSpec{Args: []string{"stray-positional"}})
	prepared, err := launcher.Prepare(context.Background(), positional)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := PrepareExecution(context.Background(), prepared); !errors.Is(err, ErrPositionalAfterProjection) {
		t.Fatalf("PrepareExecution = %v, want ErrPositionalAfterProjection", err)
	}

	option := compiledWith(t, "claude", runtimes.ModeStreamingStdio, agentlaunch.InjectionSpec{Args: []string{"--model", "sonnet"}})
	prepared, err = launcher.Prepare(context.Background(), option)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	exec, err := PrepareExecution(context.Background(), prepared)
	if err != nil {
		t.Fatalf("PrepareExecution with a leading option: %v", err)
	}
	if argv := exec.Bindings.Argv; !slices.Contains(argv, "--model") || slices.Index(argv, "--model") > slices.Index(argv, "--") {
		t.Errorf("injection args not placed among the flags: %v", argv)
	}

	dashdash := compiledWith(t, "claude", runtimes.ModeStreamingStdio, agentlaunch.InjectionSpec{Args: []string{"--model", "sonnet", "--", "more"}})
	prepared, err = launcher.Prepare(context.Background(), dashdash)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := PrepareExecution(context.Background(), prepared); !errors.Is(err, ErrPositionalAfterProjection) {
		t.Fatalf("PrepareExecution with a \"--\" launch flag = %v, want ErrPositionalAfterProjection", err)
	}
}

// Since go-providers v0.34.1 a projected argv with a boot prompt ends in
// "-- <prompt>". Provider.Flags and Injection.Args must land before that
// "--": after it they are prompt text, not flags (CW-20261001-0102). Every
// launch flag precedes "--", in order, and the prompt is last.
func TestPrepareExecution_LaunchFlagsPrecedeDashDash(t *testing.T) {
	flags := []string{"--flag-a", "--flag-b", "b-value"}
	args := []string{"--inj", "inj-value"}
	for _, c := range []struct {
		provider string
		mode     runtimes.Mode
	}{
		{"claude", runtimes.ModeSubprocessPerTurn},
		{"claude", runtimes.ModeStreamingStdio},
		{"claude", runtimes.ModePTY},
		{"codex", runtimes.ModeSubprocessPerTurn},
	} {
		isolateHome(t)
		compiled := compiledWith(t, c.provider, c.mode, agentlaunch.InjectionSpec{Args: args})
		compiled.Plan.Provider.Flags = flags
		prepared, err := launcher.Prepare(context.Background(), compiled)
		if err != nil {
			t.Fatalf("%s/%s: prepare: %v", c.provider, c.mode, err)
		}
		exec, err := PrepareExecution(context.Background(), prepared)
		if err != nil {
			t.Fatalf("%s/%s: PrepareExecution: %v", c.provider, c.mode, err)
		}
		argv := exec.Bindings.Argv
		dd := slices.Index(argv, "--")
		if dd < 0 {
			t.Fatalf("%s/%s: no \"--\" before the boot prompt; this test needs go-providers v0.34.1 or later: %q", c.provider, c.mode, argv)
		}
		want := append(append([]string{}, flags...), args...)
		if dd < len(want) || !slices.Equal(argv[dd-len(want):dd], want) {
			t.Errorf("%s/%s: launch flags are not immediately before \"--\": %q", c.provider, c.mode, argv)
		}
		if got := argv[dd+1:]; !slices.Equal(got, []string{"TASK-KICKOFF"}) {
			t.Errorf("%s/%s: after \"--\" = %q, want only the boot prompt", c.provider, c.mode, got)
		}
	}
}
