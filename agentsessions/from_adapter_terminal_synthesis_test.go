package agentsessions

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// Regression coverage for: "a real OpenCode chat turn spawns, runs, and
// exits cleanly — but the chat harness never observes it as done." Root
// cause: OpencodeAdapter.ParseLine (go-providers) only ever emits
// llmtypes.EventDelta, by design — opencode's `opencode run` has no
// structured completion signal on stdout. Before this fix,
// adapterSession.handleRunnerEvent's runner.EventProcessExited case did
// nothing but reset the tracked pid; no terminal llmtypes.StreamEvent
// (EventDone/EventError) ever reached EventFanout/Fanout for such an
// adapter, so any consumer keyed off a terminal event (the wrapper's
// event_translator, and transitively Nanite's chat streamLoop) hung
// forever despite the real subprocess having already exited cleanly.
//
// deltaOnlyAdapter mirrors OpencodeAdapter's real ParseLine contract
// exactly: EventDelta for every non-empty stdout line, never a terminal
// event of its own, regardless of how the process exits.

// deltaOnlyAdapter is a provider.CLIAdapter whose ParseLine never emits a
// terminal llmtypes.StreamEvent (EventDone / EventError / EventUsage),
// mirroring go-providers' provider.OpencodeAdapter as it was before run
// mode moved to structured --format json output (it now emits its own
// EventDone at the end of a turn).
type deltaOnlyAdapter struct {
	script string
}

func (a *deltaOnlyAdapter) Name() string { return "delta-only-test" }

func (a *deltaOnlyAdapter) BuildArgs(prompt, _ string, _ string) []string {
	// Single-arg invocation: the test script ignores its argv entirely.
	return []string{}
}

func (a *deltaOnlyAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}
	text := strings.TrimRight(string(line), "\r\n")
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: text}}, nil
}

func (a *deltaOnlyAdapter) Detect() (string, bool) {
	return a.script, a.script != ""
}

// echoUsageAdapter is a provider.CLIAdapter whose ParseLine maps a "usage"
// line to llmtypes.EventUsage and nothing else to EventDone/EventError —
// used to confirm EventUsage alone is not a terminal signal and does not
// suppress synthesis.
type echoUsageAdapter struct {
	script string
}

func (a *echoUsageAdapter) Name() string { return "echo-usage-test" }

func (a *echoUsageAdapter) BuildArgs(prompt, _ string, _ string) []string {
	return []string{}
}

func (a *echoUsageAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	s := strings.TrimRight(string(line), "\r\n")
	switch {
	case strings.HasPrefix(s, "delta:"):
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: strings.TrimPrefix(s, "delta:")}}, nil
	case s == "usage":
		return []llmtypes.StreamEvent{{Type: llmtypes.EventUsage}}, nil
	}
	return nil, nil
}

func (a *echoUsageAdapter) Detect() (string, bool) {
	return a.script, a.script != ""
}

