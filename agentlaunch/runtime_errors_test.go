package agentlaunch

import (
	"errors"
	"strings"
	"testing"
)

// A persisted old spelling says what it got, not just "unknown runtime".
func TestUnknownRuntimeErrorsNameTheValue(t *testing.T) {
	p := LaunchPlan{
		Project:   ProjectSpec{ID: "p"},
		Agent:     AgentSpec{ID: "a"},
		Provider:  ProviderSpec{ID: "claude"},
		Runtime:   "subprocess",
		Workspace: WorkspaceSpec{Mode: WorkspaceTemp},
		Mode:      LaunchInteractive,
	}
	err := p.Validate()
	if !errors.Is(err, ErrUnknownRuntime) || !strings.Contains(err.Error(), `"subprocess"`) {
		t.Errorf("LaunchPlan.Validate = %v, want ErrUnknownRuntime naming \"subprocess\"", err)
	}
	err = RuntimeBinding{Provider: "codex", RuntimeKind: "app-server"}.Validate()
	if !errors.Is(err, ErrUnknownRuntime) || !strings.Contains(err.Error(), `"app-server"`) {
		t.Errorf("RuntimeBinding.Validate = %v, want ErrUnknownRuntime naming \"app-server\"", err)
	}
}
