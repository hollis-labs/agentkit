package providerplant

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	permission "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

func preparedWithPosture(t *testing.T, providerID string, mode runtimes.Mode, posture permission.Mode) *agentlaunch.PreparedExecution {
	t.Helper()
	isolateHome(t)
	compiled := compiledWith(t, providerID, mode, agentlaunch.InjectionSpec{Args: []string{"--injected"}})
	compiled.Plan.Provider.Flags = []string{"--flag-a"}
	compiled.Plan.Provider.Permission = posture
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("%s/%s: prepare: %v", providerID, mode, err)
	}
	exec, err := PrepareExecution(context.Background(), prepared)
	if err != nil {
		t.Fatalf("%s/%s %s: PrepareExecution: %v", providerID, mode, posture, err)
	}
	return exec
}

// An explicit posture reaches the launch through the registry's Posture
// hook: its flags lead the launch's extra arguments, so the first turn and
// every later one carry them before "--" (CW-20260930-0138), and its
// environment is set over the launch's.
func TestPrepareExecution_PostureFromTheRegistry(t *testing.T) {
	for _, c := range []struct {
		provider string
		mode     runtimes.Mode
		posture  permission.Mode
	}{
		{"claude", runtimes.ModeStreamingStdio, permission.ModeAcceptEdits},
		{"claude", runtimes.ModeSubprocessPerTurn, permission.ModePlan},
		{"claude", runtimes.ModePTY, permission.ModeDefault},
		{"codex", runtimes.ModeJSONRPCStdio, permission.ModeDefault},
		{"codex", runtimes.ModeSubprocessPerTurn, permission.ModeYolo},
		{"opencode", runtimes.ModeSubprocessPerTurn, permission.ModePlan},
		{"opencode", runtimes.ModeHTTPSSE, permission.ModeAcceptEdits},
		{"antigravity", runtimes.ModeSubprocessPerTurn, permission.ModeAcceptEdits},
	} {
		label := c.provider + "/" + string(c.mode) + " " + string(c.posture)
		desc, ok := registry.Lookup(c.provider)
		if !ok {
			t.Fatalf("%s: not in the registry", c.provider)
		}
		want, err := desc.PostureFor(c.posture, c.mode)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		exec := preparedWithPosture(t, c.provider, c.mode, c.posture)

		extras := append(append(slices.Clone(want.Args), "--flag-a"), "--injected")
		if got := exec.Bindings.Launch.ExtraArgs; !slices.Equal(got, extras) {
			t.Errorf("%s: launch extra args = %q, want %q", label, got, extras)
		}
		checkFlagsBeforePrompt(t, label+" first turn", exec.Bindings.Argv, extras)
		turn, err := exec.Bindings.Launch.TurnArgv(provider.TurnInput{Prompt: "say hi", ResumeID: "resume-id"})
		if err != nil {
			t.Fatalf("%s: TurnArgv: %v", label, err)
		}
		checkFlagsBeforePrompt(t, label+" next turn", turn, extras)

		for name, value := range want.Env {
			if got := exec.Bindings.Env[name]; got.Value != value || got.Source != "posture" {
				t.Errorf("%s: env %s = %+v, want %q from the posture", label, name, got, value)
			}
		}
	}
}

// checkFlagsBeforePrompt fails unless flags appear together in argv, before
// the prompt: before "--", or before agy's inline -p=<prompt>.
func checkFlagsBeforePrompt(t *testing.T, label string, argv, flags []string) {
	t.Helper()
	at := -1
	for i := range argv {
		if i+len(flags) <= len(argv) && slices.Equal(argv[i:i+len(flags)], flags) {
			at = i
			break
		}
	}
	if at < 0 {
		t.Errorf("%s: %q not together in %q", label, flags, argv)
		return
	}
	prompt := slices.Index(argv, "--")
	if prompt < 0 {
		prompt = slices.IndexFunc(argv, func(a string) bool { return len(a) > 3 && a[:3] == "-p=" })
	}
	if prompt >= 0 && at+len(flags) > prompt {
		t.Errorf("%s: flags after the prompt starts: %q", label, argv)
	}
}

// A caller's own flag follows the posture's, so a CLI that takes the last
// value of a repeated flag honours the caller's.
func TestPrepareExecution_PostureLeadsTheCallerFlags(t *testing.T) {
	exec := preparedWithPosture(t, "claude", runtimes.ModeStreamingStdio, permission.ModePlan)
	argv := exec.Bindings.Argv
	if p, f := slices.Index(argv, "--permission-mode"), slices.Index(argv, "--flag-a"); p < 0 || f < p {
		t.Errorf("posture flag not before the caller's: %q", argv)
	}
}

// A posture set after Compile is still checked: PrepareExecution refuses one
// that is not a go-permission Mode instead of launching without it.
func TestPrepareExecution_RefusesAnInvalidPosture(t *testing.T) {
	isolateHome(t)
	compiled := compiledWith(t, "claude", runtimes.ModeStreamingStdio, agentlaunch.InjectionSpec{})
	compiled.Plan.Provider.Permission = "bypassPermissions"
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := PrepareExecution(context.Background(), prepared); !errors.Is(err, registry.ErrInvalidPosture) {
		t.Errorf("PrepareExecution = %v, want ErrInvalidPosture", err)
	}
}
