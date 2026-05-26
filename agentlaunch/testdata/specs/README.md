# S4.4 — Specs + templates (re-expressed launches/ + boot-profiles/)

This directory is the **new, additive** parameterized re-expression of
the legacy `~/.tether/catalog/launches/` (64 files) and
`~/.tether/catalog/boot-profiles/` (~50 files) directories.

It does **not** replace or mutate the live catalog — the live catalog is
untouched. This is the parallel artifact set the S4.5 parity harness
loads to prove the new model reproduces the old one's intent.

## Layout

```
launch-assembly.yaml     The ONE canonical LaunchSpec. Every legacy
                         launch collapses to a bag handed to this spec.
                         Folds the boot-profile slots into vars+template.

templates/               Common-setup templates: the recurring
                         runner + isolation combinations, named once.
                         See templates/README.md.

launches/                One LaunchBag per concrete launch — the file
                         that replaces a single legacy launches/*.yaml.
```

## What collapsed

| Legacy | New model |
|---|---|
| 64 `launches/*.yaml` (2 modes x N runners x M projects) | 1 `LaunchSpec` + N `LaunchBag` files |
| `<launch>.worktree` TWIN file per launch | `isolation` input value — **twins deleted** |
| `<project>-<provider>` launch file per provider | `runner` input value (D3: provider feeds runtime-binding) |
| `boot-profiles/*.yaml` `slots:` block | `vars:` + merge-tag `template:` body |

## Minimum valid config

The minimum valid launch config is **two knobs**: `work_dir` + `runner`.
`isolation` and `bus` are the only other knobs and both default.
Everything else (project, agent role, prompt flags, lineage) is a
defaulted convenience input. `launches/tether-minimum.yaml` is the
smallest legal bag — work dir + runner, nothing else — and
`agentlaunch.ValidateMinimumConfig` enforces the rule.

## Loading

`agentlaunch.LoadLaunchSpec` and `agentlaunch.LoadLaunchBag` parse and
validate these files. `LaunchSpec.Render` (the S4.1 engine) renders a
bag into the boot prompt; `agentlaunch.ValidateMinimumConfig` checks a
bag against its spec. The S4.5 parity harness drives exactly these
entry points.

## Out of scope

`projects/` and `sandbox-profiles/` are deliberately NOT re-expressed
here — they have no registry kind, a known and deferred follow-up. Only
`launches/` + `boot-profiles/` are in S4.4 scope.

## Note on var sources

The `vars:` in `launch-assembly.yaml` use `literal` sources so the spec
validates and renders standalone in tests. A live deployment swaps those
for the `cmd` / `call` sources the legacy boot-profile slots used
(`git log`, the recall endpoint, the skill index) — the var names and
the template wiring are unchanged, which is the S4.2 contract.
