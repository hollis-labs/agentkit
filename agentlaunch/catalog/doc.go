// Package catalog ports the Tether catalog YAML schema into the
// agentlaunch contract types.
//
// Three on-disk shapes are recognised:
//
//   - global.yaml — top-level catalog descriptor. May either point at
//     sibling subdirectories (Tether's native layout) via the
//     CatalogRoots / catalog.roots block, or carry inline lists of
//     projects, agents, providers, and launches.
//   - launches/<id>.yaml — one launch profile per file (LaunchProfile).
//   - boot-profiles/<id>.yaml — one boot profile per file (BootProfile).
//
// The types in this package mirror the LITERAL YAML field names used by
// Tether so existing fixtures round-trip without modification. The
// translator methods (GlobalCatalog.Resolve, LaunchProfile.ToLaunchPlan)
// convert the on-disk shape into a populated agentlaunch.LaunchPlan and
// run agentlaunch.LaunchPlan.Validate on the result before returning.
//
// A provider's runtime_kind is Tether's own token; the translator maps it
// onto an agent-contracts-leaf runtimes.Mode ("subprocess" is
// subprocess-per-turn, "serve-http" is http-sse) and nowhere else in
// agentkit translates those spellings.
//
// This package imports only the parent agentlaunch package,
// agent-contracts-leaf's runtimes vocabulary, the Go standard library, and
// gopkg.in/yaml.v3. It deliberately does NOT depend on Tether, Nanite,
// Torque, go-providers, or agentsessions.
package catalog
