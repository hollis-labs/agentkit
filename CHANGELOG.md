# Changelog

All notable changes to agentkit are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

### Added — `Report.StaleExpectedCaller` / `Report.StaleExpectedBuiltin`

`StaleExpected` reports every registered expectation the run never fired. It
merges this package's built-in registries with the caller's, and there is no
way to unregister a built-in — so a consumer running its own corpus could not
assert on staleness at all once a built-in entry stopped firing against its
catalog.

That is not hypothetical. `expectedOldErrors` registers
`hollislabs-web-writer-claude` against a missing `agents/web-writer.yaml`.
Tether's live catalog has since grown that file, so the entry never fires
there and `StaleExpected` reports it — correctly, and unactionably. Deleting
the entry is not the fix either: `testdata/catalog` ships that launch with no
`web-writer.yaml` on purpose, so removing the registration fails
`TestParity_FixtureCorpus`. The entry is stale for the consumer and required
here at the same time.

So staleness now carries provenance:

- `StaleExpectedCaller()` — entries registered through `WithExpectedDiffs` /
  `WithExpectedOldErrors`. **This is what a consumer should assert on.**
- `StaleExpectedBuiltin()` — entries from this package's registries.
  Informational for a consumer: each says a catalog defect the harness still
  documents has been fixed in the catalog that run read. Log, do not fail.

An entry registered on both sides counts as the caller's — they have one to
delete either way. The two views partition `StaleExpected` exactly.

`StaleExpected` itself is unchanged, so this is additive: existing callers
keep their current behavior, including consumers currently working around the
problem by filtering the built-in entry out by name.

### Verification

- `gofmt -l .` clean; `go vet ./...` clean.
- `golangci-lint run --max-same-issues=0 --max-issues-per-linter=0` — `0 issues.`
- `go test ./... -count=1` — green except the pre-existing, environment-linked
  `agentlaunch/parity.TestParity_LiveCatalog`, whose failure is byte-identical
  before and after this change: the developer host's catalog resolves
  `hollislabs-web-writer-claude` `work_dir` to `sites/hollis-labs.com` where the
  bag says `sites/hollislabs-web`. Unrelated to staleness reporting, and
  documented under v0.5.1.
- Two new tests: `TestParity_StaleExpectedProvenance` (both directions, plus
  the exact partition and the doubly-registered case) and
  `TestParity_StaleExpectedCallerCleanWhenNothingRegistered`.

## v0.5.1 — 2026-08-25

### Fixed — build and lint hygiene only; no API or behavioral change

**Nothing in this release changes what agentkit does.** Every exported
signature, every runtime behavior, and every documented contract is what
v0.5.0 shipped. Consumers can bump the pin without reading further — the rest
of this entry is about the repository's own CI gate, which had never once
passed.

Every `check` workflow run in GitHub's retained history was a failure, going
back to the initial v0.1.0 release commit. The gate failed at its first step,
`go fmt (verify)`, and so never reached a single step after it.
`agentlaunch/bootassembly_test.go` had been left unformatted by the
`RenderFrontEnd` → `MissingPolicy` rename documented under v0.3.0 below: the
replacement field name `OnMissing` is longer than the `FrontEnd` it replaced,
which changed gofmt's required key alignment in six struct literals, and gofmt
was never re-run afterward. The file is reformatted here; that part of the
diff is whitespace only.

Because gofmt gated everything behind it, `golangci-lint` had never executed
in CI at all, and its findings had been accumulating unseen since May. The
linter was also pinned to v2.1.6 — a binary built against a Go older than this
module's `go 1.26.1` directive, which would have refused to load its own
configuration had it ever been reached. That pin is now v2.13.1, so the second
gate works as well as the first.

