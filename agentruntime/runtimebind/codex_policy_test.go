package runtimebind

import (
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
)

func TestResolveCodexPolicyPinsJsonrpcSandboxBypass(t *testing.T) {
	jsonrpc := ResolveCodexPolicy(CodexPolicyRequest{Runtime: runtimekind.JSONRPCStdio, Bypass: true})
	if jsonrpc.SandboxMode != "danger-full-access" {
		t.Fatalf("jsonrpc bypass sandbox = %q", jsonrpc.SandboxMode)
	}
	if jsonrpc.ApprovalPolicy != "never" || jsonrpc.Env["CODEX_HOME"] != "{{.BootDir}}" {
		t.Fatalf("jsonrpc policy = %#v", jsonrpc)
	}

	subprocess := ResolveCodexPolicy(CodexPolicyRequest{Runtime: runtimekind.Subprocess, Bypass: true})
	if subprocess.SandboxMode != "workspace-write" {
		t.Fatalf("subprocess bypass sandbox = %q, want workspace-write", subprocess.SandboxMode)
	}
}
