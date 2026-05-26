package matrix

import "github.com/hollis-labs/agentkit/agentlaunch"

// KnownProviders returns the lowercased set of provider IDs the matrix
// understands, in stable order ("claude", "codex", "opencode"). The
// returned slice is a fresh copy; callers may sort or filter without
// affecting the matrix's internal state.
//
// This is the matrix's own catalog of providers — independent of the
// catalog port (CW-0003), which may apply stricter rules at parse time.
// Consumers iterating providers for admin / introspection endpoints use
// this list as the authoritative "what the matrix can launch" set.
func KnownProviders() []string {
	return []string{ProviderClaude, ProviderCodex, ProviderOpencode}
}

// KnownRuntimes returns the five runtime kinds the matrix accepts (one
// per agentlaunch.RuntimeKind constant). Note: this is the full set of
// recognized RuntimeKinds, NOT the set that has at least one legal pair
// — there is no "isolated runtime" filter here. Callers that want the
// set of runtimes with at least one supported provider can derive it
// from Supported().
func KnownRuntimes() []agentlaunch.RuntimeKind {
	return []agentlaunch.RuntimeKind{
		agentlaunch.RuntimeSubprocess,
		agentlaunch.RuntimePTY,
		agentlaunch.RuntimeStreamingStdio,
		agentlaunch.RuntimeJsonRpcStdio,
		agentlaunch.RuntimeServeHTTP,
	}
}
