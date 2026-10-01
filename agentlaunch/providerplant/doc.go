// Package providerplant is the go-providers integration layer for
// go-agent-launch: it owns translating a PreparedLaunch into the
// provider-specific files planted inside the materialized bootdir.
//
// # Why this package exists
//
// The core agentlaunch package is deliberately import-free of
// go-providers — it owns launch schema, the provider×runtime matrix,
// compile/provenance, and workspace/bootdir allocation, but it stops at
// an empty (or hook-populated) bootdir. Before this package, every
// consumer (Tether, Torque, Nanite) had to reimplement the last and
// most provider-specific step: invoking go-providers' BootDirSpec
// renderers and writing CLAUDE.md / AGENTS.md / agents/<name>.md /
// config.toml / .mcp.json into the bootdir.
//
// providerplant closes that gap. It imports go-providers, resolves the
// right adapter for the launch's provider×runtime pair, renders the
// adapter's BootDirSpec, and writes the result — plus native extra
// files and the injection overlay — into PreparedLaunch.PlantedBootDir.
//
// # Entry points
//
//   - Plant materializes provider files into an already-Prepared launch.
//   - PrepareAndPlant is the one-call API: it runs launcher.Prepare and
//     then Plant, returning a fully materialized PreparedLaunch.
//
// # Planting order
//
// Plant writes files into the bootdir in a fixed, documented order so
// overwrites are deterministic:
//
//  1. Provider BootDirSpec files (CLAUDE.md, boot.md, .mcp.json, …).
//  2. InjectionSpec.NativeFiles (provider-native skills, context docs,
//     raw user files). A native file MAY override a provider file when
//     their resolved paths collide (e.g. a raw AGENTS.md over codex's
//     rendered AGENTS.md) — this is intentional: native files are a
//     caller override.
//  3. InjectionSpec.BootDirOverlay (the flat path→content escape hatch).
//     Applied last, so an overlay entry wins over both provider files
//     and native files at the same path.
//
// After file planting, Plant rewires the PreparedLaunch in place: the
// provider's environment is merged into PreparedLaunch.Env, PreparedLaunch.Workdir
// is set to the provider's working directory, and PreparedLaunch.Launch is set
// to the provider's launch convention (a TurnTemplate) with the launch's own
// flags at its extra-argument slot. PreparedLaunch.Argv is that template's first
// turn. Runtimes resolve every turn from Launch, so each turn carries its own
// prompt and resume id. A legacy BootDirSpec provider has no template; its
// ProjectDirArg and the launch's flags are appended to Argv.
//
// # Relationship to go-agent-sessions
//
// go-agent-sessions can also plant bootdirs (its AutoPlantBootDir path).
// When a launch goes through providerplant the bootdir is ALREADY
// planted, so the sessionshim package emits StartOptions with
// AutoPlantBootDir disabled — the two planters never run twice over the
// same dir.
package providerplant
