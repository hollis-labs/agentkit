//go:build !windows

package agentsessions

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/providertest"
)

// startStreamingClaude starts a streaming-stdio Claude session on fake and
// returns it with its event stream.
func startStreamingClaude(t *testing.T, fake *providertest.Fake) (Session, <-chan llmtypes.StreamEvent) {
	t.Helper()
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "streaming-interrupt", Adapter: adapter, Caps: Capabilities{StreamingStdio: true}})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan llmtypes.StreamEvent, 256)
	dir := t.TempDir()
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		LogPath:     filepath.Join(dir, "session.log"),
		EventFanout: events,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Stop(context.Background())
		_, _ = sess.Wait()
	})
	return sess, events
}

func userFrame(text string) []byte {
	return []byte(`{"type":"user","message":{"role":"user","content":"` + text + `"}}`)
}

// waitEvent returns the first event match accepts, failing after 10s.
func waitEvent(t *testing.T, events <-chan llmtypes.StreamEvent, what string, match func(llmtypes.StreamEvent) bool) llmtypes.StreamEvent {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %s within 10s", what)
		}
	}
}

// The live capture (claude 2.1.286): a turn whose tool is running gets a
// control_request interrupt. InterruptTurn returns on Claude's
// control_response; the turn ends with Claude's error result; the process
// stays up and the next input runs a normal turn (CW-20261001-0103).
func TestStreamingStdioSession_InterruptTurnKeepsTheProcess(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_interrupt"))
	sess, events := startStreamingClaude(t, fake)

	if err := sess.SendInput(context.Background(), userFrame("run something slow")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	waitEvent(t, events, "tool use", func(ev llmtypes.StreamEvent) bool { return ev.Type == llmtypes.EventToolUse })
	pid := sess.(*streamingStdioSession).LivePID()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sess.(TurnInterrupter).InterruptTurn(ctx); err != nil {
		t.Fatalf("InterruptTurn: %v", err)
	}
	waitEvent(t, events, "the interrupted turn's error", func(ev llmtypes.StreamEvent) bool { return ev.Type == llmtypes.EventError })

	if err := sess.SendInput(context.Background(), userFrame("next")); err != nil {
		t.Fatalf("SendInput after the interrupt: %v", err)
	}
	waitEvent(t, events, "the next turn's done", func(ev llmtypes.StreamEvent) bool { return ev.Type == llmtypes.EventDone })
	if got := sess.(*streamingStdioSession).LivePID(); got != pid || !sess.Health().Alive {
		t.Errorf("the process did not survive the interrupt: pid %d → %d, alive %v", pid, got, sess.Health().Alive)
	}
	if c := fake.Call(0); len(c.Stdin) != 3 || !strings.Contains(c.Stdin[1], `"subtype":"interrupt"`) {
		t.Errorf("stdin = %q, want user, interrupt, user", c.Stdin)
	}
	if len(fake.Calls()) != 1 {
		t.Errorf("the CLI was started %d times", len(fake.Calls()))
	}
}

// With no turn in flight Claude acknowledges and nothing else follows;
// InterruptTurn must return on the acknowledgement, not wait for a result.
func TestStreamingStdioSession_InterruptWithNoTurnReturnsOnTheAck(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(
		providertest.Stdout(`{"type":"system","subtype":"init","session_id":"s1"}`),
		providertest.Recv(`{"type":"control_request","request_id":"recorded","request":{"subtype":"interrupt"}}`),
		providertest.Send(`{"type":"control_response","response":{"subtype":"success","request_id":"recorded","response":{"still_queued":[]}}}`),
		providertest.AwaitEOF(),
	))
	sess, _ := startStreamingClaude(t, fake)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sess.(TurnInterrupter).InterruptTurn(ctx); err != nil {
		t.Fatalf("InterruptTurn: %v", err)
	}
}

func TestStreamingStdioSession_InterruptRefusedOrUnanswered(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		fake := providertest.New(t, runtimes.Claude, providertest.Script(
			providertest.Recv(`{"type":"control_request","request_id":"r","request":{"subtype":"interrupt"}}`),
			providertest.Send(`{"type":"control_response","response":{"subtype":"error","request_id":"r","error":"busy"}}`),
			providertest.AwaitEOF(),
		))
		sess, _ := startStreamingClaude(t, fake)
		err := sess.(TurnInterrupter).InterruptTurn(context.Background())
		if !errors.Is(err, provider.ErrInterruptRefused) || !strings.Contains(err.Error(), "busy") {
			t.Errorf("InterruptTurn = %v, want the refusal", err)
		}
	})
	t.Run("no answer before ctx ends", func(t *testing.T) {
		fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.AwaitEOF()))
		sess, _ := startStreamingClaude(t, fake)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if err := sess.(TurnInterrupter).InterruptTurn(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("InterruptTurn = %v, want the deadline", err)
		}
	})
	t.Run("the CLI exits before answering", func(t *testing.T) {
		fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.RecvLine(), providertest.Exit(0)))
		sess, _ := startStreamingClaude(t, fake)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := sess.(TurnInterrupter).InterruptTurn(ctx); !errors.Is(err, ErrInterruptUnanswered) && !errors.Is(err, ErrNoInputChannel) {
			t.Errorf("InterruptTurn = %v, want ErrInterruptUnanswered", err)
		}
	})
}

// An adapter without provider.TurnInterrupter cannot interrupt.
func TestStreamingStdioSession_InterruptUnsupported(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.AwaitEOF()))
	adapter := provider.NewClaudeAdapterStreamingStdio()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "no-interrupt", Adapter: plainCLI{adapter}, Caps: Capabilities{StreamingStdio: true}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, LogPath: filepath.Join(dir, "session.log")})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()); _, _ = sess.Wait() })
	if err := sess.(TurnInterrupter).InterruptTurn(context.Background()); !errors.Is(err, ErrInterruptUnsupported) {
		t.Errorf("InterruptTurn = %v, want ErrInterruptUnsupported", err)
	}
}

// plainCLI hides every optional interface of the adapter it wraps.
type plainCLI struct{ provider.CLIAdapter }
