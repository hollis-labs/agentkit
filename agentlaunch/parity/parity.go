package parity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/catalog"
)

// NormalizedPlan is the launch-identity projection both resolution paths
// agree on. The old side projects it from a resolved agentlaunch.LaunchPlan;
// the new side projects it from a resolved LaunchBag. It deliberately holds
// ONLY the fields that are launch identity in BOTH models — project, work
// directory, runner/provider, and workspace-isolation mode. Fields that
// exist on only one side (the old LaunchPlan's matrix-derived RuntimeKind,
// the new template's rendered prose) are not identity and are not compared:
// comparing them would manufacture noise, not signal.
type NormalizedPlan struct {
	// Project is the project identifier the launch is scoped to.
	Project string

	// WorkDir is the agent working directory — the repo/tree the session
	// operates on. Compared verbatim (un-expanded ~) so the harness is
	// host-independent.
	WorkDir string

	// Runner is the runner/provider id (claude-code, claude-stream,
	// codex-cli, ...). Old side: LaunchPlan.Provider.ID. New side: the
	// `runner` input.
	Runner string

	// Isolation is the workspace-isolation mode token. Old side: the
	// `tether.workspace_mode_raw` annotation (the legacy workspace.mode
	// before it was mapped onto an agentlaunch.WorkspaceMode). New side:
	// the `isolation` input. Both use the Tether tokens hybrid/worktree.
	Isolation string
}

// fields returns the NormalizedPlan as ordered (name, value) pairs so the
// differ can walk them deterministically.
func (p NormalizedPlan) fields() []struct{ name, value string } {
	return []struct{ name, value string }{
		{"project", p.Project},
		{"work_dir", p.WorkDir},
		{"runner", p.Runner},
		{"isolation", p.Isolation},
	}
}

// FieldDiff is one field where the old-side and new-side NormalizedPlan
// disagree.
type FieldDiff struct {
	// Field is the NormalizedPlan field name (project / work_dir / runner /
	// isolation).
	Field string

	// Old is the old-side (legacy static-file) value.
	Old string

	// New is the new-side (Spec + LaunchBag) value.
	New string

	// Expected is non-empty when this diff matches a registered intentional
	// divergence; it carries the documented rationale. An empty Expected on
	// a non-zero diff is an UNEXPLAINED diff — a harness failure.
	Expected string
}

// Explained reports whether the diff is a documented intentional difference.
func (d FieldDiff) Explained() bool { return d.Expected != "" }

// CaseResult is the parity outcome for one launch in the corpus.
type CaseResult struct {
	// Launch is the corpus launch name (the testdata bag name, which equals
	// the legacy launches/<id>.yaml stem).
	Launch string

	// OldErr is set when the old-side static path could not resolve the
	// launch at all. A resolution failure is itself a parity finding.
	OldErr error

	// NewErr is set when the new-side Spec + bag path could not resolve.
	NewErr error

	// Old and New are the projected plans (zero-value when the matching
	// side errored).
	Old NormalizedPlan
	New NormalizedPlan

	// Diffs lists every field where the two projections disagree.
	Diffs []FieldDiff

	// OldErrExpected is the rationale when OldErr is a documented,
	// intentional legacy-catalog defect — resolved against the run's
	// effective expected-old-error registry at RunParity time. Empty when
	// OldErr is nil or the failure is unexplained.
	OldErrExpected string
}

// ExpectedOldErr is non-empty when the old-side resolution failure is a
// documented, intentional legacy-catalog defect (see expectedOldErrors).
// An expected old-side error is NOT a parity failure: it records that the
// new model deliberately diverges from a broken legacy launch.
func (c CaseResult) expectedOldErrRationale() (string, bool) {
	if c.OldErr == nil || c.OldErrExpected == "" {
		return "", false
	}
	return c.OldErrExpected, true
}

// Parity reports whether the case is a clean pass. A case passes when
// EITHER both sides resolved and every field diff is a documented
// intentional difference, OR the old side failed with a documented,
// intentional legacy-catalog defect (an expected old-side error).
func (c CaseResult) Parity() bool {
	if c.NewErr != nil {
		return false
	}
	if c.OldErr != nil {
		_, ok := c.expectedOldErrRationale()
		return ok
	}
	for _, d := range c.Diffs {
		if !d.Explained() {
			return false
		}
	}
	return true
}

