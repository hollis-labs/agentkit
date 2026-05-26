package matrix

import (
	"errors"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// TestLookupSupportedPairs walks all six legal pairs and asserts every
// Descriptor field matches the static table. The table here is
// independent of legalPairs so a copy-paste typo in matrix.go would
// surface as a test failure rather than a silent mismatch.
func TestLookupSupportedPairs(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		runtime  agentlaunch.RuntimeKind
		wantCaps Capabilities
		wantBoot BootDirRenderer
		wantBin  string
	}{
		{
			name:     "claude/subprocess",
			provider: "claude",
			runtime:  agentlaunch.RuntimeSubprocess,
			wantCaps: Capabilities{BinaryRequired: true},
			wantBoot: BootDirRendererClaude,
			wantBin:  "claude",
		},
		{
			name:     "claude/pty",
			provider: "claude",
			runtime:  agentlaunch.RuntimePTY,
			wantCaps: Capabilities{PTY: true, Resize: true, BinaryRequired: true},
			wantBoot: BootDirRendererClaude,
			wantBin:  "claude",
		},
		{
			name:     "claude/streaming-stdio",
			provider: "claude",
			runtime:  agentlaunch.RuntimeStreamingStdio,
			wantCaps: Capabilities{StreamingStdio: true, BinaryRequired: true},
			wantBoot: BootDirRendererClaude,
			wantBin:  "claude",
		},
		{
			name:     "codex/subprocess",
			provider: "codex",
			runtime:  agentlaunch.RuntimeSubprocess,
			wantCaps: Capabilities{BinaryRequired: true},
			wantBoot: BootDirRendererCodex,
			wantBin:  "codex",
		},
		{
			name:     "codex/jsonrpc-stdio",
			provider: "codex",
			runtime:  agentlaunch.RuntimeJsonRpcStdio,
			wantCaps: Capabilities{JsonRpcStdio: true, BinaryRequired: true},
			wantBoot: BootDirRendererCodex,
			wantBin:  "codex",
		},
		{
			name:     "opencode/subprocess",
			provider: "opencode",
			runtime:  agentlaunch.RuntimeSubprocess,
			wantCaps: Capabilities{BinaryRequired: true},
			wantBoot: BootDirRendererOpencode,
			wantBin:  "opencode",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := Lookup(agentlaunch.ProviderSpec{ID: tc.provider}, tc.runtime)
			if err != nil {
				t.Fatalf("Lookup(%q,%q) returned unexpected error: %v", tc.provider, tc.runtime, err)
			}
			if got.ProviderID != tc.provider {
				t.Errorf("ProviderID = %q, want %q", got.ProviderID, tc.provider)
			}
			if got.Runtime != tc.runtime {
				t.Errorf("Runtime = %q, want %q", got.Runtime, tc.runtime)
			}
			if got.Caps != tc.wantCaps {
				t.Errorf("Caps = %+v, want %+v", got.Caps, tc.wantCaps)
			}
			if got.BootDirRenderer != tc.wantBoot {
				t.Errorf("BootDirRenderer = %q, want %q", got.BootDirRenderer, tc.wantBoot)
			}
			if got.BinaryName != tc.wantBin {
				t.Errorf("BinaryName = %q, want %q", got.BinaryName, tc.wantBin)
			}
		})
	}
}

// TestLookupUnsupportedPairs covers (provider, runtime) combinations
// where both inputs are individually known but the pair has no adapter.
// All cases must wrap ErrUnsupportedCombo (errors.Is checks).
func TestLookupUnsupportedPairs(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		runtime  agentlaunch.RuntimeKind
	}{
		{"claude/jsonrpc-stdio", "claude", agentlaunch.RuntimeJsonRpcStdio},
		{"codex/pty", "codex", agentlaunch.RuntimePTY},
		{"codex/streaming-stdio", "codex", agentlaunch.RuntimeStreamingStdio},
		{"opencode/pty", "opencode", agentlaunch.RuntimePTY},
		{"opencode/streaming-stdio", "opencode", agentlaunch.RuntimeStreamingStdio},
		{"opencode/jsonrpc-stdio", "opencode", agentlaunch.RuntimeJsonRpcStdio},
		{"claude lowercase explicit", "claude", agentlaunch.RuntimeJsonRpcStdio},
		{"claude mixed case unsupported", "Claude", agentlaunch.RuntimeJsonRpcStdio},
		{"codex uppercase unsupported", "CODEX", agentlaunch.RuntimePTY},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := Lookup(agentlaunch.ProviderSpec{ID: tc.provider}, tc.runtime)
			if err == nil {
				t.Fatalf("Lookup(%q,%q) returned nil error; want ErrUnsupportedCombo", tc.provider, tc.runtime)
			}
			if !errors.Is(err, ErrUnsupportedCombo) {
				t.Fatalf("Lookup(%q,%q) error = %v; want errors.Is(..., ErrUnsupportedCombo)", tc.provider, tc.runtime, err)
			}
		})
	}
}

