# Shared materialization implementation handoff

Status: M17 handoff for Torque task `CW-20260906-0020`.

This is the library-side adoption guide for the shared materialization work. It records the APIs implemented in the five shared Go modules and the sequence needed to release them. It does not perform app adoption, create downstream tasks, push commits, tag releases, or mutate Cairn, Nanite, Torque, Tether or Tachyon source.

## Final module graph

The integrated graph is acyclic and keeps provider projection independent from materialization:

```text
go-providers

go-sandbox
  <- go-runner

go-providers + go-sandbox + go-runner
  <- agentkit

agentkit + go-providers + go-sandbox
  <- go-agent-wrapper
```

`go-providers` has no `agentkit` import. Provider packages return pure projection and runtime-preparation values; `agentkit/agentlaunch` converts those provider values to `artifact.Tree`, `materialize.Request` and `PreparedExecution` values. `go-sandbox` owns resolved OS access policy and enforcement outcomes. `go-runner`, `agentkit/agentsessions`, and `go-agent-wrapper` only carry and apply those policies at process start boundaries.

The repository `go.mod` files must not carry absolute `replace` directives. Until the releases below exist, validate the integrated graph with a temporary workspace file outside the repositories:

```sh
cat > /Users/chrispian/dev/agent-os/workspaces/shared-materialization-20260906/m17-go.work <<'WORK'
go 1.26.6

use (
	/Users/chrispian/dev/agent-os/workspaces/shared-materialization-20260906/go-providers-m06
	/Users/chrispian/dev/agent-os/workspaces/shared-materialization-20260906/go-sandbox-m08
	/Users/chrispian/dev/agent-os/workspaces/shared-materialization-20260906/go-runner-m11
	/Users/chrispian/dev/hollis-labs/libs/agentkit
	/Users/chrispian/dev/hollis-labs/libs/go-agent-wrapper
)
WORK
export GOWORK=/Users/chrispian/dev/agent-os/workspaces/shared-materialization-20260906/m17-go.work
```

Do not commit that file and do not change global Go environment settings.

`go-runner` and `agentkit` should remain readable in normal module mode against their current published pins. `go-agent-wrapper` intentionally requires this temporary workspace until `agentkit v0.6.0` is released, because it imports new `agentkit/artifact` and `agentkit/materialize` packages that are absent from the current published `agentkit` tag. After `agentkit v0.6.0` exists, bump wrapper dependencies and run `GOWORK=off go mod tidy` before the wrapper release. `go mod tidy -diff` in `go-agent-wrapper` is therefore a post-agentkit-release check, not a pre-release local-workspace gate for this session.

## API entry points

Use the highest-level entry point that matches the caller's ownership of inputs.

| Caller input | API path | What the shared libraries do |
| --- | --- | --- |
| Raw files and trees | `artifact.Tree`, `artifact.FilesystemSource`, `artifact.ImmutableRef`, `agentlaunch.PrepareRequest{Kind: PrepareInputArtifacts}` | Resolve bounded source trees, verify immutable content, preserve modes/binary bytes/empty dirs, then materialize or prepare launch roots. |
| Already-resolved composition | `agentcontext.ResolvedComposition`, `agentlaunch.PrepareRequest{Kind: PrepareInputResolvedComposition}` | Install resolved documents/artifacts without re-running app-specific composition policy. |
| Authored recipe | `agentcontext.AuthoredRecipe`, `agentcontext.DefaultComposer`, `agentlaunch.PrepareRequest{Kind: PrepareInputAuthoredRecipe}` | Resolve ordered bases/parts, apply declared merge rules, resolve slots, assemble documents, then hand the result to materialization. |
| Existing mixed directory | `materialize.Engine.Reconcile` with a prior `materialize.Handle` | Refresh owned entries and managed JSON/TOML keys while preserving unowned files and keys. |
| Launch-ready process | `agentlaunch.PreparedExecution` | Carry exact argv/env/cwd, roots, materialization handle, provider projection and access requirements to wrapper/session runtimes. |
| Wrapper convenience | `wrapper.Config.PrepareRequest` or `wrapper.Config.PreparedExecution` | Let `go-agent-wrapper` either resolve shared preparation or run an already prepared execution without double planting. |

Use `materialize.Engine.Create` for new boot roots. Use reconcile/refresh only when the target directory is intentionally mixed ownership and the caller has a previous manifest/handle. Raw `artifact.Tree` callers bypass composition; authored recipes should not be forced through app-specific schemas.

## Ownership, refresh and deprecation notes

`artifact` entries carry stable paths, modes, ownership group IDs, provenance and digests. Reading source content does not grant the child process access to the source path; child access is controlled separately through `agentlaunch.AccessRequirements` and `go-sandbox` policies.

