package matrix

import (
	"errors"
	"slices"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// Every pair the registry lists is launchable, with the registry's binary.
func TestLookupFollowsTheRegistry(t *testing.T) {
	pairs := Supported()
	if len(pairs) == 0 {
		t.Fatal("no supported pairs")
	}
	for _, p := range pairs {
		d, err := LookupCase(string(p.ProviderID), p.Runtime)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		reg, _ := registry.Lookup(string(p.ProviderID))
		if d.ProviderID != p.ProviderID || d.Runtime != p.Runtime || d.BinaryName != reg.Binary || d.Registry.ID != reg.ID {
			t.Errorf("%s: descriptor %+v", p, d)
		}
	}
	for _, want := range []Pair{
		{runtimes.Claude, runtimes.ModeStreamingStdio},
		{runtimes.Codex, runtimes.ModeJSONRPCStdio},
		{runtimes.OpenCode, runtimes.ModeHTTPSSE},
		{runtimes.Antigravity, runtimes.ModeSubprocessPerTurn},
		{runtimes.Copilot, runtimes.ModeACPTCP},
		{runtimes.Pi, runtimes.ModeACPStdio},
	} {
		if !slices.Contains(pairs, want) {
			t.Errorf("Supported() lacks %s", want)
		}
	}
}

func TestLookupAliasesAndCase(t *testing.T) {
	for _, name := range []string{"claude-code", "CLAUDE", " Claude "} {
		d, err := LookupCase(name, runtimes.ModeStreamingStdio)
		if err != nil || d.ProviderID != runtimes.Claude {
			t.Errorf("%q: %+v, %v", name, d, err)
		}
	}
	if d, err := LookupCase("agy", runtimes.ModeSubprocessPerTurn); err != nil || d.BinaryName != "agy" {
		t.Errorf("agy: %+v, %v", d, err)
	}
}

func TestLookupErrors(t *testing.T) {
	cases := []struct {
		provider string
		mode     runtimes.Mode
		want     error
	}{
		{"claude", runtimes.ModeJSONRPCStdio, ErrUnsupportedCombo},
		{"antigravity", runtimes.ModeStreamingStdio, ErrUnsupportedCombo},
		{"pi", runtimes.ModeSubprocessPerTurn, ErrUnsupportedCombo},
		{"gemini", runtimes.ModeSubprocessPerTurn, ErrUnknownProvider},
		{"", runtimes.ModeSubprocessPerTurn, ErrUnknownProvider},
		{"claude", "subprocess", ErrUnknownRuntime},
		{"claude", "", ErrUnknownRuntime},
	}
	for _, c := range cases {
		if _, err := LookupCase(c.provider, c.mode); !errors.Is(err, c.want) {
			t.Errorf("Lookup(%q, %q) = %v, want %v", c.provider, c.mode, err, c.want)
		}
		if IsSupported(c.provider, c.mode) {
			t.Errorf("IsSupported(%q, %q) = true", c.provider, c.mode)
		}
	}
}

func TestLookupBinaryOverride(t *testing.T) {
	d, err := Lookup(agentlaunch.ProviderSpec{ID: "codex", Binary: "  /opt/codex  "}, runtimes.ModeJSONRPCStdio)
	if err != nil || d.BinaryName != "/opt/codex" {
		t.Fatalf("override: %+v, %v", d, err)
	}
	d, err = Lookup(agentlaunch.ProviderSpec{ID: "codex", Binary: "   "}, runtimes.ModeJSONRPCStdio)
	if err != nil || d.BinaryName != "codex" {
		t.Fatalf("blank override: %+v, %v", d, err)
	}
}

func TestKnownProvidersIsTheRegistry(t *testing.T) {
	if !slices.Equal(KnownProviders(), runtimes.IDs()) {
		t.Fatalf("KnownProviders() = %v, want %v", KnownProviders(), runtimes.IDs())
	}
}

// Acceptance (CW-20260930-0133): a runtime added to the registry is
// launchable here with no edit to this package.
func TestANewRegistryRuntimeIsLaunchable(t *testing.T) {
	registry.RegisterForTest(t, registry.Descriptor{
		ID:          "fake-cli",
		Binary:      "fake-cli",
		EnvOverride: "FAKE_CLI_PATH",
		Modes:       []registry.ModeSupport{{Mode: runtimes.ModeSubprocessPerTurn}},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
	})
	d, err := LookupCase("fake-cli", runtimes.ModeSubprocessPerTurn)
	if err != nil || d.BinaryName != "fake-cli" {
		t.Fatalf("fake-cli: %+v, %v", d, err)
	}
	if !slices.Contains(KnownProviders(), "fake-cli") {
		t.Error("KnownProviders() lacks the registered runtime")
	}
}

func TestPairString(t *testing.T) {
	if got := (Pair{runtimes.Codex, runtimes.ModeJSONRPCStdio}).String(); got != "codex/jsonrpc-stdio" {
		t.Errorf("String() = %q", got)
	}
}
