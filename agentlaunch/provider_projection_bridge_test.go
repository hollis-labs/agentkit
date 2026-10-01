package agentlaunch

import (
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
)

// Claude print and bare are one mode with different variants; their
// artifacts must not share a group, or reconciling one prunes the other.
func TestProviderProjectionGroupIDCarriesTheVariant(t *testing.T) {
	groups := map[string]bool{}
	for _, a := range []*provider.ClaudeAdapter{{}, {Bare: true}} {
		proj, err := a.ProviderProjection(provider.PlantContext{}, provider.ProjectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		out := ProviderProjectionFromProvider(proj)
		if out.Runtime != runtimes.ModeSubprocessPerTurn {
			t.Errorf("Runtime = %q, want subprocess-per-turn", out.Runtime)
		}
		g := out.Artifacts.Entries[0].Ownership.GroupID
		groups[g] = true
		if a.Bare && !strings.HasSuffix(g, "+bare") {
			t.Errorf("bare group id %q lacks the variant", g)
		}
	}
	if len(groups) != 2 {
		t.Fatalf("print and bare share a group id: %v", groups)
	}
}

// Effects are classified and redacted by go-providers; an effect kind
// agentkit has never seen is a redacted credential.
func TestProviderProjectionEffectsUseProviderClassification(t *testing.T) {
	out := ProviderProjectionFromProvider(provider.ProviderProjection{
		Provider: runtimes.Codex,
		Mode:     runtimes.ModeJSONRPCStdio,
		Effects: []provider.ProviderEffect{
			{Kind: provider.EffectClaudeWorkspaceTrust},
			{Kind: provider.EffectCodexAuthJSON},
			{Kind: "some-future-effect"},
		},
	})
	want := []struct {
		kind     RuntimeEffectKind
		redacted bool
	}{
		{RuntimeEffectHostConfig, false},
		{RuntimeEffectCredential, true},
		{RuntimeEffectCredential, true},
	}
	for i, w := range want {
		if e := out.Effects[i]; e.Kind != w.kind || e.Redacted != w.redacted {
			t.Errorf("effect %s: kind %s redacted %v, want %s %v", e.Name, e.Kind, e.Redacted, w.kind, w.redacted)
		}
	}
}