With both gates actually running, golangci-lint reported 23 findings: 19
visible, plus 4 more hidden behind golangci-lint's default `max-same-issues: 3`
output cap. All 23 are fixed in code — seven unchecked `Close`/`Remove` returns
turned into explicit discards, fourteen staticcheck simplifications (De Morgan
rewrites, a tagged switch in `tokenizeJSONPath`, `fmt.Fprintf` in place of
`Write([]byte(fmt.Sprintf(...)))`, a merged conditional assignment, and the
removal of five `runtime.GOOS == "windows"` guards that are dead under their
own files' `//go:build !windows` constraint), one dead assignment in
`DefaultRenderer.Render`, and one unreferenced helper. Each edit is
semantically neutral, and no `.golangci.yml` was added: the gate is repaired by
making the code pass, not by configuring the linter not to fail.

`go.mod` and `go.sum` are untouched by this release.

### Verification

- `gofmt -l .` — clean. `go vet ./...` — clean.
- `golangci-lint run --max-same-issues=0 --max-issues-per-linter=0` — `0 issues.`
  The uncapped flags matter here: golangci-lint's default cap concealed four
  real findings, so a capped-clean run is not the same thing as a clean one.
- `go test -race -count=1 ./...` — green across every package except the
  pre-existing, environment-linked `agentlaunch/parity.TestParity_LiveCatalog`
  failure, whose output is unchanged from before this release: the live
  `~/.tether/catalog` entry for `hollislabs-web-writer-claude` still resolves
  `work_dir` to `sites/hollis-labs.com` where the directory is now
  `sites/hollislabs-web`. That is drift in the developer host's catalog, not an
  agentkit defect, and the test skips when no catalog is present — as on CI.
- CI run 32903208117 is the first green `check` run in this repository's
  history. Every step passes, with `golangci-lint found no issues` at v2.13.1
  and `No vulnerabilities found.` from govulncheck under the runner's go1.27.0.

## v0.5.0 — 2026-08-21

### Fixed — BEHAVIORAL CHANGE, not just a bug fix — read before bumping your pin

**`agentsessions.NewFromAdapter`'s subprocess-per-turn runtime (`adapterSession`,
`Caps{}` all false — the default, and the shape every `CLIAdapter` gets unless it
opts into PTY / StreamingStdio / JsonRpcStdio / ServeHTTP) now always delivers a
terminal `llmtypes.StreamEvent` — `EventDone` on a clean turn, `EventError`
otherwise — to `StartOptions.EventFanout` / `StartOptions.Fanout`, even when the
driven `provider.CLIAdapter`'s own `ParseLine` never emits one of its own.**

Previously, `adapterSession.handleRunnerEvent`'s `runner.EventProcessExited` case
did nothing but reset the tracked PID. For an adapter whose `ParseLine` never
emits `llmtypes.EventDone` / `EventError` / `EventUsage` — true today for
`go-providers`' `OpencodeAdapter` (Mode `""`, i.e. `opencode run`) by design,
since opencode has no structured completion line on stdout — a turn's real
subprocess could spawn, run, produce real output, and exit cleanly, and **no
terminal event would ever reach `EventFanout`/`Fanout`**, regardless of how long
the caller waited. Any downstream consumer that keys turn completion off a
terminal `llmtypes.StreamEvent` (e.g. `go-agent-wrapper`'s
`event_translator.go`, which maps `EventDone`/`EventUsage` to
`runtimeevents.KindTurnCompleted`) would hang forever even though the process
itself had long since exited — a real, 100%-reproducible, live-dogfeed-confirmed
bug for every OpenCode CLI-hosted chat turn.

`adapterSession.SendInput` now tracks, per turn, whether `handleRunnerEvent` ever
observed the adapter's own `EventDone`/`EventError`/`EventUsage` — a session
field, `turnSawTerminal`, reset at the top of each `SendInput` and set from
`handleRunnerEvent`'s `EventProviderEvent` case. After `runner.Run` returns
(covering both `EventProcessExited` and `EventProcessTimeout` — every path
`runner.Run` can return through), if the adapter never produced its own terminal
event, `SendInput` synthesizes one from `runner.Run`'s own return value: `nil` →
`EventDone`, non-nil → `EventError` with the error text. The synthesized event
flows through the exact same `tryEventFanout`/`encodeStreamEvent` path
`handleRunnerEvent` already used, so downstream consumers cannot distinguish a
synthesized terminal event from one the adapter emitted itself.

**Adapters whose `ParseLine` already emits its own terminal event (Codex's
`"turn.completed"` line, for example) are unaffected — `turnSawTerminal` short-
circuits the synthesis, so no double-fire.** Verified directly by a dedicated
regression test (`TestAdapterRuntime_DoesNotDoubleFireTerminalEvent_WhenAdapterEmitsItsOwn`)
using a fake adapter whose `ParseLine` behaves exactly like Codex's shape (emits
its own terminal event before the process exits) — confirms the fanout carries
exactly one `EventDone`, not two, under the fix. A second variant
(`...WhenAdapterEmitsUsageOnly`) confirms `EventUsage` alone (no `EventDone`)
also counts as "already terminal" and suppresses synthesis, per this fix's
explicit scope (`EventDone`/`EventError`/`EventUsage`, not just `EventDone`).

