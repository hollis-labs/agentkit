package sessionshim

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/providertest"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
	"github.com/hollis-labs/agentkit/agentlaunch/providerplant"
	"github.com/hollis-labs/agentkit/agentsessions"
)

// These tests drive a prepared launch end to end (compile, Prepare,
// PrepareExecution, the shim, an agentsessions runtime) against a fake CLI
// replaying captured output, and check the argv each spawn got. They are the
// acceptance checks for CW-20260930-0135: every turn carries its own prompt
// and resume id, the provider argv is not appended twice, and streaming-stdio
// Claude gets its boot prompt on stdin.

const bootKickoff = "TASK-KICKOFF"

func preparedFor(t *testing.T, providerID string, mode runtimes.Mode, inj agentlaunch.InjectionSpec) (*agentlaunch.PreparedLaunch, *agentlaunch.PreparedExecution) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	plan := agentlaunch.LaunchPlan{
		Project:   agentlaunch.ProjectSpec{ID: "proj", Name: "Project", Root: t.TempDir()},
		Agent:     agentlaunch.AgentSpec{ID: "agent-id", Name: "agent-name"},
		Provider:  agentlaunch.ProviderSpec{ID: providerID},
		Runtime:   mode,
		Workspace: agentlaunch.WorkspaceSpec{Mode: agentlaunch.WorkspaceTemp, TempPrefix: t.TempDir()},
		BootProfile: agentlaunch.BootProfileRef{Inline: &agentlaunch.BootProfileInline{
			BootPrompt:  "PERSONA-PROMPT",
			BootContent: bootKickoff,
			BootMode:    agentlaunch.BootModePlanted,
		}},
		Injection: inj,
		Mode:      agentlaunch.LaunchInteractive,
	}
	compiled, err := launcher.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	exec, err := providerplant.PrepareExecution(context.Background(), prepared)
	if err != nil {
		t.Fatalf("PrepareExecution: %v", err)
	}
	// Confinement is not what these tests exercise.
	exec.Access.Mode = agentlaunch.AccessOptional
	return prepared, exec
}

func startSession(t *testing.T, adapter provider.CLIAdapter, caps agentsessions.Capabilities, opts agentsessions.StartOptions) agentsessions.Session {
	t.Helper()
	rt, err := agentsessions.NewFromAdapter(agentsessions.AdapterRuntimeConfig{ID: "test", Adapter: adapter, Caps: caps})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	sess, err := rt.Start(ctx, opts)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Stop(context.Background())
		_, _ = sess.Wait()
	})
	return sess
}

func waitForCalls(t *testing.T, fake *providertest.Fake, n int, ready func(providertest.Call) bool) []providertest.Call {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if calls := fake.Calls(); len(calls) >= n && ready(calls[n-1]) {
			return calls
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the fake did not reach call %d: %+v", n, fake.Calls())
	return nil
}

func countArg(argv []string, arg string) int {
	n := 0
	for _, a := range argv {
		if a == arg {
			n++
		}
	}
	return n
}

func fixtureSessionID(t *testing.T, name string) string {
	t.Helper()
	for _, l := range providertest.FixtureLines(t, name) {
		var ev struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(l, &ev) == nil && ev.SessionID != "" {
			return ev.SessionID
		}
	}
	t.Fatalf("%s: no session_id", name)
	return ""
}

// The prepared per-turn path used to reuse the first turn's frozen argv: the
// boot prompt on every turn, no --resume, and the projection appended after
// BuildArgs. Each turn now resolves the launch template for its own prompt and
// session id.
func TestPreparedPerTurnClaude_EachTurnCarriesItsPromptAndResumeID(t *testing.T) {
	_, exec := preparedFor(t, "claude", runtimes.ModeSubprocessPerTurn, agentlaunch.InjectionSpec{Args: []string{"--model", "haiku"}})
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/print_turn1"),
		providertest.Replay("claude/print_turn2_resume"),
	)
	sl, err := ToSessionLaunchFromPreparedExecution(exec)
	if err != nil {
		t.Fatal(err)
	}
	if sl.Options.Launch == nil || len(sl.Options.ExtraArgs) != 0 {
		t.Fatalf("shim: Launch %v, ExtraArgs %q; want the template and no extra args", sl.Options.Launch, sl.Options.ExtraArgs)
	}
	adapter := provider.NewClaudeAdapter()
	adapter.Binary = fake.Path
	sess := startSession(t, adapter, agentsessions.Capabilities{ProviderSessionID: true}, sl.Options)

	ctx := context.Background()
	if err := sess.SendInput(ctx, []byte("first turn")); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := sess.SendInput(ctx, []byte("second turn")); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	calls := waitForCalls(t, fake, 2, func(c providertest.Call) bool { return c.Exited })

	project := exec.Roots.ProjectRoot
	for i, want := range []struct{ prompt, resume string }{
		{"first turn", ""},
		{"second turn", fixtureSessionID(t, "claude/print_turn1.jsonl")},
	} {
		c := calls[i]
		if got, _ := c.ArgAfter("-p"); got != want.prompt {
			t.Errorf("turn %d: -p %q, want %q: %q", i+1, got, want.prompt, c.Args)
		}
		if got, _ := c.ArgAfter("--resume"); got != want.resume {
			t.Errorf("turn %d: --resume %q, want %q: %q", i+1, got, want.resume, c.Args)
		}
		if c.HasArg(bootKickoff) {
			t.Errorf("turn %d: argv still carries the boot kickoff: %q", i+1, c.Args)
		}
		for _, flag := range []string{"-p", "--output-format", "--mcp-config", "--add-dir", "--model"} {
			if n := countArg(c.Args, flag); n != 1 {
				t.Errorf("turn %d: %s appears %d times, want once: %q", i+1, flag, n, c.Args)
			}
		}
		if got, _ := c.ArgAfter("--add-dir"); got != project {
			t.Errorf("turn %d: --add-dir %q, want the project %q", i+1, got, project)
		}
		if slices.Index(c.Args, "--model") > slices.Index(c.Args, "--add-dir") {
			t.Errorf("turn %d: injection args after the variadic --add-dir: %q", i+1, c.Args)
		}
	}
}

