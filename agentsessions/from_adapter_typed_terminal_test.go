//go:build !windows

package agentsessions

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	pevents "github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// typedEchoAdapter is echoAdapter that also taps typed events, as the built-in
// adapters do (provider.EventParser): "delta:x" is a Delta, "done" a Done and
// "boom:x" an Error.
type typedEchoAdapter struct {
	echoAdapter
}

func (a *typedEchoAdapter) ParseLineEvents(line []byte) ([]pevents.Event, error) {
	s := strings.TrimRight(string(line), "\r\n")
	switch {
	case strings.HasPrefix(s, "delta:"):
		return []pevents.Event{pevents.Delta{Text: strings.TrimPrefix(s, "delta:")}}, nil
	case s == "done":
		return []pevents.Event{pevents.Done{}}, nil
	case strings.HasPrefix(s, "boom:"):
		return []pevents.Event{pevents.Error{Message: strings.TrimPrefix(s, "boom:")}}, nil
	}
	return nil, nil
}

// typedTurns runs turns SendInputs on a subprocess-per-turn session over adapter
// and returns what TypedEventCallback saw, with each SendInput's error.
func typedTurns(t *testing.T, adapter provider.CLIAdapter, turns int) ([]pevents.Event, []error) {
	t.Helper()
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "typed-terminal", Kind: "cli", Adapter: adapter})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	var mu sync.Mutex
	var typed []pevents.Event
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir: t.TempDir(),
		TypedEventCallback: func(ev pevents.Event) {
			mu.Lock()
			typed = append(typed, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()
	var errs []error
	for range turns {
		errs = append(errs, sess.SendInput(context.Background(), []byte("ignored")))
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]pevents.Event(nil), typed...), errs
}

func countTerminals(evs []pevents.Event) int {
	n := 0
	for _, ev := range evs {
		switch ev.(type) {
		case pevents.Done, pevents.Error:
			n++
		}
	}
	return n
}

// A turn that ends without a terminal line must still end on the typed surface,
// or a consumer that reduces turns from it waits forever and the next turn's
// events join the unfinished one (CW-20261002-0061).
func TestAdapterRuntime_TypedCallbackGetsTheSynthesizedTerminal(t *testing.T) {
	t.Run("clean exit with no terminal line", func(t *testing.T) {
		dir := t.TempDir()
		script := writeTestScript(t, dir, []string{"delta:hello"})
		typed, errs := typedTurns(t, &typedEchoAdapter{echoAdapter{script: script}}, 1)
		if errs[0] != nil {
			t.Fatalf("SendInput: %v", errs[0])
		}
		if want := []pevents.Event{pevents.Delta{Text: "hello"}, pevents.Done{}}; !reflect.DeepEqual(typed, want) {
			t.Fatalf("typed = %+v, want %+v", typed, want)
		}
	})

	t.Run("process fails after output", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "fail.sh")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'delta:partial\\n'\nexit 7\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		typed, errs := typedTurns(t, &typedEchoAdapter{echoAdapter{script: path}}, 1)
		if errs[0] == nil {
			t.Fatal("SendInput = nil, want the process failure")
		}
		if len(typed) != 2 || typed[0] != (pevents.Delta{Text: "partial"}) {
			t.Fatalf("typed = %+v, want the delta then an error", typed)
		}
		got, ok := typed[1].(pevents.Error)
		if !ok || got.Message == "" || got.Err == nil || !strings.Contains(got.Message, "exited 7") {
			t.Fatalf("terminal = %+v, want an error naming the exit code", typed[1])
		}
	})

	t.Run("process fails with no output at all", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "die.sh")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'not signed in\\n' 1>&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		typed, errs := typedTurns(t, &typedEchoAdapter{echoAdapter{script: path}}, 1)
		if errs[0] == nil {
			t.Fatal("SendInput = nil, want the process failure")
		}
		if len(typed) != 1 {
			t.Fatalf("typed = %+v, want one error: a turn that fails before any output still ends", typed)
		}
		if _, ok := typed[0].(pevents.Error); !ok {
			t.Fatalf("typed[0] = %+v, want an error", typed[0])
		}
	})

	t.Run("each turn ends once", func(t *testing.T) {
		dir := t.TempDir()
		script := writeTestScript(t, dir, []string{"delta:hello"})
		typed, _ := typedTurns(t, &typedEchoAdapter{echoAdapter{script: script}}, 2)
		want := []pevents.Event{pevents.Delta{Text: "hello"}, pevents.Done{}, pevents.Delta{Text: "hello"}, pevents.Done{}}
		if !reflect.DeepEqual(typed, want) {
			t.Fatalf("typed = %+v, want %+v", typed, want)
		}
	})
}

