package runtimebind

import (
	"errors"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

func TestResolveCodexPolicyPinsJsonrpcSandboxBypass(t *testing.T) {
	jsonrpc, err := ResolveCodexPolicy(CodexPolicyRequest{Runtime: runtimes.ModeJSONRPCStdio, Bypass: true})
	if err != nil {
		t.Fatal(err)
	}
	if jsonrpc.SandboxMode != "danger-full-access" {
		t.Fatalf("jsonrpc bypass sandbox = %q", jsonrpc.SandboxMode)
	}
	if jsonrpc.ApprovalPolicy != "never" || jsonrpc.Env["CODEX_HOME"] != "{{.BootDir}}" {
		t.Fatalf("jsonrpc policy = %#v", jsonrpc)
	}

	subprocess, err := ResolveCodexPolicy(CodexPolicyRequest{Runtime: runtimes.ModeSubprocessPerTurn, Bypass: true})
	if err != nil {
		t.Fatal(err)
	}
	if subprocess.SandboxMode != "workspace-write" {
		t.Fatalf("subprocess bypass sandbox = %q, want workspace-write", subprocess.SandboxMode)
	}
}

// Codex's default mode is app-server (D-74), so a host that resolves the
// default and asks for Bypass gets danger-full-access.
func TestResolveCodexPolicyOnTheDefaultMode(t *testing.T) {
	b, err := Resolve(Request{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ResolveCodexPolicy(CodexPolicyRequest{Runtime: b.Runtime, Bypass: true})
	if err != nil || p.SandboxMode != "danger-full-access" {
		t.Fatalf("default-mode bypass = %+v, %v; want danger-full-access", p, err)
	}
}

// An old spelling must not silently fall back to workspace-write.
func TestResolveCodexPolicyRejectsOldSpellings(t *testing.T) {
	for _, old := range []runtimes.Mode{"app-server", "subprocess", "jsonrpc"} {
		if _, err := ResolveCodexPolicy(CodexPolicyRequest{Runtime: old, Bypass: true}); !errors.Is(err, ErrUnsupportedBinding) {
			t.Errorf("Runtime %q: err = %v, want ErrUnsupportedBinding", old, err)
		}
	}
}
