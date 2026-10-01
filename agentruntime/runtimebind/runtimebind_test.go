package runtimebind

import (
	"errors"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

func TestResolveDefaultsComeFromTheRegistry(t *testing.T) {
	for _, d := range registry.All() {
		b, err := Resolve(Request{Provider: string(d.ID)})
		if err != nil {
			t.Fatalf("%s: %v", d.ID, err)
		}
		if b.Provider != string(d.ID) || b.Runtime != d.DefaultMode {
			t.Errorf("%s: binding %+v, want the registry default %s", d.ID, b, d.DefaultMode)
		}
	}
}

// D-74: Codex defaults to app-server (jsonrpc-stdio), as go-agent-wrapper
// already did; subprocess-per-turn stays selectable.
func TestResolveCodexDefaultsToAppServer(t *testing.T) {
	b, err := Resolve(Request{Provider: "codex"})
	if err != nil || b.Runtime != runtimes.ModeJSONRPCStdio || !b.Managed {
		t.Fatalf("codex = %+v, %v; want jsonrpc-stdio", b, err)
	}
	b, err = Resolve(Request{Provider: "codex", RequestedRuntime: runtimes.ModeSubprocessPerTurn})
	if err != nil || b.Runtime != runtimes.ModeSubprocessPerTurn {
		t.Fatalf("codex exec = %+v, %v", b, err)
	}
}

func TestResolveAliasesOverridesAndPosture(t *testing.T) {
	claude, err := Resolve(Request{Provider: "Claude-Code"})
	if err != nil || claude.Provider != "claude" || claude.Runtime != runtimes.ModeStreamingStdio {
		t.Fatalf("claude-code = %+v, %v", claude, err)
	}
	over, err := Resolve(Request{Provider: "claude", Overrides: map[runtimes.ID]runtimes.Mode{runtimes.Claude: runtimes.ModeSubprocessPerTurn}})
	if err != nil || over.Runtime != runtimes.ModeSubprocessPerTurn {
		t.Fatalf("override = %+v, %v", over, err)
	}
	debug, err := Resolve(Request{Provider: "claude", Posture: PostureDebug})
	if err != nil || debug.Runtime != runtimes.ModePTY || !debug.HumanOnly || debug.Managed {
		t.Fatalf("claude debug = %+v, %v; want a human-only PTY", debug, err)
	}
	if _, err := Resolve(Request{Provider: "claude", RequestedRuntime: runtimes.ModePTY}); !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("claude PTY without debug = %v, want ErrUnsupportedBinding", err)
	}
	if b, err := Resolve(Request{Provider: "pty-claude", RequestedRuntime: runtimes.ModePTY, AllowPTY: true}); err != nil || b.Provider != "claude" {
		t.Fatalf("pty- prefix = %+v, %v", b, err)
	}
}

func TestResolveRejectsUnsupportedModes(t *testing.T) {
	for _, c := range []struct {
		provider string
		mode     runtimes.Mode
	}{
		// agy's stream-json stdin does not report turn ends reliably.
		{"agy", runtimes.ModeStreamingStdio},
		{"claude", runtimes.ModeJSONRPCStdio},
		{"copilot", runtimes.ModeSubprocessPerTurn},
		{"claude", "subprocess"}, // the old spelling is not a mode
	} {
		if _, err := Resolve(Request{Provider: c.provider, RequestedRuntime: c.mode}); !errors.Is(err, ErrUnsupportedBinding) {
			t.Errorf("%s/%s = %v, want ErrUnsupportedBinding", c.provider, c.mode, err)
		}
	}
}

// ACP is a mode like any other: Copilot and Pi resolve to it by default, and
// a runtime with a native default can still be bound to ACP explicitly.
func TestResolveACP(t *testing.T) {
	for _, name := range []string{"copilot", "pi"} {
		b, err := Resolve(Request{Provider: name})
		if err != nil || b.Runtime != runtimes.ModeACPStdio || !b.Managed {
			t.Errorf("%s = %+v, %v; want acp-stdio", name, b, err)
		}
	}
	if b, err := Resolve(Request{Provider: "copilot", RequestedRuntime: runtimes.ModeACPTCP}); err != nil || b.Runtime != runtimes.ModeACPTCP {
		t.Errorf("copilot acp-tcp = %+v, %v", b, err)
	}
	if b, err := Resolve(Request{Provider: "opencode", RequestedRuntime: runtimes.ModeACPStdio}); err != nil || b.Runtime != runtimes.ModeACPStdio {
		t.Errorf("opencode acp-stdio = %+v, %v", b, err)
	}
}

