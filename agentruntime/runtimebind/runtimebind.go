// Package runtimebind resolves provider plus requested runtime mode into a
// shared binding without imposing an app's product policy.
//
// Which runtimes exist, their aliases, the modes each supports and each one's
// default mode come from the go-providers runtime registry; this package adds
// only the policy every app shares: a raw PTY is a human/debug path, and an
// API provider is not a CLI runtime at all.
package runtimebind

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

var ErrUnsupportedBinding = errors.New("runtimebind: unsupported provider/runtime binding")

type Posture string

const (
	PostureManaged Posture = "managed"
	PostureDebug   Posture = "debug"
	PostureAPI     Posture = "api"
)

// APIProvider is the Binding.Provider of an API binding: a direct provider
// HTTP API rather than an agent CLI runtime.
const APIProvider = "api"

type Request struct {
	// Provider is a runtime id or registry alias ("claude", "claude-code",
	// "agy"), or an API provider name ("api", "anthropic", "openai").
	Provider         string
	RequestedRuntime runtimes.Mode
	Posture          Posture
	AllowPTY         bool
	// AllowGenericSubprocess admits a provider the registry does not know,
	// driven one subprocess per turn.
	AllowGenericSubprocess bool
	// Overrides replaces the registry default mode, keyed by canonical
	// runtime id, when RequestedRuntime is empty.
	Overrides map[runtimes.ID]runtimes.Mode
}

type Binding struct {
	// Provider is the canonical runtime id, APIProvider for an API
	// binding, or the normalized name of a generic subprocess provider.
	Provider string
	// Runtime is the mode the runtime is driven in. Empty for an API
	// binding.
	Runtime runtimes.Mode
	// API marks a direct provider API binding: no CLI runtime and no mode.
	API       bool
	Posture   Posture
	Managed   bool
	HumanOnly bool
	Notes     []string
}

// Resolve binds req to a runtime and mode. The mode is RequestedRuntime, else
// the caller's override, else the registry's default for the runtime (a
// debug posture prefers PTY where the runtime has one). A mode the registry
// does not list for the runtime is ErrUnsupportedBinding, and so is a raw PTY
// unless AllowPTY or the debug posture is explicit.
func Resolve(req Request) (Binding, error) {
	name := normalizeProvider(req.Provider)
	if req.Posture == PostureAPI || isAPIProvider(name) {
		if req.RequestedRuntime != "" {
			return Binding{}, fmt.Errorf("%w: API provider %q has no runtime mode, got %q", ErrUnsupportedBinding, req.Provider, req.RequestedRuntime)
		}
		return Binding{Provider: APIProvider, API: true, Posture: req.Posture, Managed: true}, nil
	}

	mode := req.RequestedRuntime
	if mode != "" && !mode.Valid() {
		return Binding{}, fmt.Errorf("%w: %q is not a runtime mode", ErrUnsupportedBinding, mode)
	}

	d, known := registry.Lookup(name)
	if !known {
		if !req.AllowGenericSubprocess {
			return Binding{}, fmt.Errorf("%w: unknown provider %q", ErrUnsupportedBinding, req.Provider)
		}
		if mode != "" && mode != runtimes.ModeSubprocessPerTurn {
			return Binding{}, fmt.Errorf("%w: generic provider %q runs only %s, got %s", ErrUnsupportedBinding, name, runtimes.ModeSubprocessPerTurn, mode)
		}
		return Binding{Provider: name, Runtime: runtimes.ModeSubprocessPerTurn, Posture: req.Posture, Managed: true}, nil
	}

	if mode == "" {
		mode = req.Overrides[d.ID]
	}
	if mode == "" {
		mode = defaultMode(d, req.Posture)
	}
	b := Binding{Provider: string(d.ID), Runtime: mode, Posture: req.Posture}
	b.HumanOnly = mode == runtimes.ModePTY
	b.Managed = !b.HumanOnly

	if !d.Supports(mode) {
		return Binding{}, fmt.Errorf("%w: %s/%s", ErrUnsupportedBinding, d.ID, mode)
	}
	if b.HumanOnly && !req.AllowPTY && req.Posture != PostureDebug {
		return Binding{}, fmt.Errorf("%w: %s/%s is TUI/debug only", ErrUnsupportedBinding, d.ID, mode)
	}
	return b, nil
}

// defaultMode is the registry's default, except that a debug posture takes
// the runtime's PTY when it has one.
func defaultMode(d registry.Descriptor, posture Posture) runtimes.Mode {
	if posture == PostureDebug && d.Supports(runtimes.ModePTY) {
		return runtimes.ModePTY
	}
	return d.DefaultMode
}

// normalizeProvider lowercases and trims name and strips the catalog
// prefixes apps put in front of a provider name.
func normalizeProvider(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	for _, p := range []string{"bootprofile:", "pty-", "api-"} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

// isAPIProvider reports whether name is a direct provider API rather than a
// CLI runtime.
func isAPIProvider(name string) bool {
	return name == APIProvider || strings.Contains(name, "anthropic") || strings.Contains(name, "openai")
}
