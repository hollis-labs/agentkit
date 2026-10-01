package runtimebind

import (
	"fmt"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

type CodexPolicyRequest struct {
	Runtime       runtimes.Mode
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
// danger-full-access only in the app-server mode (jsonrpc-stdio), which is
// Codex's default mode (D-74), so a host that resolves Codex's default and
// asks for Bypass gets danger-full-access. Subprocess-per-turn (exec)
// callers must opt into the provider's normal exec-mode policy separately.
//
// A Runtime that is not a runtimes.Mode (the old "app-server" or
// "subprocess" spellings) is ErrUnsupportedBinding, never a silent
// workspace-write.
func ResolveCodexPolicy(req CodexPolicyRequest) (CodexPolicy, error) {
	if req.Runtime != "" && !req.Runtime.Valid() {
		return CodexPolicy{}, fmt.Errorf("%w: codex runtime %q is not a runtime mode", ErrUnsupportedBinding, req.Runtime)
	}
	approval := req.Approval
	if approval == "" {
		approval = "never"
	}
	sandbox := req.Sandbox
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	if req.Bypass && req.Runtime == runtimes.ModeJSONRPCStdio {
		sandbox = "danger-full-access"
	}
	return CodexPolicy{
		ApprovalPolicy: approval,
		SandboxMode:    sandbox,
		Env:            map[string]string{"CODEX_HOME": "{{.BootDir}}"},
		WritableRoots:  append([]string(nil), req.WritableRoots...),
	}, nil
}
