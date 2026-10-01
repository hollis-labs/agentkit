//go:build !windows

package agentsessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/providertest"
)

// --- Codex app-server ------------------------------------------------------

func startCodexAppServer(t *testing.T, fake *providertest.Fake, hook func(string, json.RawMessage)) Session {
	t.Helper()
	adapter := provider.NewCodexAdapterAppServer()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "codex-interrupt", Adapter: adapter, Caps: Capabilities{JsonRpcStdio: true, BinaryRequired: true}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, LogPath: filepath.Join(dir, "session.log"), JsonRpcNotificationHook: hook})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })
	return sess
}

// The live capture (codex-cli 0.159.2): a turn whose command is running gets
// turn/interrupt for the turn turn/started announced; Codex answers {} and
// completes the turn "interrupted"; the next turn runs on the same process
// and thread (CW-20261001-0160).
func TestJsonRpcStdioSession_InterruptTurnKeepsTheProcess(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Replay("codex/app_server_interrupt"))
	notes := make(chan string, 256)
	statuses := make(chan string, 8)
	sess := startCodexAppServer(t, fake, func(method string, params json.RawMessage) {
		notes <- method
		if method == "turn/completed" {
			var p struct {
				Turn struct {
					Status string `json:"status"`
				} `json:"turn"`
			}
			_ = json.Unmarshal(params, &p)
			statuses <- p.Turn.Status
		}
	})
	caller := sess.(JsonRpcCaller)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, call := range []struct {
		method string
		params any
	}{
		{"initialize", map[string]any{"clientInfo": map[string]any{"name": "t", "version": "0"}}},
		{"thread/start", map[string]any{}},
		{"turn/start", map[string]any{"threadId": "x", "input": []any{}}},
	} {
		if _, err := caller.Call(ctx, call.method, call.params); err != nil {
			t.Fatalf("%s: %v", call.method, err)
		}
	}
	waitNote(t, notes, "item/started")
	pid := sess.(*jsonRpcStdioSession).LivePID()
	if err := sess.(TurnInterrupter).InterruptTurn(ctx); err != nil {
		t.Fatalf("InterruptTurn: %v", err)
	}
	if got := waitStatus(t, statuses); got != "interrupted" {
		t.Fatalf("the interrupted turn completed %q", got)
	}
	if _, err := caller.Call(ctx, "turn/start", map[string]any{"threadId": "x", "input": []any{}}); err != nil {
		t.Fatalf("turn/start after the interrupt: %v", err)
	}
	if got := waitStatus(t, statuses); got != "completed" {
		t.Errorf("the next turn completed %q", got)
	}
	if sess.(*jsonRpcStdioSession).LivePID() != pid || len(fake.Calls()) != 1 {
		t.Error("the process did not survive the interrupt")
	}
	// No turn open now: nothing to interrupt, nothing sent.
	if err := sess.(TurnInterrupter).InterruptTurn(ctx); err != nil {
		t.Errorf("InterruptTurn with no turn open = %v", err)
	}
	_ = sess.Stop(context.Background())
	_, _ = sess.Wait()
	if errs := fake.Errors(); len(errs) != 0 {
		t.Errorf("fake errors: %q", errs)
	}
	var interrupts int
	for _, line := range fake.Call(0).Stdin {
		if strings.Contains(line, `"turn/interrupt"`) {
			interrupts++
			if !strings.Contains(line, `"turnId":"00000000-0000-4000-8000-000000000003"`) {
				t.Errorf("turn/interrupt names another turn: %s", line)
			}
		}
	}
	if interrupts != 1 {
		t.Errorf("sent %d turn/interrupt requests, want 1", interrupts)
	}
}

func waitNote(t *testing.T, notes <-chan string, method string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-notes:
			if m == method {
				return
			}
		case <-deadline:
			t.Fatalf("no %s notification", method)
		}
	}
}

func waitStatus(t *testing.T, statuses <-chan string) string {
	t.Helper()
	select {
	case s := <-statuses:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("no turn/completed")
		return ""
	}
}

func TestJsonRpcStdioSession_InterruptUnsupported(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Script(providertest.AwaitEOF()))
	sess := startJsonRpcFake(t, fake, StartOptions{})
	if err := sess.(TurnInterrupter).InterruptTurn(context.Background()); !errors.Is(err, ErrInterruptUnsupported) {
		t.Errorf("InterruptTurn without RPCTurnInterrupter = %v", err)
	}
}

// --- OpenCode serve ---------------------------------------------------------

type recordedRequest struct {
	method, path string
	status       int
	body         json.RawMessage
	// pre are the events recorded between the request and its response,
	// post those after the response, before the next request.
	pre, post []json.RawMessage
}

