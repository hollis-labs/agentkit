package runtimebind

import (
	agentlaunch "github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
)

type CodexPolicyRequest struct {
	Runtime       agentlaunch.RuntimeKind
	Bypass        bool
	Approval      string
	Sandbox       string
	WritableRoots []string
}

type CodexPolicy struct {
	ApprovalPolicy string
	SandboxMode    string
	Env            map[string]string
	WritableRoots  []string
}

// ResolveCodexPolicy captures the shared headless Codex contract. Defaults are
// non-interactive and workspace-write. Full sandbox bypass maps to
// danger-full-access only for the explicit jsonrpc/app-server lane; subprocess
// callers must opt into the provider's normal exec-mode policy separately.
func ResolveCodexPolicy(req CodexPolicyRequest) CodexPolicy {
	approval := req.Approval
	if approval == "" {
		approval = "never"
	}
	sandbox := req.Sandbox
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	if req.Bypass && runtimekind.Parse(string(req.Runtime)) == runtimekind.JSONRPCStdio {
		sandbox = "danger-full-access"
	}
	return CodexPolicy{
		ApprovalPolicy: approval,
		SandboxMode:    sandbox,
		Env:            map[string]string{"CODEX_HOME": "{{.BootDir}}"},
		WritableRoots:  append([]string(nil), req.WritableRoots...),
	}
}