// Report is the structured result of one RunParity over the whole corpus.
type Report struct {
	// CatalogRoot is the live catalog directory the old side read.
	CatalogRoot string

	// SpecsRoot is the testdata specs directory the new side read.
	SpecsRoot string

	// Cases is one CaseResult per corpus launch, in corpus order.
	Cases []CaseResult

	// effExpectedDiffs / effExpectedOldErrors are the effective expected
	// registries this run classified against — the built-in sets merged
	// with any WithExpectedDiffs / WithExpectedOldErrors entries. Retained
	// so StaleExpected can report registry entries the run never observed.
	effExpectedDiffs     []ExpectedDiff
	effExpectedOldErrors map[string]string
}

// StaleExpected reports expected-diff / expected-old-error registry entries
// that the run's corpus never actually produced — a stale entry would
// silently mask a future real divergence on the same launch+field. The
// returned slice is sorted; empty means every registered expectation fired.
//
// This generalizes the harness's own staleness guard: a consumer running
// RunParity over a wider corpus with WithExpectedDiffs / WithExpectedOldErrors
// can assert len(report.StaleExpected()) == 0 against its own registry.
func (r Report) StaleExpected() []string {
	observedDiff := map[string]bool{}
	observedOldErr := map[string]bool{}
	for _, c := range r.Cases {
		if c.OldErr != nil && c.OldErrExpected != "" {
			observedOldErr[c.Launch] = true
		}
		for _, d := range c.Diffs {
			if d.Explained() {
				observedDiff[c.Launch+"/"+d.Field] = true
			}
		}
	}
	var stale []string
	for _, e := range r.effExpectedDiffs {
		if e.Launch == "" {
			continue // wildcard entry — cannot key-check
		}
		if !observedDiff[e.Launch+"/"+e.Field] {
			stale = append(stale, "expected-diff "+e.Launch+"/"+e.Field)
		}
	}
	for launch := range r.effExpectedOldErrors {
		if !observedOldErr[launch] {
			stale = append(stale, "expected-old-error "+launch)
		}
	}
	sort.Strings(stale)
	return stale
}

// Passed reports whether every case in the corpus is a clean parity pass.
func (r Report) Passed() bool {
	for _, c := range r.Cases {
		if !c.Parity() {
			return false
		}
	}
	return true
}

// UnexplainedDiffs returns every non-zero field diff across the corpus that
// is NOT a registered intentional difference. A non-empty result is a
// genuine parity failure.
func (r Report) UnexplainedDiffs() []struct {
	Launch string
	Diff   FieldDiff
} {
	var out []struct {
		Launch string
		Diff   FieldDiff
	}
	for _, c := range r.Cases {
		for _, d := range c.Diffs {
			if !d.Explained() {
				out = append(out, struct {
					Launch string
					Diff   FieldDiff
				}{c.Launch, d})
			}
		}
	}
	return out
}

// Summary renders a human-readable multi-line report. CI prints this on
// every run so a divergence is visible in the workflow log.
func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "launch-plan parity: %d cases\n", len(r.Cases))
	fmt.Fprintf(&b, "  old side (static catalog): %s\n", r.CatalogRoot)
	fmt.Fprintf(&b, "  new side (Spec + LaunchBag): %s\n", r.SpecsRoot)
	pass, explained := 0, 0
	for _, c := range r.Cases {
		switch {
		case c.OldErr != nil:
			if rat, ok := c.expectedOldErrRationale(); ok {
				explained++
				fmt.Fprintf(&b, "  PASS %-32s old-side resolve error (expected: %s)\n", c.Launch, rat)
				fmt.Fprintf(&b, "       ~ old-err: %v\n", c.OldErr)
			} else {
				fmt.Fprintf(&b, "  FAIL %-32s old-side resolve error: %v\n", c.Launch, c.OldErr)
			}
		case c.NewErr != nil:
			fmt.Fprintf(&b, "  FAIL %-32s new-side resolve error: %v\n", c.Launch, c.NewErr)
		case len(c.Diffs) == 0:
			pass++
			fmt.Fprintf(&b, "  PASS %-32s identical\n", c.Launch)
		default:
			clean := c.Parity()
			tag := "FAIL"
			if clean {
				tag = "PASS"
				explained++
			}
			fmt.Fprintf(&b, "  %s %-32s %d diff(s)\n", tag, c.Launch, len(c.Diffs))
			for _, d := range c.Diffs {
				if d.Explained() {
					fmt.Fprintf(&b, "       ~ %-10s old=%q new=%q (expected: %s)\n",
						d.Field, d.Old, d.New, d.Expected)
				} else {
					fmt.Fprintf(&b, "       ! %-10s old=%q new=%q UNEXPLAINED\n",
						d.Field, d.Old, d.New)
				}
			}
		}
	}
	fmt.Fprintf(&b, "  total: %d identical, %d explained-diff, %d case(s), passed=%v\n",
		pass, explained, len(r.Cases), r.Passed())
	return b.String()
}

