package runtimebind

import (
	"errors"
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
)

func TestResolveDefaultsAndOverrides(t *testing.T) {
	claude, err := Resolve(Request{Provider: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if claude.Runtime != runtimekind.StreamingStdio {
		t.Fatalf("claude runtime = %q", claude.Runtime)
	}

	codex, err := Resolve(Request{Provider: "codex", RequestedRuntime: runtimekind.JSONRPCStdio})
	if err != nil {
		t.Fatal(err)
	}
	if codex.Runtime != runtimekind.JSONRPCStdio {
		t.Fatalf("codex runtime = %q", codex.Runtime)
	}

	if _, err := Resolve(Request{Provider: "claude", RequestedRuntime: runtimekind.PTY}); !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("claude PTY without debug = %v, want ErrUnsupportedBinding", err)
	}
}

func TestResolveRejectsUnknownProviderUnlessGenericSubprocessOptIn(t *testing.T) {
	if _, err := Resolve(Request{Provider: "some-random-provider"}); !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("unknown provider err = %v, want ErrUnsupportedBinding", err)
	}

	b, err := Resolve(Request{Provider: "some-random-provider", AllowGenericSubprocess: true})
	if err != nil {
		t.Fatalf("generic subprocess opt-in: %v", err)
	}
	if b.Runtime != runtimekind.Subprocess {
		t.Fatalf("runtime = %q, want subprocess", b.Runtime)
	}
}

func TestResolveAntigravity(t *testing.T) {
	for _, name := range []string{"antigravity", "agy", "Antigravity-CLI"} {
		b, err := Resolve(Request{Provider: name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b.Provider != "antigravity" || b.Runtime != runtimekind.Subprocess {
			t.Fatalf("%s: binding = %+v", name, b)
		}
	}
	// stream-json stdin does not report turn ends reliably in agy 1.2.7.
	if _, err := Resolve(Request{Provider: "agy", RequestedRuntime: runtimekind.StreamingStdio}); !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("agy streaming-stdio = %v, want ErrUnsupportedBinding", err)
	}
}
