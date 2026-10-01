# agentkit examples

Runnable examples for the `agentkit` packages live here — one `main`
package per subdirectory, run from the repository root:

- `go run ./examples/go-agent-broker/deterministic` — `broker.DeterministicBroker`'s
  rule priority, one call per rule.
- `go run ./examples/go-agent-launch/providerplant` — `launcher.Compile`,
  `providerplant.PrepareAndPlant` and `sessionshim.ToSessionLaunch` end to end
  (opencode, under the system tempdir).
- `go run ./examples/go-agent-launch/with-context` — an `agentcontext` provider
  wired into `launcher.Prepare` through `contexthook`.
- `go run ./examples/go-agent-sessions/runner_session` — a subprocess-per-turn
  `agentsessions` runtime registered with a `Manager`, with attach streaming.