// Streaming-stdio Claude takes its turns, the first included, as stream-json
// frames on stdin; its argv carries no prompt. The shim makes the boot
// kickoff the auto-fired first frame.
func TestPreparedStreamingClaude_BootPromptArrivesOnStdin(t *testing.T) {
	_, exec := preparedFor(t, "claude", runtimes.ModeStreamingStdio, agentlaunch.InjectionSpec{})
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume"))
	sl, err := ToSessionLaunchFromPreparedExecution(exec)
	if err != nil {
		t.Fatal(err)
	}
	if !sl.Options.AutoFireFirstTurn || sl.Options.BootMode != "" || sl.Options.BootPrompt != "" {
		t.Fatalf("shim did not move the boot prompt to the first turn: %+v", sl.Options)
	}
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = fake.Path
	startSession(t, adapter, agentsessions.Capabilities{StreamingStdio: true}, sl.Options)

	c := waitForCalls(t, fake, 1, func(c providertest.Call) bool { return len(c.Stdin) > 0 })[0]
	var frame struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(c.Stdin[0]), &frame); err != nil || frame.Type != "user" || frame.Message.Content != bootKickoff {
		t.Fatalf("first stdin line = %q, want a stream-json user frame carrying the boot kickoff", c.Stdin[0])
	}
	if !c.HasArg("--input-format") || c.HasArg(bootKickoff) || countArg(c.Args, "-p") != 1 {
		t.Errorf("argv = %q, want stream-json input mode with no prompt", c.Args)
	}
}

// The legacy PreparedLaunch shim (Tether's path) carries the same template
// and boot delivery.
func TestToSessionLaunch_StreamingClaudeUsesTheTemplate(t *testing.T) {
	prepared, _ := preparedFor(t, "claude", runtimes.ModeStreamingStdio, agentlaunch.InjectionSpec{})
	if err := providerplant.Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	sl, err := ToSessionLaunch(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if sl.Options.Launch == nil || len(sl.Options.ExtraArgs) != 0 {
		t.Fatalf("Launch %v, ExtraArgs %q; want the template and no extra args", sl.Options.Launch, sl.Options.ExtraArgs)
	}
	if !sl.Options.AutoFireFirstTurn || !strings.Contains(string(sl.Options.FirstTurnPayload), bootKickoff) {
		t.Errorf("first turn = %v %q, want the framed boot kickoff", sl.Options.AutoFireFirstTurn, sl.Options.FirstTurnPayload)
	}
}

// Copying the projected argv into ExtraArgs made the jsonrpc runtime spawn
// `codex app-server app-server` (CW-20261001-0015).
func TestPreparedCodexAppServer_ArgvIsNotDoubled(t *testing.T) {
	_, exec := preparedFor(t, "codex", runtimes.ModeJSONRPCStdio, agentlaunch.InjectionSpec{})
	fake := providertest.New(t, runtimes.Codex, providertest.Script(providertest.AwaitEOF()))
	sl, err := ToSessionLaunchFromPreparedExecution(exec)
	if err != nil {
		t.Fatal(err)
	}
	adapter := provider.NewCodexAdapterAppServer()
	adapter.Binary = fake.Path
	startSession(t, adapter, agentsessions.Capabilities{JsonRpcStdio: true}, sl.Options)

	c := waitForCalls(t, fake, 1, func(providertest.Call) bool { return true })[0]
	if !slices.Equal(c.Args, []string{"app-server"}) {
		t.Errorf("argv = %q, want exactly [app-server]", c.Args)
	}
}
