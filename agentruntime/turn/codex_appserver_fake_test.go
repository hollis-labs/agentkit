//go:build !windows

package turn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	permission "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/go-providers/provider"
)

// The fake Codex app-server is this test binary re-executed with
// fakeCodexEnv set. It speaks enough of the app-server protocol to drive
// CodexAppServerSession through a real agentsessions JSON-RPC stdio session:
//
//   - initialize answers with a user agent.
//   - thread/start answers with thread "thread-new".
//   - thread/resume answers with the requested thread when it is listed in
//     FAKE_CODEX_THREADS (or with FAKE_CODEX_RESUME_AS when that is set), and
//     otherwise with the error codex-cli 0.159.2 returns for a thread it has
//     no rollout for.
//   - turn/start, when FAKE_CODEX_APPROVAL_METHOD is set, first sends that
//     server-initiated request and waits for the client's answer before it
//     answers the turn, the way Codex blocks an action on an approval.
//
// Every frame it receives is appended to FAKE_CODEX_LOG, one per line.
const fakeCodexEnv = "AGENTKIT_FAKE_CODEX_APP_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(fakeCodexEnv) == "1" {
		if err := runFakeCodexAppServer(); err != nil {
			fmt.Fprintln(os.Stderr, "fake codex app-server:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeFrame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func runFakeCodexAppServer() error {
	logFile, err := os.OpenFile(os.Getenv("FAKE_CODEX_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	known := strings.Split(os.Getenv("FAKE_CODEX_THREADS"), ",")
	out := json.NewEncoder(os.Stdout)
	in := bufio.NewScanner(os.Stdin)

	reply := func(id json.RawMessage, result any) error {
		return out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	fail := func(id json.RawMessage, code int, msg string) error {
		return out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	}
	read := func() (fakeFrame, []byte, bool) {
		if !in.Scan() {
			return fakeFrame{}, nil, false
		}
		line := append([]byte(nil), in.Bytes()...)
		_, _ = logFile.Write(append(append([]byte(nil), line...), '\n'))
		var f fakeFrame
		_ = json.Unmarshal(line, &f)
		return f, line, true
	}

	for {
		f, _, ok := read()
		if !ok {
			return in.Err()
		}
		switch f.Method {
		case "initialize":
			err = reply(f.ID, map[string]any{"userAgent": "fake-codex/0"})
		case "thread/start":
			err = reply(f.ID, map[string]any{"thread": map[string]any{"id": "thread-new"}})
		case "thread/resume":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(f.Params, &p)
			switch {
			case os.Getenv("FAKE_CODEX_RESUME_AS") != "":
				err = reply(f.ID, map[string]any{"thread": map[string]any{"id": os.Getenv("FAKE_CODEX_RESUME_AS")}})
			case slices.Contains(known, p.ThreadID):
				err = reply(f.ID, map[string]any{"thread": map[string]any{"id": p.ThreadID}})
			default:
				err = fail(f.ID, -32600, "no rollout found for thread id "+p.ThreadID)
			}
		case "turn/start":
			if method := os.Getenv("FAKE_CODEX_APPROVAL_METHOD"); method != "" {
				req := map[string]any{"jsonrpc": "2.0", "id": "srv-1", "method": method, "params": json.RawMessage(os.Getenv("FAKE_CODEX_APPROVAL_PARAMS"))}
				if err := out.Encode(req); err != nil {
					return err
				}
				for {
					answer, _, ok := read()
					if !ok {
						return errors.New("stdin closed before the approval was answered")
					}
					if string(answer.ID) == `"srv-1"` && answer.Method == "" {
						break
					}
				}
			}
			err = reply(f.ID, map[string]any{"turn": map[string]any{"id": "turn-1"}})
		default:
			if len(f.ID) > 0 && f.Method != "" {
				err = fail(f.ID, -32601, "fake codex: unknown method "+f.Method)
			}
		}
		if err != nil {
			return err
		}
	}
}

type fakeCodexAdapter struct{ binary string }

func (a fakeCodexAdapter) Name() string                                       { return "fake-codex" }
func (a fakeCodexAdapter) BuildArgs(_, _, _ string) []string                  { return []string{"-test.run=^$"} }
func (a fakeCodexAdapter) ParseLine(_ []byte) ([]llmtypes.StreamEvent, error) { return nil, nil }
func (a fakeCodexAdapter) Detect() (string, bool)                             { return a.binary, true }

type fakeCodex struct {
	rpc     JSONRPCSender
	logPath string
}

// startFakeCodex launches the fake app-server as an agentsessions JSON-RPC
// stdio session, with hook as its JsonRpcRequestHook.
func startFakeCodex(t *testing.T, env []string, hook func(string, json.RawMessage) (any, *agentsessions.JsonRpcError)) fakeCodex {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "frames.log")
	rt, err := agentsessions.NewFromAdapter(agentsessions.AdapterRuntimeConfig{
		ID:      "fake-codex-app-server",
		Kind:    "cli",
		Adapter: fakeCodexAdapter{binary: os.Args[0]},
		Caps:    agentsessions.Capabilities{JsonRpcStdio: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := rt.Start(context.Background(), agentsessions.StartOptions{
		Workdir:            dir,
		LogPath:            filepath.Join(dir, "session.log"),
		Env:                append(append(os.Environ(), fakeCodexEnv+"=1", "FAKE_CODEX_LOG="+logPath), env...),
		JsonRpcRequestHook: hook,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Stop(context.Background())
		_, _ = sess.Wait()
	})
	rpc, ok := sess.(agentsessions.JsonRpcCaller)
	if !ok {
		t.Fatal("session does not implement JsonRpcCaller")
	}
	return fakeCodex{rpc: rpc, logPath: logPath}
}

// frames returns what the fake app-server received, in order.
func (f fakeCodex) frames(t *testing.T) []fakeFrame {
	t.Helper()
	raw, err := os.ReadFile(f.logPath)
	if err != nil {
		t.Fatal(err)
	}
	var out []fakeFrame
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var fr fakeFrame
		if err := json.Unmarshal([]byte(line), &fr); err != nil {
			t.Fatalf("frame %q: %v", line, err)
		}
		out = append(out, fr)
	}
	return out
}

func (f fakeCodex) methods(t *testing.T) []string {
	var out []string
	for _, fr := range f.frames(t) {
		if fr.Method != "" {
			out = append(out, fr.Method)
		}
	}
	return out
}

func fakeCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestFakeCodexAppServerResumesKnownThread(t *testing.T) {
	codex := startFakeCodex(t, []string{"FAKE_CODEX_THREADS=thread-abc"}, nil)
	var session CodexAppServerSession
	opts := CodexAppServerOptions{CWD: "/work", ResumeThreadID: "thread-abc"}
	for _, text := range []string{"one", "two"} {
		if err := session.SendTurn(fakeCtx(t), codex.rpc, text, opts); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := codex.methods(t), []string{"initialize", "thread/resume", "turn/start", "turn/start"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %v, want %v", got, want)
	}
	var resume struct {
		ThreadID     string `json:"threadId"`
		ExcludeTurns bool   `json:"excludeTurns"`
		CWD          string `json:"cwd"`
	}
	if err := json.Unmarshal(codex.frames(t)[1].Params, &resume); err != nil {
		t.Fatal(err)
	}
	if resume.ThreadID != "thread-abc" || !resume.ExcludeTurns || resume.CWD != "/work" {
		t.Fatalf("thread/resume params = %+v", resume)
	}
	if session.ThreadID() != "thread-abc" {
		t.Fatalf("ThreadID = %q", session.ThreadID())
	}
}

func TestFakeCodexAppServerLostThreadNeverStartsAFreshOne(t *testing.T) {
	codex := startFakeCodex(t, []string{"FAKE_CODEX_THREADS=thread-abc"}, nil)
	var session CodexAppServerSession
	opts := CodexAppServerOptions{ResumeThreadID: "019a0000-0000-7000-8000-000000000000"}
	for attempt := 0; attempt < 2; attempt++ {
		err := session.SendTurn(fakeCtx(t), codex.rpc, "hello", opts)
		if !errors.Is(err, provider.ErrProviderSessionLost) {
			t.Fatalf("attempt %d: err = %v, want ErrProviderSessionLost", attempt, err)
		}
		var lost *agentsessions.SessionLostError
		if !errors.As(err, &lost) || lost.RequestedID != opts.ResumeThreadID || lost.ActualID != "" {
			t.Fatalf("attempt %d: SessionLostError = %+v", attempt, lost)
		}
	}
	if got, want := codex.methods(t), []string{"initialize", "thread/resume", "thread/resume"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %v, want %v (no thread/start, no turn/start)", got, want)
	}
}

func TestFakeCodexAppServerResumeIntoDifferentThread(t *testing.T) {
	codex := startFakeCodex(t, []string{"FAKE_CODEX_RESUME_AS=thread-other"}, nil)
	var session CodexAppServerSession
	err := session.SendTurn(fakeCtx(t), codex.rpc, "hello", CodexAppServerOptions{ResumeThreadID: "thread-abc"})
	var lost *agentsessions.SessionLostError
	if !errors.As(err, &lost) || lost.RequestedID != "thread-abc" || lost.ActualID != "thread-other" {
		t.Fatalf("err = %v, want SessionLostError{thread-abc, thread-other}", err)
	}
	if got, want := codex.methods(t), []string{"initialize", "thread/resume"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %v, want %v", got, want)
	}
}

// The responder, installed as the session's JsonRpcRequestHook, answers the
// approval Codex blocks a turn on, so turn/start completes instead of hanging
// and the child receives the posture's decision.
func TestFakeCodexAppServerApprovalResponder(t *testing.T) {
	cases := []struct {
		name   string
		mode   permission.Mode
		method string
		params string
		want   string
	}{
		{"default approves MCP tool call", permission.ModeDefault, CodexElicitationMethod, codexMCPToolCallElicitation, `{"action":"accept","content":{}}`},
		{"default declines command", permission.ModeDefault, CodexCommandApprovalMethod, codexCommandApproval, `{"decision":"decline"}`},
		{"accept-edits approves file change", permission.ModeAcceptEdits, CodexFileChangeApprovalMethod, codexFileChangeApproval, `{"decision":"accept"}`},
		{"plan declines MCP tool call", permission.ModePlan, CodexElicitationMethod, codexMCPToolCallElicitation, `{"action":"decline"}`},
		{"yolo approves command", permission.ModeYolo, CodexCommandApprovalMethod, codexCommandApproval, `{"decision":"accept"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compact, err := compactJSON(tc.params)
			if err != nil {
				t.Fatal(err)
			}
			codex := startFakeCodex(t, []string{
				"FAKE_CODEX_APPROVAL_METHOD=" + tc.method,
				"FAKE_CODEX_APPROVAL_PARAMS=" + compact,
			}, CodexApprovalResponder{Mode: tc.mode}.Hook())
			var session CodexAppServerSession
			if err := session.SendTurn(fakeCtx(t), codex.rpc, "do the thing", CodexAppServerOptions{}); err != nil {
				t.Fatal(err)
			}
			var answer *fakeFrame
			for _, fr := range codex.frames(t) {
				if string(fr.ID) == `"srv-1"` && fr.Method == "" {
					answer = &fr
				}
			}
			if answer == nil {
				t.Fatal("the fake app-server never received an answer to its approval request")
			}
			if len(answer.Error) > 0 {
				t.Fatalf("approval answered with error %s", answer.Error)
			}
			if string(answer.Result) != tc.want {
				t.Fatalf("approval result = %s, want %s", answer.Result, tc.want)
			}
		})
	}
}

func compactJSON(s string) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	return string(b), err
}
