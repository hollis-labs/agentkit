package providerplant

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

func TestPrepareAndPlant(t *testing.T) {
	isolateHome(t)
	prepared, err := PrepareAndPlant(context.Background(), compiledFor(t, "claude", agentlaunch.RuntimePTY))
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
		compiledFor(t, "claude", agentlaunch.RuntimePTY),
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
		compiledFor(t, "codex", agentlaunch.RuntimeSubprocess),
		WithPlantOption(WithAdapter(provider.NewCodexAdapter())),
	)
	if err != nil {
		t.Fatalf("PrepareAndPlant: %v", err)
	}
	assertExists(t, prepared.PlantedBootDir, "config.toml")
}
