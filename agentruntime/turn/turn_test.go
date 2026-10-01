package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

func TestClaudeStreamingUserFrame(t *testing.T) {
	raw, err := ClaudeStreamingUserFrame("# Boot\nsay \"hi\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\n") {
		t.Fatalf("serialized frame contains raw newline: %q", raw)
	}
	var got struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "user" || got.Message.Role != "user" || got.Message.Content != "# Boot\nsay \"hi\"\n" {
		t.Fatalf("decoded frame = %#v", got)
	}
}

func TestStreamingStdioDoesNotSendRawMarkdown(t *testing.T) {
	s := &captureSender{}
	if err := SendTurn(context.Background(), s, "# raw markdown", Options{Runtime: runtimekind.StreamingStdio}); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(s.last), "# raw") {
		t.Fatalf("sent raw markdown to streaming stdio: %q", s.last)
	}
}

func TestServeHTTPSendTurnUsesRawSendInput(t *testing.T) {
	s := &captureSender{}
	if err := SendTurn(context.Background(), s, "hello http", Options{Runtime: runtimekind.ServeHTTP}); err != nil {
		t.Fatal(err)
	}
	if string(s.last) != "hello http" {
		t.Fatalf("serve-http payload = %q", s.last)
	}
}

func TestCodexAppServerSessionHandshakeAndCachedThread(t *testing.T) {
	rpc := &recordingRPC{responses: map[string]json.RawMessage{
		"thread/start": json.RawMessage(`{"thread":{"id":"thread-123"}}`),
	}}
	var session CodexAppServerSession
	opts := CodexAppServerOptions{ClientName: "torque", ClientVersion: "0.1-dev", CWD: "/work/root"}
	if err := session.SendTurn(context.Background(), rpc, "first", opts); err != nil {
		t.Fatal(err)
	}
	if err := session.SendTurn(context.Background(), rpc, "second", opts); err != nil {
		t.Fatal(err)
	}

	gotMethods := rpc.methods()
	wantMethods := []string{"initialize", "thread/start", "turn/start", "turn/start"}
	if !reflect.DeepEqual(gotMethods, wantMethods) {
		t.Fatalf("methods = %v, want %v", gotMethods, wantMethods)
	}

	initParams := rpc.calls[0].params.(map[string]any)
	clientInfo := initParams["clientInfo"].(map[string]any)
	if clientInfo["name"] != "torque" || clientInfo["version"] != "0.1-dev" {
		t.Fatalf("initialize clientInfo = %v", clientInfo)
	}
	startParams := rpc.calls[1].params.(map[string]any)
	if startParams["cwd"] != "/work/root" {
		t.Fatalf("thread/start cwd = %v", startParams["cwd"])
	}
	firstTurn := rpc.calls[2].params.(map[string]any)
	if firstTurn["threadId"] != "thread-123" {
		t.Fatalf("turn/start threadId = %v", firstTurn["threadId"])
	}
	input := firstTurn["input"].([]map[string]any)
	if input[0]["type"] != "text" || input[0]["text"] != "first" {
		t.Fatalf("turn/start input = %v", input)
	}
	secondTurn := rpc.calls[3].params.(map[string]any)
	if secondTurn["threadId"] != "thread-123" {
		t.Fatalf("second turn threadId = %v", secondTurn["threadId"])
	}
	if session.ThreadID() != "thread-123" {
		t.Fatalf("cached thread id = %q", session.ThreadID())
	}
}

func TestCodexAppServerCacheSeparatesAndForgetsSessions(t *testing.T) {
	rpc := &recordingRPC{responses: map[string]json.RawMessage{
		"thread/start": json.RawMessage(`{"thread":{"id":"thread-cache"}}`),
	}}
	var cache CodexAppServerCache
	if err := cache.SendTurn(context.Background(), "sess-a", rpc, "hello", CodexAppServerOptions{}); err != nil {
		t.Fatal(err)
	}
	if id, ok := cache.ThreadID("sess-a"); !ok || id != "thread-cache" {
		t.Fatalf("ThreadID = %q, %v", id, ok)
	}
	cache.Forget("sess-a")
	if id, ok := cache.ThreadID("sess-a"); ok || id != "" {
		t.Fatalf("ThreadID after forget = %q, %v", id, ok)
	}
}

func TestDecodeCodexThreadIDErrors(t *testing.T) {
	if _, err := DecodeCodexThreadID(json.RawMessage(`{"thread":{}}`)); !errors.Is(err, ErrMissingThreadID) {
		t.Fatalf("err = %v, want ErrMissingThreadID", err)
	}
	if id, err := DecodeCodexThreadID(json.RawMessage(`{"threadId":"legacy"}`)); err != nil || id != "legacy" {
		t.Fatalf("legacy thread id = %q, %v", id, err)
	}
}

