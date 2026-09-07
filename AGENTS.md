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
  `composer.go` walks the slot list; `hash.go` canonicalizes the request.
- `agentlaunch/` owns LaunchPlan → CompiledLaunch → PreparedLaunch.
  `matrix/` holds the legal (provider, runtime) table, `providerplant/` renders
  provider-native boot directories, `parity/` is the old-vs-new cutover gate.
- `agentsessions/` runs one agent process. `types.go` defines the Capabilities
  flags that select the lifecycle shape; `manager.go` owns registration,
  supervision and attach fan-out.
- `artifact/` models authorized materialization inputs; `materialize/engine.go`
  stages and publishes a tree, and `materialize/reconcile.go` owns the manifest.
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

`.github/workflows/check.yml` runs those five checks on push and on pull request
to `main`, and is the landing gate. Use `-race` before landing anything in
`agentsessions` or `materialize`; both are concurrent by design.

## Boundaries

App-neutrality is a convention here, not a lint rule — nothing fails if you
break it. No package may import an orchestrator. `agentcontext` is stricter
still: standard library and `gopkg.in/yaml.v3` only. Persistence is
consumer-owned; the library defines StateSink, AttachmentSink and EventSink and
ships no implementation of any of them. Adding one is the wrong layer.

`agentcontext` hashes the request, not the resolved output. Identical
ContextRequests must render byte-identically, which is why slots are walked in
slice order and the request is canonicalized with sorted keys before hashing.
Resolver-side non-determinism is the caller's problem and does not license
reordering here.

`materialize` writes through an `os.Root` opened on the target parent, stages
under a private directory, and publishes with `Root.Rename`. Symlinks anywhere
in the destination parent chain are rejected before that root is opened. A mode
that cannot uphold those guarantees returns an unsupported or unsafe-target
error instead of weakening the boundary. Reconcile deliberately does not claim
whole-directory transaction semantics over a mixed-ownership home.

`agentlaunch/parity` reads the live `~/.tether/catalog/` on this machine, and
must only ever read it. `TestParity_ReadOnlyContract` guards that the in-memory
runtime-kind normalization never reaches disk. The harness fails an unexplained
diff rather than going green: a genuine new divergence is registered in
`expected_diffs.go` with its rationale. That catalog is machine-local and
mutable, so a local parity failure can reflect catalog drift rather than your
change — and where the catalog is absent the suite skips, so CI never exercises
it.

At most one `agentsessions.Capabilities` lifecycle flag may be set — PTY,
StreamingStdio, JsonRpcStdio and ServeHTTP are mutually exclusive. Any Runtime
added here has to pass the `agentsessions/compliance` suite.
