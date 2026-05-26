// Package agentlaunch provides a portfolio-shared launch substrate for
// agent runtimes. It defines the LaunchPlan → CompiledLaunch →
// PreparedLaunch pipeline that turns a declarative catalog entry plus a
// runtime selection into a materialized boot directory and a
// ready-to-Start config the caller hands to go-agent-sessions.
//
// The package sits above github.com/hollis-labs/go-providers and beside
// agentkit/agentsessions, below the app-specific orchestrators (Tether,
// Torque, Nanite) that each previously grew their own near-identical
// launch pipelines. It owns:
//
//   - the LaunchPlan / CompiledLaunch / PreparedLaunch types and the
//     Compile / Prepare entry points that move between them,
//   - the provider × runtime support matrix that validates a plan against
//     the capabilities of the selected go-providers adapter and the
//     go-agent-sessions lifecycle shape,
//   - the Tether-compatible catalog schema so a single catalog entry can
//     drive every consumer in the portfolio,
//   - the frozen shared contract types for RuntimeBinding, BootSpec,
//     VarSpec, and the bootdir materializer API.
//
// The package is intentionally app-neutral. It imports
// agentkit/agentsessions and go-providers but no app-specific
// repository (Tether, Torque, Nanite). Consumers configure the
// pipeline through caller-supplied types and sinks rather than direct
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
