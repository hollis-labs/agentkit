// Package matrix answers which (provider, runtime mode) pairs the
// agent-launch pipeline can launch, and with what binary.
//
// It holds no table of its own. The runtimes, their aliases, the modes each
// supports and each one's binary come from the go-providers runtime registry,
// over the agent-contracts-leaf runtimes vocabulary (D-73), so a runtime added
// to the registry is launchable here without an edit. What this package adds
// is agentlaunch's error vocabulary: sentinel errors a caller branches on,
// and the plan's Provider.Binary override.
package matrix

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// Sentinel errors returned by Lookup. Callers branch with errors.Is.
//
// Wrapped error messages include the offending (provider, runtime) pair
// so operators can diagnose without re-deriving the inputs.
var (
	// ErrUnsupportedCombo is returned when the registry knows the runtime
	// and the mode is a runtimes.Mode, but the runtime does not support
	// that mode.
	ErrUnsupportedCombo = errors.New("matrix: unsupported provider/runtime combination")

	// ErrUnknownProvider is returned when ProviderSpec.ID is neither a
	// runtime id nor an alias in the go-providers registry.
	ErrUnknownProvider = errors.New("matrix: unknown provider id")

	// ErrUnknownRuntime is returned when the mode is not a runtimes.Mode.
	// It is distinct from agentlaunch.ErrUnknownRuntime so callers can
	// branch on matrix sentinels uniformly.
	ErrUnknownRuntime = errors.New("matrix: unknown runtime kind")
)

// Descriptor is a legal (runtime, mode) pair as Lookup resolved it.
type Descriptor struct {
	// ProviderID is the canonical runtime id, whatever id or alias the
	// ProviderSpec used.
	ProviderID runtimes.ID

	// Runtime is the mode the runtime is launched in.
	Runtime runtimes.Mode

	// BinaryName is the executable to spawn: ProviderSpec.Binary when
	// set (verbatim, no PATH resolution here), else the registry's
	// binary name.
	BinaryName string

	// Registry is the runtime's full registry descriptor: capabilities
	// per mode, env override, layout.
	Registry registry.Descriptor
}

// Pair is a single (ProviderID, Runtime) tuple, as Supported lists them.
type Pair struct {
	ProviderID runtimes.ID
	Runtime    runtimes.Mode
}

// String returns "<provider>/<runtime>", the rendering used in error
// messages and test failure output.
func (p Pair) String() string {
	return string(p.ProviderID) + "/" + string(p.Runtime)
}

// Lookup validates that (provider, mode) is a pair the registry supports and
// returns its Descriptor. The provider is matched by id or alias, ignoring
// case.
//
// Returns ErrUnknownRuntime when mode is not a runtimes.Mode,
// ErrUnknownProvider when the registry has no such runtime, and
// ErrUnsupportedCombo when the runtime does not support the mode.
func Lookup(provider agentlaunch.ProviderSpec, mode runtimes.Mode) (Descriptor, error) {
	if !mode.Valid() {
		return Descriptor{}, fmt.Errorf("matrix: unknown runtime kind (provider=%q, runtime=%q): %w",
			provider.ID, string(mode), ErrUnknownRuntime)
	}
	d, ok := registry.Lookup(provider.ID)
	if !ok {
		return Descriptor{}, fmt.Errorf("matrix: unknown provider id (provider=%q, runtime=%q): %w",
			provider.ID, string(mode), ErrUnknownProvider)
	}
	if !d.Supports(mode) {
		return Descriptor{}, fmt.Errorf("matrix: unsupported pair (provider=%s, runtime=%s): %w",
			d.ID, string(mode), ErrUnsupportedCombo)
	}
	out := Descriptor{ProviderID: d.ID, Runtime: mode, BinaryName: d.Binary, Registry: d}
	if override := strings.TrimSpace(provider.Binary); override != "" {
		out.BinaryName = override
	}
	return out, nil
}

// LookupCase is Lookup with a bare provider id and no binary override.
func LookupCase(providerID string, mode runtimes.Mode) (Descriptor, error) {
	return Lookup(agentlaunch.ProviderSpec{ID: providerID}, mode)
}

// IsSupported reports whether (providerID, mode) is a supported pair. Callers
// that need to tell the three failures apart use Lookup.
func IsSupported(providerID string, mode runtimes.Mode) bool {
	_, err := LookupCase(providerID, mode)
	return err == nil
}

// Supported returns every supported pair: each registry runtime's modes, in
// registry order.
func Supported() []Pair {
	var out []Pair
	for _, d := range registry.All() {
		for _, ms := range d.Modes {
			out = append(out, Pair{ProviderID: d.ID, Runtime: ms.Mode})
		}
	}
	return out
}

// KnownProviders returns the canonical id of every registry runtime, in
// registry order.
func KnownProviders() []runtimes.ID {
	all := registry.All()
	out := make([]runtimes.ID, len(all))
	for i, d := range all {
		out[i] = d.ID
	}
	return out
}