// An adapter that ends its own turn on the typed surface is not given a second
// terminal event.
func TestAdapterRuntime_TypedCallbackIsNotDoubleFired(t *testing.T) {
	dir := t.TempDir()
	script := writeTestScript(t, dir, []string{"delta:hello", "done"})
	typed, errs := typedTurns(t, &typedEchoAdapter{echoAdapter{script: script}}, 2)
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("SendInput: %v", errs)
	}
	if n := countTerminals(typed); n != 2 {
		t.Fatalf("%d terminal events for two turns that each ended themselves, want 2: %+v", n, typed)
	}
}

// typedOnlyTerminalAdapter ends a turn on the typed surface only: its ParseLine
// reports the "done" line as nothing, so the session synthesizes the legacy
// terminal event, and must not then add a second typed one.
type typedOnlyTerminalAdapter struct {
	typedEchoAdapter
}

func (a *typedOnlyTerminalAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if strings.TrimRight(string(line), "\r\n") == "done" {
		return nil, nil
	}
	return a.echoAdapter.ParseLine(line)
}

func TestAdapterRuntime_TypedCallbackIsNotDoubleFiredWhenOnlyTheTypedParserEndsTheTurn(t *testing.T) {
	dir := t.TempDir()
	script := writeTestScript(t, dir, []string{"delta:hello", "done"})
	typed, errs := typedTurns(t, &typedOnlyTerminalAdapter{typedEchoAdapter{echoAdapter{script: script}}}, 1)
	if errs[0] != nil {
		t.Fatalf("SendInput: %v", errs[0])
	}
	if want := []pevents.Event{pevents.Delta{Text: "hello"}, pevents.Done{}}; !reflect.DeepEqual(typed, want) {
		t.Fatalf("typed = %+v, want the adapter's own done and no second one", typed)
	}
}

// Only a session that taps typed events owes the typed surface a terminal: an
// adapter with no typed parser reports nothing there, as documented, and keeps
// reporting nothing.
func TestAdapterRuntime_TypedCallbackStaysSilentForAnAdapterWithNoTypedParser(t *testing.T) {
	dir := t.TempDir()
	script := writeTestScript(t, dir, []string{"hello"})
	typed, errs := typedTurns(t, &deltaOnlyAdapter{script: script}, 1)
	if errs[0] != nil {
		t.Fatalf("SendInput: %v", errs[0])
	}
	if len(typed) != 0 {
		t.Fatalf("typed = %+v, want nothing", typed)
	}
}

// The real Claude adapter, against what it writes: a turn that ends itself
// reports exactly one done, and one whose process dies after an assistant
// message ends on an error.
func TestAdapterRuntime_TypedTerminalWithTheRealClaudeAdapter(t *testing.T) {
	t.Run("a turn that ends itself", func(t *testing.T) {
		fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/print_turn1"))
		adapter := provider.NewClaudeAdapter()
		adapter.Binary = fake.Path
		typed, errs := typedTurns(t, adapter, 1)
		if errs[0] != nil {
			t.Fatalf("SendInput: %v", errs[0])
		}
		if countTerminals(typed) != 1 {
			t.Fatalf("typed = %+v, want exactly one terminal event", typed)
		}
		if _, ok := typed[len(typed)-1].(pevents.Done); !ok {
			t.Fatalf("last typed event = %+v, want the turn's own done", typed[len(typed)-1])
		}
	})

	t.Run("a process that dies mid-turn", func(t *testing.T) {
		const assistant = `{"type":"assistant","uuid":"u1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"working on it"}]}}`
		fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.Stdout(assistant), providertest.Exit(1)))
		adapter := provider.NewClaudeAdapter()
		adapter.Binary = fake.Path
		typed, errs := typedTurns(t, adapter, 1)
		if errs[0] == nil {
			t.Fatal("SendInput = nil, want the process failure")
		}
		if len(typed) != 2 {
			t.Fatalf("typed = %+v, want the assistant text then an error", typed)
		}
		if d, ok := typed[0].(pevents.Delta); !ok || d.Text != "working on it" {
			t.Fatalf("typed[0] = %+v, want the assistant delta", typed[0])
		}
		if _, ok := typed[1].(pevents.Error); !ok {
			t.Fatalf("typed[1] = %+v, want the synthesized error", typed[1])
		}
	})
}
