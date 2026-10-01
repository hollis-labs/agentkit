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
