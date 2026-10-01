package providerplant

import (
	"context"
	"errors"
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

// The projected Claude argv carries --add-dir <project> itself in every
// mode (go-providers v0.31.0); providerplant appends nothing, so it appears
// exactly once.
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
	if argv := exec.Bindings.Argv; argv[len(argv)-2] != "--model" || argv[len(argv)-1] != "sonnet" {
		t.Errorf("injection args not appended last: %v", argv)
	}
}
