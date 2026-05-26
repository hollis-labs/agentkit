// Package parity is the S4.5 old-vs-new launch-plan parity harness.
//
// It is the cutover gate for the Tether platform reshape (EP-20260516-0001,
// sprint S4): before S5 flips live consumers onto the new launch model, the
// new model must provably reproduce the old model's launch-identity
// resolution. This package proves it.
//
// # The two sides
//
// Old side — the legacy static-file path. agentlaunch/catalog.LoadGlobal
// reads the live ~/.tether/catalog/ directory tree (read-only) and
// GlobalCatalog.Resolve turns one launches/<id>.yaml plus its referenced
// project / agent / provider entries into an agentlaunch.LaunchPlan.
//
// New side — the S4.4 Spec + LaunchBag model. agentlaunch.LoadLaunchSpec
// loads the one canonical launch-assembly.yaml LaunchSpec;
// agentlaunch.LoadLaunchBag loads one re-expressed launch bag;
// ValidateMinimumConfig + LaunchSpec.Render exercise the S4.1 engine.
//
// # The comparable surface
//
// The two sides produce structurally different artifacts (a LaunchPlan
// struct versus a rendered boot body). They are NOT byte-comparable. What
// IS comparable — and what actually matters for the cutover — is the
// launch IDENTITY both sides resolve: the project, the work directory, the
// runner/provider, and the workspace-isolation mode. NormalizedPlan is that
// projection; both sides project into it and the harness diffs the
// projections field by field.
//
// # Honest diffs
//
// Per the S4.5 acceptance contract, a diff is acceptable only if it is
// either zero OR a documented, intentional difference. The expectedDiffs
// registry in expected_diffs.go enumerates every intentional divergence
// with its rationale; RunParity classifies each non-zero field diff against
// that registry. An unexplained diff fails the harness. The harness never
// papers over a divergence to go green.
//
// # Read-only contract
//
// The harness opens ~/.tether/catalog/ for reading only — os.Stat,
// os.ReadDir, os.ReadFile via the catalog loader. It never writes, deletes,
// or mutates the live catalog. The runtime-kind normalization
// (NormalizeRuntimeKinds) mutates the IN-MEMORY GlobalCatalog only.
package parity
