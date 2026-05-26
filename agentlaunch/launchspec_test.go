package agentlaunch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// resolveSpecVars resolves a LaunchSpec's declared vars through the S4.2
// VarResolver so they can be handed to the S4.1 renderer. The canonical
// S4.4 spec uses literal var sources, so resolution is total and needs no
// transport. This is the same two-stage path the live launcher uses:
// VarResolver resolves, AssemblySpec.Render projects.
func resolveSpecVars(t *testing.T, spec LaunchSpec) map[string]any {
	t.Helper()
	vr := NewVarResolver(VarResolverOptions{Authorizer: AllowAllTrustAuthorizer{}})
	resolved, err := vr.ResolveAll(context.Background(), &spec.BootSpec, nil, nil)
	if err != nil {
		t.Fatalf("ResolveAll: %v", err)
	}
	out := make(map[string]any, len(resolved))
	for name, rv := range resolved {
		out[name] = rv.Value
	}
	return out
}

// renderBag resolves the spec vars and renders bag against spec — the
// full S4.2 + S4.1 path.
func renderBag(t *testing.T, spec LaunchSpec, bag LaunchBag) RenderResult {
	t.Helper()
	req := bag.RenderRequest(FrontEndAutonomous)
	req.Vars = resolveSpecVars(t, spec)
	res, err := spec.Render(req)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return res
}

// specsDir is the in-repo S4.4 testdata: the re-expressed launches/ +
// boot-profiles/ as Specs + templates.
const specsDir = "testdata/specs"

// loadCanonicalSpec loads the one canonical LaunchSpec the whole S4.4
// model collapses onto.
func loadCanonicalSpec(t *testing.T) LaunchSpec {
	t.Helper()
	spec, err := LoadLaunchSpec(filepath.Join(specsDir, "launch-assembly.yaml"))
	if err != nil {
		t.Fatalf("LoadLaunchSpec: %v", err)
	}
	return spec
}

func TestLaunchSpecLoadsAndValidates(t *testing.T) {
	spec := loadCanonicalSpec(t)
	if spec.ID != "tether.launch" {
		t.Fatalf("spec ID = %q, want tether.launch", spec.ID)
	}
	// The canonical spec must declare the full minimum-config knob set.
	declared := make(map[string]BootInput)
	for _, in := range spec.Inputs {
		declared[in.Name] = in
	}
	for _, name := range MinimumConfigInputs() {
		if _, ok := declared[name]; !ok {
			t.Fatalf("canonical spec missing minimum-config input %q", name)
		}
	}
	// work_dir + runner must be required; isolation + bus must NOT be.
	if !declared[LaunchInputWorkDir].Required || !declared[LaunchInputRunner].Required {
		t.Fatalf("work_dir and runner must be required inputs")
	}
	if declared[LaunchInputIsolation].Required || declared[LaunchInputBus].Required {
		t.Fatalf("isolation and bus must be optional inputs")
	}
}

// TestEveryLaunchBagLoadsAndPasses confirms every re-expressed launch bag
// loads, validates, passes the minimum-config check against the canonical
// spec, and renders a non-empty boot prompt.
func TestEveryLaunchBagLoadsAndPasses(t *testing.T) {
	spec := loadCanonicalSpec(t)
	bagDir := filepath.Join(specsDir, "launches")
	entries, err := os.ReadDir(bagDir)
	if err != nil {
		t.Fatalf("read launches dir: %v", err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		count++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			bag, err := LoadLaunchBag(filepath.Join(bagDir, name))
			if err != nil {
				t.Fatalf("LoadLaunchBag: %v", err)
			}
			if bag.Spec != spec.ID {
				t.Fatalf("bag spec ref = %q, want %q", bag.Spec, spec.ID)
			}
			if err := ValidateMinimumConfig(spec, bag); err != nil {
				t.Fatalf("ValidateMinimumConfig: %v", err)
			}
			res := renderBag(t, spec, bag)
			if strings.TrimSpace(res.Body) == "" {
				t.Fatalf("rendered boot prompt is empty")
			}
			// runner is provider-as-input — it must reach the body.
			if !strings.Contains(res.Body, "Runner:") {
				t.Fatalf("rendered body missing Runner line")
			}
		})
	}
	if count == 0 {
		t.Fatal("no launch bags found")
	}
}

