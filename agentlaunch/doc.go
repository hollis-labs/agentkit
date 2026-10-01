// Package agentlaunch provides a portfolio-shared launch substrate for
// agent runtimes. It defines the LaunchPlan → CompiledLaunch →
// PreparedLaunch pipeline that turns a declarative catalog entry plus a
// runtime selection into a materialized boot directory and a
// ready-to-Start config the caller hands to agentsessions.
//
// The package sits above github.com/hollis-labs/go-providers and below
// agentkit/agentsessions (which imports it for PreparedExecution and
// TurnTemplate) and the app-specific orchestrators (Tether, Torque, Nanite)
// that each previously grew their own near-identical launch pipelines. It
// owns:
//
//   - the LaunchPlan / CompiledLaunch / PreparedLaunch types; the
//     launcher subpackage's Compile / Prepare move between them,
//   - the PreparedExecution handoff (exact argv/env/cwd bindings, the
//     per-turn TurnTemplate, roots, access requirements, materialization
//     handle) that providerplant.PrepareExecution and ResolvePreparation
//     produce and agentsessions.StartOptions.PreparedExecution consumes,
//   - the provider × runtime matrix (subpackage matrix), read from the
//     go-providers runtime registry over the agent-contracts-leaf runtimes
//     vocabulary,
//   - ProviderSpec.Permission, the launch's permission posture as a
//     go-permission Mode, which the registry's PostureFor maps onto each
//     provider's own flags or environment,
//   - MCPSpec: the loopback URL and the per-session MCP servers (stdio or
//     HTTP) that the go-providers renderers plant into each runtime's
//     native MCP config,
//   - the Tether-compatible catalog schema so a single catalog entry can
//     drive every consumer in the portfolio,
//   - the frozen shared contract types for RuntimeBinding, BootSpec,
//     VarSpec, and the bootdir materializer API.
//
// The package is intentionally app-neutral. It imports go-providers,
// go-permission, go-materialize and agentkit/agentcontext but no
// app-specific repository (Tether, Torque, Nanite). Consumers configure
// the pipeline through caller-supplied types and sinks rather than direct
// dependencies on any orchestrator.
//
// API note: RuntimeBinding and BootSpec are intentionally distinct.
// RuntimeBinding is the synchronously-readable provider/model/runtime
// selection. BootSpec is the parameterized blueprint that produces boot
// files, injections, derived vars, and the associated runtime contract.
//
// LaunchPlan remains the stable LaunchSpec-equivalent integration view
// for existing consumers; runtime-critical consumer overlays still win
// at compile/prepare time.
package agentlaunch
