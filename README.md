# agentkit

## Moved to substrate

This standalone repository is deprecated. New development lives in the
[`github.com/hollis-labs/substrate/harness`](https://github.com/hollis-labs/substrate/tree/harness/v0.3.0/harness)
module, released as **`harness/v0.3.0`**.

```sh
go get github.com/hollis-labs/substrate/harness@v0.3.0
```

Follow the [package and API migration guide](https://github.com/hollis-labs/substrate/blob/harness/v0.3.0/harness/docs/units/agentkit/MIGRATION.md) when updating imports;
the consolidation can include API changes. Existing standalone tags and history
are preserved. The documentation below describes the standalone releases and
is retained for historical reference. Applications migrate separately; this
redirect does not deploy or update any consumer.

Unified Go agent runtime toolkit

Module path: `github.com/hollis-labs/agentkit`

`agentkit` is the consolidation of the previously separate `go-agent-*`
runtime libraries (`go-agent-context`, `go-agent-launch`,
`go-agent-sessions`, `go-agent-runtime`, `go-agent-broker`) into a single
Go module with multiple public packages. See [CHANGELOG.md](./CHANGELOG.md)
for the package mapping and release history.

External dependencies (`agent-contracts-leaf`, `go-llm-types`,
`go-llm-contracts`, `go-materialize`, `go-permission`, `go-providers`,
`go-runner`, `go-sandbox`) remain separate modules and are required
through normal `go.mod` declarations. `go-agentmux-client` is deliberately
excluded (see the migration map).

## Packages

- `github.com/hollis-labs/agentkit/agentcontext`
  - Slot-source resolver framework (static_file, static_dir, inline, cmd,
    http_text, http_json, role_summary, skill_index). Deterministic
    boot-prompt assembly with byte/token budgets and per-slot provenance.
    Recipe composition (`AuthoredRecipe` → `ResolvedComposition`).
  - Subpackages: `agentcontext/resolvers`, `agentcontext/skills`.
- `github.com/hollis-labs/agentkit/agentlaunch`
  - LaunchPlan → CompiledLaunch → PreparedLaunch pipeline
    (`launcher.Compile` / `launcher.Prepare`), and the `PreparedExecution`
    handoff `providerplant.PrepareExecution` builds for session runtimes.
    The permission posture (`ProviderSpec.Permission`) is a go-permission
    Mode the go-providers registry maps per provider; `MCPSpec` carries the
    loopback URL and per-session MCP servers. Provider × runtime matrix read
    from the go-providers registry. Bootdir materialization (`Populate`,
    `Replant`). Tether-compatible catalog schema.
  - Subpackages: `catalog`, `contexthook`, `launcher`, `matrix`,
    `parity`, `providerplant`, `sessionshim`.
- `github.com/hollis-labs/agentkit/agentsessions`
  - Session lifecycle over go-providers adapters: subprocess-per-turn
    (default), PTY, streaming-stdio, JSON-RPC stdio and serve-http
    (`http-sse`) runtimes, plus HTTP API providers. Optional turn interrupt
    (`TurnInterrupter`), sandboxing at spawn, append-only session logs,
    auto-plant bootdir helpers. The `compliance` subpackage is the
    behavioral suite every Runtime must pass.
- `github.com/hollis-labs/agentkit/agentruntime`
  - Runtime helpers: turn, checkpoint, bootdir, loopback, runtimebind,
    sessionkit, smoke. `turn` also carries the Codex app-server client
    (thread binding, notification parsers, `CodexApprovalResponder`).
    Runtime ids, modes and per-runtime facts come from agent-contracts-leaf
    `runtimes` and the go-providers `registry`.
- `github.com/hollis-labs/agentkit/broker`
  - Envelope/messaging harness used by agent runtimes for inter-component
    coordination.

## Install

```sh
go get github.com/hollis-labs/agentkit/...
```

## Development

```sh
go test -race ./...   # tests
go vet ./...          # vet
gofmt -l .            # formatting check (no output = clean)
golangci-lint run     # lint
govulncheck ./...     # vulnerability scan
```

CI (`.github/workflows/check.yml`) runs the same checks on push to `main`
and on every pull request.

## License

MIT — see [LICENSE](./LICENSE).