**If you drive `NewFromAdapter`'s default (non-PTY) runtime and previously
relied on the adapter runtime silently producing no terminal event for an
adapter like OpenCode's — e.g. a consumer that itself synthesized completion
some other way, or that intentionally left a turn "open" pending a later
out-of-band signal — re-check that assumption before bumping this pin.** A turn
driving such an adapter will now, for the first time, see a terminal
`llmtypes.StreamEvent` land on `EventFanout`/`Fanout` shortly after the real
subprocess exits.

### Verification

- darwin host: `go build ./...`, `go vet ./...` — clean.
- `go test -race -count=3 ./agentsessions/...` — green, including four new
  real-subprocess (not mocked) regression tests in
  `agentsessions/from_adapter_terminal_synthesis_test.go`: a fake adapter whose
  `ParseLine` only ever emits `EventDelta` (mirroring `OpencodeAdapter`'s real
  contract) driving a real spawned-and-cleanly-exited subprocess
  (`TestAdapterRuntime_SynthesizesEventDone_WhenAdapterNeverEmitsTerminalEvent`)
  and a real spawned-and-non-zero-exited subprocess
  (`TestAdapterRuntime_SynthesizesEventError_WhenAdapterNeverEmitsTerminalEvent_AndProcessFails`),
  plus the two no-double-fire variants above.
- `go test -race -count=1 ./...` — green across every package except the
  pre-existing, environment-linked `agentlaunch/parity.TestParity_LiveCatalog`
  failure (a live-catalog drift against `~/.tether/catalog`, already documented
  as unrelated in the v0.4.0 entry below and untouched by this change — this
  fix's diff is scoped entirely to `agentsessions/from_adapter.go` plus its own
  new test file).
