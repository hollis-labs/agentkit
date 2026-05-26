package turn

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
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
}

func (r *recordingRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	r.calls = append(r.calls, rpcCall{method: method, params: params})
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
