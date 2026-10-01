//go:build !windows

package agentsessions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/providertest"
)

// A stdout line over the old 1 MiB scanner limit stopped the session's
// reader: nothing drained stdout again, the child blocked on the full pipe,
// the session still reported alive and every later Call waited out its
// deadline (CW-20261001-0086, Torque run1112's lost steering).

const overOneMiB = 1536 * 1024

func bigNotification(n int) string {
	return `{"jsonrpc":"2.0","method":"item/completed","params":{"output":"` + strings.Repeat("x", n) + `"}}`
}

func TestReadLines(t *testing.T) {
	defer func(old int) { maxLineBytes = old }(maxLineBytes)
	maxLineBytes = 16

	var lines []string
	var over []int
	in := "short\r\n" + strings.Repeat("y", 40) + "\nexactly-16-bytes\nno-newline"
	err := readLines(strings.NewReader(in), func(l []byte) { lines = append(lines, string(l)) }, func(n int) { over = append(over, n) })
	if err != nil {
		t.Fatalf("readLines: %v", err)
	}
	if want := []string{"short", "exactly-16-bytes", "no-newline"}; !slices.Equal(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	if len(over) != 1 || over[0] != 41 {
		t.Errorf("oversize = %v, want one 41-byte line", over)
	}

	boom := errors.New("boom")
	err = readLines(io.MultiReader(strings.NewReader("a\n"), &errReader{boom}), func([]byte) {}, nil)
	if !errors.Is(err, boom) || !readerFailed(err) {
		t.Errorf("err = %v, want the read error, classified as a failure", err)
	}
	for _, end := range []error{nil, io.EOF, os.ErrClosed, io.ErrClosedPipe} {
		if readerFailed(end) {
			t.Errorf("readerFailed(%v) = true; that is how a reader normally stops", end)
		}
	}
}

// A line well over 1 MiB is still delivered whole: below maxLineBytes it is
// routed like any other.
func TestReadLines_LineOverOneMiB(t *testing.T) {
	big := bigNotification(overOneMiB)
	var got []int
	if err := readLines(strings.NewReader(big+"\n{}\n"), func(l []byte) { got = append(got, len(l)) }, nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != len(big) || got[1] != 2 {
		t.Errorf("line lengths = %v, want [%d 2]", got, len(big))
	}
}

type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

type notes struct {
	mu      sync.Mutex
	methods []string
	sizes   []int
}

func (n *notes) hook(method string, params json.RawMessage) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.methods = append(n.methods, method)
	n.sizes = append(n.sizes, len(params))
}

func (n *notes) has(method string) (int, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if i := slices.Index(n.methods, method); i >= 0 {
		return n.sizes[i], true
	}
	return 0, false
}

func startJsonRpcFake(t *testing.T, fake *providertest.Fake, opts StartOptions) Session {
	t.Helper()
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "jsonrpc-long-lines",
		Adapter: &minimalAdapter{binary: fake.Path},
		Caps:    Capabilities{JsonRpcStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	opts.Workdir = dir
	opts.LogPath = filepath.Join(dir, "session.log")
	sess, err := rt.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Stop(context.Background())
		_, _ = sess.Wait()
	})
	return sess
}

// The torque#149 shape: a 1.5 MiB item/completed, then a normal frame, then a
// steering turn/start that must be answered rather than time out.
func TestJsonRpcStdioSession_LineOverOneMiBKeepsTheSessionUsable(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Script(
		providertest.Stdout(bigNotification(overOneMiB)),
		providertest.Stdout(`{"jsonrpc":"2.0","method":"after/big","params":{}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"turn/start"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":1,"result":{"steered":true}}`),
		providertest.AwaitEOF(),
	))
	var n notes
	sess := startJsonRpcFake(t, fake, StartOptions{JsonRpcNotificationHook: n.hook})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := sess.(JsonRpcCaller).Call(ctx, "turn/start", map[string]any{"input": "steer"})
	if err != nil {
		t.Fatalf("steering Call after a 1.5 MiB frame: %v", err)
	}
	if !strings.Contains(string(res), "steered") {
		t.Errorf("result = %s", res)
	}
	if size, ok := n.has("item/completed"); !ok || size < overOneMiB {
		t.Errorf("the 1.5 MiB notification was not routed whole (size %d, seen %v)", size, ok)
	}
	if _, ok := n.has("after/big"); !ok {
		t.Error("the frame after the big one was not routed")
	}
	if !sess.Health().Alive {
		t.Error("session reports not alive")
	}
}