- Codex's own already-terminal-event-emitting `ParseLine`
  (`go-providers/provider/pty_codex.go`'s `"turn.completed"` handling) was
  independently modeled (not exercised via the real `codex` binary — that binary
  was not invoked from this repo) by the `echoAdapter` fake already used
  throughout `agentsessions`' existing test suite, which emits its own `done`
  line the same way Codex's real adapter emits its own `EventDone` — confirmed
  not to double-fire under this fix (see the "no double-fire" test above). A
  live `codex` binary re-verification against the real adapter is the
  Nanite-side dogfeed's job, not this library-level fix's.

## v0.4.0 — 2026-08-21

### Fixed — BEHAVIORAL CHANGE, not just a bug fix — read before bumping your pin

**`agentsessions.Session.Wait()` (and, transitively, `Manager.WaitSession`)
now returns a real, correctly-populated `*ExitError` for an abnormal exit
under the *unsupervised* waiter path — the default, `StartOptions.
Supervisor == nil`, and for most direct `agentsessions` consumers today
the *only* waiter path any real CLI session actually exercises.**

Every runtime kind's unsupervised legacy waiter (`streaming_stdio_
session.go`, `jsonrpc_stdio_session.go`, `pty_session.go`, and
`serve_http_session.go`, which has no supervised variant at all) shared
the same bug: `cmd.Wait()` returns a `*exec.ExitError` for *any* abnormal
exit — a non-zero exit code **and** a signal-based death (SIGKILL
included) both take that branch — but the legacy waiter's
`errors.As(err, &ee)` handling stored only the numeric exit code and
never populated the returned error. The net effect: `Wait()` returned
`(code, nil)` — a **nil error** — for the overwhelming majority of
real-world abnormal exits, including an externally-SIGKILL'd process.
A nil error is indistinguishable from a clean exit to any caller
classifying terminations via `errors.As(err, &xe)` against `*ExitError`
(the pattern this package's own `WaitSession` godoc has always
documented as the correct one) — so a killed session's crash/kill was
silently swallowed with zero signal that anything went wrong.

All four runtimes now build the returned error the same way the
supervised path already did (`buildExitError`, unchanged): `Code`
populated from the real exit code (`-1` for a signal death, matching
`exec.ExitError.ExitCode()`'s own convention), `Signal` and `Killed`
populated from the process's wait status on a signal death, and `Cause`
left empty — no `Supervisor` is attached on the unsupervised path to
have driven the exit, matching the same "ordinary, non-supervisor-driven
exit" convention `ExitError.Cause` already documented for the supervised
path (ordinary non-zero exits and Stop/ctx-cancel under supervision also
carry an empty `Cause` despite `*ExitError` still being returned). No new
`Cause` constant was introduced.

**If your code calls `Session.Wait()` or `Manager.WaitSession()` and
treats a nil error as "the process exited cleanly, nothing to do" —
re-check that assumption before bumping this pin.** A process that
crashed, was killed by a signal (including an external SIGKILL), or
exited non-zero, under the unsupervised waiter path, previously reported
back as `(code, nil)`; it now correctly reports back as
`(code, *ExitError)`. Consumers with a downstream classifier
(recovery/retry logic, alerting, telemetry) gated on `err != nil` were
previously never reaching that code for the unsupervised path — they
will now, for the first time, actually see it.

Also fixed alongside: `serve_http_session.go`'s `Start()` started its
`finishOnProcessExit` waiter goroutine twice (once immediately after
spawn, once again after health-check + session-creation succeeded). Both
goroutines raced to receive the single value off the buffered,
close-once `processDone` channel; roughly half the time the
later-started goroutine instead received the channel's post-close zero
value and won the `sync.Once` race, silently discarding the real exit
error regardless of the fix above. The redundant second goroutine spawn
is removed — one waiter, started once, observes the process's exit
correctly at any point in `Start()`'s lifetime.

### Verification

- darwin host: `gofmt -l .` clean, `go vet ./...`, `go build ./...` —
  green.
- `go test -race -count=3 ./agentsessions/...` — green, including four
  new real-subprocess (not mocked) regression tests — one per runtime
  kind — that spawn a real child via the unsupervised waiter path,
  `SIGKILL` it externally (matching this fix's own repro), and assert
  `Wait()` returns a non-nil, `errors.As`-extractable `*ExitError` with
  the correct `Code`/`Signal`/`Killed`/`Cause`.
- `go test -race -count=1 ./...` — green across all 27 packages, except
  the pre-existing `agentlaunch/parity.TestParity_LiveCatalog` failure
  (an environment-linked live-catalog drift against `~/.tether/catalog`,
  unrelated to this change and reproducible against the unmodified
  v0.3.0 tag).

## v0.3.0 — 2026-05-26

### Changed

- **Strict-by-default missing-value policy** for `agentlaunch.AssemblySpec.Render`.
  The existing strict-when-autonomous semantic is unchanged; the
  surrounding API is renamed for clarity:
    - `RenderFrontEnd` → `MissingPolicy`
    - `FrontEndAutonomous` → `PolicyError` (zero value, default = strict)
    - `FrontEndInteractive` → `PolicyCollect` (opt-in soft-fail)
    - `RenderRequest.FrontEnd` → `RenderRequest.OnMissing`
    - `LaunchBag.RenderRequest(frontEnd)` parameter → `RenderRequest(onMissing)`
  An empty `RenderRequest{}` now reads naturally as the strict default
  (`PolicyError` is implicit). Callers wanting the previous interactive
  behavior pass `OnMissing: PolicyCollect` explicitly.

## v0.2.0 — 2026-05-26

### Changed

- Renamed `agentlaunch.PreparedPlantContext` fields from `MuxCommand`,
  `MuxArgs`, and `MuxEnv` to the neutral `SelfMCPCommand`,
  `SelfMCPArgs`, and `SelfMCPEnv`.

### Fixed

- Removed stale Agent Mux path defaults from shipped Tether catalog
  fixtures.
- Cleaned README/example copy that still referred to the pre-`agentkit`
  split libraries.

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
- `github.com/hollis-labs/go-runner` v0.5.0
- `github.com/hollis-labs/go-sandbox` v0.2.1

### Migration notes

Per-consumer import rewrite spec lives in the migration map. The
absorbed packages keep their original names (`agentcontext`,
`agentlaunch`, etc.) so call-site selectors do not change — only import
paths change.

### Verification

- darwin host: `gofmt -l .` clean, `go vet ./...`, `go build ./...`,
  `go test -race -count=1 -timeout 180s ./...` — green (21 packages).
