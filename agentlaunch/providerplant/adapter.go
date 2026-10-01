package providerplant

import (
	"fmt"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/matrix"
)

// AdapterResolver maps a CompiledLaunch to the go-providers adapter whose
// BootDirSpec should be planted. Callers override the built-in
// DefaultResolver via WithResolver — e.g. to select bare-mode Claude, a
// pinned CLI variant, or a custom provider not in the registry.
//
// A resolver MUST return an adapter that also implements
// provider.BootDirProvider; Plant surfaces ErrNoBootDirSpec otherwise.
type AdapterResolver func(*agentlaunch.CompiledLaunch) (provider.BootDirProvider, error)

// DefaultResolver resolves the go-providers adapter for a launch's runtime
// and mode, both checked against the go-providers registry:
//
//   - claude   → provider.NewClaudeAdapter()
//   - codex    → provider.NewCodexAdapter(), or NewCodexAdapterAppServer()
//     in jsonrpc-stdio (the app-server daemon rejects --cd, so its
//     BootDirSpec suppresses ProjectDirArg)
//   - opencode → provider.NewOpencodeAdapter(), or
//     NewOpencodeAdapterServeHTTP() in http-sse, with Agent set
//   - antigravity → provider.NewAntigravityAdapter()
//
// An ACP mode (Copilot, Pi, or ACP selected for any runtime) has no boot dir
// and returns ErrNoNativeAdapter. So does a registry runtime this switch does
// not build: constructing a native adapter needs that adapter's own fields,
// so a new native runtime still needs a case here (CW-20260930-0134).
//
// The agent name fed to the opencode adapter is AgentSpec.Name, falling
// back to AgentSpec.ID — the same precedence Prepare uses for
// PreparedPlantContext.AgentName.
//
// DefaultResolver returns adapters in their plain (non-bare) shape: the
// planted BootDirSpec files are identical across the PTY / streaming /
// subprocess variants of a provider, so the runtime mode only needs to be
// distinguished where it changes the spec (the codex app-server case
// above). Consumers that need bare-mode Claude flag injection wire that at
// the go-agent-sessions boundary.
func DefaultResolver(compiled *agentlaunch.CompiledLaunch) (provider.BootDirProvider, error) {
	if compiled == nil || compiled.Plan == nil {
		return nil, ErrNilCompiled
	}
	plan := compiled.Plan
	desc, err := matrix.Lookup(plan.Provider, plan.Runtime)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAdapterResolution, err)
	}
	if plan.Runtime.ACP() {
		return nil, fmt.Errorf("%w: %s/%s runs over ACP and has no boot dir to plant", ErrNoNativeAdapter, desc.ProviderID, plan.Runtime)
	}
	switch desc.ProviderID {
	case runtimes.Claude:
		// plan.Provider.Permission carries the launch's permission posture
		// (claude vocabulary: default/acceptEdits/plan/bypassPermissions).
		// Threading it onto ClaudeAdapter.PermissionMode is what makes the
		// planted .claude/settings.json carry permissions.defaultMode — an
		// empty value leaves a headless claude in interactive mode and it
		// hangs on the first approval prompt.
		a := provider.NewClaudeAdapter()
		a.PermissionMode = plan.Provider.Permission
		return a, nil
	case runtimes.Codex:
		var a *provider.CodexAdapter
		if plan.Runtime == runtimes.ModeJSONRPCStdio {
			a = provider.NewCodexAdapterAppServer()
		} else {
			a = provider.NewCodexAdapter()
		}
		// codex vocabulary: untrusted/on-failure/on-request/never. An empty
		// value is safe — go-providers defaults ApprovalPolicy to "never".
		a.ApprovalPolicy = plan.Provider.Permission
		return a, nil
	case runtimes.OpenCode:
		// Both planted BootDirSpecs are identical (same opencode agent file
		// shape); only the constructor + argv shape differs between
		// `opencode run` per turn and the long-lived `opencode serve`.
		var a *provider.OpencodeAdapter
		if plan.Runtime == runtimes.ModeHTTPSSE {
			a = provider.NewOpencodeAdapterServeHTTP()
		} else {
			a = provider.NewOpencodeAdapter()
		}
		a.Agent = agentName(plan)
		return a, nil
	case runtimes.Antigravity:
		// The planted spec does not depend on model or permission; those
		// are per-turn argv owned by the consumer's runtime adapter.
		return provider.NewAntigravityAdapter(), nil
	default:
		// The registry knows the runtime, but building its go-providers
		// adapter is still a per-runtime edit here (CW-20260930-0134).
		return nil, fmt.Errorf("%w: DefaultResolver has no native adapter constructor for %s (CW-20260930-0134); pass WithAdapter or WithResolver", ErrNoNativeAdapter, desc.ProviderID)
	}
}

// agentName returns the display name the opencode adapter and the
// PlantContext renderers reference: AgentSpec.Name, then AgentSpec.ID.
func agentName(plan *agentlaunch.LaunchPlan) string {
	if plan.Agent.Name != "" {
		return plan.Agent.Name
	}
	return plan.Agent.ID
}