// A line over maxLineBytes is skipped and noted, not fatal: the reader keeps
// draining and routing.
func TestJsonRpcStdioSession_OversizeLineIsSkippedNotFatal(t *testing.T) {
	// Restored in a cleanup registered before the session's, so it runs
	// after the session (and its reader) has stopped.
	old := maxLineBytes
	t.Cleanup(func() { maxLineBytes = old })
	maxLineBytes = 4096
	fake := providertest.New(t, runtimes.Codex, providertest.Script(
		providertest.Stdout(bigNotification(64*1024)),
		providertest.Stdout(`{"jsonrpc":"2.0","method":"after/big","params":{}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"turn/start"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		providertest.AwaitEOF(),
	))
	var n notes
	sess := startJsonRpcFake(t, fake, StartOptions{JsonRpcNotificationHook: n.hook})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := sess.(JsonRpcCaller).Call(ctx, "turn/start", nil); err != nil {
		t.Fatalf("Call after an oversize line: %v", err)
	}
	if _, ok := n.has("item/completed"); ok {
		t.Error("the oversize frame was routed")
	}
	if _, ok := n.has("after/big"); !ok {
		t.Error("the frame after the oversize one was not routed")
	}
}

// A reader failure makes the session unusable at once: Health reports it
// not alive, and Call and SendInput fail with the reader's error instead of
// waiting out their deadlines.
func TestJsonRpcStdioSession_ReaderFaultFailsCallsFast(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Script(providertest.AwaitEOF()))
	sess := startJsonRpcFake(t, fake, StartOptions{})
	js := sess.(*jsonRpcStdioSession)
	js.readerFault.set(errors.New("agentsessions: jsonrpc-stdio output reader failed: boom"))

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.Call(ctx, "turn/start", nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("Call = %v, want the reader fault", err)
	}
	if err := js.SendInput(ctx, []byte("{}")); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("SendInput = %v, want the reader fault", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("failing took %v; it must not wait for a deadline", time.Since(start))
	}
	if sess.Health().Alive {
		t.Error("Health reports alive after a reader fault")
	}
}

// Once the reader has stopped, a Call fails at once instead of registering a
// response nobody will read.
func TestJsonRpcStdioSession_CallAfterReaderExitFailsFast(t *testing.T) {
	fake := providertest.New(t, runtimes.Codex, providertest.Script(providertest.AwaitEOF()))
	sess := startJsonRpcFake(t, fake, StartOptions{})
	js := sess.(*jsonRpcStdioSession)
	js.readerExited.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := js.Call(ctx, "turn/start", nil); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Call = %v, want an immediate reader-exited error", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Call took %v", time.Since(start))
	}
}

// Every Claude streaming session read its stdout through the same 1 MiB
// scanner, so one huge tool_result ended it. The reader now carries on: the
// session id after the big line arrives, and input still flows.
func TestStreamingStdioSession_LineOverOneMiBKeepsTheSessionUsable(t *testing.T) {
	big := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"` + strings.Repeat("z", overOneMiB) + `"}]}}`
	fake := providertest.New(t, runtimes.Claude, providertest.Script(
		providertest.Stdout(big),
		providertest.Stdout(`{"type":"system","subtype":"init","session_id":"after-big"}`),
		providertest.RecvLine(),
		providertest.Stdout(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"after-big"}`),
		providertest.AwaitEOF(),
	))
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "streaming-long-lines", Adapter: adapter, Caps: Capabilities{StreamingStdio: true}})
	if err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 4)
	dir := t.TempDir()
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		LogPath:     filepath.Join(dir, "session.log"),
		OnSessionID: func(id string) { ids <- id },
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Stop(context.Background())
		_, _ = sess.Wait()
	})

	select {
	case id := <-ids:
		if id != "after-big" {
			t.Fatalf("session id = %q", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no session id after the 1.5 MiB line: the reader stopped")
	}
	if err := sess.SendInput(context.Background(), []byte(`{"type":"user","message":{"role":"user","content":"next"}}`)); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c := fake.Calls(); len(c) > 0 && len(c[0].Stdin) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c := fake.Call(0); len(c.Stdin) == 0 || !strings.Contains(c.Stdin[0], "next") {
		t.Errorf("the child did not receive the next turn: %q", c.Stdin)
	}
	if !sess.Health().Alive {
		t.Error("session reports not alive")
	}
}
