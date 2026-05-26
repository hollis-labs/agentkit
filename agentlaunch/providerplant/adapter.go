package providerplant

import (
	"fmt"

	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/matrix"
)

// AdapterResolver maps a CompiledLaunch to the go-providers adapter whose
// BootDirSpec should be planted. Callers override the built-in
// DefaultResolver via WithResolver — e.g. to select bare-mode Claude, a
// pinned CLI variant, or a custom provider not in the matrix.
//
// A resolver MUST return an adapter that also implements
// provider.BootDirProvider; Plant surfaces ErrNoBootDirSpec otherwise.
type AdapterResolver func(*agentlaunch.CompiledLaunch) (provider.BootDirProvider, error)

// DefaultResolver resolves the standard adapter for a launch's
// provider×runtime pair via the matrix:
//
//   - claude   → provider.NewClaudeAdapter()
//   - codex    → provider.NewCodexAdapter(), or NewCodexAdapterAppServer()
//     for the jsonrpc-stdio runtime (the app-server daemon rejects the
//     --cd flag, so its BootDirSpec suppresses ProjectDirArg)
//   - opencode → &provider.OpencodeAdapter{Agent: <agent name>}
//
// The agent name fed to the opencode adapter is AgentSpec.Name, falling
// back to AgentSpec.ID — the same precedence Prepare uses for
// PreparedPlantContext.AgentName.
//
// DefaultResolver returns adapters in their plain (non-bare) shape: the
// planted BootDirSpec files are identical across the PTY / streaming /
// subprocess variants of a provider, so the runtime variant only needs
// to be distinguished where it changes the spec (the codex app-server
// case above). Consumers that need bare-mode Claude flag injection wire
// that at the go-agent-sessions boundary.
func DefaultResolver(compiled *agentlaunch.CompiledLaunch) (provider.BootDirProvider, error) {
	if compiled == nil || compiled.Plan == nil {
		return nil, ErrNilCompiled
	}
	plan := compiled.Plan
	desc, err := matrix.Lookup(plan.Provider, plan.Runtime)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAdapterResolution, err)
	}
	switch desc.BootDirRenderer {
	case matrix.BootDirRendererClaude:
		// plan.Provider.Permission carries the launch's permission posture
		// (claude vocabulary: default/acceptEdits/plan/bypassPermissions).
		// Threading it onto ClaudeAdapter.PermissionMode is what makes the
		// planted .claude/settings.json carry permissions.defaultMode — an
		// empty value leaves a headless claude in interactive mode and it
		// hangs on the first approval prompt.
		a := provider.NewClaudeAdapter()
		a.PermissionMode = plan.Provider.Permission
		return a, nil
	case matrix.BootDirRendererCodex:
		var a *provider.CodexAdapter
		if plan.Runtime == agentlaunch.RuntimeJsonRpcStdio {
			a = provider.NewCodexAdapterAppServer()
		} else {
			a = provider.NewCodexAdapter()
		}
		// codex vocabulary: untrusted/on-failure/on-request/never. An empty
		// value is safe — go-providers defaults ApprovalPolicy to "never".
		a.ApprovalPolicy = plan.Provider.Permission
		return a, nil
	case matrix.BootDirRendererOpencode:
		// opencode has two runtime shapes:
		//   - subprocess:  one-shot `opencode run --agent <name>` per turn
		//                  (NewOpencodeAdapter)
		//   - serve-http:  long-lived `opencode serve` child + HTTP attach
		//                  (NewOpencodeAdapterServeHTTP, go-providers v0.23.0+)
		// Both planted BootDirSpecs are identical (same opencode agent file
		// shape); only the constructor + argv shape differs. Branch here so
		// the right argv is emitted at Start time.
		var a *provider.OpencodeAdapter
		if plan.Runtime == agentlaunch.RuntimeServeHTTP {
			a = provider.NewOpencodeAdapterServeHTTP()
		} else {
			a = provider.NewOpencodeAdapter()
		}
		a.Agent = agentName(plan)
		return a, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownRenderer, desc.BootDirRenderer)
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
