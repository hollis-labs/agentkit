//go:build !windows

package agentsessions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// The event shapes below are OpenCode 1.18.33's own: session.error carries
// {sessionID, error: {name, data: {message}}} (SessionProcessor.halt
// publishes R.Event.Error with the error's toObject()), session.compacted
// carries {sessionID}, and a failure outside any session (a plugin that did
// not load) carries no sessionID.
const errorsSessionID = "ses_errors_test"

func ocEvent(typ, props string) string {
	return fmt.Sprintf(`{"type":%q,"properties":%s}`, typ, props)
}

var (
	ocBusy      = ocEvent("session.status", `{"sessionID":"`+errorsSessionID+`","status":{"type":"busy"}}`)
	ocIdleState = ocEvent("session.status", `{"sessionID":"`+errorsSessionID+`","status":{"type":"idle"}}`)
	ocIdle      = ocEvent("session.idle", `{"sessionID":"`+errorsSessionID+`"}`)
	ocOverflow  = ocEvent("session.error", `{"sessionID":"`+errorsSessionID+`","error":{"name":"ContextOverflowError","data":{"message":"Context overflow: prompt is too long"}}}`)
	ocCompacted = ocEvent("session.compacted", `{"sessionID":"`+errorsSessionID+`"}`)
	ocPlugin    = ocEvent("session.error", `{"error":{"name":"UnknownError","data":{"message":"Failed to load plugin: example-plugin"}}}`)
	ocAPIError  = ocEvent("session.error", `{"sessionID":"`+errorsSessionID+`","error":{"name":"APIError","data":{"message":"overloaded","isRetryable":false}}}`)
)

func ocDelta(text string) string {
	return ocEvent("message.part.delta", `{"sessionID":"`+errorsSessionID+`","delta":"`+text+`"}`)
}

// startScriptedServe runs a serve-http session against an OpenCode stand-in
// that answers the first prompt by streaming turn.
func startScriptedServe(t *testing.T, turn []string) <-chan llmtypes.StreamEvent {
	t.Helper()
	events := make(chan string, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = w.Write([]byte(`{"id":"` + errorsSessionID + `"}`))
		case r.URL.Path == "/event":
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
		case strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(http.StatusNoContent)
			for _, ev := range turn {
				events <- ev
			}
		default:
			_, _ = w.Write([]byte(`true`))
		}
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "serve-http-errors",
		Kind:    "cli",
		Adapter: &echoAdapter{script: writeServeHTTPFakeBinary(t, dir)},
		Caps:    Capabilities{ServeHTTP: true, BinaryRequired: true, ProviderSessionID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	fanout := make(chan llmtypes.StreamEvent, 64)
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		LogPath:     filepath.Join(dir, "session.log"),
		Env:         append(os.Environ(), "TEST_SERVER_URL="+server.URL),
		EventFanout: fanout,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })
	if err := sess.SendInput(context.Background(), []byte("hi")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	return fanout
}

// turnEnd collects events until the turn's first terminal event, then
// checks nothing else ends it.
func turnEnd(t *testing.T, fanout <-chan llmtypes.StreamEvent) (llmtypes.StreamEvent, []llmtypes.StreamEvent) {
	t.Helper()
	var seen []llmtypes.StreamEvent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-fanout:
			seen = append(seen, ev)
			if ev.Type == llmtypes.EventDone || ev.Type == llmtypes.EventError {
				select {
				case extra := <-fanout:
					if extra.Type == llmtypes.EventDone || extra.Type == llmtypes.EventError {
						t.Errorf("the turn ended twice: %v, then %v", ev.Type, extra.Type)
					}
				case <-time.After(300 * time.Millisecond):
				}
				return ev, seen
			}
		case <-deadline:
			t.Fatalf("the turn never ended; saw %v", seen)
		}
	}
}

// OpenCode reports a context overflow as a session.error, then compacts the
// context and carries on with the turn (SessionProcessor.halt sets
// needsCompaction and returns without going idle). The turn completes; the
// overflow is not its failure.
func TestServeHTTPSession_ContextOverflowThenCompactionContinues(t *testing.T) {
	fanout := startScriptedServe(t, []string{
		ocBusy, ocDelta("partial"), ocOverflow, ocCompacted, ocBusy, ocDelta("after compaction"), ocIdleState, ocIdle,
	})
	end, seen := turnEnd(t, fanout)
	if end.Type != llmtypes.EventDone {
		t.Fatalf("turn ended %v (%s); want done, the overflow was compacted", end.Type, end.Error)
	}
	var text string
	for _, ev := range seen {
		text += ev.Content
	}
	if !strings.Contains(text, "after compaction") {
		t.Errorf("the turn ended before its reply after compaction: %q", text)
	}
}

// With compaction off, or a session too large to compact, the overflow is
// the turn's end: OpenCode publishes the error and goes idle.
func TestServeHTTPSession_ContextOverflowWithoutCompactionFailsTheTurn(t *testing.T) {
	fanout := startScriptedServe(t, []string{ocBusy, ocOverflow, ocIdleState, ocIdle})
	end, _ := turnEnd(t, fanout)
	if end.Type != llmtypes.EventError || !strings.Contains(end.Error, "ContextOverflowError") {
		t.Fatalf("turn ended %v (%s); want the overflow as its error", end.Type, end.Error)
	}
}

// An error that belongs to no session (a plugin that failed to load) is a
// diagnostic: the turn in flight runs on and completes.
func TestServeHTTPSession_SessionlessErrorDoesNotFailTheTurn(t *testing.T) {
	fanout := startScriptedServe(t, []string{ocBusy, ocPlugin, ocDelta("still here"), ocIdleState, ocIdle})
	end, _ := turnEnd(t, fanout)
	if end.Type != llmtypes.EventDone {
		t.Fatalf("turn ended %v (%s); a sessionless error must not fail it", end.Type, end.Error)
	}
}

// Any other error for this session still ends the turn, once: OpenCode
// publishes it and goes idle.
func TestServeHTTPSession_SessionErrorFailsTheTurnOnce(t *testing.T) {
	fanout := startScriptedServe(t, []string{ocBusy, ocAPIError, ocIdleState, ocIdle})
	end, _ := turnEnd(t, fanout)
	if end.Type != llmtypes.EventError || !strings.Contains(end.Error, "APIError") {
		t.Fatalf("turn ended %v (%s); want the APIError", end.Type, end.Error)
	}
}
