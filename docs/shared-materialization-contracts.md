# Shared Materialization Contracts

Status: M01 contract baseline for Torque task `CW-20260906-0004`.

M17 adoption handoff: see `docs/shared-materialization-handoff.md` for the final dependency closure, migration mapping and release sequence.

This document records the library ownership and compatibility matrix for the shared composition, materialization and prepared-execution work. It is intentionally narrower than the approved architecture: it fixes concrete package surfaces for implementation tasks without reopening app adoption or release work.

## Package Ownership

| Package | Owns | Must not own |
| --- | --- | --- |
| `agentcontext` | Authored recipe and resolved composition contracts, ordered parts, declared merge rules, documents, contributor provenance | Provider file names, runtime spawn policy, app product schema |
| `artifact` | Provider-neutral file/directory/tree entries, immutable content references, modes, digests, provenance, source resolver contract | Provider layout rules, session launch, child process access grants |
| `materialize` | Create/reconcile/refresh/plan request shape, target roots, owned manifest, change reports, engine interface | Provider semantics, real OS sandbox enforcement, app retention policy |
| `agentlaunch` | Prepared execution handoff, structured argv/env/cwd, runtime roots, explicit effects, access requirements, diagnostics, legacy compatibility markers | go-sandbox backend implementation, live credential reads in pure projection |
| `go-providers` | Pure provider projection values and serializers for Claude, Codex and OpenCode | `agentkit` imports or filesystem mutation |
| `go-sandbox` | Resolved access policy and pre-spawn enforcement capabilities | Composition, provider projection, materialization |
| `go-runner` | Passing resolved sandbox policy through every process start/restart path | Provider layouts or a second materializer |
| `go-agent-wrapper` | Orchestration over prepared/convenience inputs and provider process lifecycle | App adoption policy or duplicate planting engines |

## Module Graph

The intended graph remains acyclic:

```text
artifact
  <- agentcontext
  <- materialize
  <- agentlaunch
go-providers <- consumed by agentlaunch at the integration boundary
go-sandbox <- consumed by go-runner/agentlaunch/session boundaries after M08
go-runner <- consumed by agentkit session runtime paths
agentkit <- consumed by go-agent-wrapper
```

`go-providers` must not import `agentkit`. Provider-owned values are translated to `artifact.Tree` at the `agentlaunch` boundary. `materialize` imports only `artifact`; it has no runtime/provider dependency.

## Entry Points

Callers can enter at three levels:

| Entry | Contract type | Used when |
| --- | --- | --- |
| Direct artifacts | `artifact.Tree`, `agentlaunch.PrepareRequest{Kind: PrepareInputArtifacts}` | A caller already has files/trees and wants render/install/launch preparation without composition |
| Resolved composition | `agentcontext.ResolvedComposition`, `PrepareInputResolvedComposition` | A caller has already resolved ordered parts, documents and artifacts |
| Authored recipe | `agentcontext.AuthoredRecipe`, `PrepareInputAuthoredRecipe` | A caller wants shared composition to resolve base/parts/merge/document assembly |

Materialization is independent through `materialize.Request` and `materialize.Engine`; render/install callers do not need a runtime, wrapper or daemon.

## Legacy Compatibility

| Existing surface | Compatibility stance |
| --- | --- |
| `agentlaunch.BootSpec`, `BootFileSpec`, `BootInjectionSpec`, `DefaultMaterializer` | Kept as compatibility surfaces. Later tasks adapt them onto neutral `artifact` and `materialize` values. |
| `agentlaunch.PreparedLaunch` | Kept as a legacy session shim value. New lossless handoff is `agentlaunch.PreparedExecution`. |
| `agentlaunch.NativeFile` | Kept for flat raw files and legacy single-file skills; tree-capable modern inputs use `artifact.Tree`. |
| `InjectionSpec.BootDirOverlay` | Kept as content-only compatibility. It loses executable modes, binary identity and tree structure by design. |
| `providerplant.Plant` | Later tasks must make it delegate to the shared engine and stop mutating argv/env/cwd on repeated calls. |
| `agentsessions.StartOptions.AutoPlantBootDir` | Existing callers remain supported, but a supplied prepared/materialized handle must disable redundant planting. |

Unsupported features must produce diagnostics instead of disappearing. A requested required confinement guarantee must fail before spawn when the selected host/runtime cannot enforce it.

## Access Contract

`agentlaunch.AccessRequirements` distinguishes:

| Name | Meaning |
| --- | --- |
| `ProjectRoot` | The user project/repo root the agent works on |
| `BootRoot` | The materialized provider/config/context tree |
| `StateRoot` | Durable session/provider state controlled by the caller |
| `ScratchRoot` | Private temporary write area for the session |
| `CWD` | The process working directory passed to spawn |

These values may coincide, but no implementation may infer one from another after preparation. `AccessRequirements.Mode` is `disabled`, `optional` or `required`; `Host` is `local` or `remote`. Filesystem entries are explicit read/write/deny paths, with deny taking precedence. Runtime reads, loopback/network requirements, subprocess permission and provider-native approval settings remain separate from OS confinement.

For local required confinement, `go-sandbox` must produce an actual pre-spawn guarantee or an unsupported/failed outcome. For remote or already-running endpoints, local code may report explicit unconfined mode, but it must not claim that local policy applies to an execution host it cannot control.

## Source Provenance

Current read-only reference checkouts captured for M01:

| App | Path | HEAD | Dirty state | Relevant evidence |
| --- | --- | --- | --- | --- |
| Cairn | `/Users/chrispian/dev/projects/cairn` | `3735a92` | clean | `profile/resolve.go`, `bootdir/render.go`, `bootdir/layout.go`, `bootdir/template.go`, `bootdir/plant.go`, `install/install.go`, `install/write.go` |
| Nanite | `/Users/chrispian/dev/hollis-labs/apps/nanite` | `c56aad70` | clean | `internal/runtime/agent/bootdir.go`, `internal/runtime/agent/bootdir_plant.go`, `internal/runtime/agent/bootdir_provider_config.go`, `internal/runtime/agent/skill_plant.go`, `internal/store/host_runtime_feed.go` |
| Torque | `/Users/chrispian/dev/hollis-labs/apps/torque` | `1e33e28` | clean | `internal/runtime/agent/boot.go`, `internal/runtime/agent/task_context.go`, `internal/runtime/bootstrap/loopback.go` |
| Tether | `/Users/chrispian/dev/hollis-labs/apps/tether` | `5406114` | untracked `planning/docs/materialization-planting-review.md` | `cmd/mux/torque_tasks.go`, `cmd/mux/torque_tasks_test.go`, `internal/app/shared_launch.go`, `internal/launch/agentlaunch.go`, `internal/launch/resolver.go`, `internal/workspace/workroot.go` |

Consumer module versions observed in `go.mod`:

| Consumer | agentkit | go-agent-wrapper | go-providers | go-sandbox | go-runner |
| --- | --- | --- | --- | --- | --- |
| Cairn | `v0.5.1` | `v0.8.1` | `v0.25.0` | - | - |
| Nanite | `v0.5.0` | `v0.9.1` | `v0.24.0` | `v0.2.1` | `v0.5.0` indirect |
| Torque | `v0.3.0` | - | `v0.23.0` | `v0.2.1` | `v0.5.0` |
| Tether | `v0.5.1` | - | `v0.25.0` | `v0.2.1` | `v0.6.0` indirect |

Fixtures under `agentlaunch/testdata/shared-materialization/` are synthetic and credential-free. They identify the app shape and source paths but do not copy product content or mutate app repositories.