// TestMinimumConfigBagIsTwoKnobs proves the minimum valid config is
// exactly work_dir + runner: the tether-minimum bag supplies only those
// two and still passes and renders.
func TestMinimumConfigBagIsTwoKnobs(t *testing.T) {
	spec := loadCanonicalSpec(t)
	bag, err := LoadLaunchBag(filepath.Join(specsDir, "launches", "tether-minimum.yaml"))
	if err != nil {
		t.Fatalf("LoadLaunchBag: %v", err)
	}
	// The minimum bag must carry ONLY the two required knobs.
	if len(bag.Inputs) != 2 {
		t.Fatalf("minimum bag has %d inputs, want exactly 2 (work_dir + runner)", len(bag.Inputs))
	}
	if _, ok := bag.Inputs[LaunchInputWorkDir]; !ok {
		t.Fatalf("minimum bag missing %q", LaunchInputWorkDir)
	}
	if _, ok := bag.Inputs[LaunchInputRunner]; !ok {
		t.Fatalf("minimum bag missing %q", LaunchInputRunner)
	}
	if err := ValidateMinimumConfig(spec, bag); err != nil {
		t.Fatalf("ValidateMinimumConfig on minimum bag: %v", err)
	}
	if res := renderBag(t, spec, bag); strings.TrimSpace(res.Body) == "" {
		t.Fatalf("rendered minimum bag is empty")
	}
}

func TestValidateMinimumConfigRejectsMissingWorkDir(t *testing.T) {
	spec := loadCanonicalSpec(t)
	bag := LaunchBag{
		Spec:   spec.ID,
		Name:   "no-work-dir",
		Inputs: map[string]any{LaunchInputRunner: "claude-code"},
	}
	err := ValidateMinimumConfig(spec, bag)
	if !errors.Is(err, ErrLaunchSpecMissingWorkDir) {
		t.Fatalf("err = %v, want ErrLaunchSpecMissingWorkDir", err)
	}
}

func TestValidateMinimumConfigRejectsMissingRunner(t *testing.T) {
	spec := loadCanonicalSpec(t)
	bag := LaunchBag{
		Spec:   spec.ID,
		Name:   "no-runner",
		Inputs: map[string]any{LaunchInputWorkDir: "~/dev/x"},
	}
	err := ValidateMinimumConfig(spec, bag)
	if !errors.Is(err, ErrLaunchSpecMissingRunner) {
		t.Fatalf("err = %v, want ErrLaunchSpecMissingRunner", err)
	}
}

func TestValidateMinimumConfigRejectsUnknownInput(t *testing.T) {
	spec := loadCanonicalSpec(t)
	bag := LaunchBag{
		Spec: spec.ID,
		Name: "typo",
		Inputs: map[string]any{
			LaunchInputWorkDir: "~/dev/x",
			LaunchInputRunner:  "claude-code",
			"workdir":          "~/dev/x", // typo of work_dir
		},
	}
	err := ValidateMinimumConfig(spec, bag)
	if !errors.Is(err, ErrLaunchSpecUnknownInput) {
		t.Fatalf("err = %v, want ErrLaunchSpecUnknownInput", err)
	}
}

