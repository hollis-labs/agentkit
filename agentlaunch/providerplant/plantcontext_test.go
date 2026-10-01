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
			SelfMCPCommand: "/usr/bin/mux",
			SelfMCPArgs:    []string{"mcp", "--proxy"},
			SelfMCPEnv:     map[string]string{"TOKEN": "t", "ALPHA": "a"},
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

// CW-20260930-0136 W4a: MCP servers reach go-providers' PlantContext, URL as
// HTTPURL and env flattened and sorted.
func TestPlantContextFor_MCPServers(t *testing.T) {
	prepared := &agentlaunch.PreparedLaunch{PlantContext: agentlaunch.PreparedPlantContext{
		MCPLoopbackURL: "http://127.0.0.1:7000/mcp",
		MCPServers: []agentlaunch.MCPServerSpec{
			{Name: "hadron", URL: "http://127.0.0.1:7777/mcp"},
			{Name: "nanite", Command: "/bin/nanite", Args: []string{"mcp"}, Env: map[string]string{"B": "2", "A": "1"}},
		},
	}}
	pc := PlantContextFor(prepared)
	if pc.MCPLoopbackURL != "http://127.0.0.1:7000/mcp" {
		t.Errorf("MCPLoopbackURL = %q", pc.MCPLoopbackURL)
	}
	if len(pc.MCPServers) != 2 {
		t.Fatalf("MCPServers = %+v", pc.MCPServers)
	}
	if h := pc.MCPServers[0]; h.Name != "hadron" || h.HTTPURL != "http://127.0.0.1:7777/mcp" || h.Command != "" {
		t.Errorf("http server = %+v", h)
	}
	if n := pc.MCPServers[1]; n.Command != "/bin/nanite" || !slices.Equal(n.Args, []string{"mcp"}) || !slices.Equal(n.Env, []string{"A=1", "B=2"}) {
		t.Errorf("stdio server = %+v", n)
	}
	if PlantContextFor(&agentlaunch.PreparedLaunch{}).MCPServers != nil {
		t.Error("no servers should plant nil, keeping configs byte-identical")
	}
}
