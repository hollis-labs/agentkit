// Package matrix owns the provider × runtime support matrix for the
// agent-launch pipeline. It enumerates the legal (ProviderSpec.ID,
// RuntimeKind) pairs the library currently knows how to launch, exposes
// a small Lookup facade that maps a chosen pair to a NATIVE Descriptor
// (capability shape, bootdir-renderer key, default binary name), and
// reports unsupported / unknown inputs through sentinel errors.
//
// # Live coupling to go-providers and go-agent-sessions
//
// The legal-pairs list defined here is the source of truth for the
// agentlaunch package, but the actual adapter implementations live in
// github.com/hollis-labs/go-providers (CLI adapters + PTY adapters +
// bootdir renderers) and the lifecycle shapes are realized by the
// sibling agentkit/agentsessions package (Capabilities). When a new
// provider lands in go-providers, OR a new runtime kind is wired into
// an existing provider's adapter set, THIS FILE must be updated to
// match; otherwise the catalog and matrix will disagree with the
// runtime substrate at Prepare time.
//
// The Descriptor returned by Lookup deliberately uses NATIVE types only
// (a local Capabilities struct, a typed-string BootDirRenderer key).
// CW-0005's Prepare-stage shim translates these to the concrete
// agentsessions.Capabilities and provider.PlantClaude / PlantCodex /
// PlantOpencode renderers; the matrix package does not import
// go-providers or go-agent-sessions and therefore cannot be pulled into
// dependency cycles by consumers of agentlaunch.
//
// # Current legal pairs
//
//   - claude × subprocess        — no caps, claude CLI subprocess-per-turn
//   - claude × pty               — Caps.PTY,            claude PTY runtime
//   - claude × streaming-stdio   — Caps.StreamingStdio, claude NDJSON stdio
//   - codex  × subprocess        — no caps, codex CLI subprocess-per-turn
//   - codex  × jsonrpc-stdio     — Caps.JsonRpcStdio,   codex JSON-RPC stdio
//   - opencode × subprocess      — no caps, opencode CLI subprocess-per-turn
//
// All other combinations (claude × jsonrpc-stdio, codex × pty,
// opencode × pty, opencode × streaming-stdio, etc.) return
// ErrUnsupportedCombo.
package matrix

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// Sentinel errors returned by Lookup. Callers branch with errors.Is.
//
// Wrapped error messages include the offending (provider, runtime) pair
// so operators can diagnose without re-deriving the inputs.
var (
	// ErrUnsupportedCombo is returned when the (ProviderID, Runtime) pair
	// is not in the legal-pairs list — both inputs are individually
	// recognized, but the combination has no adapter wired up yet.
	ErrUnsupportedCombo = errors.New("matrix: unsupported provider/runtime combination")

	// ErrUnknownProvider is returned when ProviderSpec.ID (normalized to
	// lowercase) is not one of the three provider IDs the matrix knows:
	// "claude", "codex", "opencode". This is the matrix's own
	// catalog-of-providers check; ProviderSpec.Validate only enforces
	// non-empty ID. Catalog port (CW-0003) may impose a different /
	// stricter check at parse time.
	ErrUnknownProvider = errors.New("matrix: unknown provider id")

	// ErrUnknownRuntime is returned when the supplied RuntimeKind fails
	// agentlaunch.RuntimeKind.Valid (i.e. is not one of the four declared
	// constants). The matrix re-checks runtime validity locally instead of
	// re-exporting agentlaunch.ErrUnknownRuntime so callers can branch on
	// matrix.* sentinels uniformly; the relationship is documented but the
	// sentinels are distinct values.
	ErrUnknownRuntime = errors.New("matrix: unknown runtime kind")
)

// Known provider IDs the matrix recognizes. The matrix is the
// portfolio-wide source of truth for "which provider IDs the library
// understands"; it is intentionally a small, hand-maintained set rather
// than a registry lookup so unknown IDs surface as compile-time test
// failures when this file changes.
const (
	ProviderClaude   = "claude"
	ProviderCodex    = "codex"
	ProviderOpencode = "opencode"
)

// Pair is a single (ProviderID, Runtime) tuple in the legal-pairs list.
// Returned by Supported as a static, ordered slice for documentation and
// test introspection.
type Pair struct {
	// ProviderID is the provider's stable identifier, lowercased
	// (e.g. "claude", "codex", "opencode").
	ProviderID string

	// Runtime is the agentlaunch.RuntimeKind paired with ProviderID.
	Runtime agentlaunch.RuntimeKind
}

// String returns "<provider>/<runtime>" — a stable, low-noise rendering
// used in error messages and test failure output.
func (p Pair) String() string {
	return p.ProviderID + "/" + string(p.Runtime)
}

// entry is the static row in the legal-pairs table. Lookup composes a
// public Descriptor from the matched entry plus the caller-supplied
// ProviderSpec (for Binary overrides).
type entry struct {
	provider     string
	runtime      agentlaunch.RuntimeKind
	caps         Capabilities
	bootRenderer BootDirRenderer
	binary       string
}

