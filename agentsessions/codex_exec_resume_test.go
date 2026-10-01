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

// codexResumeTailOK reports whether the arguments between `resume <id>` and
// "--" are ones codex-cli 0.159.2 accepts after the resume subcommand.
// Measured: -c, -m and --dangerously-bypass-approvals-and-sandbox parse
// there; -s, --cd and --add-dir fail with "unexpected argument". The fake
// replays output without parsing argv, so this check stands in for codex's
// own parser.
func codexResumeTailOK(tail []string) bool {
	for i := 0; i < len(tail); i++ {
		switch tail[i] {
		case "-c", "--config", "-m", "--model":
			i++ // the value
		case "--dangerously-bypass-approvals-and-sandbox":
		default:
			return false
		}
	}
	return true
}

// An auto-planted codex exec session resumes its thread on turn 2 with
// `exec … --cd <project> resume <id> -- <prompt>`: --cd in front of the
// subcommand, where codex takes it, and nothing after `resume <id>` that
// codex refuses there. Before agentkit v0.20.4 the planted --cd was spliced
// in through ExtraArgs, after `resume <id>`, and turn 2 failed against the
// real CLI (CW-20261001-0194). Turn 1's argv is unchanged.
func TestAutoPlantedCodexExecResumesWithCdBeforeResume(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra []string
	}{
		{"no extra args", nil},
		// StartOptions.ExtraArgs are still spliced before "--", which on a
		// resume turn is after `resume <id>`: they must be resume-safe.
		// This pins that ordering; CW-20261001-0197 moves them to the
		// convention's extra slot, in front of `resume`.
		{"resume-safe extra args", []string{"-c", `sandbox_mode="read-only"`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Codex,
				providertest.Replay("codex/exec_turn1"),
				providertest.Replay("codex/exec_turn2_resume"))
			adapter := provider.NewCodexAdapter()
			adapter.Binary = fake.Path
			rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "codex-exec-resume", Kind: "cli", Adapter: adapter, Caps: Capabilities{ProviderSessionID: true}})
			if err != nil {
				t.Fatal(err)
			}
			dir, project := t.TempDir(), t.TempDir()
			sess, err := rt.Start(context.Background(), StartOptions{
				Workdir:          project,
				LogPath:          filepath.Join(dir, "session.log"),
				AutoPlantBootDir: true,
				BootDirRoot:      dir,
				ExtraArgs:        c.extra,
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() {
				_ = sess.Stop(context.Background())
				_, _ = sess.Wait()
			})
			for _, prompt := range []string{"say hi", "say bye"} {
				if err := sess.SendInput(context.Background(), []byte(prompt)); err != nil {
					t.Fatalf("SendInput(%q): %v", prompt, err)
				}
			}
			calls := fake.Calls()
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want 2", len(calls))
			}

			first := calls[0].Args
			if c.extra == nil {
				want := []string{"exec", "--json", "--skip-git-repo-check", "--cd", project, "--", "say hi"}
				if !slices.Equal(first, want) {
					t.Errorf("turn 1 argv = %q, want %q (unchanged)", first, want)
				}
			}
			if slices.Contains(first, "resume") {
				t.Errorf("turn 1 resumed: %q", first)
			}

			second := calls[1].Args
			r, dd := slices.Index(second, "resume"), slices.Index(second, "--")
			if r < 0 || dd < r+2 {
				t.Fatalf("turn 2 argv = %q, want `resume <id>` before \"--\"", second)
			}
			if second[r+1] != "00000000-0000-4000-8000-000000000001" {
				t.Errorf("turn 2 resumes %q, want turn 1's thread", second[r+1])
			}
			if cd := slices.Index(second, "--cd"); cd < 0 || cd > r || second[cd+1] != project {
				t.Errorf("turn 2 argv = %q, want --cd %s before resume", second, project)
			}
			if tail := second[r+2 : dd]; !codexResumeTailOK(tail) {
				t.Errorf("turn 2 argv = %q: codex refuses %q after `resume <id>`", second, tail)
			}
			if !slices.Equal(second[dd+1:], []string{"say bye"}) {
				t.Errorf("turn 2 after \"--\" = %q, want the prompt", second[dd+1:])
			}
		})
	}
}