// CorpusEntry maps one new-side launch bag to its legacy launch id. The
// corpus is the set of S4.4 testdata bags; each one names the legacy
// launches/<LegacyID>.yaml it re-expresses.
type CorpusEntry struct {
	// BagFile is the testdata bag filename stem (under specs/launches/).
	BagFile string

	// LegacyID is the legacy launches/<id>.yaml stem the bag re-expresses.
	// Empty means the bag has no legacy counterpart and is skipped.
	LegacyID string
}

// Corpus is the S4.4 testdata bag set mapped to legacy launch ids. Each
// BagFile is one file under testdata/specs/launches/; LegacyID is the
// legacy launches/<id>.yaml stem it reproduces. The mapping is by the
// reproduction note in the head of each bag file (see CHANGELOG S4.4).
//
// tether-minimum.yaml is intentionally absent: it is the synthetic
// smallest-legal-bag fixture and has no legacy counterpart, so there is
// nothing to diff it against.
var Corpus = []CorpusEntry{
	{BagFile: "tether-claude", LegacyID: "tether-claude"},
	{BagFile: "tether-claude-worktree", LegacyID: "tether-claude-worktree"},
	{BagFile: "nanite-claude-stream", LegacyID: "nanite-claude-stream"},
	{BagFile: "nanite-claude-stream-worktree", LegacyID: "nanite-claude-stream-worktree"},
	{BagFile: "agent-mux-claude", LegacyID: "agent-mux-claude"},
	{BagFile: "agent-mux-codex-launch", LegacyID: "agent-mux-codex-launch"},
	{BagFile: "agent-mux-codex-app-server", LegacyID: "agent-mux-codex-app-server"},
	{BagFile: "agent-mux-opencode", LegacyID: "agent-mux-opencode"},
	{BagFile: "agent-mux-claude-stream-worktree", LegacyID: "agent-mux-claude-stream-worktree"},
	{BagFile: "hollislabs-web-writer-claude", LegacyID: "hollislabs-web-writer-claude"},
	{BagFile: "tether-launcher-claude-tui", LegacyID: "tether-launcher-claude-tui"},
}

// runtimeKindFromBootstrap maps a Tether provider bootstrap.mode token to a
// runtime-kind token, for those bootstrap modes that ARE runtime kinds.
//
// Why this exists: the catalog port (agentlaunch/catalog) resolves a
// launch's runtime from the provider's `runtime_kind` field. The LIVE
// ~/.tether/catalog/ providers, however, mostly omit `runtime_kind` and
// only carry a `bootstrap.mode`. Without this bridge GlobalCatalog.Resolve
// fails on 63 of the 64 live launches with ErrUnsupportedRuntime — the old
// side cannot be driven in-process at all. See NormalizeRuntimeKinds and
// the live-catalog finding in the S4.5 PR body.
//
// Only the three bootstrap modes that are genuinely runtime shapes are
// bridged here. stdin / agents_md / prepend are prompt-delivery strategies,
// not runtimes; providers carrying those get the subprocess default.
var runtimeKindFromBootstrap = map[string]string{
	"streaming-stdio": "streaming-stdio",
	"jsonrpc-stdio":   "jsonrpc-stdio",
	"pty":             "pty",
}

