package providerplant

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

var updateEmptyPosture = flag.Bool("update-empty-posture", false, "rewrite testdata/empty_posture.json from this tree")

// emptyPostureGolden is what a launch that names no permission posture
// carries: its argv, its launch template's extra arguments and its
// environment, with the per-test directories replaced by placeholders.
// testdata/empty_posture.json was generated from agentkit v0.16.0, before
// the posture hook existed; a launch with an empty Provider.Permission must
// still produce exactly that (CW-20260930-0138). Regenerate it with
// -update-empty-posture only for a deliberate argv or environment change.
type emptyPostureGolden struct {
	Argv      []string          `json:"argv"`
	ExtraArgs []string          `json:"extra_args"`
	Env       map[string]string `json:"env"`
}

func TestPrepareExecution_EmptyPostureMatchesV0_16_0(t *testing.T) {
	cases := []struct {
		provider string
		mode     runtimes.Mode
	}{
		{"claude", runtimes.ModeStreamingStdio},
		{"claude", runtimes.ModeSubprocessPerTurn},
		{"claude", runtimes.ModePTY},
		{"codex", runtimes.ModeJSONRPCStdio},
		{"codex", runtimes.ModeSubprocessPerTurn},
		{"opencode", runtimes.ModeSubprocessPerTurn},
		{"opencode", runtimes.ModeHTTPSSE},
		{"antigravity", runtimes.ModeSubprocessPerTurn},
	}
	got := map[string]emptyPostureGolden{}
	for _, c := range cases {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
		compiled := compiledWith(t, c.provider, c.mode, agentlaunch.InjectionSpec{Args: []string{"--injected"}})
		compiled.Plan.Provider.Flags = []string{"--flag-a"}
		prepared, err := launcher.Prepare(context.Background(), compiled)
		if err != nil {
			t.Fatalf("%s/%s: prepare: %v", c.provider, c.mode, err)
		}
		exec, err := PrepareExecution(context.Background(), prepared)
		if err != nil {
			t.Fatalf("%s/%s: PrepareExecution: %v", c.provider, c.mode, err)
		}
		placeholders := strings.NewReplacer(
			exec.Roots.BootRoot, "<boot>",
			exec.Roots.ProjectRoot, "<project>",
			prepared.WorkspaceDir, "<state>",
			home, "<home>",
		)
		g := emptyPostureGolden{Env: map[string]string{}}
		for _, a := range exec.Bindings.Argv {
			g.Argv = append(g.Argv, placeholders.Replace(a))
		}
		if exec.Bindings.Launch != nil {
			g.ExtraArgs = append(g.ExtraArgs, exec.Bindings.Launch.ExtraArgs...)
		}
		for k, v := range exec.Bindings.Env {
			g.Env[k] = placeholders.Replace(v.Value)
		}
		got[c.provider+"/"+string(c.mode)] = g
	}

	path := filepath.Join("testdata", "empty_posture.json")
	if *updateEmptyPosture {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]emptyPostureGolden
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	wantJSON, _ := json.MarshalIndent(want, "", "  ")
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("an empty posture changed the launch from v0.16.0's:\ngot  %s\nwant %s", gotJSON, wantJSON)
	}
}
