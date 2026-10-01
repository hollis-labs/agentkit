//go:build !windows

package agentsessions

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/providertest"
)

func TestWithExtraArgs(t *testing.T) {
	for _, c := range []struct {
		name              string
		args, extra, want []string
	}{
		{"no extra", []string{"-p", "--", "hi"}, nil, []string{"-p", "--", "hi"}},
		{"no --, appended", []string{"-p", "--verbose"}, []string{"--add-dir", "/d"}, []string{"-p", "--verbose", "--add-dir", "/d"}},
		{"before --", []string{"-p", "--", "hi"}, []string{"--add-dir", "/d"}, []string{"-p", "--add-dir", "/d", "--", "hi"}},
		{"before the first --", []string{"-p", "--", "a", "--"}, []string{"-x"}, []string{"-p", "-x", "--", "a", "--"}},
		{"extra with its own --, appended", []string{"-p", "--", "hi"}, []string{"-x", "--", "p"}, []string{"-p", "--", "hi", "-x", "--", "p"}},
	} {
		if got := withExtraArgs(slices.Clone(c.args), c.extra); !slices.Equal(got, c.want) {
			t.Errorf("%s: withExtraArgs(%q, %q) = %q, want %q", c.name, c.args, c.extra, got, c.want)
		}
	}
}

// splicingOnly hides an adapter's optional interfaces, ExtraArgsBuilder
// among them.
type splicingOnly struct{ provider.CLIAdapter }

// adapterArgs places extras at the convention slot through
// provider.ExtraArgsBuilder when the adapter has it, and splices them before
// "--" when it does not, or when they carry their own "--" (CW-20261001-0197).
func TestAdapterArgsPrefersTheConventionSlot(t *testing.T) {
	codex := &provider.CodexAdapter{ProjectDir: "/p"}
	extra := []string{"-s", "read-only"}
	for _, c := range []struct {
		name    string
		adapter provider.CLIAdapter
		extra   []string
		want    []string
	}{
		{"builder: in front of resume", codex, extra,
			[]string{"exec", "-s", "read-only", "--json", "--skip-git-repo-check", "--cd", "/p", "resume", "t1", "--", "hi"}},
		{"no builder: spliced before --", splicingOnly{codex}, extra,
			[]string{"exec", "--json", "--skip-git-repo-check", "--cd", "/p", "resume", "t1", "-s", "read-only", "--", "hi"}},
		{"extra with its own --: appended", codex, []string{"-x", "--", "tail"},
			[]string{"exec", "--json", "--skip-git-repo-check", "--cd", "/p", "resume", "t1", "--", "hi", "-x", "--", "tail"}},
		{"no extra", codex, nil,
			[]string{"exec", "--json", "--skip-git-repo-check", "--cd", "/p", "resume", "t1", "--", "hi"}},
	} {
		if got := adapterArgs(c.adapter, "hi", "", "t1", c.extra); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// Since go-providers v0.34.1 claude -p's argv ends in "-- <prompt>". A
// session's ExtraArgs (a caller's, or AutoPlantBootDir's --add-dir) used to be
// appended after it and reached claude as prompt text (CW-20261001-0102).
// They now precede "--", and the turn's prompt is last.
func TestSubprocessSession_ExtraArgsPrecedeThePrompt(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/print_turn1"))
	adapter := provider.NewClaudeAdapter()
	adapter.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "extra-args", Kind: "cli", Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	extra := []string{"--add-dir", "/work/project", "--flag-a"}
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, LogPath: filepath.Join(dir, "session.log"), ExtraArgs: extra})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Stop(context.Background())
		_, _ = sess.Wait()
	})
	if err := sess.SendInput(context.Background(), []byte("say hi")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	args := fake.Call(0).Args
	dd := slices.Index(args, "--")
	if dd < 0 {
		t.Fatalf("argv has no \"--\"; this test needs go-providers v0.34.1 or later: %q", args)
	}
	if dd < len(extra) || !slices.Equal(args[dd-len(extra):dd], extra) {
		t.Errorf("ExtraArgs are not immediately before \"--\": %q", args)
	}
	if got := args[dd+1:]; !slices.Equal(got, []string{"say hi"}) {
		t.Errorf("after \"--\" = %q, want only the turn's prompt", got)
	}
}

// An extra that carries its own "--" is a whole argv tail whose positionals
// its caller placed. It keeps the old splice even when the adapter can place
// extras at the convention slot: appended after BuildArgs' output, unchanged
// (CW-20261001-0197).
func TestSubprocessSession_ExtraArgsWithTheirOwnDashDashAreAppendedAsBefore(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/print_turn1"))
	adapter := provider.NewClaudeAdapter()
	adapter.Binary = fake.Path
	if _, ok := any(adapter).(provider.ExtraArgsBuilder); !ok {
		t.Fatal("the claude adapter should implement ExtraArgsBuilder, so this test exercises the fallback")
	}
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "extra-args-tail", Kind: "cli", Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	extra := []string{"--add-dir", "/work/extra", "--", "tail-positional"}
	sess, err := rt.Start(context.Background(), StartOptions{Workdir: dir, LogPath: filepath.Join(dir, "session.log"), ExtraArgs: extra})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Stop(context.Background())
		_, _ = sess.Wait()
	})
	if err := sess.SendInput(context.Background(), []byte("say hi")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	want := append(adapter.BuildArgs("say hi", "", ""), extra...)
	if got := fake.Call(0).Args; !slices.Equal(got, want) {
		t.Errorf("argv with an extra carrying its own \"--\"\n got %q\nwant %q (BuildArgs' output, then the extras unchanged)", got, want)
	}
}
