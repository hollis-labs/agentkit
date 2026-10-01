# agentkit

agentkit is the shared Go runtime toolkit for agents: it assembles boot-prompt
context, compiles a launch plan into a materialized boot directory, and runs the
result as a supervised session. It is app-neutral — it holds no orchestrator
logic, ships no persistence, and never imports Tether, Torque or Nanite.

## Start Here

- `README.md` maps the five consolidated `go-agent-*` libraries onto today's
  packages; `CHANGELOG.md` carries that mapping and the release history.
- Each package's `doc.go` is its contract rather than a summary. Read it before
  the implementation.
- `agentcontext/` resolves typed slot sources into a deterministic boot body.
  `provider.go` walks the slot list; `hash.go` canonicalizes the request;
  `composer.go` merges authored recipes into a `ResolvedComposition`.
- `agentlaunch/` owns LaunchPlan → CompiledLaunch → PreparedLaunch
  (`launcher/`) and the `PreparedExecution` handoff to session runtimes.
  `matrix/` answers which (runtime, mode) pairs launch, read from the
  go-providers `registry` (it holds no table), `providerplant/` renders and
  materializes provider-native boot directories and maps the permission
  posture (`PrepareExecution`), `parity/` is the old-vs-new cutover gate.
- `agentsessions/` runs one agent process. `types.go` defines the Capabilities
  flags that select the lifecycle shape and the StartOptions every runtime
  takes; `from_adapter.go` (`NewFromAdapter`) routes them to the
  subprocess-per-turn, PTY, streaming-stdio, jsonrpc-stdio or serve-http
  runtime; `manager.go` owns registration, terminal-exit watching and attach
  fan-out.
- Materialization inputs (`Entry`/`Tree`) and the write engine (staged create,
  `Reconcile`/`Refresh`) live in
  [`go-materialize`](https://github.com/hollis-labs/go-materialize)
  (`artifact`/`materialize` packages), not in this repo — agentkit depends on
  it like any other consumer. See that module's own `doc.go` files for the
  contract.
- `agentruntime/` is a deliberately thin facade; its surface is in subpackages.
- `broker/` routes one turn to an agent profile.

## Commands

```bash
go test ./agentsessions          # smallest run that covers the changed package
go test -race ./...
gofmt -l .                       # no output is clean
go vet ./...
golangci-lint run
govulncheck ./...
```

`.github/workflows/check.yml` runs those five checks on push to `main` and on
every pull request, and is the landing gate. Use `-race` before landing
anything in `agentsessions`; it's concurrent by design.

Tests that need an agent CLI run the real go-providers adapter against
go-providers' `providertest` fake binary (captured wire output, or a scripted
run), not an installed CLI.

## Boundaries

App-neutrality is a convention here, not a lint rule — nothing fails if you
break it. No package may import an orchestrator. `agentcontext` is stricter
still: standard library and go-materialize's `artifact` types only (its
`skills` subpackage adds `gopkg.in/yaml.v3`). Persistence is
consumer-owned; the library defines StateSink, AttachmentSink and EventSink and
ships no implementation of any of them. Adding one is the wrong layer.

`agentcontext` hashes the request, not the resolved output. Identical
ContextRequests must render byte-identically, which is why slots are walked in
slice order and the request is canonicalized with sorted keys before hashing.
Resolver-side non-determinism is the caller's problem and does not license
reordering here.

`agentlaunch/parity` reads the live `~/.tether/catalog/` on this machine, and
must only ever read it. `TestParity_ReadOnlyContract` guards that the in-memory
runtime-kind normalization never reaches disk. The harness fails an unexplained
diff rather than going green: a genuine new divergence is registered in
`expected_diffs.go` with its rationale. That catalog is machine-local and
mutable, so a local parity failure can reflect catalog drift rather than your
change — and where the catalog is absent the suite skips, so CI never exercises
it.

agentkit holds no list of runtimes. Which runtimes exist, their aliases,
binaries, modes, default mode and layout come from the go-providers
`registry` over the agent-contracts-leaf `runtimes` vocabulary (D-73); there is
no runtime-kind enum here and no alias for the old spellings (`subprocess`,
`serve-http`, `pty-debug`). A runtime added to the registry must resolve with
no agentkit edit — `TestResolveANewRegistryRuntime` (runtimebind) and
`TestANewRegistryRuntimeResolves` (matrix) guard it. Planting a new native
runtime needs a constructor in go-providers' `provider.NewAdapter` table,
which providerplant's `DefaultResolver` calls; until one exists it returns
`ErrNoNativeAdapter` (`TestDefaultResolver_NewNativeRuntimeNeedsAConstructor`).
The Tether catalog's own `runtime_kind` tokens are translated at the catalog
boundary (`mapRuntimeKind`), and nowhere else.

At most one `agentsessions.Capabilities` lifecycle flag may be set — PTY,
StreamingStdio, JsonRpcStdio and ServeHTTP are mutually exclusive. Any Runtime
added here has to pass the `agentsessions/compliance` suite, open its session
log through `openSessionLog` (append-only, never truncated: a host such as
Torque appends its own lines to the same file), and read child output through
`readLines`, which skips a line over 64 MiB instead of stalling the pipe.