// loadServeAbort reads opencode/serve_abort.http.jsonl into its requests,
// with the events that followed each.
func loadServeAbort(t *testing.T) (initial []json.RawMessage, reqs []*recordedRequest) {
	t.Helper()
	for _, line := range providertest.FixtureLines(t, "opencode/serve_abort.http.jsonl") {
		var step struct {
			Request *struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"request"`
			Response *struct {
				Status int             `json:"status"`
				Body   json.RawMessage `json:"body"`
			} `json:"response"`
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(line, &step); err != nil {
			t.Fatal(err)
		}
		switch {
		case step.Request != nil:
			reqs = append(reqs, &recordedRequest{method: step.Request.Method, path: step.Request.Path})
		case step.Response != nil:
			last := reqs[len(reqs)-1]
			last.status, last.body = step.Response.Status, step.Response.Body
		case len(reqs) == 0:
			initial = append(initial, step.Event)
		case reqs[len(reqs)-1].status == 0:
			reqs[len(reqs)-1].pre = append(reqs[len(reqs)-1].pre, step.Event)
		default:
			reqs[len(reqs)-1].post = append(reqs[len(reqs)-1].post, step.Event)
		}
	}
	return initial, reqs
}

// serveAbortReplay serves the capture: each recorded request, made in
// order, is answered as it was and releases the events recorded after it.
// With lateCleanup, the events after the abort's response (the aborted
// tool's cleanup, with its second session.idle) are held back until the
// next request, as they arrived in a live run.
func serveAbortReplay(t *testing.T, lateCleanup bool) *httptest.Server {
	t.Helper()
	initial, reqs := loadServeAbort(t)
	events := make(chan json.RawMessage, 1024)
	for _, ev := range initial {
		events <- ev
	}
	var mu sync.Mutex
	next := 0
	var held []json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
			return
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("content-type", "text/event-stream")
			flusher := w.(http.Flusher)
			for {
				select {
				case ev := <-events:
					_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
					flusher.Flush()
				case <-r.Context().Done():
					return
				}
			}
		case r.URL.Path == "/global/dispose":
			_, _ = w.Write([]byte(`true`))
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		defer mu.Unlock()
		if next >= len(reqs) {
			// Stop's own abort, after the scripted exchange.
			_, _ = w.Write([]byte(`true`))
			return
		}
		want := reqs[next]
		if r.Method != want.method || r.URL.Path != want.path {
			t.Errorf("request %d = %s %s, the capture made %s %s", next, r.Method, r.URL.Path, want.method, want.path)
			http.NotFound(w, r)
			return
		}
		next++
		for _, ev := range held {
			events <- ev
		}
		held = nil
		for _, ev := range want.pre {
			events <- ev
		}
		if lateCleanup && strings.HasSuffix(want.path, "/abort") {
			held = want.post
		} else {
			for _, ev := range want.post {
				events <- ev
			}
		}
		if want.status == http.StatusNoContent {
			w.WriteHeader(want.status)
			return
		}
		var body any
		_ = json.Unmarshal(want.body, &body)
		if s, ok := body.(string); ok {
			w.WriteHeader(want.status)
			_, _ = w.Write([]byte(s))
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(want.status)
		_, _ = w.Write(want.body)
	}))
	t.Cleanup(server.Close)
	return server
}

func startServeAbort(t *testing.T, server *httptest.Server) (Session, <-chan llmtypes.StreamEvent, *syncBuffer) {
	t.Helper()
	dir := t.TempDir()
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "serve-http-abort",
		Kind:    "cli",
		Adapter: &echoAdapter{script: writeServeHTTPFakeBinary(t, dir)},
		Caps:    Capabilities{ServeHTTP: true, BinaryRequired: true, ProviderSessionID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan llmtypes.StreamEvent, 256)
	var raw syncBuffer
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		LogPath:     filepath.Join(dir, "session.log"),
		Env:         append(os.Environ(), "TEST_SERVER_URL="+server.URL),
		EventFanout: events,
		Fanout:      &raw,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })
	return sess, events, &raw
}

// The live capture (opencode 1.18.33 serve): InterruptTurn aborts the
// running turn, which ends with one error; the abort's idles end nothing;
// the next turn runs on the same server and session and ends once
// (CW-20261001-0160). With lateCleanup the aborted tool's second
// session.idle reaches the session after the next prompt, as it did live,
// and must not end that turn.
func TestServeHTTPSession_InterruptTurnKeepsTheSession(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late cleanup %v", late), func(t *testing.T) {
			sess, events, raw := startServeAbort(t, serveAbortReplay(t, late))
			if err := sess.SendInput(context.Background(), []byte("run something slow")); err != nil {
				t.Fatalf("SendInput: %v", err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(raw.String(), `"status":"running"`) {
				if time.Now().After(deadline) {
					t.Fatal("the tool never ran")
				}
				time.Sleep(10 * time.Millisecond)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := sess.(TurnInterrupter).InterruptTurn(ctx); err != nil {
				t.Fatalf("InterruptTurn: %v", err)
			}
			got := collectUntil(t, events, llmtypes.EventError)
			if slices.Contains(got, llmtypes.EventDone) {
				t.Errorf("the aborted turn also completed: %v", got)
			}
			if err := sess.SendInput(context.Background(), []byte("next")); err != nil {
				t.Fatalf("SendInput after the abort: %v", err)
			}
			got = collectUntil(t, events, llmtypes.EventDone)
			if !slices.Contains(got, llmtypes.EventDelta) {
				t.Errorf("the next turn ended before its reply: %v", got)
			}
			select {
			case ev := <-events:
				if ev.Type == llmtypes.EventDone || ev.Type == llmtypes.EventError {
					t.Errorf("a stray turn end after the next turn: %v", ev.Type)
				}
			case <-time.After(300 * time.Millisecond):
			}
			if !sess.Health().Alive {
				t.Error("the session did not survive the abort")
			}
		})
	}
}

// collectUntil returns the event types up to and including the first of
// type end.
func collectUntil(t *testing.T, events <-chan llmtypes.StreamEvent, end llmtypes.EventType) []llmtypes.EventType {
	t.Helper()
	var got []llmtypes.EventType
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			got = append(got, ev.Type)
			if ev.Type == end {
				return got
			}
		case <-deadline:
			t.Fatalf("no %s; saw %v", end, got)
		}
	}
}
