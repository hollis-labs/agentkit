package launcher

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// TestPrepareHookOrdering confirms the documented invocation order:
// WorkspaceHook → BootDirHook → ContextHook.
func TestPrepareHookOrdering(t *testing.T) {
	plan := validPlanForPrepare(t)
	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}

	var calls []string
	ws := func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) error {
		calls = append(calls, "workspace")
		return nil
	}
	bd := func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) error {
		calls = append(calls, "bootdir")
		return nil
	}
	ctxHook := func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) (string, error) {
		calls = append(calls, "context")
		return "", nil
	}

	_, err = Prepare(context.Background(), compiled,
		WithWorkspaceHook(ws),
		WithBootDirHook(bd),
		WithContextHook(ctxHook),
	)
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}

	want := []string{"workspace", "bootdir", "context"}
	if len(calls) != len(want) {
		t.Fatalf("call order = %v, want %v", calls, want)
	}
	for i, v := range want {
		if calls[i] != v {
			t.Fatalf("calls[%d] = %q, want %q (full=%v)", i, calls[i], v, calls)
		}
	}
}

// TestPrepareContextHookOverridesBootPrompt confirms a non-empty return
// from the context hook overrides the inline BootPrompt.
func TestPrepareContextHookOverridesBootPrompt(t *testing.T) {
	plan := validPlanForPrepare(t)
	plan.BootProfile.Inline.BootPrompt = "from-inline"

	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}

	ctxHook := func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) (string, error) {
		return "from-context-hook", nil
	}
	prepared, err := Prepare(context.Background(), compiled,
		WithContextHook(ctxHook),
	)
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	if prepared.BootPrompt != "from-context-hook" {
		t.Fatalf("BootPrompt = %q, want from-context-hook", prepared.BootPrompt)
	}
}

// TestPrepareContextHookEmptyDoesNotOverride confirms an empty return
// from the context hook preserves the inline BootPrompt.
func TestPrepareContextHookEmptyDoesNotOverride(t *testing.T) {
	plan := validPlanForPrepare(t)
	plan.BootProfile.Inline.BootPrompt = "inline-wins"

	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}
	prepared, err := Prepare(context.Background(), compiled,
		WithContextHook(func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) (string, error) {
			return "", nil
		}),
	)
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	if prepared.BootPrompt != "inline-wins" {
		t.Fatalf("BootPrompt = %q, want inline-wins", prepared.BootPrompt)
	}
}

// TestPrepareHookErrorsShortCircuit confirms a hook returning an error
// stops Prepare and the error surfaces wrapped under the expected
// prefix per hook.
func TestPrepareHookErrorsShortCircuit(t *testing.T) {
	mySentinel := errors.New("hook boom")

	cases := []struct {
		name string
		opt  PrepareOption
		want string
	}{
		{
			name: "workspace hook error",
			opt: WithWorkspaceHook(func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) error {
				return mySentinel
			}),
			want: "workspace hook:",
		},
		{
			name: "bootdir hook error",
			opt: WithBootDirHook(func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) error {
				return mySentinel
			}),
			want: "bootdir hook:",
		},
		{
			name: "context hook error",
			opt: WithContextHook(func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) (string, error) {
				return "", mySentinel
			}),
			want: "context hook:",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			plan := validPlanForPrepare(t)
			compiled, err := Compile(context.Background(), plan,
				WithNow(func() time.Time { return fixedTime }),
			)
			if err != nil {
				t.Fatalf("Compile = %v", err)
			}
			_, err = Prepare(context.Background(), compiled, tc.opt)
			if err == nil {
				t.Fatalf("Prepare = nil, want error containing %q", tc.want)
			}
			if !errors.Is(err, mySentinel) {
				t.Fatalf("Prepare err = %v, want errors.Is mySentinel", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Prepare err msg = %q, want contains %q", err.Error(), tc.want)
			}
		})
	}
}

// TestPrepareWorkspaceHookErrorStopsBeforeBootDir confirms that when
// the workspace hook fails, the bootdir hook is not invoked.
func TestPrepareWorkspaceHookErrorStopsBeforeBootDir(t *testing.T) {
	plan := validPlanForPrepare(t)
	compiled, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}

	var bootDirCalled bool
	_, err = Prepare(context.Background(), compiled,
		WithWorkspaceHook(func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) error {
			return errors.New("boom")
		}),
		WithBootDirHook(func(ctx context.Context, dir string, c *agentlaunch.CompiledLaunch) error {
			bootDirCalled = true
			return nil
		}),
	)
	if err == nil {
		t.Fatalf("Prepare = nil, want error")
	}
	if bootDirCalled {
		t.Fatalf("bootdir hook called after workspace hook errored")
	}
}
