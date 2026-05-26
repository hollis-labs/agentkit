# Changelog

All notable changes to agentkit are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.1.0 — 2026-05-26

Initial release. Consolidates the previously separate `go-agent-*`
runtime libraries into a single module per
[agentkit-migration-map.md](../../agentkit-migration-map.md).

### Absorbed

- `github.com/hollis-labs/go-agent-context` v0.1.0 → `agentkit/agentcontext`
  (plus `resolvers/`, `skills/`)
- `github.com/hollis-labs/go-agent-launch` v0.4.0 → `agentkit/agentlaunch`
  (plus `catalog/`, `contexthook/`, `launcher/`, `matrix/`, `parity/`,
  `providerplant/`, `sessionshim/`)
- `github.com/hollis-labs/go-agent-sessions` v0.10.0 → `agentkit/agentsessions`
  (plus `compliance/`)
- `github.com/hollis-labs/go-agent-runtime` v0.5.0 → `agentkit/agentruntime`
  (plus `bootdir/`, `checkpoint/`, `loopback/`, `runtimebind/`,
  `runtimekind/`, `sessionkit/`, `smoke/`, `turn/`)
- `github.com/hollis-labs/go-agent-broker` v0.2.1 → `agentkit/broker`

### Excluded

- `go-agentmux-client` — deferred per migration-map Decision 2.

### External dependencies

- `github.com/hollis-labs/go-llm-contracts` v0.3.0
- `github.com/hollis-labs/go-llm-types` v0.3.0
- `github.com/hollis-labs/go-providers` v0.23.0
- `github.com/hollis-labs/go-runner` v0.6.0
- `github.com/hollis-labs/go-sandbox` v0.2.1

### Migration notes

Per-consumer import rewrite spec lives in the migration map. The
absorbed packages keep their original names (`agentcontext`,
`agentlaunch`, etc.) so call-site selectors do not change — only import
paths change.

### Verification

- darwin host: `gofmt -l .` clean, `go vet ./...`, `go build ./...`,
  `go test -race -count=1 -timeout 180s ./...` — green (21 packages).
