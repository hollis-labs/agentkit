package providerplant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

func TestDefaultResolver_Claude(t *testing.T) {
	a, err := DefaultResolver(compiledFor(t, "claude", runtimes.ModePTY))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, ok := a.(*provider.ClaudeAdapter); !ok {
		t.Errorf("got %T, want *provider.ClaudeAdapter", a)
	}
}

// TestDefaultResolver_PermissionThreading pins that LaunchPlan.Provider.Permission
// is applied onto the resolved go-providers adapter — the value plumbing that
// makes the planted boot dir carry the non-interactive approval contract.
func TestDefaultResolver_PermissionThreading(t *testing.T) {
	// claude: Provider.Permission → ClaudeAdapter.PermissionMode.
	cc := compiledFor(t, "claude", runtimes.ModePTY)
	cc.Plan.Provider.Permission = "acceptEdits"
	a, err := DefaultResolver(cc)
	if err != nil {
		t.Fatalf("resolve claude: %v", err)
	}
	claude, ok := a.(*provider.ClaudeAdapter)
	if !ok {
		t.Fatalf("got %T, want *provider.ClaudeAdapter", a)
	}
	if claude.PermissionMode != "acceptEdits" {
		t.Errorf("ClaudeAdapter.PermissionMode = %q, want acceptEdits", claude.PermissionMode)
	}

	// codex: Provider.Permission → CodexAdapter.ApprovalPolicy.
	cx := compiledFor(t, "codex", runtimes.ModeSubprocessPerTurn)
	cx.Plan.Provider.Permission = "on-request"
	c, err := DefaultResolver(cx)
	if err != nil {
		t.Fatalf("resolve codex: %v", err)
	}
	codex, ok := c.(*provider.CodexAdapter)
	if !ok {
		t.Fatalf("got %T, want *provider.CodexAdapter", c)
	}
	if codex.ApprovalPolicy != "on-request" {
		t.Errorf("CodexAdapter.ApprovalPolicy = %q, want on-request", codex.ApprovalPolicy)
	}

	// Empty Permission → the adapter field stays empty (claude: the caller
	// must set it; codex: go-providers defaults ApprovalPolicy to "never").
	empty, err := DefaultResolver(compiledFor(t, "claude", runtimes.ModePTY))
	if err != nil {
		t.Fatalf("resolve claude (empty permission): %v", err)
	}
	if claude2 := empty.(*provider.ClaudeAdapter); claude2.PermissionMode != "" {
		t.Errorf("empty Provider.Permission: PermissionMode = %q, want empty", claude2.PermissionMode)
	}
}

func TestDefaultResolver_CodexExecVsAppServer(t *testing.T) {
	exec, err := DefaultResolver(compiledFor(t, "codex", runtimes.ModeSubprocessPerTurn))
	if err != nil {
		t.Fatalf("resolve exec: %v", err)
	}
	if cx, ok := exec.(*provider.CodexAdapter); !ok || cx.Mode == "app-server" {
		t.Errorf("subprocess runtime: got %T mode=%q, want exec-mode CodexAdapter", exec, modeOf(exec))
	}

	app, err := DefaultResolver(compiledFor(t, "codex", runtimes.ModeJSONRPCStdio))
	if err != nil {
		t.Fatalf("resolve app-server: %v", err)
	}
	if cx, ok := app.(*provider.CodexAdapter); !ok || cx.Mode != "app-server" {
		t.Errorf("jsonrpc runtime: got %T mode=%q, want app-server CodexAdapter", app, modeOf(app))
	}
}

func TestDefaultResolver_Opencode(t *testing.T) {
	a, err := DefaultResolver(compiledFor(t, "opencode", runtimes.ModeSubprocessPerTurn))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	oc, ok := a.(*provider.OpencodeAdapter)
	if !ok {
		t.Fatalf("got %T, want *provider.OpencodeAdapter", a)
	}
	if oc.Agent != "agent-name" {
		t.Errorf("OpencodeAdapter.Agent = %q, want agent-name (from AgentSpec.Name)", oc.Agent)
	}
}

// An ACP-only runtime (no layout, no boot dir) has no native adapter to
// plant; the resolver says so instead of guessing.
func TestDefaultResolver_ACPOnlyHasNoNativeAdapter(t *testing.T) {
	for _, id := range []string{"copilot", "pi"} {
		if _, err := DefaultResolver(compiledFor(t, id, runtimes.ModeACPStdio)); !errors.Is(err, ErrNoNativeAdapter) {
			t.Errorf("%s: err = %v, want ErrNoNativeAdapter", id, err)
		}
	}
}

// A registry runtime with a native mode resolves in the matrix, but planting
// it still needs a constructor in go-providers' provider.NewAdapter table; the
// error says so instead of claiming the runtime is ACP-only.
func TestDefaultResolver_NewNativeRuntimeNeedsAConstructor(t *testing.T) {
	registry.RegisterForTest(t, registry.Descriptor{
		ID:          "fake-cli",
		Binary:      "fake-cli",
		EnvOverride: "FAKE_CLI_PATH",
		Modes:       []registry.ModeSupport{{Mode: runtimes.ModeSubprocessPerTurn}},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
	})
	compiled := compiledFor(t, "claude", runtimes.ModeSubprocessPerTurn)
	compiled.Plan.Provider.ID = "fake-cli"
	_, err := DefaultResolver(compiled)
	if !errors.Is(err, ErrNoNativeAdapter) || !strings.Contains(err.Error(), "no native adapter constructor") || strings.Contains(err.Error(), "ACP") {
		t.Fatalf("DefaultResolver(fake-cli) = %v, want ErrNoNativeAdapter naming the missing constructor", err)
	}
}

func TestDefaultResolver_NilCompiled(t *testing.T) {
	if _, err := DefaultResolver(nil); !errors.Is(err, ErrNilCompiled) {
		t.Fatalf("DefaultResolver(nil) err = %v, want ErrNilCompiled", err)
	}
}

// TestPlant_WithAdapterOverride proves WithAdapter bypasses resolver
// lookup — here a plain codex adapter planted for a claude launch.
func TestPlant_WithAdapterOverride(t *testing.T) {
	isolateHome(t)
	prepared, err := launcher.Prepare(context.Background(), compiledFor(t, "claude", runtimes.ModePTY))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Plant(context.Background(), prepared, WithAdapter(provider.NewCodexAdapter())); err != nil {
		t.Fatalf("plant: %v", err)
	}
	// The codex adapter's BootDirSpec was planted despite the claude plan.
	assertExists(t, prepared.PlantedBootDir, "config.toml")
}

func modeOf(a provider.BootDirProvider) string {
	if cx, ok := a.(*provider.CodexAdapter); ok {
		return cx.Mode
	}
	return ""
}