func TestCodexAppServerSessionResumesPresetThread(t *testing.T) {
	rpc := &recordingRPC{responses: map[string]json.RawMessage{
		"thread/resume": json.RawMessage(`{"thread":{"id":"thread-old"}}`),
	}}
	var session CodexAppServerSession
	opts := CodexAppServerOptions{CWD: "/work/root", ResumeThreadID: "thread-old"}
	for _, text := range []string{"first", "second"} {
		if err := session.SendTurn(context.Background(), rpc, text, opts); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := rpc.methods(), []string{"initialize", "thread/resume", "turn/start", "turn/start"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %v, want %v", got, want)
	}
	resume := rpc.calls[1].params.(map[string]any)
	if resume["threadId"] != "thread-old" || resume["cwd"] != "/work/root" || resume["excludeTurns"] != true {
		t.Fatalf("thread/resume params = %v", resume)
	}
	if turn := rpc.calls[2].params.(map[string]any); turn["threadId"] != "thread-old" {
		t.Fatalf("turn/start threadId = %v", turn["threadId"])
	}
	if session.ThreadID() != "thread-old" {
		t.Fatalf("ThreadID = %q", session.ThreadID())
	}
}

func TestCodexAppServerSessionLostThreadIsAnErrorNotAFreshThread(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"typed rpc error", &agentsessions.JsonRpcError{Code: -32600, Message: "no rollout found for thread id thread-gone"}},
		{"wrapped by the caller's sender", fmt.Errorf("manager call: %w", &agentsessions.JsonRpcError{Code: -32600, Message: "invalid session id: invalid character"})},
		{"flattened to text by the caller's sender", errors.New("jsonrpc error -32600: no rollout found for thread id thread-gone")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &recordingRPC{errs: map[string]error{"thread/resume": tc.err}}
			var session CodexAppServerSession
			opts := CodexAppServerOptions{ResumeThreadID: "thread-gone"}
			for attempt := 0; attempt < 2; attempt++ {
				err := session.SendTurn(context.Background(), rpc, "hello", opts)
				if !errors.Is(err, provider.ErrProviderSessionLost) {
					t.Fatalf("attempt %d: err = %v, want ErrProviderSessionLost", attempt, err)
				}
				var lost *agentsessions.SessionLostError
				if !errors.As(err, &lost) || lost.RequestedID != "thread-gone" || lost.ActualID != "" {
					t.Fatalf("attempt %d: SessionLostError = %+v", attempt, lost)
				}
			}
			for _, m := range rpc.methods() {
				if m == "thread/start" || m == "turn/start" {
					t.Fatalf("methods = %v: a lost resume must not start a thread or a turn", rpc.methods())
				}
			}
			if session.ThreadID() != "" {
				t.Fatalf("ThreadID = %q after a failed resume", session.ThreadID())
			}
		})
	}
}

func TestCodexAppServerSessionResumeOtherFailureIsNotSessionLost(t *testing.T) {
	rpc := &recordingRPC{errs: map[string]error{
		"thread/resume": &agentsessions.JsonRpcError{Code: -32603, Message: "internal error"},
	}}
	var session CodexAppServerSession
	err := session.SendTurn(context.Background(), rpc, "hello", CodexAppServerOptions{ResumeThreadID: "thread-x"})
	if err == nil || errors.Is(err, provider.ErrProviderSessionLost) {
		t.Fatalf("err = %v, want a non-session-lost error", err)
	}
	var rpcErr *agentsessions.JsonRpcError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32603 {
		t.Fatalf("err = %v, want the rpc error preserved", err)
	}
	if got, want := rpc.methods(), []string{"initialize", "thread/resume"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %v, want %v", got, want)
	}
}

func TestCodexAppServerSessionResumeIntoDifferentThreadIsSessionLost(t *testing.T) {
	rpc := &recordingRPC{responses: map[string]json.RawMessage{
		"thread/resume": json.RawMessage(`{"thread":{"id":"thread-other"}}`),
	}}
	var session CodexAppServerSession
	err := session.SendTurn(context.Background(), rpc, "hello", CodexAppServerOptions{ResumeThreadID: "thread-asked"})
	var lost *agentsessions.SessionLostError
	if !errors.As(err, &lost) || lost.RequestedID != "thread-asked" || lost.ActualID != "thread-other" {
		t.Fatalf("err = %v, want SessionLostError{thread-asked, thread-other}", err)
	}
	if !errors.Is(err, provider.ErrProviderSessionLost) || !errors.Is(err, ErrCodexThreadMismatch) {
		t.Fatalf("err = %v, want ErrProviderSessionLost and ErrCodexThreadMismatch", err)
	}
	if session.ThreadID() != "" {
		t.Fatalf("ThreadID = %q, want the mismatched thread left unbound", session.ThreadID())
	}
}

// ResumeThreadID only matters until a thread is bound: after Reset the caller
// that clears it gets a fresh thread, and a caller that keeps passing it on a
// bound session keeps its thread.
func TestCodexAppServerSessionResumeOnlyBindsOnce(t *testing.T) {
	rpc := &recordingRPC{responses: map[string]json.RawMessage{
		"thread/start": json.RawMessage(`{"thread":{"id":"thread-new"}}`),
	}}
	var session CodexAppServerSession
	if err := session.SendTurn(context.Background(), rpc, "one", CodexAppServerOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := session.SendTurn(context.Background(), rpc, "two", CodexAppServerOptions{ResumeThreadID: "thread-elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if got, want := rpc.methods(), []string{"initialize", "thread/start", "turn/start", "turn/start"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %v, want %v", got, want)
	}
	if session.ThreadID() != "thread-new" {
		t.Fatalf("ThreadID = %q", session.ThreadID())
	}
}

type captureSender struct{ last []byte }

func (c *captureSender) SendInput(_ context.Context, data []byte) error {
	c.last = append([]byte(nil), data...)
	return nil
}

type rpcCall struct {
	method string
	params any
}

type recordingRPC struct {
	calls     []rpcCall
	responses map[string]json.RawMessage
	errs      map[string]error
}

func (r *recordingRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	r.calls = append(r.calls, rpcCall{method: method, params: params})
	if err := r.errs[method]; err != nil {
		return nil, err
	}
	if res := r.responses[method]; len(res) > 0 {
		return res, nil
	}
	return json.RawMessage(`{}`), nil
}

func (r *recordingRPC) methods() []string {
	out := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, call.method)
	}
	return out
}