// legalPairs is the single source of truth for which (provider, runtime)
// pairs the library currently supports. Order is stable for tests and
// human inspection.
//
// LIVE ASSUMPTION: when a new provider/runtime adapter ships in
// go-providers (e.g. opencode gains PTY), append the row HERE and the
// matrix is implicitly updated everywhere. See the package godoc for
// the coupling rationale.
var legalPairs = []entry{
	{
		provider:     ProviderClaude,
		runtime:      agentlaunch.RuntimeSubprocess,
		caps:         Capabilities{BinaryRequired: true},
		bootRenderer: BootDirRendererClaude,
		binary:       "claude",
	},
	{
		provider:     ProviderClaude,
		runtime:      agentlaunch.RuntimePTY,
		caps:         Capabilities{PTY: true, Resize: true, BinaryRequired: true},
		bootRenderer: BootDirRendererClaude,
		binary:       "claude",
	},
	{
		provider:     ProviderClaude,
		runtime:      agentlaunch.RuntimeStreamingStdio,
		caps:         Capabilities{StreamingStdio: true, BinaryRequired: true},
		bootRenderer: BootDirRendererClaude,
		binary:       "claude",
	},
	{
		provider:     ProviderCodex,
		runtime:      agentlaunch.RuntimeSubprocess,
		caps:         Capabilities{BinaryRequired: true},
		bootRenderer: BootDirRendererCodex,
		binary:       "codex",
	},
	{
		provider:     ProviderCodex,
		runtime:      agentlaunch.RuntimeJsonRpcStdio,
		caps:         Capabilities{JsonRpcStdio: true, BinaryRequired: true},
		bootRenderer: BootDirRendererCodex,
		binary:       "codex",
	},
	{
		provider:     ProviderOpencode,
		runtime:      agentlaunch.RuntimeSubprocess,
		caps:         Capabilities{BinaryRequired: true},
		bootRenderer: BootDirRendererOpencode,
		binary:       "opencode",
	},
	{
		// opencode × serve-http: long-lived `opencode serve` child with
		// HTTP-API attach (go-agent-sessions v0.10.0+ serve_http_session +
		// go-providers v0.23.0+ NewOpencodeAdapterServeHTTP). Added in
		// v0.4.0 for V2-pipeline multi-turn opencode workers — Copilot
		// poll loops, address-the-round revisions, etc. The subprocess
		// variant above stays for bounded mechanical tasks.
		provider:     ProviderOpencode,
		runtime:      agentlaunch.RuntimeServeHTTP,
		caps:         Capabilities{ServeHTTP: true, BinaryRequired: true},
		bootRenderer: BootDirRendererOpencode,
		binary:       "opencode",
	},
}

// knownProviders is the lowercased set of provider IDs that appear in
// the legal-pairs list. Cached at package init so the per-call hot path
// is a single map lookup.
var knownProviders = func() map[string]struct{} {
	m := make(map[string]struct{}, 3)
	for _, e := range legalPairs {
		m[e.provider] = struct{}{}
	}
	return m
}()

// Lookup validates that (provider, runtime) is a legal pair and returns
// the matrix Descriptor for it. Provider ID matching is case-insensitive
// — the input is normalized via strings.ToLower before lookup.
//
// Returns ErrUnknownRuntime when runtime.Valid() is false.
// Returns ErrUnknownProvider when the normalized provider ID is not one
// of the three known IDs ("claude", "codex", "opencode").
// Returns ErrUnsupportedCombo when both inputs are individually known
// but the pair is not in the legal-pairs list.
//
// On success, Descriptor.BinaryName takes provider.Binary as an override
// (verbatim, no PATH resolution at this layer); otherwise the adapter's
// default executable name is used.
func Lookup(provider agentlaunch.ProviderSpec, runtime agentlaunch.RuntimeKind) (Descriptor, error) {
	if !runtime.Valid() {
		return Descriptor{}, fmt.Errorf("matrix: unknown runtime kind (provider=%q, runtime=%q): %w",
			provider.ID, string(runtime), ErrUnknownRuntime)
	}

	id := strings.ToLower(strings.TrimSpace(provider.ID))
	if _, ok := knownProviders[id]; !ok {
		return Descriptor{}, fmt.Errorf("matrix: unknown provider id (provider=%q, runtime=%q): %w",
			provider.ID, string(runtime), ErrUnknownProvider)
	}

	for _, e := range legalPairs {
		if e.provider == id && e.runtime == runtime {
			d := Descriptor{
				ProviderID:      e.provider,
				Runtime:         e.runtime,
				Caps:            e.caps,
				BootDirRenderer: e.bootRenderer,
				BinaryName:      e.binary,
			}
			if override := strings.TrimSpace(provider.Binary); override != "" {
				d.BinaryName = override
			}
			return d, nil
		}
	}

	return Descriptor{}, fmt.Errorf("matrix: unsupported pair (provider=%s, runtime=%s): %w",
		id, string(runtime), ErrUnsupportedCombo)
}

// LookupCase is a convenience that accepts a bare provider ID string
// (instead of a full ProviderSpec) and forwards to Lookup. Equivalent to
// constructing ProviderSpec{ID: providerID} and calling Lookup; the
// binary defaults to the matrix's adapter default. Useful in tests and
// quick-probe consumers.
func LookupCase(providerID string, runtime agentlaunch.RuntimeKind) (Descriptor, error) {
	return Lookup(agentlaunch.ProviderSpec{ID: providerID}, runtime)
}

// IsSupported reports whether (providerID, runtime) is in the legal
// pairs list. Case-insensitive on providerID. False when either input
// is unknown or the combination is unsupported — callers that need to
// distinguish the three failure modes use Lookup.
func IsSupported(providerID string, runtime agentlaunch.RuntimeKind) bool {
	if !runtime.Valid() {
		return false
	}
	id := strings.ToLower(strings.TrimSpace(providerID))
	for _, e := range legalPairs {
		if e.provider == id && e.runtime == runtime {
			return true
		}
	}
	return false
}

// Supported returns the static legal-pairs list in declaration order.
// The returned slice is a copy — callers may sort or filter it without
// affecting the matrix's internal table. Useful for self-documenting
// the matrix in tests, docs, and admin endpoints.
func Supported() []Pair {
	out := make([]Pair, len(legalPairs))
	for i, e := range legalPairs {
		out[i] = Pair{ProviderID: e.provider, Runtime: e.runtime}
	}
	return out
}
