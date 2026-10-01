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
// pinned CLI variant, or a custom provider not in the registry.
//
// A resolver MUST return an adapter that also implements
// provider.BootDirProvider; Plant surfaces ErrNoBootDirSpec otherwise.
type AdapterResolver func(*agentlaunch.CompiledLaunch) (provider.BootDirProvider, error)

// DefaultResolver resolves the go-providers adapter for a launch's runtime
// and mode, both checked against the go-providers registry, through
// provider.NewAdapter: go-providers' one table of native constructors. The
// adapter matches the mode, so the projection it plants and the argv it
// projects are the mode's own: streaming-stdio Claude projects
// `-p --input-format stream-json …`, not print mode's `-p <prompt>`, and
// Codex jsonrpc-stdio projects app-server (whose BootDirSpec suppresses
// --cd).
//
// The launch's permission posture is then set where the adapter takes one:
// Claude's PermissionMode (planted as permissions.defaultMode; empty leaves a
// headless claude waiting on its first approval prompt) and Codex's
// ApprovalPolicy. OpenCode's agent is AgentSpec.Name, falling back to
// AgentSpec.ID, the same precedence Prepare uses for
// PreparedPlantContext.AgentName.
//
// An ACP mode (Copilot, Pi, or ACP selected for any runtime) has no boot dir
// and returns ErrNoNativeAdapter, as does a runtime and mode with no native
// constructor. Consumers that need bare-mode Claude flag injection wire that
// at the go-agent-sessions boundary.
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
	adapter, err := provider.NewAdapter(desc.ProviderID, plan.Runtime)
	if err != nil {
		return nil, fmt.Errorf("%w: go-providers has no native adapter constructor for %s/%s (%v); pass WithAdapter or WithResolver", ErrNoNativeAdapter, desc.ProviderID, plan.Runtime, err)
	}
	switch a := adapter.(type) {
	case *provider.ClaudeAdapter:
		// plan.Provider.Permission is in claude's vocabulary
		// (default/acceptEdits/plan/bypassPermissions).
		a.PermissionMode = plan.Provider.Permission
	case *provider.CodexAdapter:
		// codex vocabulary: untrusted/on-failure/on-request/never. An empty
		// value is safe — go-providers defaults ApprovalPolicy to "never".
		a.ApprovalPolicy = plan.Provider.Permission
	case *provider.OpencodeAdapter:
		a.Agent = agentName(plan)
	}
	bootDir, ok := adapter.(provider.BootDirProvider)
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s adapter plants no boot dir", ErrNoNativeAdapter, desc.ProviderID, plan.Runtime)
	}
	return bootDir, nil
}

// agentName returns the display name the opencode adapter and the
// PlantContext renderers reference: AgentSpec.Name, then AgentSpec.ID.
func agentName(plan *agentlaunch.LaunchPlan) string {
	if plan.Agent.Name != "" {
		return plan.Agent.Name
	}
	return plan.Agent.ID
}
