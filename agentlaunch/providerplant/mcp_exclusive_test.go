package providerplant

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/registry"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
	"github.com/hollis-labs/agentkit/agentlaunch/matrix"
)

// CW-20261001-0225: Provider.MCPExclusive keeps a launch to the MCP servers
// it plants. go-providers owns the mechanism per runtime and mode; these tests
// hold that the preparer passes the request through, refuses what cannot honor
// it, and leaves a launch that did not ask exactly as it was.

const strictMCPFlag = "--strict-mcp-config"

// prepareMCP compiles a minimal launch, sets the MCPExclusive request on the
// compiled plan (so the preparer is tested on its own, not behind Compile's
// check) and prepares it.
func prepareMCP(t *testing.T, providerID string, mode runtimes.Mode, exclusive bool, env map[string]string, opts ...Option) (*agentlaunch.PreparedExecution, error) {
	t.Helper()
	isolateHome(t)
	compiled := compiledFor(t, providerID, mode)
	compiled.Plan.Provider.MCPExclusive = exclusive
	compiled.Plan.Provider.Env = env
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("%s/%s: prepare: %v", providerID, mode, err)
	}
	return PrepareExecution(context.Background(), prepared, opts...)
}

// shape renders what a prepared launch is, with the per-run directories
// replaced, so two preparations of the same plan compare equal.
func shape(exec *agentlaunch.PreparedExecution) (argv []string, env map[string]string) {
	norm := func(s string) string {
		s = strings.ReplaceAll(s, exec.Roots.BootRoot, "<boot>")
		return strings.ReplaceAll(s, exec.Roots.ProjectRoot, "<project>")
	}
	for _, a := range exec.Bindings.Argv {
		argv = append(argv, norm(a))
	}
	env = map[string]string{}
	for k, v := range exec.Bindings.Env {
		env[k] = norm(v.Value) + "|" + v.Source
	}
	return argv, env
}

func countArg(argv []string, want string) int {
	n := 0
	for _, a := range argv {
		if a == want {
			n++
		}
	}
	return n
}

// Every supported runtime and mode either gets its mechanism or is refused,
// and a launch that does not ask is the launch it was.
func TestPrepareExecution_MCPExclusive(t *testing.T) {
	for _, pair := range matrix.Supported() {
		label := pair.String()
		t.Run(label, func(t *testing.T) {
			d, ok := registry.Lookup(string(pair.ProviderID))
			if !ok {
				t.Fatalf("%s: not in the registry", label)
			}
			how := d.MCPExclusivity(pair.Runtime)

			off, err := prepareMCP(t, string(pair.ProviderID), pair.Runtime, false, nil)
			if errors.Is(err, ErrNoNativeAdapter) {
				// An ACP mode has no boot dir to plant, asked or not. The
				// request must not make it prepare, and Compile refuses it
				// first (launcher's TestCompileMCPExclusive).
				if _, onErr := prepareMCP(t, string(pair.ProviderID), pair.Runtime, true, nil); onErr == nil {
					t.Error("a mode with no adapter prepared when asked for exclusivity")
				}
				return
			}
			if err != nil {
				t.Fatalf("not asked: %v", err)
			}
			offArgv, offEnv := shape(off)
			if countArg(offArgv, strictMCPFlag) != 0 {
				t.Errorf("a launch that did not ask carries %s: %q", strictMCPFlag, offArgv)
			}

			on, err := prepareMCP(t, string(pair.ProviderID), pair.Runtime, true, nil)
			switch how {
			case registry.MCPExclusivityNone, registry.MCPExclusivityAbsent:
				if !errors.Is(err, agentlaunch.ErrMCPExclusiveUnsupported) {
					t.Fatalf("no measured mechanism: err = %v, want ErrMCPExclusiveUnsupported", err)
				}
				// A host reads which launch it asked for and did not get.
				for _, want := range []string{string(pair.ProviderID), string(pair.Runtime)} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %q", err, want)
					}
				}
			case registry.MCPExclusivityFlag:
				if err != nil {
					t.Fatalf("asked: %v", err)
				}
				onArgv, onEnv := shape(on)
				if n := countArg(onArgv, strictMCPFlag); n != 1 {
					t.Fatalf("%s %d times in %q, want once", strictMCPFlag, n, onArgv)
				}
				// Only the flag is added, and before the prompt's "--".
				if without := slices.DeleteFunc(slices.Clone(onArgv), func(a string) bool { return a == strictMCPFlag }); !slices.Equal(without, offArgv) {
					t.Errorf("the request changed more than the flag:\n  without %q\n  not asked %q", without, offArgv)
				}
				if at, end := slices.Index(onArgv, strictMCPFlag), slices.Index(onArgv, "--"); end >= 0 && at > end {
					t.Errorf("%s after the prompt's \"--\": %q", strictMCPFlag, onArgv)
				}
				if !slices.Equal(mapPairs(onEnv), mapPairs(offEnv)) {
					t.Errorf("the request changed the environment:\n  asked %v\n  not asked %v", onEnv, offEnv)
				}
				// Every later turn carries it too, not only the first.
				turn, turnErr := on.Bindings.Launch.TurnArgv(provider.TurnInput{Prompt: "next", ResumeID: "resume-id"})
				if turnErr != nil {
					t.Fatalf("TurnArgv: %v", turnErr)
				}
				if n := countArg(turn, strictMCPFlag); n != 1 {
					t.Errorf("a later turn has %s %d times, want once: %q", strictMCPFlag, n, turn)
				}
			case registry.MCPExclusivityProjectedLayout:
				if err != nil {
					t.Fatalf("asked: %v", err)
				}
				onArgv, onEnv := shape(on)
				if !slices.Equal(onArgv, offArgv) {
					t.Errorf("a layout mode adds nothing to argv:\n  asked %q\n  not asked %q", onArgv, offArgv)
				}
				if !slices.Equal(mapPairs(onEnv), mapPairs(offEnv)) {
					t.Errorf("a layout mode adds nothing to the environment:\n  asked %v\n  not asked %v", onEnv, offEnv)
				}
				if got := on.Bindings.Env["CODEX_HOME"].Value; got != on.Roots.BootRoot {
					t.Errorf("CODEX_HOME = %q, want the planted dir %q", got, on.Roots.BootRoot)
				}
			}
		})
	}
}

func mapPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out
}

// A plan's own CODEX_HOME cannot move the config root a Codex launch is kept
// exclusive by: the launch's variable wins over the caller's.
func TestPrepareExecution_MCPExclusiveCodexHomeIsThePlantedDir(t *testing.T) {
	for _, mode := range []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeJSONRPCStdio} {
		t.Run(string(mode), func(t *testing.T) {
			exec, err := prepareMCP(t, "codex", mode, true, map[string]string{"CODEX_HOME": "/somewhere/else"})
			if err != nil {
				t.Fatal(err)
			}
			got := exec.Bindings.Env["CODEX_HOME"]
			if got.Value != exec.Roots.BootRoot || got.Source != "provider" {
				t.Errorf("CODEX_HOME = %+v, want the planted dir %q from the provider", got, exec.Roots.BootRoot)
			}
		})
	}
}

// ignoresOption is a ProjectionProvider that is not go-providers': it drops
// the MCPExclusive request. A pinned adapter or a custom resolver is how a
// launch would run non-exclusive without anyone being told.
type ignoresOption struct{ *provider.ClaudeAdapter }

func (a ignoresOption) ProviderProjection(ctx provider.PlantContext, opts provider.ProjectionOptions) (provider.ProviderProjection, error) {
	opts.MCPExclusive = false
	return a.ClaudeAdapter.ProviderProjection(ctx, opts)
}

// honorsOption passes the options through, as the built-in adapter does.
type honorsOption struct{ *provider.ClaudeAdapter }

