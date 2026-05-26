package parity

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch/catalog"
)

// repoRoot walks up from this test file to the go-agent-launch module
// root, so the harness can locate the S4.4 testdata specs and the shipped
// fixture catalog regardless of the working directory CI runs from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../agentlaunch/parity/parity_test.go -> module root is two dirs up.
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// specsRoot is the S4.4 testdata specs directory — the new side's input.
func specsRoot(t *testing.T) string {
	return filepath.Join(repoRoot(t), "agentlaunch", "testdata", "specs")
}

// callerDir returns the directory holding this test file (the parity
// package dir), used to locate the shipped fixture catalog under testdata/.
func callerDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

// TestParity_FixtureCorpus is the CI-load-bearing parity check. It runs the
// old-vs-new harness over the shipped fixture catalog (deterministic, no
// Tether install required) and asserts:
//
//   - every corpus launch resolves on at least one side;
//   - every non-zero field diff is a documented intentional difference;
//   - there are zero UNEXPLAINED diffs.
//
// This is the test the .github/workflows CI job runs on every push/PR.
func TestParity_FixtureCorpus(t *testing.T) {
	catalogRoot := filepath.Join(callerDir(t), "testdata", "catalog")
	report, err := RunParity(catalogRoot, specsRoot(t))
	if err != nil {
		t.Fatalf("RunParity setup failed: %v", err)
	}

	t.Log("\n" + report.Summary())

	if len(report.Cases) == 0 {
		t.Fatal("parity report has no cases — corpus did not run")
	}

	// Every UNEXPLAINED diff is a hard failure.
	if un := report.UnexplainedDiffs(); len(un) > 0 {
		for _, u := range un {
			t.Errorf("UNEXPLAINED diff: launch=%s field=%s old=%q new=%q",
				u.Launch, u.Diff.Field, u.Diff.Old, u.Diff.New)
		}
	}

	// Every case must be a clean parity pass (identical, or every
	// divergence documented).
	for _, c := range report.Cases {
		if c.NewErr != nil {
			t.Errorf("%s: new-side resolution failed: %v", c.Launch, c.NewErr)
			continue
		}
		if c.OldErr != nil {
			if _, ok := c.expectedOldErrRationale(); !ok {
				t.Errorf("%s: unexplained old-side resolution failure: %v", c.Launch, c.OldErr)
			}
			continue
		}
		if !c.Parity() {
			t.Errorf("%s: case did not reach parity", c.Launch)
		}
	}

	if !report.Passed() {
		t.Errorf("parity report did not pass — see summary above")
	}
}

// TestParity_ExpectedDiffsAreObserved guards the expected-diff /
// expected-old-error registries against rot: every registered intentional
// divergence must actually be produced by the harness. A registry entry
// that never fires is stale and would silently mask a future real diff on
// the same launch+field. Report.StaleExpected does the check.
func TestParity_ExpectedDiffsAreObserved(t *testing.T) {
	catalogRoot := filepath.Join(callerDir(t), "testdata", "catalog")
	report, err := RunParity(catalogRoot, specsRoot(t))
	if err != nil {
		t.Fatalf("RunParity: %v", err)
	}
	if stale := report.StaleExpected(); len(stale) != 0 {
		t.Errorf("stale expected-registry entries (registered but never observed): %v", stale)
	}
}

