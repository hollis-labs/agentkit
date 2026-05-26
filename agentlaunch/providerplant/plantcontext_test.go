package providerplant

import (
	"slices"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

func TestPlantContextFor_Mapping(t *testing.T) {
	prepared := &agentlaunch.PreparedLaunch{
		PlantedBootDir: "/boot",
		WorkspaceDir:   "/ws",
		Workdir:        "/proj",
		BootPrompt:     "SYS-PROMPT",
		BootContent:    "TASK-BODY",
		Argv:           []string{"claude"},
		PlantContext: agentlaunch.PreparedPlantContext{
			AgentName:      "Reviewer",
			MCPLoopbackURL: "http://127.0.0.1:9000/mcp",
			MuxCommand:     "/usr/bin/mux",
			MuxArgs:        []string{"mcp", "--proxy"},
			MuxEnv:         map[string]string{"TOKEN": "t", "ALPHA": "a"},
		},
	}
	pc := PlantContextFor(prepared)

	if pc.SystemPrompt != "SYS-PROMPT" {
		t.Errorf("SystemPrompt = %q", pc.SystemPrompt)
	}
	if pc.BootContent != "TASK-BODY" {
		t.Errorf("BootContent = %q", pc.BootContent)
	}
	if pc.AgentName != "Reviewer" {
		t.Errorf("AgentName = %q", pc.AgentName)
	}
	if pc.ProjectDir != "/proj" {
		t.Errorf("ProjectDir = %q, want /proj (Workdir)", pc.ProjectDir)
	}
	if pc.BootDir != "/boot" {
		t.Errorf("BootDir = %q", pc.BootDir)
	}
	if pc.MCPLoopbackURL != "http://127.0.0.1:9000/mcp" {
		t.Errorf("MCPLoopbackURL = %q", pc.MCPLoopbackURL)
	}
	// MuxEnv flattens map → sorted KEY=VALUE slice.
	if want := []string{"ALPHA=a", "TOKEN=t"}; !slices.Equal(pc.MuxEnv, want) {
		t.Errorf("MuxEnv = %v, want %v", pc.MuxEnv, want)
	}
}

// TestPlantContextFor_BootContentFallback proves an empty BootContent
// falls back to BootPrompt (mirrors go-agent-sessions back-compat).
func TestPlantContextFor_BootContentFallback(t *testing.T) {
	prepared := &agentlaunch.PreparedLaunch{
		BootPrompt: "ONLY-PROMPT",
		Argv:       []string{"claude"},
	}
	if pc := PlantContextFor(prepared); pc.BootContent != "ONLY-PROMPT" {
		t.Errorf("BootContent = %q, want fallback to BootPrompt", pc.BootContent)
	}
}

func TestPlantContextFor_Nil(t *testing.T) {
	if pc := PlantContextFor(nil); pc.SystemPrompt != "" || pc.BootDir != "" {
		t.Errorf("PlantContextFor(nil) = %+v, want zero value", pc)
	}
}