`materialize` owns filesystem creation, manifests, selected refresh, owned removal, and managed JSON/TOML key reconciliation. It preserves unowned content and reports conflicts for user-edited owned outputs unless the caller selects an explicit overwrite policy. It does not decide app retention policy, reload timing, skill revocation semantics, or provider credential sourcing.

Legacy flat bootdir APIs remain compatibility surfaces:

| Existing surface | Current status | Migration direction |
| --- | --- | --- |
| `agentlaunch.NativeFile`, `BootFileSpec`, `BootInjectionSpec`, `DefaultMaterializer` | Supported for existing flat-file launch flows. | Prefer `artifact.Tree` and shared materialization for tree-capable files, executable modes and binary content. |
| `agentlaunch.PreparedLaunch` | Supported as the legacy prepared-launch value. | Prefer `agentlaunch.PreparedExecution`; use `agentlaunch/sessionshim.ToSessionLaunchFromPreparedExecution` when bridging to sessions. |
| `agentlaunch/providerplant.Plant` | Delegates through shared artifacts/materialization and rewrites launch bindings from a single projection. | New callers should prefer `PrepareExecution` or wrapper `PrepareRequest` to avoid repeated planting. |
| `agentsessions.StartOptions.AutoPlantBootDir` | Still available for old session callers. | Disable redundant planting when `PreparedExecution` carries a materialization handle. |
| `go-runner.Config.Profile` and wrapper `SandboxProfile` | Runner keeps legacy profiles; wrapper ACP rejects legacy profile sandboxing. | Prefer resolved `go-sandbox.ResolvedAccessPolicy` through `SandboxPolicy`. |

## Provider projection versus explicit effects

`go-providers` pure projection produces provider-owned files and launch conventions without reading credentials, mutating `HOME`, trusting workspaces, or starting provider processes. Runtime effects are explicit values that the caller must approve and prepare at the execution edge.

| Provider family | Pure projection owns | Explicit effects remain outside projection |
| --- | --- | --- |
| Claude | Project-scoped instructions/settings/MCP files, native layout, argv/env/cwd conventions. | Workspace trust seeding, credentials, helper execution risk decisions. |
| Codex | `AGENTS.md`, `config.toml`, `.mcp.json` mirror, skill-package tree, `CODEX_HOME`/argv root conventions. | `auth.json` copy, real account credentials, hooks/custom subagents not represented in the current projection. |
| OpenCode | Instructions/config layout, mode-aware native files and launch conventions. | Provider auth and any runtime-specific credential resolution. |

Unsupported provider/runtime features return diagnostics. Consumers should not infer support from a file being present; use provider capability and projection results.

## Roots and required sandbox semantics

Prepared launch roots are independent values:

| Root | Meaning |
| --- | --- |
| `ProjectRoot` | User project/repo content the agent may work on. |
| `BootRoot` | Materialized provider/context tree. |
| `StateRoot` | Durable provider/session state controlled by the caller. |
| `ScratchRoot` | Session-private temporary write area. |
| `CWD` | Process working directory used at spawn. |

Do not infer any root from `cwd` after preparation. If required local confinement cannot be enforced, the runtime must fail before spawn or report an unsupported outcome. Remote or already-running ACP endpoints cannot be locally confined by the caller and must not report local enforcement.

Current capability limits:

| Runtime path | Supported behavior |
| --- | --- |
| Native macOS sessions | Resolved filesystem/network/subprocess policy is applied with Darwin backend before process start where supported. |
| Linux bwrap sessions | Resolved filesystem/network policy is applied before process start; loopback forwarding is child-to-host. |
| `go-runner` subprocess/restart | `SandboxPolicy` is applied before every initial start and supervisor restart; required setup failures prevent process events. |
| Agentkit sessions | `StartOptions.PreparedExecution` supplies exact bindings and a derived sandbox policy; explicit `SandboxPolicy` conflicts with prepared access policy. |
| Local ACP stdio | ACP adapters apply resolved local sandbox policy before bridge start and report configured/started/failed outcomes. |
| Local ACP TCP on Linux bwrap | Required private/loopback policy is rejected before spawn because parent-to-child TCP dialing cannot preserve private-network semantics. |
| Remote/pre-existing ACP endpoint | Required sandboxing is unsupported; disabled mode may be reported honestly as disabled. |

## Consumer mappings

These mappings are implementation handoff notes for downstream app sessions. They intentionally describe what each app should call later; this session did not edit app source.