// TestParity_RunOptions exercises the consumer-extensibility surface:
// WithCorpus (RunParity iterates exactly the supplied corpus) and
// WithExpectedOldErrors (a caller-registered rationale is merged in and
// consulted, and is staleness-checked like a built-in entry).
func TestParity_RunOptions(t *testing.T) {
	catalogRoot := filepath.Join(callerDir(t), "testdata", "catalog")

	// WithCorpus + WithExpectedOldErrors: a 2-entry corpus — one clean
	// launch, one dangling-agent launch the caller registers — passes.
	corpus := []CorpusEntry{
		{BagFile: "tether-claude", LegacyID: "tether-claude"},
		{BagFile: "hollislabs-web-writer-claude", LegacyID: "hollislabs-web-writer-claude"},
	}
	report, err := RunParity(catalogRoot, specsRoot(t),
		WithCorpus(corpus),
		WithExpectedOldErrors(map[string]string{
			"hollislabs-web-writer-claude": "caller-registered dangling-agent defect",
		}),
	)
	if err != nil {
		t.Fatalf("RunParity: %v", err)
	}
	if len(report.Cases) != 2 {
		t.Fatalf("WithCorpus: got %d cases, want 2", len(report.Cases))
	}
	if !report.Passed() {
		t.Errorf("2-entry corpus with a caller-registered old-error should pass; cases=%+v", report.Cases)
	}
	// The caller's expected-old-error fired → it must NOT be reported stale.
	for _, s := range report.StaleExpected() {
		if s == "expected-old-error hollislabs-web-writer-claude" {
			t.Error("a caller expected-old-error that fired should not be stale")
		}
	}

	// StaleExpected catches a caller entry that never fires: an
	// expected-old-error registered for a cleanly-resolving launch.
	bogus, err := RunParity(catalogRoot, specsRoot(t),
		WithCorpus([]CorpusEntry{{BagFile: "tether-claude", LegacyID: "tether-claude"}}),
		WithExpectedOldErrors(map[string]string{"tether-claude": "bogus — tether-claude resolves fine"}),
	)
	if err != nil {
		t.Fatalf("RunParity (bogus): %v", err)
	}
	foundStale := false
	for _, s := range bogus.StaleExpected() {
		if s == "expected-old-error tether-claude" {
			foundStale = true
		}
	}
	if !foundStale {
		t.Error("a caller expected-old-error for a cleanly-resolving launch should be reported stale")
	}
}

// TestParity_CleanCasesAreIdentical pins the launches that must show ZERO
// field diffs — the launches with a faithful legacy counterpart. If a
// future change introduces a divergence on one of these, this test fails
// before TestParity_FixtureCorpus's expected-diff classifier could ever
// mask it.
func TestParity_CleanCasesAreIdentical(t *testing.T) {
	catalogRoot := filepath.Join(callerDir(t), "testdata", "catalog")
	report, err := RunParity(catalogRoot, specsRoot(t))
	if err != nil {
		t.Fatalf("RunParity: %v", err)
	}

	cleanLaunches := map[string]bool{
		"tether-claude":                 true,
		"tether-claude-worktree":        true,
		"nanite-claude-stream":          true,
		"nanite-claude-stream-worktree": true,
		"tether-launcher-claude-tui":    true,
	}
	for _, c := range report.Cases {
		if !cleanLaunches[c.Launch] {
			continue
		}
		if c.OldErr != nil || c.NewErr != nil {
			t.Errorf("%s: expected a clean case but a side failed (old=%v new=%v)",
				c.Launch, c.OldErr, c.NewErr)
			continue
		}
		if len(c.Diffs) != 0 {
			t.Errorf("%s: expected zero diffs, got %d: %+v", c.Launch, len(c.Diffs), c.Diffs)
		}
	}
}