func TestResolveAPI(t *testing.T) {
	for _, req := range []Request{{Provider: "api"}, {Provider: "anthropic-messages"}, {Provider: "claude", Posture: PostureAPI}} {
		b, err := Resolve(req)
		if err != nil || !b.API || b.Provider != APIProvider || b.Runtime != "" || !b.Managed {
			t.Errorf("%+v = %+v, %v; want an API binding with no mode", req, b, err)
		}
	}
	if _, err := Resolve(Request{Provider: "openai", RequestedRuntime: runtimes.ModeSubprocessPerTurn}); !errors.Is(err, ErrUnsupportedBinding) {
		t.Errorf("an API provider with a mode = %v, want ErrUnsupportedBinding", err)
	}
}

func TestResolveRejectsUnknownProviderUnlessGenericSubprocessOptIn(t *testing.T) {
	if _, err := Resolve(Request{Provider: "some-random-provider"}); !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("unknown provider err = %v, want ErrUnsupportedBinding", err)
	}
	b, err := Resolve(Request{Provider: "some-random-provider", AllowGenericSubprocess: true})
	if err != nil || b.Runtime != runtimes.ModeSubprocessPerTurn || b.Provider != "some-random-provider" {
		t.Fatalf("generic subprocess opt-in = %+v, %v", b, err)
	}
	if _, err := Resolve(Request{Provider: "some-random-provider", AllowGenericSubprocess: true, RequestedRuntime: runtimes.ModePTY}); !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("generic provider in pty = %v, want ErrUnsupportedBinding", err)
	}
}

// Acceptance (CW-20260930-0133): a runtime added to the registry resolves
// here with no edit to this package.
func TestResolveANewRegistryRuntime(t *testing.T) {
	registry.RegisterForTest(t, registry.Descriptor{
		ID:          "fake-cli",
		Aliases:     []string{"fake"},
		Binary:      "fake-cli",
		EnvOverride: "FAKE_CLI_PATH",
		Modes:       []registry.ModeSupport{{Mode: runtimes.ModeSubprocessPerTurn}, {Mode: runtimes.ModeACPStdio}},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
	})
	b, err := Resolve(Request{Provider: "fake"})
	if err != nil || b.Provider != "fake-cli" || b.Runtime != runtimes.ModeSubprocessPerTurn {
		t.Fatalf("fake = %+v, %v", b, err)
	}
	if b, err := Resolve(Request{Provider: "fake-cli", RequestedRuntime: runtimes.ModeACPStdio}); err != nil || b.Runtime != runtimes.ModeACPStdio {
		t.Fatalf("fake acp = %+v, %v", b, err)
	}
}

// The registry is consulted before the API-name heuristic: a runtime id or
// alias containing "openai" or "anthropic" is still a runtime.
func TestResolveRegistryWinsOverTheAPIHeuristic(t *testing.T) {
	registry.RegisterForTest(t, registry.Descriptor{
		ID:          "openai-codex-cli",
		Aliases:     []string{"anthropic-agent"},
		Binary:      "openai-codex-cli",
		EnvOverride: "OPENAI_CODEX_CLI_PATH",
		Modes:       []registry.ModeSupport{{Mode: runtimes.ModeSubprocessPerTurn}},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
	})
	for _, name := range []string{"openai-codex-cli", "anthropic-agent"} {
		b, err := Resolve(Request{Provider: name})
		if err != nil || b.API || b.Provider != "openai-codex-cli" || b.Runtime != runtimes.ModeSubprocessPerTurn {
			t.Errorf("%s = %+v, %v; want the registry runtime, not an API binding", name, b, err)
		}
	}
}
