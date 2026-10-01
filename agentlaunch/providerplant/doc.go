// Package providerplant is the go-providers integration layer for
// agentlaunch: it owns translating a PreparedLaunch into the
// provider-specific files planted inside the materialized bootdir and the
// launch bindings that run them.
//
// # Why this package exists
//
// The core agentlaunch package owns launch schema, compile/provenance, and
// workspace/bootdir allocation, but it stops at an empty (or
// hook-populated) bootdir. Before this package, every consumer (Tether,
// Torque, Nanite) had to reimplement the last and most provider-specific
// step: rendering the provider's boot files (CLAUDE.md / AGENTS.md /
// agents/<name>.md / config.toml / .mcp.json …) into the bootdir and
// building the argv that runs them.
//
// providerplant closes that gap. It resolves the right go-providers adapter
// for the launch's runtime and mode (DefaultResolver, through
// provider.NewAdapter; WithAdapter or WithResolver override it), renders the
// adapter's provider projection (or its legacy BootDirSpec), adds the
// injection files, and materializes the result into
// PreparedLaunch.PlantedBootDir through go-materialize.
//
// # Entry points
//
//   - PrepareExecution projects and materializes an already-Prepared launch
//     and returns the lossless agentlaunch.PreparedExecution handoff
//     (bindings, TurnTemplate, roots, access requirements, materialization
//     handle) for agentsessions.StartOptions.PreparedExecution.
//   - Plant runs PrepareExecution and copies its bindings back onto the
//     PreparedLaunch (the compatibility path).
//   - PrepareAndPlant is the one-call API: it runs launcher.Prepare and
//     then Plant, returning a fully materialized PreparedLaunch.
//   - PlantContextFor builds the provider.PlantContext (including the
//     launch's MCP servers) the renderers consume.
//
// # Permission posture
//
// A non-empty LaunchPlan.Provider.Permission (a go-permission Mode) is
// mapped through the go-providers registry's PostureFor onto the provider's
// own flags, placed first among the launch's extra arguments, and its
// environment. An empty posture adds nothing.
//
// # Planting order
//
// Files are assembled into one artifact tree in a fixed order, so later
// entries replace earlier ones at the same path:
//
//  1. Provider files (the projection's, or the legacy BootDirSpec's
//     CLAUDE.md, boot.md, .mcp.json, …).
//  2. InjectionSpec.NativeFiles (provider-native skills, context docs,
//     raw user files). A native file MAY override a provider file when
//     their resolved paths collide (e.g. a raw AGENTS.md over codex's
//     rendered AGENTS.md) — this is intentional: native files are a
//     caller override.
//  3. InjectionSpec.BootDirOverlay (the flat path→content escape hatch).
//     Applied last, so an overlay entry wins over both provider files
//     and native files at the same path.
//
// Plant then rewires the PreparedLaunch in place: the
// provider's environment is merged into PreparedLaunch.Env, PreparedLaunch.Workdir
// is set to the provider's working directory, and PreparedLaunch.Launch is set
// to the provider's launch convention (a TurnTemplate) with the launch's own
// flags at its extra-argument slot. PreparedLaunch.Argv is that template's first
// turn. Runtimes resolve every turn from Launch, so each turn carries its own
// prompt and resume id. A legacy BootDirSpec provider has no template; its
// ProjectDirArg and the launch's flags are appended to Argv.
//
// # Relationship to agentsessions
//
// agentsessions can also plant bootdirs (its AutoPlantBootDir path).
// When a launch goes through providerplant the bootdir is ALREADY
// planted, so the sessionshim package emits StartOptions with
// AutoPlantBootDir disabled — the two planters never run twice over the
// same dir.
package providerplant