// NormalizeRuntimeKinds fills in a runtime_kind for every provider in g
// that omits one, deriving it from the provider's bootstrap.mode (when that
// mode is itself a runtime kind) and otherwise defaulting to subprocess.
//
// It mutates the IN-MEMORY GlobalCatalog only — the on-disk catalog files
// are never touched. This is the documented, intentional pre-processing
// step that lets the old-side static path resolve the live catalog at all;
// it is recorded as the expected-diff rationale "live-catalog-runtime-kind"
// where it affects a comparison (it does not, because RuntimeKind is not a
// NormalizedPlan field — but the normalization is still disclosed).
//
// A provider that already declares runtime_kind is left untouched.
func NormalizeRuntimeKinds(g *catalog.GlobalCatalog) {
	if g == nil {
		return
	}
	for i := range g.Providers {
		p := &g.Providers[i]
		if p.RuntimeKind != "" {
			continue
		}
		if rk, ok := runtimeKindFromBootstrap[p.Bootstrap.Mode]; ok {
			p.RuntimeKind = rk
			continue
		}
		p.RuntimeKind = "subprocess"
	}
}

// resolveOld drives the legacy static-file resolution path for one launch
// id and projects the result into a NormalizedPlan.
//
// The path: catalog.LoadGlobal already read the catalog tree;
// GlobalCatalog.Resolve looks up the launches/<id>.yaml, resolves its
// project/agent/provider references, and returns a validated
// agentlaunch.LaunchPlan. The projection reads launch identity off that
// plan.
func resolveOld(g *catalog.GlobalCatalog, legacyID string) (NormalizedPlan, error) {
	plan, err := g.Resolve(legacyID)
	if err != nil {
		return NormalizedPlan{}, err
	}
	// Isolation: the legacy workspace.mode token, preserved verbatim by the
	// translator under this annotation before it was mapped onto an
	// agentlaunch.WorkspaceMode. That raw token (hybrid / worktree) is the
	// like-for-like comparand for the new side's `isolation` input.
	isolation := plan.Metadata.Annotations["tether.workspace_mode_raw"]
	// WorkDir: the legacy model has no per-launch work_dir; the working
	// tree IS the project's repo_root. That is what the new-side work_dir
	// input was populated from in S4.4, so it is the correct comparand.
	workDir := plan.Project.Root
	return NormalizedPlan{
		Project:   plan.Project.ID,
		WorkDir:   workDir,
		Runner:    plan.Provider.ID,
		Isolation: isolation,
	}, nil
}

// resolveNew drives the new Spec + LaunchBag resolution path for one bag
// and projects the result into a NormalizedPlan.
//
// The pipeline:
//
//  1. agentlaunch.LoadLaunchBag loads + validates the bag (S4.4).
//  2. agentlaunch.ValidateMinimumConfig checks it against the spec (S4.4).
//  3. LaunchSpec.Render runs the S4.1 engine over the bag inputs and projects
//     launch identity off RenderResult.ResolvedInputs.
//
// Identity vs. vars — why var resolution is best-effort here. Parity compares
// launch IDENTITY only — project / work_dir / runner / isolation — and that
// is derived entirely from the bag's INPUTS. Var resolution (S4.2) and
// template rendering feed the boot BODY, which parity does not compare. So
// neither may fail the case: a spec whose vars use gated `call`/`cmd` sources
// fail-closes under this offline harness (it configures no TrustAuthorizer by
// design), but the launch's identity is unaffected. resolveNew therefore
// resolves vars best-effort and reads identity off ResolvedInputs even when
// Render reports missing vars — Render populates ResolvedInputs regardless of
// the var/template outcome, and the interactive front-end reports missing
// vars rather than hard-erroring. (An earlier version ran var resolution as a
// mandatory step and failed the whole case on a gated source — conflating var
// resolution with identity resolution.)
func resolveNew(ctx context.Context, spec agentlaunch.LaunchSpec, bagPath string) (NormalizedPlan, error) {
	bag, err := agentlaunch.LoadLaunchBag(bagPath)
	if err != nil {
		return NormalizedPlan{}, err
	}
	if err := agentlaunch.ValidateMinimumConfig(spec, bag); err != nil {
		return NormalizedPlan{}, fmt.Errorf("minimum-config: %w", err)
	}

	req := bag.RenderRequest(agentlaunch.PolicyCollect)
	if req.Vars == nil {
		req.Vars = map[string]any{}
	}
	// Best-effort var resolution: when the spec's vars resolve (literal/file
	// sources) feed them in; when they fail-close (gated sources, no
	// TrustAuthorizer) skip them — identity does not depend on vars.
	if vars, verr := resolveSpecVars(ctx, spec); verr == nil {
		for name, val := range vars {
			if _, ok := req.Vars[name]; !ok {
				req.Vars[name] = val
			}
		}
	}

	// Render error is deliberately tolerated: ResolvedInputs (the identity
	// source) is populated regardless, and the interactive front-end does
	// not hard-error on missing vars. A genuine input-shape problem was
	// already caught by ValidateMinimumConfig above; anything else surfaces
	// as a parity field diff, not a false green.
	res, _ := spec.Render(req)
	return NormalizedPlan{
		Project:   inputString(res.ResolvedInputs, "project"),
		WorkDir:   inputString(res.ResolvedInputs, agentlaunch.LaunchInputWorkDir),
		Runner:    inputString(res.ResolvedInputs, agentlaunch.LaunchInputRunner),
		Isolation: inputString(res.ResolvedInputs, agentlaunch.LaunchInputIsolation),
	}, nil
}

