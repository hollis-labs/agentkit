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
// exactly once, in every mode. DefaultResolver builds each mode's own Claude
// adapter, so the argv differs by mode: print carries the boot prompt after
// -p, streaming-stdio carries no prompt (turns arrive on stdin) and the TUI no
// print flags at all.
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
		switch mode {
		case runtimes.ModeStreamingStdio:
			if !slices.Contains(argv, "--input-format") || slices.Contains(argv, prepared.BootContent) && prepared.BootContent != "" {
				t.Errorf("%s: want stream-json input and no boot prompt in argv: %v", mode, argv)
			}
		case runtimes.ModePTY:
			if slices.Contains(argv, "-p") {
				t.Errorf("%s: the TUI takes no -p: %v", mode, argv)
			}
		case runtimes.ModeSubprocessPerTurn:
			if i := slices.Index(argv, "-p"); i < 0 || slices.Contains(argv, "--input-format") {
				t.Errorf("%s: want print-mode -p <prompt>: %v", mode, argv)
			}
		}
		if exec.Bindings.Launch == nil || exec.Bindings.Launch.Convention.Mode != mode {
			t.Errorf("%s: bindings carry no launch template for the mode: %+v", mode, exec.Bindings.Launch)
		}
	}
}

// Provider.Flags and Injection.Args go at the convention's extra-argument
// slot, before Claude's variadic --add-dir: a positional first would be read
// as part of the prompt or as a directory, so it is refused. A leading option
// is fine.
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
	argv := exec.Bindings.Argv
	if m, add := slices.Index(argv, "--model"), slices.Index(argv, "--add-dir"); m < 0 || argv[m+1] != "sonnet" || add < m {
		t.Errorf("injection args not at the extra-argument slot before --add-dir: %v", argv)
	}
	if got := exec.Bindings.Launch.ExtraArgs; !slices.Equal(got, []string{"--model", "sonnet"}) {
		t.Errorf("launch template extra args = %v", got)
	}
}
