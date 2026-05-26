// Package runtimebind resolves provider plus requested runtime into a shared
// binding without imposing an app's product policy.
package runtimebind

import (
	"errors"
	"fmt"
	"strings"

	agentlaunch "github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
)

var ErrUnsupportedBinding = errors.New("runtimebind: unsupported provider/runtime binding")

type Posture string

const (
	PostureManaged Posture = "managed"
	PostureDebug   Posture = "debug"
	PostureAPI     Posture = "api"
)

type Request struct {
	Provider               string
	RequestedRuntime       agentlaunch.RuntimeKind
	Posture                Posture
	AllowPTY               bool
	AllowGenericSubprocess bool
	Overrides              map[string]agentlaunch.RuntimeKind
}

type Binding struct {
	Provider  string
	Runtime   agentlaunch.RuntimeKind
	Posture   Posture
	Managed   bool
	HumanOnly bool
	Notes     []string
}

// Resolve applies the shared default matrix while allowing callers to override
// provider defaults. Unsupported raw PTY automation is rejected unless AllowPTY
// or debug posture is explicit.
func Resolve(req Request) (Binding, error) {
	provider := normalizeProvider(req.Provider)
	if provider == "" {
		provider = "unknown"
	}
	if override, ok := req.Overrides[provider]; ok && req.RequestedRuntime == "" {
		req.RequestedRuntime = override
	}
	if !knownProvider(provider) && !req.AllowGenericSubprocess {
		return Binding{}, fmt.Errorf("%w: unknown provider %q", ErrUnsupportedBinding, provider)
	}
	runtime := req.RequestedRuntime
	if runtime == "" || runtime == runtimekind.Unknown {
		runtime = defaultRuntime(provider, req.Posture)
	}
	runtime = runtimekind.Parse(string(runtime))

	b := Binding{Provider: provider, Runtime: runtime, Posture: req.Posture}
	b.Managed = runtimekind.IsManagedAutomation(runtime)
	b.HumanOnly = runtime == runtimekind.PTY || runtime == runtimekind.PTYDebug

	if b.HumanOnly && !req.AllowPTY && req.Posture != PostureDebug {
		return Binding{}, fmt.Errorf("%w: %s/%s is TUI/debug only", ErrUnsupportedBinding, provider, runtime)
	}
	if req.AllowGenericSubprocess && !knownProvider(provider) && runtime == runtimekind.Subprocess {
		return b, nil
	}
	if !supported(provider, runtime) {
		return Binding{}, fmt.Errorf("%w: %s/%s", ErrUnsupportedBinding, provider, runtime)
	}
	return b, nil
}

func normalizeProvider(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"bootprofile:", "pty-", "api-"} {
		s = strings.TrimPrefix(s, p)
	}
	switch {
	case strings.HasPrefix(s, "claude"):
		return "claude"
	case strings.HasPrefix(s, "codex"):
		return "codex"
	case strings.HasPrefix(s, "opencode"):
		return "opencode"
	case strings.Contains(s, "anthropic"), strings.Contains(s, "openai"):
		return "api"
	default:
		return s
	}
}

func defaultRuntime(provider string, posture Posture) agentlaunch.RuntimeKind {
	if posture == PostureAPI || provider == "api" {
		return runtimekind.API
	}
	switch provider {
	case "claude":
		if posture == PostureDebug {
			return runtimekind.PTY
		}
		return runtimekind.StreamingStdio
	case "codex":
		return runtimekind.Subprocess
	case "opencode":
		return runtimekind.Subprocess
	default:
		return runtimekind.Subprocess
	}
}

func knownProvider(provider string) bool {
	switch provider {
	case "api", "claude", "codex", "opencode":
		return true
	default:
		return false
	}
}

func supported(provider string, runtime agentlaunch.RuntimeKind) bool {
	if runtime == runtimekind.API {
		return provider == "api" || provider == "anthropic" || provider == "openai"
	}
	switch provider {
	case "claude":
		return runtime == runtimekind.StreamingStdio || runtime == runtimekind.Subprocess || runtime == runtimekind.PTY || runtime == runtimekind.PTYDebug
	case "codex":
		return runtime == runtimekind.Subprocess || runtime == runtimekind.JSONRPCStdio
	case "opencode":
		return runtime == runtimekind.Subprocess || runtime == runtimekind.ServeHTTP
	default:
		return false
	}
}