| Consumer | Current app-shaped need | Shared API mapping | Adoption caution |
| --- | --- | --- | --- |
| Cairn | Compose profile/base/parts, copy install files/trees, update managed JSON/TOML install state, produce a fresh boot directory. | `agentcontext.AuthoredRecipe` with app-authored definitions; `artifact.FilesystemSource` for install trees; `materialize.MergeDocument` for managed JSON/TOML keys; `agentlaunch.ResolvePreparation` for boot roots. | Keep Cairn product schema and CLI behavior in Cairn. Do not import Cairn packages into shared libraries. |
| Nanite | Materialize hosted skill package/blob trees, preserve modes/empty dirs, refresh and remove owned stale skills. | `artifact.ImmutableRef` with a Nanite-owned content resolver; `materialize.Engine.Create/Reconcile` with ownership groups and selected refresh/removal. | Nanite decides revocation/reload policy and storage authorization; shared code only verifies immutable content and owned filesystem results. |
| Torque | Build generated task context, include copied task directories, carry task-scoped loopback and root requirements into session launch. | Generated `artifact.Tree`; `artifact.FilesystemSource` for copied directories; `agentlaunch.AccessRequirements` for task roots and loopback effects; `agentsessions.StartOptions.PreparedExecution` for runtime handoff. | Torque scheduling, task state, and control-plane loopback policy stay in Torque. |
| Tether | Prepare multiple task bundles/copied trees and provider overlays for launch. | Multiple artifact ownership groups plus `agentlaunch/providerplant.PrepareExecution` or `go-agent-wrapper/plant.SharedPlanter` depending on the host path. | Tether catalog, mux/task ownership and dirty local planning docs remain app-owned; adoption should happen in the Tether workstream. |
| Tachyon | Future adoption of shared launch/materialization semantics for its own agent flows. | Start from raw artifacts or authored recipes depending on Tachyon's product model; use `PreparedExecution` only after roots/access requirements are explicit. | Tachyon adoption is out of scope for this plan and requires its own source review. |

## Proposed release and dependency-bump sequence

No release execution is authorized by M17. The following is the coherent sequence to use when release authority is granted.

| Step | Module | Current local base tag | Proposed next tag | Depends on |
| --- | --- | --- | --- | --- |
| 1 | `github.com/hollis-labs/go-providers` | `v0.25.0` | `v0.26.0` | none |
| 2 | `github.com/hollis-labs/go-sandbox` | `v0.2.1` | `v0.3.0` | none |
| 3 | `github.com/hollis-labs/go-runner` | `v0.6.0` | `v0.7.0` | bump `go-sandbox` to `v0.3.0` |
| 4 | `github.com/hollis-labs/agentkit` | `v0.5.1` | `v0.6.0` | bump `go-providers` to `v0.26.0`, `go-sandbox` to `v0.3.0`, `go-runner` to `v0.7.0` |
| 5 | `github.com/hollis-labs/go-agent-wrapper` | `v0.9.1` | `v0.10.0` | bump `agentkit` to `v0.6.0`, `go-providers` to `v0.26.0`, `go-sandbox` to `v0.3.0`, and indirect `go-runner` to `v0.7.0` |

After each tag exists, update the dependent module's `require` versions and run that module's full test/tidy/vet/diff-check suite before tagging the next module. Do not commit temporary workspace wiring.

## Downstream acceptance checklist

Each app adoption workstream should verify the following against its own product behavior:

1. The app can produce raw artifacts, resolved composition, or authored recipes without exposing credentials to projection or materialization.
2. Files and arbitrary trees are represented together, with executable modes, binary bytes, empty dirs, ownership groups and provenance preserved.
3. Managed JSON/TOML changes are key-scoped and preserve unowned keys.
4. Refresh and removal only affect entries previously owned by the materialization manifest or explicitly authorized by the app.
5. Provider projection diagnostics are handled as product errors, not ignored.
6. `PreparedExecution` bindings are treated as exact: argv/env/cwd/roots are not recomputed by the runtime host.
7. Required local sandboxing either starts under an enforced backend or fails before process start; unsupported remote/TCP cases surface as unsupported.
8. App migration tests include real macOS/Linux evidence where the app claims OS enforcement.

## Validation record

M17 validation should use the temporary `GOWORK` file shown above for compile/test/vet across unreleased modules. Run `go mod tidy -diff` for `go-providers`, `go-sandbox`, `go-runner` and `agentkit`; defer wrapper tidy until after the proposed `agentkit v0.6.0` release and wrapper dependency bump. The plan's M16 artifact records the latest real macOS/Linux denial evidence, including Linux Docker runs for `go-sandbox`, `go-runner` and `go-agent-wrapper`.

Known local limitation: `agentkit/agentlaunch/parity` live catalog parity is excluded from broad validation because this machine's `~/.tether/catalog` contains pre-existing path drift unrelated to shared materialization. This does not affect the shared modules or synthetic conformance fixtures.