// TestParity_LiveCatalog runs the harness against the real
// ~/.tether/catalog/ when it is present. On a CI runner with no Tether
// install the catalog is absent and the test is skipped — an absent
// catalog is not a parity failure. When the catalog IS present the same
// pass criteria as the fixture corpus apply.
func TestParity_LiveCatalog(t *testing.T) {
	root := DefaultCatalogRoot()
	if err := RequireCatalog(root); err != nil {
		if errors.Is(err, ErrCatalogAbsent) {
			t.Skipf("live catalog absent (%s) — skipping live parity check", root)
		}
		t.Fatalf("RequireCatalog: %v", err)
	}

	report, err := RunParity(root, specsRoot(t))
	if err != nil {
		t.Fatalf("RunParity over live catalog: %v", err)
	}
	t.Log("\n" + report.Summary())

	if un := report.UnexplainedDiffs(); len(un) > 0 {
		for _, u := range un {
			t.Errorf("live-catalog UNEXPLAINED diff: launch=%s field=%s old=%q new=%q",
				u.Launch, u.Diff.Field, u.Diff.Old, u.Diff.New)
		}
	}
	for _, c := range report.Cases {
		if c.NewErr != nil {
			t.Errorf("%s: new-side resolution failed: %v", c.Launch, c.NewErr)
		}
		if c.OldErr != nil {
			if _, ok := c.expectedOldErrRationale(); !ok {
				t.Errorf("%s: unexplained old-side failure: %v", c.Launch, c.OldErr)
			}
		}
	}
	if !report.Passed() {
		t.Errorf("live-catalog parity did not pass — see summary above")
	}
}

// TestParity_ReadOnlyContract is a guard for the read-only constraint:
// NormalizeRuntimeKinds mutates only the in-memory GlobalCatalog, never the
// on-disk files. It loads the fixture catalog twice and confirms a second
// independent load is byte-identical to the first (i.e. the first run's
// normalization did not leak to disk).
func TestParity_ReadOnlyContract(t *testing.T) {
	catalogRoot := filepath.Join(callerDir(t), "testdata", "catalog")

	first, err := catalog.LoadGlobal(catalogRoot)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	NormalizeRuntimeKinds(first)

	second, err := catalog.LoadGlobal(catalogRoot)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	// A fresh load must NOT see the runtime_kind the in-memory
	// normalization injected — proof the on-disk files were untouched.
	for _, p := range second.Providers {
		if p.ID == "claude-code" && p.RuntimeKind != "" {
			t.Errorf("read-only violation: claude-code.yaml gained runtime_kind=%q on disk", p.RuntimeKind)
		}
	}
}

// TestNormalizeRuntimeKinds_Mapping unit-tests the bootstrap.mode ->
// runtime_kind bridge in isolation.
func TestNormalizeRuntimeKinds_Mapping(t *testing.T) {
	g := &catalog.GlobalCatalog{
		Providers: []catalog.ProviderEntry{
			{ID: "a", Bootstrap: catalog.BootstrapSpec{Mode: "streaming-stdio"}},
			{ID: "b", Bootstrap: catalog.BootstrapSpec{Mode: "jsonrpc-stdio"}},
			{ID: "c", Bootstrap: catalog.BootstrapSpec{Mode: "stdin"}},
			{ID: "d", Bootstrap: catalog.BootstrapSpec{Mode: "agents_md"}},
			{ID: "e", RuntimeKind: "pty", Bootstrap: catalog.BootstrapSpec{Mode: "stdin"}},
		},
	}
	NormalizeRuntimeKinds(g)
	want := map[string]string{
		"a": "streaming-stdio", // bootstrap mode IS a runtime kind
		"b": "jsonrpc-stdio",   // bootstrap mode IS a runtime kind
		"c": "subprocess",      // stdin is a delivery strategy -> default
		"d": "subprocess",      // agents_md is a delivery strategy -> default
		"e": "pty",             // already declared -> untouched
	}
	for _, p := range g.Providers {
		if p.RuntimeKind != want[p.ID] {
			t.Errorf("provider %s: runtime_kind=%q want %q", p.ID, p.RuntimeKind, want[p.ID])
		}
	}
}

// TestCorpus_Sorted is a trivial sanity check that the corpus is
// enumerable and the helper is stable.
func TestCorpus_Sorted(t *testing.T) {
	names := sortedLaunchNames()
	if len(names) != len(Corpus) {
		t.Fatalf("sortedLaunchNames len=%d want %d", len(names), len(Corpus))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("sortedLaunchNames not sorted at %d: %q > %q", i, names[i-1], names[i])
		}
	}
}