// TestWorktreeTwinCollapsed is the core S4.4 acceptance check: the legacy
// `.worktree` TWIN file is gone. The two tether bags differ ONLY in the
// `isolation` input — mode is an input, not a second file. Their rendered
// bodies differ only in the lines carrying isolation/display_name.
func TestWorktreeTwinCollapsed(t *testing.T) {
	spec := loadCanonicalSpec(t)
	hybrid, err := LoadLaunchBag(filepath.Join(specsDir, "launches", "tether-claude.yaml"))
	if err != nil {
		t.Fatalf("load hybrid bag: %v", err)
	}
	worktree, err := LoadLaunchBag(filepath.Join(specsDir, "launches", "tether-claude-worktree.yaml"))
	if err != nil {
		t.Fatalf("load worktree bag: %v", err)
	}

	// Both reference the SAME spec — there is no twin spec.
	if hybrid.Spec != worktree.Spec {
		t.Fatalf("hybrid and worktree bags reference different specs: %q vs %q",
			hybrid.Spec, worktree.Spec)
	}

	// The bags differ ONLY in isolation (and the display_name label).
	diffKeys := inputDiffKeys(hybrid.Inputs, worktree.Inputs)
	sort.Strings(diffKeys)
	want := []string{"display_name", LaunchInputIsolation}
	sort.Strings(want)
	if strings.Join(diffKeys, ",") != strings.Join(want, ",") {
		t.Fatalf("hybrid vs worktree bag differ in %v, want only %v", diffKeys, want)
	}

	if hybrid.Inputs[LaunchInputIsolation] != "hybrid" {
		t.Fatalf("hybrid bag isolation = %v, want hybrid", hybrid.Inputs[LaunchInputIsolation])
	}
	if worktree.Inputs[LaunchInputIsolation] != "worktree" {
		t.Fatalf("worktree bag isolation = %v, want worktree", worktree.Inputs[LaunchInputIsolation])
	}

	// Both render against the one spec. The rendered bodies must differ
	// only in the isolation and display_name lines — same spec, same
	// vars, one input flipped.
	hybridBody := renderBag(t, spec, hybrid).Body
	worktreeBody := renderBag(t, spec, worktree).Body
	if !strings.Contains(hybridBody, "Isolation:  hybrid") {
		t.Fatalf("hybrid body missing 'Isolation:  hybrid'")
	}
	if !strings.Contains(worktreeBody, "Isolation:  worktree") {
		t.Fatalf("worktree body missing 'Isolation:  worktree'")
	}
}

// TestProviderIsAnInput proves provider/runner is an INPUT, not blueprint
// identity (D3): five agent-mux bags differ only in runner and reuse the
// one spec — the legacy catalog had a separate launch file per provider.
func TestProviderIsAnInput(t *testing.T) {
	spec := loadCanonicalSpec(t)
	bags := []string{
		"agent-mux-claude.yaml",
		"agent-mux-codex-launch.yaml",
		"agent-mux-codex-app-server.yaml",
		"agent-mux-opencode.yaml",
		"agent-mux-claude-stream-worktree.yaml",
	}
	seenRunners := make(map[string]struct{})
	for _, name := range bags {
		bag, err := LoadLaunchBag(filepath.Join(specsDir, "launches", name))
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		if bag.Spec != spec.ID {
			t.Fatalf("%s references spec %q, want %q (provider must not change spec identity)",
				name, bag.Spec, spec.ID)
		}
		runner, _ := bag.Inputs[LaunchInputRunner].(string)
		seenRunners[runner] = struct{}{}
	}
	if len(seenRunners) < 4 {
		t.Fatalf("expected >=4 distinct runners across agent-mux bags, got %d", len(seenRunners))
	}
}

// TestCommonSetupTemplatesExist confirms the starter common-setup
// template set ships and each template carries a runner.
func TestCommonSetupTemplatesExist(t *testing.T) {
	tmplDir := filepath.Join(specsDir, "templates")
	entries, err := os.ReadDir(tmplDir)
	if err != nil {
		t.Fatalf("read templates dir: %v", err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		count++
		raw, err := os.ReadFile(filepath.Join(tmplDir, e.Name()))
		if err != nil {
			t.Fatalf("read template %s: %v", e.Name(), err)
		}
		if !strings.Contains(string(raw), "runner:") {
			t.Fatalf("template %s declares no runner", e.Name())
		}
	}
	if count < 4 {
		t.Fatalf("expected a starter set of >=4 common-setup templates, got %d", count)
	}
}

// inputDiffKeys returns the keys whose values differ between two input
// maps (keys present in only one map count as differing).
func inputDiffKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{})
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	var diff []string
	for k := range seen {
		if a[k] != b[k] {
			diff = append(diff, k)
		}
	}
	return diff
}
