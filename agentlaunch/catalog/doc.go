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
// This package imports only the parent agentlaunch package, the Go
// standard library, and gopkg.in/yaml.v3. It deliberately does NOT
// depend on Tether, Nanite, Torque, go-providers, or go-agent-sessions.
package catalog
