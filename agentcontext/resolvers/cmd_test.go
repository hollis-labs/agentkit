package resolvers

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-shell test; skipped on Windows")
	}
}

func TestCmdResolver_Echo(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)
	r := NewCmdResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "echo",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindCmd,
				Cmd:  agentcontext.CmdSource{Run: "echo hello"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.TrimSpace(got.Content) != "hello" {
		t.Fatalf("Content: got %q", got.Content)
	}
	if !strings.HasPrefix(got.Provenance.Source, "cmd:echo") {
		t.Fatalf("Source: %q", got.Provenance.Source)
	}
	if got.Provenance.ContentHash == "" {
		t.Fatalf("ContentHash empty")
	}
}

func TestCmdResolver_NonZeroExit(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)
	r := NewCmdResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "fail",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindCmd,
				Cmd:  agentcontext.CmdSource{Run: "false"},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrCmdFailed) {
		t.Fatalf("expected ErrCmdFailed, got %v", err)
	}
}

func TestCmdResolver_Timeout(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)
	r := NewCmdResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "slow",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindCmd,
				Cmd:  agentcontext.CmdSource{Run: "sleep 2", Timeout: 50 * time.Millisecond},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrCmdTimeout) {
		t.Fatalf("expected ErrCmdTimeout, got %v", err)
	}
}

func TestCmdResolver_TimeoutClampedToMax(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)
	// caller asks 10 minutes; resolver clamps to 50ms.
	r := NewCmdResolver(WithCmdMaxTimeout(50 * time.Millisecond))
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "long",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindCmd,
				Cmd:  agentcontext.CmdSource{Run: "sleep 2", Timeout: 10 * time.Minute},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrCmdTimeout) {
		t.Fatalf("expected ErrCmdTimeout, got %v", err)
	}
}

func TestCmdResolver_EmptyRun(t *testing.T) {
	t.Parallel()
	r := NewCmdResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name:   "e",
			Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindCmd},
		},
		agentcontext.ResolverEnv{})
	if err == nil || !strings.Contains(err.Error(), "empty Run") {
		t.Fatalf("expected empty-Run error, got %v", err)
	}
}

func TestCmdResolver_WrongKind(t *testing.T) {
	t.Parallel()
	r := NewCmdResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name:   "x",
			Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindInline},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrResolverNotApplicable) {
		t.Fatalf("expected ErrResolverNotApplicable, got %v", err)
	}
}

func TestCmdResolver_DefaultTimeoutOverride(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)
	r := NewCmdResolver(WithCmdDefaultTimeout(50*time.Millisecond), WithCmdMaxTimeout(100*time.Millisecond))
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "default-timeout",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindCmd,
				Cmd:  agentcontext.CmdSource{Run: "sleep 2"}, // no per-spec timeout
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrCmdTimeout) {
		t.Fatalf("expected ErrCmdTimeout, got %v", err)
	}
}

func TestCmdResolver_StderrTailRecorded(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)
	r := NewCmdResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "stderr",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindCmd,
				Cmd:  agentcontext.CmdSource{Run: "echo oops 1>&2; exit 2"},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrCmdFailed) {
		t.Fatalf("expected ErrCmdFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestCmdResolver_Determinism(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)
	r := NewCmdResolver()
	spec := agentcontext.SlotSpec{
		Name: "d",
		Source: agentcontext.SlotSource{
			Kind: agentcontext.SlotSourceKindCmd,
			Cmd:  agentcontext.CmdSource{Run: "echo deterministic"},
		},
	}
	a, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a.Content != b.Content || a.Provenance.ContentHash != b.Provenance.ContentHash {
		t.Fatalf("determinism broken")
	}
}
