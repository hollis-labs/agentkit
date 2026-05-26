# agentkit

Unified Go agent runtime toolkit

Module path: `github.com/hollis-labs/agentkit`

`agentkit` is the consolidation of the previously separate `go-agent-*`
runtime libraries (`go-agent-context`, `go-agent-launch`,
`go-agent-sessions`, `go-agent-runtime`, `go-agent-broker`) into a single
Go module with multiple public packages. See
[agentkit-migration-map.md](../../agentkit-migration-map.md) for the
rationale and consumer migration plan.

External dependencies (`go-llm-types`, `go-llm-contracts`, `go-providers`,
`go-runner`, `go-sandbox`) remain separate modules and are required
through normal `go.mod` declarations. `go-agentmux-client` is deliberately
excluded (see the migration map).

## Packages

- `github.com/hollis-labs/agentkit/agentcontext`
  - Slot-source resolver framework (static, inline, cmd, http_text,
    http_json, role_summary, skill_index). Deterministic boot-prompt
    assembly with byte/token budgets and per-slot provenance.
  - Subpackages: `agentcontext/resolvers`, `agentcontext/skills`.
- `github.com/hollis-labs/agentkit/agentlaunch`
  - LaunchPlan → CompiledLaunch → PreparedLaunch pipeline. Bootdir
    materialization (`Populate`, `Replant`). Provider × runtime matrix.
    Tether-compatible catalog schema.
  - Subpackages: `catalog`, `contexthook`, `launcher`, `matrix`,
    `parity`, `providerplant`, `sessionshim`.
- `github.com/hollis-labs/agentkit/agentsessions`
  - Session lifecycle: PTY, streaming-stdio, JSON-RPC stdio adapters.
    Auto-plant bootdir helpers. Compliance subpackage for parity tests.
- `github.com/hollis-labs/agentkit/agentruntime`
  - Runtime helpers: turn, checkpoint, bootdir, loopback, runtimebind,
    runtimekind, sessionkit, smoke.
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

CI (`.github/workflows/check.yml`) runs the same checks on push and pull
request to `main`.

## License

MIT — see [LICENSE](./LICENSE).