// TestLookupCaseInsensitive verifies provider ID casing is normalized.
// "CLAUDE" / "Claude" / "  claude  " all resolve identically.
func TestLookupCaseInsensitive(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"upper", "CLAUDE"},
		{"title", "Claude"},
		{"mixed", "ClAuDe"},
		{"padded", "  claude  "},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := LookupCase(tc.in, agentlaunch.RuntimePTY)
			if err != nil {
				t.Fatalf("LookupCase(%q, PTY) returned unexpected error: %v", tc.in, err)
			}
			if got.ProviderID != ProviderClaude {
				t.Errorf("ProviderID = %q, want %q", got.ProviderID, ProviderClaude)
			}
			if !got.Caps.PTY {
				t.Errorf("Caps.PTY = false, want true")
			}
			if got.BootDirRenderer != BootDirRendererClaude {
				t.Errorf("BootDirRenderer = %q, want %q", got.BootDirRenderer, BootDirRendererClaude)
			}
		})
	}
}

// TestLookupUnknownProvider verifies that a provider ID outside the
// known set returns ErrUnknownProvider, regardless of whether the
// runtime is otherwise valid.
func TestLookupUnknownProvider(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		runtime  agentlaunch.RuntimeKind
	}{
		{"foo+subprocess", "foo", agentlaunch.RuntimeSubprocess},
		{"gpt+pty", "gpt", agentlaunch.RuntimePTY},
		{"empty id+subprocess", "", agentlaunch.RuntimeSubprocess},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := Lookup(agentlaunch.ProviderSpec{ID: tc.provider}, tc.runtime)
			if err == nil {
				t.Fatalf("Lookup(%q,%q) returned nil; want ErrUnknownProvider", tc.provider, tc.runtime)
			}
			if !errors.Is(err, ErrUnknownProvider) {
				t.Fatalf("Lookup(%q,%q) error = %v; want errors.Is(..., ErrUnknownProvider)", tc.provider, tc.runtime, err)
			}
		})
	}
}

// TestLookupUnknownRuntime verifies an unknown / zero-value RuntimeKind
// short-circuits to ErrUnknownRuntime before the provider check.
func TestLookupUnknownRuntime(t *testing.T) {
	cases := []struct {
		name    string
		runtime agentlaunch.RuntimeKind
	}{
		{"empty", agentlaunch.RuntimeKind("")},
		{"unknown token", agentlaunch.RuntimeKind("websocket")},
		{"trailing space", agentlaunch.RuntimeKind("pty ")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := Lookup(agentlaunch.ProviderSpec{ID: "claude"}, tc.runtime)
			if err == nil {
				t.Fatalf("Lookup(claude,%q) returned nil; want ErrUnknownRuntime", tc.runtime)
			}
			if !errors.Is(err, ErrUnknownRuntime) {
				t.Fatalf("Lookup(claude,%q) error = %v; want errors.Is(..., ErrUnknownRuntime)", tc.runtime, err)
			}
		})
	}
}

// TestLookupBinaryOverride verifies ProviderSpec.Binary overrides the
// adapter default verbatim.
func TestLookupBinaryOverride(t *testing.T) {
	spec := agentlaunch.ProviderSpec{
		ID:     "claude",
		Binary: "/opt/custom/claude-canary",
	}
	got, err := Lookup(spec, agentlaunch.RuntimePTY)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BinaryName != "/opt/custom/claude-canary" {
		t.Errorf("BinaryName = %q, want override value", got.BinaryName)
	}
}

// TestLookupBinaryOverrideWhitespace verifies whitespace-only Binary
// overrides are treated as empty and fall back to the default name.
func TestLookupBinaryOverrideWhitespace(t *testing.T) {
	spec := agentlaunch.ProviderSpec{ID: "claude", Binary: "   "}
	got, err := Lookup(spec, agentlaunch.RuntimePTY)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BinaryName != "claude" {
		t.Errorf("BinaryName = %q, want %q", got.BinaryName, "claude")
	}
}