// resolveSpecVars resolves every derived var declared on spec via the S4.2
// VarResolver and returns a name->value map suitable for RenderRequest.Vars.
//
// All vars are resolved for the prompt-text (session-start) sink — that is
// the sink the testdata spec declares (phase: session-start). No
// CallResolver is supplied: the spec's literal sources need none, and the
// harness must stay offline (no network, read-only). A var that fails to
// resolve under its on_error policy still yields an entry (possibly nil)
// so the renderer does not report it as missing.
func resolveSpecVars(ctx context.Context, spec agentlaunch.LaunchSpec) (map[string]any, error) {
	vr := agentlaunch.NewVarResolver(agentlaunch.VarResolverOptions{})
	bootSpec := spec.BootSpec
	sinks := make(map[string]agentlaunch.VarSinkKind, len(bootSpec.Vars))
	for i := range bootSpec.Vars {
		sinks[bootSpec.Vars[i].Name] = agentlaunch.VarSinkPromptText
	}
	resolved, err := vr.ResolveAll(ctx, &bootSpec, sinks, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(resolved))
	for name, rv := range resolved {
		out[name] = rv.Value
	}
	return out, nil
}

// inputString reads key from the resolved-input map as a string. A missing
// or nil value renders as the empty string; a non-string value is
// stringified deterministically.
func inputString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// diff compares the old and new projections and classifies every field
// disagreement against the expected-diff registry for launch.
func diff(launch string, old, new NormalizedPlan, registry []ExpectedDiff) []FieldDiff {
	var diffs []FieldDiff
	oldF := old.fields()
	newF := new.fields()
	for i := range oldF {
		if oldF[i].value == newF[i].value {
			continue
		}
		d := FieldDiff{
			Field: oldF[i].name,
			Old:   oldF[i].value,
			New:   newF[i].value,
		}
		if exp, ok := lookupExpectedDiff(registry, launch, d.Field, d.Old, d.New); ok {
			d.Expected = exp
		}
		diffs = append(diffs, d)
	}
	return diffs
}

// Option configures RunParity. With no options RunParity runs the built-in
// Corpus and built-in expected registries; the With* options let a consumer
// run parity over a wider corpus with its own expected divergences.
type Option func(*runConfig)

// runConfig is the resolved RunParity configuration (built from Options).
type runConfig struct {
	corpus              []CorpusEntry
	extraExpectedDiffs  []ExpectedDiff
	extraExpectedOldErr map[string]string
}

// WithCorpus replaces the corpus RunParity iterates (default: the built-in
// 11-entry Corpus). Use it to run parity over a consumer's full launch set.
func WithCorpus(entries []CorpusEntry) Option {
	return func(c *runConfig) { c.corpus = entries }
}

// WithExpectedDiffs registers additional intentional old-vs-new divergences,
// merged with the built-in registry. Required when a wider corpus surfaces
// documented legacy-catalog defects the built-in registry does not cover.
func WithExpectedDiffs(diffs ...ExpectedDiff) Option {
	return func(c *runConfig) { c.extraExpectedDiffs = append(c.extraExpectedDiffs, diffs...) }
}

// WithExpectedOldErrors registers additional launches whose old-side
// resolution is expected to fail (a documented legacy-catalog defect),
// merged with the built-in registry. Keyed by launch (bag) name.
func WithExpectedOldErrors(m map[string]string) Option {
	return func(c *runConfig) {
		if c.extraExpectedOldErr == nil {
			c.extraExpectedOldErr = map[string]string{}
		}
		for k, v := range m {
			c.extraExpectedOldErr[k] = v
		}
	}
}