// TestAdapterRuntime_SynthesizesEventDone_WhenAdapterNeverEmitsTerminalEvent
// is the primary regression test: a REAL subprocess (no mocks) that prints
// plain text and exits 0, driven through an adapter shaped exactly like
// OpencodeAdapter's real contract (EventDelta only, never a terminal
// event). Asserts the terminal EventDone still reaches EventFanout and the
// byte Fanout, synthesized from the real process's clean exit.
func TestAdapterRuntime_SynthesizesEventDone_WhenAdapterNeverEmitsTerminalEvent(t *testing.T) {
	dir := t.TempDir()
	// Mirrors the real opencode CLI's shape observed live: plain text on
	// stdout, then a clean exit — no structured completion line at all.
	script := writeTestScript(t, dir, []string{
		"hello, ",
		"world",
	})

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "opencode-shaped",
		Kind:    "cli",
		Adapter: &deltaOnlyAdapter{script: script},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	var fanout bytes.Buffer
	eventCh := make(chan llmtypes.StreamEvent, 8)
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		Fanout:      &fanout,
		EventFanout: eventCh,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	// SendInput blocks on a real runner.Run for the whole turn — by the
	// time it returns, the real spawned subprocess has already exited.
	if err := sess.SendInput(context.Background(), []byte("ignored prompt")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	got := drainEvents(eventCh)
	if len(got) == 0 {
		t.Fatal("EventFanout received no events at all")
	}
	last := got[len(got)-1]
	if last.Type != llmtypes.EventDone {
		t.Fatalf("last EventFanout event = %+v, want a synthesized EventDone", last)
	}
	doneCount := 0
	for _, ev := range got {
		if ev.Type == llmtypes.EventDone {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Fatalf("EventFanout carried %d EventDone events, want exactly 1: %#v", doneCount, got)
	}

	if !strings.Contains(fanout.String(), "[turn_done]") {
		t.Errorf("byte Fanout missing synthesized turn_done marker, got %q", fanout.String())
	}
}

// TestAdapterRuntime_SynthesizesEventError_WhenAdapterNeverEmitsTerminalEvent_AndProcessFails
// covers the other half of "EventDone on a clean exit, EventError on a
// non-nil runner.Run error" — a REAL subprocess that exits non-zero,
// driven through the same never-emits-a-terminal-event adapter shape.
func TestAdapterRuntime_SynthesizesEventError_WhenAdapterNeverEmitsTerminalEvent_AndProcessFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test script needs sh; not running on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fail.sh")
	body := "#!/bin/sh\nprintf 'partial output\\n'\nexit 7\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "opencode-shaped-fail",
		Kind:    "cli",
		Adapter: &deltaOnlyAdapter{script: path},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	eventCh := make(chan llmtypes.StreamEvent, 8)
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		EventFanout: eventCh,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	sendErr := sess.SendInput(context.Background(), []byte("ignored"))
	if sendErr == nil {
		t.Fatal("SendInput = nil, want a non-nil error for a real non-zero exit")
	}

	got := drainEvents(eventCh)
	if len(got) == 0 {
		t.Fatal("EventFanout received no events at all")
	}
	last := got[len(got)-1]
	if last.Type != llmtypes.EventError {
		t.Fatalf("last EventFanout event = %+v, want a synthesized EventError", last)
	}
	if last.Error == "" {
		t.Error("synthesized EventError has empty Error text")
	}
}

// TestAdapterRuntime_DoesNotDoubleFireTerminalEvent_WhenAdapterEmitsItsOwn
// mirrors Codex's shape: the adapter's own ParseLine emits a real terminal
// event (Codex's "turn.completed" line → EventDone; echoAdapter's "done"
// line here, same effect) strictly before the process exits. Confirms the
// fix's turnSawTerminal tracking suppresses synthesis so the fanout still
// carries exactly one EventDone, not two.
func TestAdapterRuntime_DoesNotDoubleFireTerminalEvent_WhenAdapterEmitsItsOwn(t *testing.T) {
	dir := t.TempDir()
	script := writeTestScript(t, dir, []string{
		"delta:hello",
		"done",
	})

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "codex-shaped",
		Kind:    "cli",
		Adapter: &echoAdapter{script: script},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	eventCh := make(chan llmtypes.StreamEvent, 8)
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		EventFanout: eventCh,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	if err := sess.SendInput(context.Background(), []byte("ignored")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	got := drainEvents(eventCh)
	doneCount := 0
	for _, ev := range got {
		if ev.Type == llmtypes.EventDone {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Fatalf("EventFanout carried %d EventDone events for an adapter that emits its own terminal event, want exactly 1 (no double-fire): %#v", doneCount, got)
	}
}

// TestAdapterRuntime_UsageIsNotTerminal_CleanExit: EventUsage alone is not
// a terminal event. OpenCode's structured run mode reports usage once per
// step, several per turn, so a clean exit after usage but no EventDone still
// gets exactly one synthesized EventDone.
func TestAdapterRuntime_UsageIsNotTerminal_CleanExit(t *testing.T) {
	dir := t.TempDir()
	script := writeTestScript(t, dir, []string{
		"delta:hello",
		"usage",
	})

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "usage-only",
		Kind:    "cli",
		Adapter: &echoUsageAdapter{script: script},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	eventCh := make(chan llmtypes.StreamEvent, 8)
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		EventFanout: eventCh,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	if err := sess.SendInput(context.Background(), []byte("ignored")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	got := drainEvents(eventCh)
	usageCount, doneCount := 0, 0
	for _, ev := range got {
		switch ev.Type {
		case llmtypes.EventUsage:
			usageCount++
		case llmtypes.EventDone:
			doneCount++
		}
	}
	if usageCount != 1 || doneCount != 1 {
		t.Fatalf("EventFanout carried %d usage / %d done events, want 1 / 1 (usage is not terminal, done is synthesized): %#v", usageCount, doneCount, got)
	}
	if got[len(got)-1].Type != llmtypes.EventDone {
		t.Fatalf("last event = %+v, want the synthesized EventDone", got[len(got)-1])
	}
}

// TestAdapterRuntime_UsageThenCrash_SynthesizesEventError: a turn that
// reports usage for a finished step and then dies must still surface an
// EventError. Before usage stopped counting as terminal, the usage line
// suppressed synthesis and the crash was silent.
func TestAdapterRuntime_UsageThenCrash_SynthesizesEventError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test script needs sh; not running on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "usage-crash.sh")
	body := "#!/bin/sh\nprintf 'delta:step one\\n'\nprintf 'usage\\n'\nexit 3\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "usage-then-crash",
		Kind:    "cli",
		Adapter: &echoUsageAdapter{script: path},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}

	eventCh := make(chan llmtypes.StreamEvent, 8)
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:     dir,
		EventFanout: eventCh,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	if err := sess.SendInput(context.Background(), []byte("ignored")); err == nil {
		t.Fatal("SendInput = nil, want the non-zero exit")
	}

	got := drainEvents(eventCh)
	if len(got) == 0 {
		t.Fatal("EventFanout received no events at all")
	}
	last := got[len(got)-1]
	if last.Type != llmtypes.EventError || last.Error == "" {
		t.Fatalf("last event = %+v, want a synthesized EventError after usage: %#v", last, got)
	}
	for _, ev := range got {
		if ev.Type == llmtypes.EventDone {
			t.Fatalf("crashed turn carried an EventDone: %#v", got)
		}
	}
}