// TestSupportedShape verifies Supported() lists exactly the seven legal
// pairs and that each is reported as supported by IsSupported.
//
// opencode/serve-http added in v0.4.0 alongside go-agent-sessions v0.10.0
// (serve_http_session.go) + go-providers v0.23.0 (NewOpencodeAdapterServeHTTP)
// for V2-pipeline long-lived opencode workers.
func TestSupportedShape(t *testing.T) {
	pairs := Supported()
	if len(pairs) != 7 {
		t.Fatalf("Supported() returned %d pairs; want 7", len(pairs))
	}
	for _, p := range pairs {
		if !IsSupported(p.ProviderID, p.Runtime) {
			t.Errorf("Supported pair %s reported as IsSupported=false", p)
		}
	}
	// Confirm the set is exactly the documented seven (order-independent).
	want := map[string]bool{
		"claude/subprocess":      false,
		"claude/pty":             false,
		"claude/streaming-stdio": false,
		"codex/subprocess":       false,
		"codex/jsonrpc-stdio":    false,
		"opencode/subprocess":    false,
		"opencode/serve-http":    false,
	}
	for _, p := range pairs {
		key := p.String()
		seen, known := want[key]
		if !known {
			t.Errorf("Supported() returned unexpected pair %q", key)
			continue
		}
		if seen {
			t.Errorf("Supported() returned pair %q twice", key)
		}
		want[key] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("Supported() missing expected pair %q", k)
		}
	}
}

// TestIsSupportedNegative covers IsSupported's negative path: unknown
// providers, unknown runtimes, and unsupported-but-known pairs all
// return false. (Lookup is the surface that distinguishes the three.)
func TestIsSupportedNegative(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		runtime  agentlaunch.RuntimeKind
	}{
		{"unknown provider", "foo", agentlaunch.RuntimeSubprocess},
		{"unknown runtime", "claude", agentlaunch.RuntimeKind("websocket")},
		{"unsupported pair", "opencode", agentlaunch.RuntimePTY},
		{"empty provider", "", agentlaunch.RuntimeSubprocess},
		{"empty runtime", "claude", agentlaunch.RuntimeKind("")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if IsSupported(tc.provider, tc.runtime) {
				t.Fatalf("IsSupported(%q,%q) = true; want false", tc.provider, tc.runtime)
			}
		})
	}
}

// TestBootDirRendererValid verifies the typed-string validator.
func TestBootDirRendererValid(t *testing.T) {
	if !BootDirRendererClaude.Valid() {
		t.Errorf("BootDirRendererClaude.Valid() = false")
	}
	if !BootDirRendererCodex.Valid() {
		t.Errorf("BootDirRendererCodex.Valid() = false")
	}
	if !BootDirRendererOpencode.Valid() {
		t.Errorf("BootDirRendererOpencode.Valid() = false")
	}
	if BootDirRenderer("").Valid() {
		t.Errorf("BootDirRenderer(\"\").Valid() = true; want false")
	}
	if BootDirRenderer("anthropic").Valid() {
		t.Errorf("BootDirRenderer(\"anthropic\").Valid() = true; want false")
	}
}

// TestPairString verifies the Pair stringer matches the documented format.
func TestPairString(t *testing.T) {
	p := Pair{ProviderID: "claude", Runtime: agentlaunch.RuntimePTY}
	if got, want := p.String(), "claude/pty"; got != want {
		t.Errorf("Pair.String() = %q, want %q", got, want)
	}
}

// TestKnownProviders verifies the convenience accessor is stable.
func TestKnownProviders(t *testing.T) {
	want := []string{"claude", "codex", "opencode"}
	got := KnownProviders()
	if len(got) != len(want) {
		t.Fatalf("KnownProviders() length = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("KnownProviders()[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestKnownRuntimes verifies the matrix re-exports the five runtime kinds.
// serve-http added in v0.4.0.
func TestKnownRuntimes(t *testing.T) {
	got := KnownRuntimes()
	if len(got) != 5 {
		t.Fatalf("KnownRuntimes() length = %d, want 5", len(got))
	}
	for _, r := range got {
		if !r.Valid() {
			t.Errorf("KnownRuntimes() contained invalid runtime %q", r)
		}
	}
}