func TestPrepareExecution_MCPExclusiveJudgesTheProjectionNotTheRequest(t *testing.T) {
	_, err := prepareMCP(t, "claude", runtimes.ModePTY, true, nil, WithAdapter(ignoresOption{provider.NewClaudeAdapterPTY()}))
	if !errors.Is(err, agentlaunch.ErrMCPExclusiveUnsupported) {
		t.Fatalf("an adapter that ignores the request: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	if !strings.Contains(err.Error(), "claude/pty") {
		t.Errorf("error %q does not name claude/pty", err)
	}
	// The same adapter is fine when nothing was asked of it.
	if _, err := prepareMCP(t, "claude", runtimes.ModePTY, false, nil, WithAdapter(ignoresOption{provider.NewClaudeAdapterPTY()})); err != nil {
		t.Errorf("an adapter that ignores a request nobody made: %v", err)
	}
	exec, err := prepareMCP(t, "claude", runtimes.ModePTY, true, nil, WithAdapter(honorsOption{provider.NewClaudeAdapterPTY()}))
	if err != nil {
		t.Fatalf("a pinned adapter that honors it: %v", err)
	}
	if n := countArg(exec.Bindings.Argv, strictMCPFlag); n != 1 {
		t.Errorf("%s %d times in %q, want once", strictMCPFlag, n, exec.Bindings.Argv)
	}
}

// legacyOnly is a provider planted from a BootDirSpec alone.
type legacyOnly struct{}

func (legacyOnly) BootDirSpec() provider.BootDirSpec { return provider.BootDirSpec{} }

func TestPrepareExecution_MCPExclusiveRefusesAProviderWithNoProjection(t *testing.T) {
	_, err := prepareMCP(t, "claude", runtimes.ModePTY, true, nil, WithAdapter(legacyOnly{}))
	if !errors.Is(err, agentlaunch.ErrMCPExclusiveUnsupported) {
		t.Fatalf("a BootDirSpec-only provider: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
}

// The launch's own variable wins over a caller's when the process environment
// is merged, so the guard is for what merges after it. It refuses a launch
// whose config root did not survive, naming the variable and where the
// surviving value came from, and holds only a mode whose exclusivity is the
// root.
func TestRequireExclusiveEnv(t *testing.T) {
	codex := agentlaunch.ProviderProjection{Provider: "codex", Runtime: runtimes.ModeSubprocessPerTurn}
	set := []provider.EnvDelta{{Name: "CODEX_HOME", Value: "/boot", Operation: provider.EnvSet, Precedence: provider.EnvProviderWins}}

	if err := requireExclusiveEnv(codex, set, map[string]agentlaunch.EnvVar{"CODEX_HOME": {Value: "/boot", Source: "provider"}}); err != nil {
		t.Errorf("the root survived: %v", err)
	}
	err := requireExclusiveEnv(codex, set, map[string]agentlaunch.EnvVar{"CODEX_HOME": {Value: "/elsewhere", Source: "posture"}})
	if !errors.Is(err, agentlaunch.ErrMCPExclusiveUnsupported) {
		t.Fatalf("a later merge moved the root: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	for _, want := range []string{"codex/subprocess-per-turn", "CODEX_HOME", "/elsewhere", "posture"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if err := requireExclusiveEnv(codex, set, map[string]agentlaunch.EnvVar{}); !errors.Is(err, agentlaunch.ErrMCPExclusiveUnsupported) {
		t.Errorf("a launch environment with no root at all: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	// A variable the launch lets the caller win is not the launch's to hold.
	callerWins := []provider.EnvDelta{{Name: "CODEX_HOME", Value: "/boot", Operation: provider.EnvSet, Precedence: provider.EnvCallerWins}}
	if err := requireExclusiveEnv(codex, callerWins, map[string]agentlaunch.EnvVar{"CODEX_HOME": {Value: "/elsewhere", Source: "caller"}}); err != nil {
		t.Errorf("a caller-wins variable: %v", err)
	}
	// A flag mode is exclusive by its argv; the environment is not its claim.
	claude := agentlaunch.ProviderProjection{Provider: "claude", Runtime: runtimes.ModePTY}
	if err := requireExclusiveEnv(claude, set, map[string]agentlaunch.EnvVar{"CODEX_HOME": {Value: "/elsewhere"}}); err != nil {
		t.Errorf("a flag mode is judged on its argv: %v", err)
	}
}

// The environment guard's own cases are above; this holds that the preparer
// runs it, and only when exclusivity was asked for. No input today makes it
// fire (the launch's variable has provider precedence and only OpenCode's
// posture sets environment), so without this a dropped call would pass every
// other test.
func TestPrepareExecution_MCPExclusiveRunsTheEnvironmentGuard(t *testing.T) {
	sentinel := errors.New("the guard ran")
	saved := checkExclusiveEnv
	checkExclusiveEnv = func(agentlaunch.ProviderProjection, []provider.EnvDelta, map[string]agentlaunch.EnvVar) error {
		return sentinel
	}
	t.Cleanup(func() { checkExclusiveEnv = saved })

	if _, err := prepareMCP(t, "codex", runtimes.ModeSubprocessPerTurn, true, nil); !errors.Is(err, sentinel) {
		t.Errorf("asked for exclusivity: err = %v, want the guard's error", err)
	}
	if _, err := prepareMCP(t, "codex", runtimes.ModeSubprocessPerTurn, false, nil); err != nil {
		t.Errorf("not asked: the guard must not run, got %v", err)
	}
}