// RunParity executes the parity harness over a corpus of launches.
//
//   - catalogRoot is the live ~/.tether/catalog/ directory (read-only).
//   - specsRoot is the S4.4 testdata specs directory (the one holding
//     launch-assembly.yaml and the launches/ bag subdir).
//   - opts default to the built-in Corpus + expected registries; WithCorpus
//     / WithExpectedDiffs / WithExpectedOldErrors run a wider corpus.
//
// RunParity returns an error only for a setup failure (a catalog or spec
// root that cannot be loaded at all). A per-launch resolution failure or a
// field divergence is captured in the Report, not returned as an error —
// the caller inspects Report.Passed / Report.StaleExpected.
func RunParity(catalogRoot, specsRoot string, opts ...Option) (Report, error) {
	cfg := runConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	corpus := Corpus
	if cfg.corpus != nil {
		corpus = cfg.corpus
	}
	expDiffs := append(append([]ExpectedDiff(nil), expectedDiffs...), cfg.extraExpectedDiffs...)
	expOldErr := map[string]string{}
	for k, v := range expectedOldErrors {
		expOldErr[k] = v
	}
	for k, v := range cfg.extraExpectedOldErr {
		expOldErr[k] = v
	}

	report := Report{
		CatalogRoot:          catalogRoot,
		SpecsRoot:            specsRoot,
		effExpectedDiffs:     expDiffs,
		effExpectedOldErrors: expOldErr,
	}

	// --- Old side setup: load the live catalog (read-only). ---
	g, err := catalog.LoadGlobal(catalogRoot)
	if err != nil {
		return report, fmt.Errorf("parity: load old-side catalog %q: %w", catalogRoot, err)
	}
	// Documented in-memory normalization — never written back to disk.
	NormalizeRuntimeKinds(g)

	// --- New side setup: load the one canonical LaunchSpec. ---
	specPath := filepath.Join(specsRoot, "launch-assembly.yaml")
	spec, err := agentlaunch.LoadLaunchSpec(specPath)
	if err != nil {
		return report, fmt.Errorf("parity: load new-side spec %q: %w", specPath, err)
	}

	for _, entry := range corpus {
		if entry.LegacyID == "" {
			continue
		}
		cr := CaseResult{Launch: entry.BagFile}

		oldPlan, oldErr := resolveOld(g, entry.LegacyID)
		if oldErr != nil {
			cr.OldErr = oldErr
			if rat, ok := lookupExpectedOldErr(expOldErr, entry.BagFile); ok {
				cr.OldErrExpected = rat
			}
		} else {
			cr.Old = oldPlan
		}

		bagPath := filepath.Join(specsRoot, "launches", entry.BagFile+".yaml")
		newPlan, newErr := resolveNew(context.Background(), spec, bagPath)
		if newErr != nil {
			cr.NewErr = newErr
		} else {
			cr.New = newPlan
		}

		if oldErr == nil && newErr == nil {
			cr.Diffs = diff(entry.BagFile, oldPlan, newPlan, expDiffs)
		}
		report.Cases = append(report.Cases, cr)
	}
	return report, nil
}

// DefaultCatalogRoot returns the live Tether catalog root,
// $HOME/.tether/catalog. It is the old-side input for RunParity.
func DefaultCatalogRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".tether", "catalog")
	}
	return filepath.Join(home, ".tether", "catalog")
}

// ErrCatalogAbsent is returned by RequireCatalog when the live catalog root
// does not exist — for example on a CI runner with no Tether install. It is
// errors.Is-comparable so callers can distinguish "no catalog to test
// against" from a genuine parity failure.
var ErrCatalogAbsent = errors.New("parity: live catalog root absent")

// RequireCatalog reports whether root names an existing directory. CI uses
// it to decide between running the live-catalog parity check and falling
// back to the self-contained fixture corpus (see parity_test.go): a runner
// with no ~/.tether/catalog/ is not a parity failure.
func RequireCatalog(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrCatalogAbsent, root)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrCatalogAbsent, root)
	}
	return nil
}

// sortedLaunchNames returns the corpus launch names in sorted order — a
// small determinism helper for tests that assert over the corpus.
func sortedLaunchNames() []string {
	names := make([]string, 0, len(Corpus))
	for _, e := range Corpus {
		names = append(names, e.BagFile)
	}
	sort.Strings(names)
	return names
}
