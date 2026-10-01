package runtimebind

import (
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

func TestResolveCodexPolicyPinsJsonrpcSandboxBypass(t *testing.T) {
	jsonrpc := ResolveCodexPolicy(CodexPolicyRequest{Runtime: runtimes.ModeJSONRPCStdio, Bypass: true})
	if jsonrpc.SandboxMode != "danger-full-access" {
		t.Fatalf("jsonrpc bypass sandbox = %q", jsonrpc.SandboxMode)
	}
	if jsonrpc.ApprovalPolicy != "never" || jsonrpc.Env["CODEX_HOME"] != "{{.BootDir}}" {
		t.Fatalf("jsonrpc policy = %#v", jsonrpc)
	}

	subprocess := ResolveCodexPolicy(CodexPolicyRequest{Runtime: runtimes.ModeSubprocessPerTurn, Bypass: true})
	if subprocess.SandboxMode != "workspace-write" {
		t.Fatalf("subprocess bypass sandbox = %q, want workspace-write", subprocess.SandboxMode)
	}
}
