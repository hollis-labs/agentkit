# Changelog

All notable changes to agentkit are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
