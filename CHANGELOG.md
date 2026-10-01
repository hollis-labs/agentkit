# Changelog

All notable changes to agentkit are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.21.2 — 2026-10-01

### Fixed

- **A serve-http (OpenCode) session no longer grows two maps for the life
  of the session** (CW-20261001-0224). The ids of reasoning parts (v0.20.6)
  and of compaction summary messages (v0.20.5) were kept forever, though
  each is unique and matters only for the deltas of its own turn. They are
  now forgotten when the next turn starts, so a session keeps one turn's
  worth.
  - They are cleared when the next turn starts, not when one ends, so a
    delta that follows its turn's ending event (OpenCode sends late events
    after an abort) is still told from the reply. A delta that arrives only
    after the next prompt was sent is the one case this does not cover.
  - Tested over 60 turns with fresh ids each: every turn's thinking, held
    back summary and reply are still told apart, and the maps hold one
    turn's ids at the end. Clearing at turn end, clearing only one of the
    two maps, and not clearing each fail a test.

## v0.21.1 — 2026-10-01

### Fixed

- **A streaming-stdio session reports a lost provider session**
  (CW-20261001-0222). v0.20.2's session-lost handling covered only the
  per-turn runtime. A Claude streaming session started to resume an id
  Claude no longer has sees the child write "No conversation found with
  session ID" to stderr and exit 1; the only output on the stream was an
  error result with an empty message, so nothing said the session was lost
  and go-agent-wrapper's `session.lost` never fired for the runtime every
  Torque Claude profile uses.
  - A resume attempt of an adapter that implements
    `provider.SessionLostClassifier` now keeps a bounded tail of its stderr
    (through a pipe the session owns; the caller's `StartOptions.Stderr`, or
    the session log, still receives every byte the child wrote before it
    exited, except that a descendant that keeps stderr open past the exit is
    cut off after the same one-second drain as stdout, and the stderr and
    stdout drains run one after the other). When the attempt exits abnormally
    the tail is classified. A clean exit, an adapter with no classifier, and
    an attempt that requested no resume id are never classified and behave as
    before.
  - **Only an attempt that never got going is classified.** A healthy
    resume emits its init (a session id) first, and its stderr can mention
    the same words, for instance in a tool's error line, so an attempt that
    produced a session id or a finished turn is not a failed resume, however
    late it later exits. Neither is an attempt the session or its supervisor
    ended: `Stop`, a canceled ctx, or an idle or watchdog kill. Their
    abnormal exits keep the ids, announce nothing, and a supervisor still
    restarts a healthy session that crashes.
  - On a loss the session emits `events.SessionLost` through
    `TypedEventCallback` and the `[session_lost]` Fanout marker, once, with
    the per-turn runtime's reason, after the child's final output. The dead
    id is dropped from `ProviderSessionID`. `OnProviderSessionLost` is not
    called, as for a failed per-turn resume.
  - `SendInput` then fails with `*SessionLostError`, which still matches
    `ErrNoInputChannel`. A `SendInput` whose write fails because the child
    just died (a broken or closed pipe) waits up to three seconds for the
    classification and returns the same error, wrapping both the write error
    and `ErrNoInputChannel`, instead of a broken pipe. The wait ends early
    with the caller's ctx and with `Stop`, and is skipped for any other write
    error.
  - The loss is decided right after the child exits and its stderr is
    drained, before stdout is drained, and announced after the stdout drain.
    So a `SendInput` made from a reader callback, which the stdout drain waits
    for, sees the loss at once, rather than waiting for a classification that
    is waiting for it. The cost: a `SendInput` can return `*SessionLostError`
    a moment before the event is announced, by as long as the stdout drain
    takes. The event is out before `Wait` returns.
  - A lost session is not restarted by the supervisor: the restart would
    resume the same dead id. Without this, `RestartOnCrash` would have
    respawned it until exhausted.
  - `Wait`'s error is unchanged (still the `*ExitError`).
  - Tested by replaying go-providers' live `claude/stream_resume_unknown_id`
    capture through the real Claude streaming adapter, with and without a
    supervisor, plus stand-in CLIs: one that stops reading stdin so the write
    fails; a healthy resume that later exits 1 (unsupervised, and restarted by
    a supervisor); a `Stop`; a crash of a resumed session on unrelated stderr,
    which restarts twice as before; and a `SendInput` from a reader callback
    after the child died. The adapter without a classifier, a run with no
    resume id, and a clean exit stay silent.

## v0.21.0 — 2026-10-01

`StartOptions.ExtraArgs` go where the adapter's convention takes them
(CW-20261001-0197). Takes go-providers v0.42.0.

### Changed

- **The adapter path (no `Launch`) places `StartOptions.ExtraArgs` through
  `provider.ExtraArgsBuilder`,** at the launch convention's extra-argument
  slot, where a `Launch` template already put them. Every go-providers
  adapter implements it from v0.42.0.
  - Before, they were spliced into `BuildArgs`' output just before "--".
    For a codex exec resume turn that put them after `resume <id>`, where
    codex refuses exec-only flags (`-s`, `--cd`, `--add-dir`), so such an
    extra broke turn 2. v0.20.4's documented "must be resume-safe"
    constraint is retired.
  - Other effects: the slot can sit earlier than "--". For example, Claude
    print puts extras before `--add-dir <project>`. go-providers'
    convention tests check that the slot never makes an extra swallow the
    prompt or join a variadic flag's values.
  - **Unchanged:**
    - an adapter without the interface still gets the splice;
    - so does a caller-supplied `AdapterRuntimeConfig.BuildArgs`;
    - extras that carry their own "--" are still appended.
  - **Tested:**
    - `TestAdapterArgsPrefersTheConventionSlot` covers the builder, the
      fallback and the "--" tail.
    - `TestSubprocessSession_ExtraArgsWithTheirOwnDashDashAreAppendedAsBefore`
      runs a session with an extra that carries its own "--" and checks the
      argv is `BuildArgs`' output followed by the extras unchanged.
    - `TestAutoPlantedCodexExecResumesWithCdBeforeResume` now includes an
      exec-only `-s read-only` that lands in front of `resume`.
    - With the builder disabled the last two fail; with the "--" guard
      removed the first two do.

## v0.20.6 — 2026-10-01

### Fixed

- **OpenCode serve: reasoning reaches the turn as thought, not reply text**
  (CW-20261001-0209). OpenCode streams a reasoning part's text as the same
  `message.part.delta` event as the reply's. Only the earlier
  `message.part.updated`, with `part.type: "reasoning"`, tells them apart.
  The serve-http runtime forwarded both as unclassified `EventDelta`s, so a
  reasoning model's thinking was spliced into the reply.
  - The runtime now records reasoning part ids. It emits their deltas with
    `Phase: llmtypes.PhaseThinking` (`"thought"`) and `BlockID` set to the
    part id, the same classification go-providers' `opencode run` parser
    gives reasoning.
  - Text parts, deltas with no part announcement, and deltas with no
    `partID` are unchanged.
  - The compaction summary's reasoning is still held back entirely
    (v0.20.5).
  - The event shapes come from the same live capture of OpenCode 1.18.33.

## v0.20.5 — 2026-10-01

### Fixed

- **OpenCode serve: the compaction summary no longer reaches the turn as
  reply text** (CW-20261001-0198). When OpenCode compacts a session, either
  after a `ContextOverflowError` or because it was asked to, it streams the
  summary as ordinary `message.part.delta` events for that same session.
  The serve-http runtime forwarded every one as an `EventDelta`, so the
  summary was spliced into the reply between the text before the overflow
  and the text after it.
  - The runtime now records the compaction message from its
    `message.updated` (`mode`/`agent` `"compaction"`, `summary: true`). It
    drops the deltas whose `messageID` belongs to that message, including
    the summary's reasoning part.
  - Those events still appear in the raw event stream (session log,
    `Fanout`).
  - A user message's object-valued `summary` (`{"diffs": [...]}`) does not
    mark anything. Deltas with no `messageID` pass through.
  - The event shapes come from a live capture of OpenCode 1.18.33
    compacting a session.

## v0.20.4 — 2026-10-01

A coherent dependency refresh, and the fix that makes go-providers v0.41.0
safe to pair with agentkit (CW-20261001-0194).

### Fixed

- **go-providers v0.41.0 with agentkit v0.20.3 or earlier breaks codex exec
  turn 2 under `AutoPlantBootDir`.**
  - go-providers v0.41.0 resumes a codex exec thread with
    `exec … --cd <project> resume <id> -- <prompt>`, and agentkit passes
    the session id back on every later turn.
  - Auto-planting spliced codex's `--cd <project>` in through `ExtraArgs`,
    just before "--", so turn 2 became
    `exec … resume <id> --cd <project> -- <prompt>`.
  - codex-cli 0.159.2 refuses that with `error: unexpected argument '--cd'
    found`. Before go-providers v0.41.0, turn 2 started a new thread and
    worked.
  - Planting now gives a codex exec adapter the project in a per-session
    clone's `ProjectDir`, as it already did for Claude's bare mode. go-providers
    then places `--cd` in front of `resume`. Turn 1's argv is byte-identical.
    With caller `ExtraArgs` present, `--cd` now precedes them instead of
    following them.
  - Tested by `TestAutoPlantedCodexExecResumesWithCdBeforeResume`: two
    auto-planted codex exec turns on go-providers' captures. It checks
    turn 2's argv against what codex accepts after `resume <id>`, which the
    fake does not check. Without the fix it fails.

### Changed

- **Dependencies:**

  | Module | From | To |
  |---|---|---|
  | go-providers | v0.40.0 | v0.41.0 (codex exec resume, `CodexAdapter.IsSessionLost`) |
  | go-sandbox | v0.5.0 | v0.6.0 (`DenyUserServiceManager`, v0.5.1's host-filesystem argv[0] fix) |
  | go-runner | v0.7.0 | v0.8.2 (resource-limit and long-line fixes) |
  | go-llm-contracts | v0.3.0 | v0.4.0 (adds `contracttest`; root API unchanged) |

  go-llm-types stays at v0.5.1, the latest.
- **Codex exec sessions resume their thread from turn 2,** and report the
  thread id as the provider session id. Before, each turn started a new
  thread.

### Documented

- **`StartOptions.ExtraArgs` on a codex exec session without `Launch`** land
  after `resume <id>` from turn 2 on, so they must be flags codex accepts
  there: `-c`, `-m`, `--dangerously-bypass-approvals-and-sandbox`.
  Exec-only flags (`-s`, `--cd`, `--add-dir`) belong in the
  `CodexAdapter`'s own `ExtraArgs` or a `Launch` template. The test above
  pins this ordering. CW-20261001-0197 tracks placing caller extras at the
  convention's slot.

## v0.20.3 — 2026-10-01

### Fixed

- **A serve-http (OpenCode) turn no longer fails on an error that does not
  end it** (CW-20261001-0193).
  - Before: any `session.error` failed the turn in flight, including one with
    no `sessionID` and a `ContextOverflowError` that OpenCode goes on to
    compact. The turn reported an error while OpenCode carried on with it.
  - A context overflow is now held until OpenCode either compacts
    (`session.compacted`, and the turn carries on to its own end) or goes
    idle without compacting. In the second case the overflow is the turn's
    error. This follows `SessionProcessor.halt` in OpenCode 1.18.33: an
    overflow with auto-compaction on publishes the error without going
    idle, then compacts; one with compaction off, or that cannot be
    compacted, publishes the error and goes idle.
  - An error with no `sessionID` (for example, a plugin that failed to load)
    is logged as a diagnostic and never fails a turn. No other turn-ending
    event without a `sessionID` ends a turn either.
  - Every other `session.error` for this session still ends the turn, once.
  - Tested by replaying OpenCode's own event shapes: overflow, then
    compaction, then a reply; overflow, then idle; a sessionless plugin
    error mid-turn; and an `APIError`.

## v0.20.2 — 2026-10-01

### Fixed

- **A per-turn resume that fails because the provider lost the session now
  says so in the event stream** (CW-20261001-0184).
  - Before: `SendInput` returned `*SessionLostError`, but the turn's events
    carried only the provider's own terminal error. For real Claude that is
    an `error_during_execution` result with an empty message, so a consumer
    of the event stream could not tell the session was lost.
    go-agent-wrapper's `session.lost` never fired for Claude or OpenCode.
  - Now the turn also emits the typed `events.SessionLost` (`RequestedID`
    set, no `ActualID`) and the Fanout `[session_lost]` marker, once per
    turn.
  - Ordering: these follow the provider's terminal event, because the stderr
    that classifies the turn is complete only after the process exits.
  - `OnProviderSessionLost` stays as it was. It reports a turn that ran on in
    a new session, which is Antigravity's case.
  - Tested by replaying go-providers' live `claude/print_resume_unknown_id`
    capture through the real `ClaudeAdapter`.

## v0.20.1 — 2026-10-01

Follow-up to v0.20.0 (CW-20261001-0160).

### Fixed

- **The jsonrpc-stdio session's own request ids start at 2^32**
  (`callIDBase`). Hosts can drive Codex app-server with raw frames through
  `SendInput`, numbered from 1, and the wrapper's Codex hosts do. Until
  v0.20.0 the session made no requests of its own beside those frames.
  `InterruptTurn`'s `turn/interrupt` is one, and with ids from 1 it could
  reuse an id a host request still had in flight, so the two responses
  would be confused. The Codex interrupt test checks the id it sends.

## v0.20.0 — 2026-10-01

Turn interrupts for Codex app-server and OpenCode serve (CW-20261001-0160).
Requires go-providers v0.40.0.

### Added

- **The jsonrpc-stdio session implements `TurnInterrupter`** for an adapter
  with `provider.RPCTurnInterrupter`, which today is Codex app-server.
  - The session follows the open turn through `turn/started` and
    `turn/completed`. `InterruptTurn` sends `turn/interrupt` for that turn and
    returns on Codex's `{}`.
  - The turn then completes with `status: "interrupted"`, and the next
    `turn/start` runs on the same process and thread.
  - With no turn open it sends nothing. A turn that ends while the request is
    in flight is not an error.
- **The serve-http session implements `TurnInterrupter`** with OpenCode's
  `POST /session/{id}/abort`. The turn ends with one error event, and the
  server and session stay up for the next `SendInput`.

### Fixed

- **serve-http: an idle or error with no turn in flight no longer ends a
  turn.**
  - OpenCode follows a `session.error` with a `session.idle`. The session
    reported both, so one failed turn looked like a failure followed by a
    completion.
  - After an abort, OpenCode sends a late second `session.idle` as the aborted
    tool cleans up. That idle could end the next turn before its reply. A
    turn after an abort now ends only once OpenCode has reported it `busy`.

### Tested

- Against go-providers' live captures, `codex/app_server_interrupt` and
  `opencode/serve_abort`. The OpenCode test also replays the abort's cleanup
  late, after the next prompt, as it arrived live.

## v0.19.1 — 2026-10-01

### Fixed

- **A session's log is opened for append and never truncated**
  (CW-20261001-0158).
  - The serve-http, streaming-stdio, jsonrpc-stdio and pty runtimes opened
    `StartOptions.LogPath` (or `<WorkspaceDir>/logs/session.log`) with
    `os.Create`. That truncated the file at Start and then wrote from its own
    offset.
  - A host keeping its own `O_APPEND` writer on the same file (Torque tees
    its first-turn error capture, redacted stderr and permission-decision
    lines into it) had those lines overwritten.
  - The log is now opened `O_APPEND|O_CREATE|O_WRONLY`, with os.Create's
    mode, so every write, the child's stderr included, lands at the end.
- **Decision: a fresh session does not truncate either.** agentkit cannot
  know whether the host has already written to the file, and truncating
  would lose exactly those lines. A host that wants a fresh log per session
  passes a fresh path, or truncates it itself before Start. A resumed or
  supervised-restarted session keeps appending.
  - **Behaviour change:** a reused `LogPath` now accumulates across
    sessions instead of starting empty.
- **Tested:** `TestSessionLog_HostAppendWriterSurvivesSession` writes a line
  before Start, then runs 300 host appends concurrently with 30
  streaming-stdio turns. Every line must survive. With `os.Create` it lost
  41, including the pre-Start line.

## v0.19.0 — 2026-10-01

Hosts can write-protect their control-plane state from the agents they
launch (CW-20260930-0237). Requires go-sandbox v0.5.0.

### Security

- **`StartOptions.ProtectedPaths`** lists absolute paths the agent must
  never write: a host's state dirs, database, config, catalog or
  allow-lists. An agent running as the operator's uid could otherwise
  rewrite them to grant itself authority.
- **Composition.** It is folded into the one sandbox that wraps the child,
  at the same point as `DenyGUILaunch` and just before it:
  - onto an existing `SandboxPolicy`, via go-sandbox `WithProtected`, or
    onto a `Profile` (`FS.Protect`). Both are copies; the caller's values
    are not mutated.
  - with no sandbox, or a disabled `SandboxPolicy`, as a minimal
    host-filesystem profile (`protect-control-plane`) whose only effect is
    the protection.
- **Fail closed.** A backend that cannot write-protect, or a
  provider-native runtime, refuses the launch with
  `ErrProtectedPathsUnsupported` instead of running with the control plane
  writable. A relative path is refused.
- **Tested end to end.** An adapter turn whose CLI tries to rewrite an
  allow-list in a registered dir, and to plant a file there, is refused
  while its workdir write lands. Without the composition the same test
  shows the allow-list rewritten.
- **Boundary.** It is go-sandbox's: Protect stops direct writes in every
  mode. Against writes delegated to another process (for example
  `systemd-run --user` over the user bus), it holds only under a narrowed
  or resolved policy that doesn't mount those sockets. The minimal
  host-filesystem profile is not a boundary against delegation.
- **Registration.** Register state directories, by their real path, that
  exist before launch. go-sandbox refuses:
  - a file, because its directory stays writable, so an atomic save or a
    database sidecar would defeat it;
  - a path through a symlink the agent could re-point;
  - on Linux, a missing path the agent could create.

## v0.18.0 — 2026-10-01

A cooperative turn interrupt for streaming-stdio Claude (CW-20261001-0103).
Requires go-providers v0.39.0.

### Added

- **`agentsessions.TurnInterrupter`**, an optional Session interface:
  `InterruptTurn(ctx)` ends the turn in flight and keeps the process. The
  streaming-stdio session implements it for an adapter with
  `provider.TurnInterrupter` (Claude), as follows:
  - It writes Claude's stream-json `control_request` interrupt on stdin, and
    returns when Claude's `control_response` answers it, or when ctx ends.
  - The interrupted turn ends on the event stream as Claude ends it: an error
    result whose `terminal_reason` is `aborted_tools` (a tool was running) or
    `aborted_streaming` (the model was generating).
  - The next `SendInput` runs a normal turn on the same process, so in-process
    state survives and there is no `--resume` cold start.
  - With no turn in flight, Claude acknowledges and nothing else happens.
  - Errors: `ErrInterruptUnsupported` for an adapter without an interrupt,
    `provider.ErrInterruptRefused` for Claude's refusal, and
    `ErrInterruptUnanswered` when the session's output ends first.
- Tested against go-providers' live `claude/stream_interrupt` capture
  (claude 2.1.286).

## v0.17.0 — 2026-10-01

The permission posture is go-permission's Mode, mapped per provider by the
go-providers registry (CW-20260930-0138, D-72). Requires go-providers v0.37.0.

### Changed

- **`LaunchPlan.Provider.Permission` and `RuntimeBinding.Permission` are a
  `permission.Mode`** (`default`, `accept-edits`, `plan`, `yolo`), the same
  for every provider. Previously each was a string in the provider's own
  vocabulary (`acceptEdits`, `bypassPermissions`, `on-request`, ...). A
  provider spelling is now refused: `Compile` returns `ErrInvalidPermission`,
  and `PrepareExecution` returns go-providers' `registry.ErrInvalidPosture`
  for one set after `Compile`.

  Claude's spellings map directly: `acceptEdits` is now `accept-edits`, and
  `bypassPermissions` is now `yolo`. Codex's approval policies have no
  one-to-one Mode, because a Mode sets both Codex's sandbox and its approval
  policy. To keep the old headless default (`never` / `workspace-write`),
  set no Permission at all.

- **`PrepareExecution` maps an explicit posture through the registry's
  Posture hook** (`registry.Descriptor.PostureFor`), whichever resolver
  supplied the adapter. The posture's flags lead the launch's extra
  arguments, before `Provider.Flags` and `Injection.Args`, so the first turn
  and every later turn carry them at the convention's extra-argument slot,
  before `--`. Its environment variables, such as OpenCode's
  `OPENCODE_PERMISSION`, are set over the launch's, with source `posture`.
  The per-provider mapping, as measured, is in go-providers v0.37.0's
  CHANGELOG.
- **`PreparedExecution.Posture`** records the Mode the bindings were mapped
  from (empty when none), so a host that also answers the agent's approval
  requests, such as the wrapper's `CodexApprovalResponder`, can answer from
  the same Mode.
- **`DefaultResolver` no longer sets `ClaudeAdapter.PermissionMode` or
  `CodexAdapter.ApprovalPolicy`.** The posture travels as launch flags
  instead. Claude's `--permission-mode` overrides a planted
  `permissions.defaultMode`, and Codex's `-c` overrides the planted
  config.toml.
- `ErrHeadlessClaudeNeedsPermission`'s message names the Modes.

### Unchanged

- **An empty `Permission` sets no posture.** The launch carries exactly the
  argv and environment it did in v0.16.0, so Codex keeps go-providers'
  `never` / `workspace-write` default and OpenCode gets no
  `OPENCODE_PERMISSION`. `TestPrepareExecution_EmptyPostureMatchesV0_16_0`
  compares claude, codex, opencode and antigravity launches in each native
  mode against a golden generated from v0.16.0.
- ACP launches have no boot dir and do not reach `PrepareExecution`. Their
  posture is the ACP permission responder's, best effort.

## v0.16.0 — 2026-10-01

### Security

- **`turn.CodexApprovalResponder.MCPAllow`** limits which MCP tool calls the
  `default` and `accept-edits` postures approve (CW-20261001-0084).
  - **Before:** both postures approved every MCP tool call from every
    planted server. A host planting the mux server therefore let a worker
    call `cerberus_ssh_exec` or `cerberus_docker_destroy` with nothing
    asking. Torque carved out its own rule to prevent that.
  - **Entries:** each is `server` (every tool on it) or `server/tool`.
    Both halves are `path.Match` patterns, for example
    `mux/torque_*`.
  - **Behaviour:**
    - With entries, a call that matches none is declined. The decline
      reason names the server, the tool and the allow-list.
    - `plan` still declines every call, and `yolo` still approves every
      call.
    - Validate rejects malformed entries.
  - **Names, as Codex sends them:**
    - The server is the elicitation's `serverName`.
    - The tool is `_meta.tool_name` when Codex sends it. codex-cli 0.154.0
      to 0.159.2 doesn't, so otherwise it is the name quoted in the
      approval message (`Allow the <server> MCP server to run tool
      "<tool>"?`).
    - Verified against a live 0.159.2 capture: the message quotes the
      tool's name even when the tool declares a title.
    - When the tool name can't be read, only `server` entries match.
  - `CodexApprovalOutcome` gains `MCPServer`, `MCPTool` and
    `MCPAllowEntry` for the `agent.permission.resolved` payload.
  - **No change for existing hosts:** with no entries every MCP tool call
    is still approved as before. Opting in is per host. The fail-closed
    default was weighed and not taken; the PR gives the reasons.

## v0.15.0 — 2026-10-01

MCP servers flow from the launch plan into every runtime's boot dir
(CW-20260930-0136, W4a, W4c and item 1). Requires go-providers v0.36.0.

### Added

- **`MCPServerSpec.URL`**: a streamable-HTTP MCP server, beside the stdio
  `Command`/`Args`/`Env`. Set exactly one; go-providers rejects neither or
  both when it renders the plant.
- `PreparedPlantContext.MCPServers`.

### Changed

- **The launcher carries `MCPSpec` into the plant.** `launcher.Prepare` copies
  `MCPSpec.LoopbackURL` and `MCPSpec.Servers` into `PreparedPlantContext`,
  where it set only `AgentName`. `providerplant.PlantContextFor` maps them
  onto go-providers' `PlantContext` (`URL` → `HTTPURL`, env flattened and
  sorted). The go-providers renderers then plant them, in each CLI's form,
  into claude's `.mcp.json`, codex's `config.toml`, opencode's
  `opencode.json` and antigravity's `.agents/plugins/tether/mcp_config.json`.
  Apps no longer hand-set the plant's MCP fields or render MCP config
  themselves.
- A launch without `MCPSpec.Servers` plants byte-identical config.
## v0.14.2 — 2026-10-01

`ExtraArgs` no longer become prompt text (CW-20261001-0102). The same fix
ships for the v0.12 line as v0.12.3.

### Fixed

- **Sessions: `StartOptions.ExtraArgs` precede `--`.** Since go-providers
  v0.34.1 an adapter's argv with a prompt ends in `-- <prompt>`, as on
  `claude -p`, `codex exec` and `opencode run` turns. Without a launch
  template, a caller's `ExtraArgs`, and `AutoPlantBootDir`'s project-dir
  argument (Claude's `--add-dir`), were appended after it, so the agent
  received them as prompt text. They now go immediately before the first `--`.
  An argv without a `--` is unchanged, and an `ExtraArgs` that carries its own
  `--` is appended as before. The launch template path already placed them
  correctly.

### Tests

- A regression guard: prepared launches keep `Provider.Flags` and
  `Injection.Args` before `--`, both in the planted argv and in every
  later turn's argv. agentkit v0.12.x got this wrong. v0.12.3 fixes it on
  that line.

## v0.14.1 — 2026-10-01

A child output line over 1 MiB no longer stops a session (CW-20261001-0086).

### Fixed

- **Session readers survive long lines.** Every runtime (jsonrpc-stdio,
  streaming-stdio, PTY, serve-http) read child stdout through a
  `bufio.Scanner` with a 1 MiB cap. A longer line, such as a Codex
  `item/completed` carrying a command's whole output or a Claude `tool_result`
  carrying a file, ended the reader. Nothing drained stdout after that: the
  child blocked on the full pipe while the session still reported alive, and
  every later `Call` waited out its deadline (torque#149's lost steering).
  Lines up to 64 MiB are now routed whole. A longer line is read through,
  skipped and noted in the process log and the session log, and reading
  carries on.
- **A reader failure is no longer silent.** When the jsonrpc-stdio or
  streaming-stdio reader stops on a real read error (not EOF or a closed read
  end), the session records the fault: `Health` reports it not alive, and
  `Call` and `SendInput` fail at once with the reader's error. The pipe keeps
  draining so the child cannot block. A jsonrpc `Call` made after the reader
  has stopped also fails at once, instead of registering a response nobody
  will read.

## v0.14.0 — 2026-10-01

Event vocabulary on the subprocess path (CW-20260930-0137; CW-20260930-0222
L2/L3 decided). Requires go-providers v0.35.0 and go-llm-types v0.5.1, bumped
together.

### Added

- **`events.AuthFailed`.** When the adapter's `AuthFailureClassifier`
  recognises a sign-in failure, the turn now also emits a typed
  `events.AuthFailed` to `TypedEventCallback` and an `[auth_failed]` marker on
  the byte Fanout. Previously the turn's error was the only signal. The
  error still wraps `provider.ErrProviderNotAuthenticated`.

### Changed

- **Typed events are on by default on the subprocess path.** An adapter that
  implements `provider.EventParser` is always tapped, whether or not
  `TypedEventCallback` is set. Without a callback, typed-only events still
  reach the byte Fanout; for example the `[permission_denied:...]` marker,
  which used to appear only when a callback was set.
- Requires go-providers v0.35.0 (block ids, normalised stop reasons,
  per-event `CostUSD`, `events.AuthFailed`) and go-llm-types v0.5.1 (was
  v0.34.1 / v0.3.0).

### Decided, not built

- No agentkit-level per-turn usage total (CW-20260930-0222 L2). Done cheaply
  it would cover only the subprocess path. go-agent-wrapper already carries
  each turn's accumulated usage and cost on its one terminal event, and
  consumers moving onto the wrapper (D-71) get it there.
- Usage stays off the byte Fanout (L3): the typed path carries it.
## v0.13.0 — 2026-10-01

One argv owner, agentkit half (CW-20260930-0135). A prepared launch now
resolves every turn's argv from the provider's launch convention instead of
reusing the first turn's: each turn carries its own prompt and resume id, the
projected argv is no longer appended a second time after the adapter's
BuildArgs, and streaming-stdio Claude gets its boot prompt on stdin. These were
the argv failures of the 2026-10-01 smoke test (CW-20261001-0015): Claude
streaming launches lost their boot prompt, and Codex ran
`app-server app-server`. Requires go-providers v0.34.1 (was v0.32.0), whose
conventions put a positional prompt last after `--` (CW-20261001-0069), and
go-sandbox v0.4.1 (was v0.4.0).

### Added

- `agentlaunch.TurnTemplate`: a provider `LaunchConvention` bound to its
  launch roots, plus the launch's own flags. `TurnArgv(provider.TurnInput,
  extra...)` resolves one turn.
- `ExecutionBindings.Launch` and `PreparedLaunch.Launch` carry the template
  (providerplant sets both for every provider with a projection), and
  `PreparedExecution.Boot` (`BootDelivery`) carries the boot mode, prompt and
  content, so `ToSessionLaunchFromPreparedExecution` hands them on.
- `agentsessions.StartOptions.Launch`. When it is set (the shims set it, and
  Start copies `PreparedExecution.Bindings.Launch` into it), every spawn's
  argv is the template resolved for that turn, with `ExtraArgs` at the
  convention's extra-argument slot. The adapter still supplies the binary and
  parsing, but its `BuildArgs` is not called. Start keeps its own deep copy
  (`TurnTemplate.Clone`), so editing the caller's template afterwards does not
  change later turns.
- `agentsessions.DeliverStreamingBoot` and `agentsessions.ClaudeStreamingUserFrame`.
  `turn.ClaudeStreamingUserFrame` now delegates to the latter.
- Start takes the boot fields from `PreparedExecution.Boot` when the caller
  set none, and delivers a streaming-stdio launch's boot prompt as its first
  turn. A consumer that passes `PreparedExecution` straight to Start, as
  go-agent-wrapper does, gets the boot turn without the shim.

### Changed

- **Breaking:** with a launch template, `ExecutionBindings.Argv` and
  `PreparedLaunch.Argv` are only the first turn's argv, for display and
  compatibility. Editing them no longer changes what runs. To add flags to
  every turn, append to `Launch.ExtraArgs`, or set the adapter's own fields.
  `PreparedExecution` no longer puts `Bindings.Argv[1:]` into
  `StartOptions.ExtraArgs`, and the shims no longer copy `Argv[1:]` into
  `ExtraArgs` when there is a template; a legacy BootDirSpec provider keeps
  the old behaviour.
  - Tether must delete `sharedExtraArgs` in the same bump. It re-extracts
    root-bound args from `Argv[1:]`, which the template already contains, so
    they would appear twice (Claude `--mcp-config`/`--add-dir`, codex exec
    `--cd P --cd P`).
  - Torque splices `--settings` into `Bindings.Argv` (runtime/agent/boot.go
    around line 358), and splices its profile args and `--model` into it on
    the wrapper path (around line 1271). Those edits no longer reach the
    spawn and must move to `Launch.ExtraArgs` or adapter fields.
  - Torque's `PrepareExecution`-then-`ToSessionLaunch(prepared)` path leaves
    `PreparedLaunch.Launch` nil (only `Plant` sets it), so that path keeps the
    old composition until Torque adopts.
- `AdapterRuntimeConfig.BuildArgs` together with a launch template is an
  error at Start: the template owns every turn's argv, and the override would
  have dropped every projected flag.
- A jsonrpc-stdio session with a launch template and `BootMode: stdin` no
  longer writes the raw boot prompt into the child's JSON-RPC stdin; it logs
  why and skips it.
- `providerplant.DefaultResolver` builds each mode's own adapter through
  go-providers' `provider.NewAdapter`. Streaming-stdio Claude now projects
  `-p --input-format stream-json …` rather than print mode's
  `-p <boot prompt> …`, and the TUI no print flags. Posture is applied as
  before: Claude `PermissionMode`, Codex `ApprovalPolicy`, OpenCode `Agent`.
- A launch's `Provider.Flags` and `Injection.Args` go at the convention's
  extra-argument slot. For Claude that is before the variadic `--add-dir`,
  with the prompt last after `--`. They are no longer appended after the
  projected argv.
  `ErrPositionalAfterProjection` still refuses a leading positional.
- The shims deliver a streaming-stdio launch's boot prompt (`BootContent`,
  else `BootPrompt`) as the auto-fired first turn, framed as a stream-json user
  message, and clear `BootMode`/`BootPrompt` so the session does not also
  write it unframed. `BootMode: none` opts out. This replaces Tether's
  `streamingStdioBootPromptFirstTurn` workaround.

### Known behaviour

- A per-turn runtime delivers the boot prompt only if the consumer sends the
  kickoff as its first turn: the template's argv carries each turn's own
  prompt.
- Streaming-stdio fires the boot turn on a resumed session too
  (`SessionIDPreset` is set after the shim runs).
- A PTY launch with a planted boot mode sends no boot turn.

## v0.12.2 — 2026-10-01

Patch (CW-20260930-0134, deferred from the agentkit#7 review).

### Fixed

- `turn.Frame` and `turn.SendTurn` frame the ACP modes (`acp-stdio`,
  `acp-tcp`) as plain text, the input go-agent-wrapper's ACP client wraps in
  `session/prompt`. Copilot and Pi resolve to `acp-stdio` by default
  (`runtimebind`), so their turns returned `ErrUnsupportedRuntime` instead.

## v0.12.1 — 2026-10-01

Patch: serve-http no longer corrupts multi-line SSE event data
(CW-20260930-0052).

### Fixed

- **serve-http joins multi-line `data:` fields with a newline.** The `/event`
  reader concatenated an event's `data:` lines with no separator, so
  `data: a` + `data: b` arrived as `ab` instead of `a\nb`, silently. It now
  follows the WHATWG event-stream rules for the data field, as go-ssekit's
  `Read` does: lines join with `\n`, an empty `data:` line or a bare `data`
  field keeps its newline, other fields and comments are ignored, an event with
  empty data is not dispatched, and an event the stream ends before terminating
  is dropped.

## v0.12.0 — 2026-10-01

Minor, breaking (pre-1.0). Pairs with go-providers v0.32.0 and adds a
dependency on agent-contracts-leaf v0.3.0. agentkit now reads its runtimes
from the go-providers registry (CW-20260930-0133, EP-20260930-0001): a runtime
added there resolves here with no agentkit edit; planting a new native
runtime's boot dir still needs a `DefaultResolver` constructor case.

### Changed

- **Breaking:** `agentlaunch.RuntimeKind` and its constants are gone; every
  runtime field is an agent-contracts-leaf `runtimes.Mode` (D-73, no aliases
  per D-22). `LaunchPlan.Runtime`, `RuntimeBinding.RuntimeKind` (field and
  `runtime_kind` key kept), `PreparedExecution`, `ProviderProjection`,
  `CapabilityDiagnostic`, `turn.Options.Runtime`, `bootdir` and
  `runtimebind` carry the leaf spellings: `subprocess` is
  `subprocess-per-turn`, `serve-http` is `http-sse`, and ACP is a mode
  (`acp-stdio`, `acp-tcp`) rather than a side path.
- **Breaking:** `runtimebind` resolves through the registry.
  - Ids and aliases come from the registry. The `claude*`/`antigravity*`
    prefix matching is gone; `claude-code` and `agy` are registry aliases.
  - The default mode is the descriptor's. **Codex now defaults to
    `jsonrpc-stdio` (app-server)**, matching go-agent-wrapper (D-74); it
    was `subprocess`. The debug posture still prefers a runtime's PTY.
  - Supported modes come from the registry.
  - An API provider is a `Binding` with `API: true` and no mode, not a
    runtime kind. The registry is consulted first, so a runtime whose id or
    alias contains "openai"/"anthropic" stays a runtime.
  - `Request.Overrides` is keyed by `runtimes.ID`.
- **Behavior change for Codex hosts (D-74 default flip):**
  - A host that resolves Codex's *default* mode and then calls
    `ResolveCodexPolicy` with `Bypass: true` (or uses the debug posture) now
    gets `danger-full-access` where it used to get `workspace-write`. Bypass
    applies in app-server mode, which is now the default.
  - Codex app-server runs with cwd = the boot dir and no `--cd`. The project
    reaches Codex only through `turn.CodexAppServerOptions.CWD`, so a host
    moving to the default must set `CWD`.
- **Breaking:** `runtimebind.ResolveCodexPolicy` returns
  `(CodexPolicy, error)`. A `Runtime` that is not a `runtimes.Mode` (the old
  `app-server`/`subprocess` spellings) is `ErrUnsupportedBinding` instead of
  a silent `workspace-write`.
- `turn.Frame`/`SendTurn` with an API binding (empty mode) now return
  `ErrUnsupportedRuntime`; the old `runtimekind.API` framed raw text.
- `LaunchPlan.Validate` and `RuntimeBinding.Validate` wrap
  `ErrUnknownRuntime` with the offending value, so a persisted `subprocess`
  says what it got.
- **Breaking:** `matrix` holds no table. `Lookup`, `IsSupported`,
  `Supported` and `KnownProviders` read the registry. `Descriptor` carries
  `ProviderID runtimes.ID`, `Runtime runtimes.Mode`, `BinaryName` and the
  full `registry.Descriptor`.
- `launcher.Compile`'s `BootDirIntent` is read from the go-providers layout
  table (instructions, boot and MCP rows), so it names what the harness
  reads, as its doc always said. Old values: claude `agentrc.yaml`, codex
  `config.toml`, opencode `OPENCODE.md` transient. New values: `CLAUDE.md`,
  `AGENTS.md`, `agents/<agent>.md`, each with `boot.md`. An ACP-only
  runtime's intent is empty.
- Skills planted through `NativeFile`/`BootInjectionSpec` use the layout's
  skills root in the directory form everywhere. Codex moves from the flat
  `skills/<name>.md`, which Codex never read, to `skills/<name>/SKILL.md`.
- The projection bridge classifies effects with go-providers'
  `ProviderEffectKind.Class`/`Secret`; an unknown effect kind is now
  redacted. Artifact group ids carry the launch shape including its variant
  (`provider:claude:subprocess-per-turn+bare`), so Claude print and bare
  launches no longer share a group.
- providerplant no longer patches `--add-dir <project>` into the projected
  argv: go-providers v0.31.0's Claude convention carries it in every mode.
  OpenCode's http-sse launch no longer gets `--dir <project>` appended either.
  That is harmless: `opencode serve` has no `--dir`, and its cwd is already
  the project.

### Added

- `agentlaunch.SkillRelPath(provider, mode, name)`, the one skill-path
  helper, and `agentlaunch.ErrNoSkillRoot`.
- `providerplant.ErrPositionalAfterProjection`: the first of
  `Provider.Flags`/`Injection.Args` must be an option. They follow the
  projected argv, whose last flag can be variadic (Claude's `--add-dir`,
  `--mcp-config`) and would swallow a positional.
- `providerplant.ErrNoNativeAdapter`: `DefaultResolver` for an ACP mode (no
  boot dir) or for a registry runtime it has no constructor for. That is the
  one edit a new native runtime still needs in agentkit (CW-20260930-0134).

### Removed

- **Breaking:**
  - package `agentruntime/runtimekind`. Its `Parse` aliases, `API`,
    `PTYDebug` and `Unknown` have no replacement: a host normalizes its own
    tokens at its boundary, `pty-debug` is `pty` plus the debug posture, and
    API is `runtimebind.Binding.API`;
  - `matrix.Capabilities`, `matrix.BootDirRenderer`, the `matrix.Provider*`
    constants and `matrix.KnownRuntimes`;
  - providerplant's `ErrUnknownRenderer`, both `skillRelPath` copies and
    `appendMissingProjectArg`.

## v0.11.1 — 2026-10-01

Patch: a child's final output is no longer lost when it exits quickly
(CW-20261001-0046).

### Fixed

- **jsonrpc-stdio, serve-http and PTY sessions keep a child's last lines.**
  - jsonrpc-stdio and serve-http read the child through `exec.Cmd`'s
    `StdoutPipe`/`StderrPipe`, which `Cmd.Wait` closes as soon as the child
    exits. The PTY waiters closed the master before reading it.
  - Either way, a child that printed its last line and exited at once could
    have that line discarded before the reader reached it. That could be a
    Codex app-server's final frame or an agent's result line, and with it
    the turn's completion.
  - Each runtime now reads from a file it owns (an `os.Pipe` read end, or the
    PTY master) and drains it to EOF after `Wait`, bounded at one second so a
    descendant holding the output open cannot stall shutdown. This is the
    treatment streaming-stdio already had.
  - Applies to both the legacy and supervised lifecycles.

## v0.11.0 — 2026-10-01

Minor, additive. No existing API changes; adds a dependency on go-permission
v0.1.0.

### Added

- **Codex thread resume.** `turn.CodexAppServerOptions.ResumeThreadID` makes a
  Codex app-server session bind its thread with `thread/resume` (thread
  metadata only, `excludeTurns`) instead of always sending `thread/start`. A
  failed resume is returned as an error and never falls back to a fresh
  thread. When Codex no longer has the thread, the error is an
  `*agentsessions.SessionLostError` with `RequestedID`
  (`errors.Is(err, provider.ErrProviderSessionLost)` holds). A resume answered
  with a different thread is a `SessionLostError` carrying `ActualID`, with
  the new `turn.ErrCodexThreadMismatch` as its `Err`.
- **`turn.CodexApprovalResponder`** answers Codex app-server approval requests
  from a go-permission `Mode`, with no human in the loop. `default` approves
  MCP tool calls and declines sandbox escalations (commands, out-of-sandbox
  file changes); `accept-edits` also approves file changes; `plan` declines
  all three; `yolo` approves all three. Any request it cannot decide for a
  human gets a JSON-RPC error in every mode. `Hook()` plugs into
  `StartOptions.JsonRpcRequestHook`; `Decide()` returns the outcome for
  callers that also report it.

### Changed

- New dependency: `github.com/hollis-labs/go-permission` v0.1.0.
- `agentruntime/turn` now imports `agentsessions`.

## v0.10.0 — 2026-09-30

Minor, additive. No existing API changes; go-sandbox is now v0.4.0.

### Added

- `StartOptions.DenyGUILaunch`: the child cannot launch GUI applications (for
  example an agent CLI whose sign-in fallback opens a browser and waits). It is
  folded into the one sandbox that wraps the child instead of stacking a second
  one (nested seatbelt profiles fail): onto `SandboxPolicy` or `Profile` when
  set, and otherwise as a minimal default-allow `Profile` carrying only the
  knob. It needs go-sandbox v0.4.0 and is enforced on macOS; where the
  platform cannot enforce it, and for provider-native runtimes, Start fails
  with the new `ErrGUILaunchDenyUnsupported` rather than run unconfined.
- `StartOptions.EndTurnOnAuthFailure` (opt-in): a subprocess-per-turn turn ends
  as soon as the adapter's `AuthFailureClassifier` matches stderr, instead of
  waiting out the CLI (agy waits 60s for a browser sign-in). The turn fails
  with an error wrapping `provider.ErrProviderNotAuthenticated`.

### Changed

- Requires go-sandbox v0.4.0 (was v0.3.0).

## v0.9.0 — 2026-09-30

Minor. One behavior change (the Claude skill path) and one additive type.

### Fixed

- **Claude skills are planted where Claude reads them.** `NativeFileSkill` for
  `claude` planted the flat file `.claude/skills/<id>.md`, which Claude Code
  (2.1.x) does not load; it reads the directory form
  `.claude/skills/<id>/SKILL.md`. Both copies of the mapping —
  `providerplant` and the `agentlaunch` materializer — now plant the directory
  form, as the OpenCode and Antigravity mappings already did. A consumer that
  read the flat path back from a planted boot directory must read the new path.

### Added

- **`agentsessions.SessionLostError`** (`RequestedID`, `ActualID`, `Err`): the
  error a resume turn fails with when the provider no longer has the session.
  `errors.Is(err, provider.ErrProviderSessionLost)` still holds and the message
  text is unchanged, so existing checks keep working; consumers can now read
  the lost id with `errors.As` instead of parsing the message. `ActualID` is
  empty on this path, where the turn fails rather than continuing in a new
  session (that case is still reported through `events.SessionLost`).

### Changed

- Docs: the shared materialization contract now names Antigravity among the
  providers go-providers projects.

## v0.8.0 — 2026-09-30

Minor: additive. No existing API is removed. Behavior changes only for
callers that set `TypedEventCallback` on a subprocess-per-turn runtime
(typed events now arrive) and for adapters that implement the new optional
go-providers extensions.

### Added

- Antigravity (`agy`) as a known provider: runtimebind (`antigravity`/`agy`,
  subprocess only), matrix pair antigravity × subprocess
  (`BootDirRendererAntigravity`, binary `agy`), providerplant
  `DefaultResolver`, and the `NativeFileSkill` path
  `.agents/skills/<id>/SKILL.md` (workspace root, cwd = bootdir).
- agentsessions, for go-providers' new optional adapter extensions:
  - `Prepare` runs `provider.Preflighter` after `Detect`.
  - A failed turn the adapter recognizes as a login failure
    (`provider.AuthFailureClassifier`) returns an error wrapping
    `provider.ErrProviderNotAuthenticated`.
  - `StartOptions.OnProviderSessionLost(requested, actual, reason)` fires
    when a resume turn on a `provider.SessionResumeVerifier` adapter ran in
    a new provider session. The same notice goes out as `events.SessionLost`
    on `TypedEventCallback` and as a `[session_lost] requested=… actual=…`
    marker on the byte Fanout. The turn is not failed.
- `TypedEventCallback` is honoured on the subprocess-per-turn path for
  adapters that implement `provider.EventParser`: the turn's adapter is
  tapped, so typed events arrive in line order. `events.PermissionDenied` is
  also marked on the byte Fanout as `[permission_denied:<action>] <name>`.

### Dependencies

- go-providers v0.29.0 (AntigravityAdapter and the optional extensions).

## v0.7.0 — 2026-09-30

Breaking in two places: the `artifact` / `materialize` packages leave
agentkit, and `agentsessions` no longer treats usage as a terminal event.
Direct importers of `agentkit/artifact` or `agentkit/materialize` must
switch to `github.com/hollis-labs/go-materialize/...`. Known importers:
Nanite `internal/runtime/agent/bootdir_claude.go`, `bootdir_hooks.go`,
`bootdir_plant.go` and `bootdir_plant_test.go`.

### Changed — agentsessions (BREAKING behavior)

- `EventUsage` no longer counts as a turn's terminal event; only
  `EventDone`/`EventError` do. A turn that reports usage and then exits
  without its own terminal event now gets a synthesized `EventDone` on a
  clean exit and `EventError` on a crash (before, usage suppressed both, so
  a crash after usage went unreported). Needed for go-providers' structured
  OpenCode run mode, which reports usage per step. Audit: no other
  go-providers or go-agent-wrapper adapter emits usage without done.
- Lost provider sessions: on a resume turn whose adapter implements
  go-providers' `SessionLostClassifier`, the session keeps the last 4KB of
  the turn's stderr (still forwarded to `StartOptions.Stderr`). When the
  turn fails and the adapter recognizes the tail, the stored provider
  session id is cleared and `SendInput` returns an error wrapping
  `provider.ErrProviderSessionLost`; the next turn starts a fresh session.
  No automatic retry.

### Fixed — agentlaunch

- OpenCode `NativeFileSkill` files are planted at `skills/<id>/SKILL.md`
  (both `providerplant` and the materializer). The old
  `.opencode/skills/<id>.md` was never read by opencode: flat files are
  ignored, and a bootdir `.opencode/` tree is only scanned when cwd is the
  bootdir. Skill content should carry a frontmatter `name`.

### Changed — BREAKING (packages moved)

- Raised the module's `go` directive to `1.26.6` (Go floor across the portfolio); CI now uses `go-version-file: go.mod`.
- `artifact` and `materialize` moved out to their own module,
  [`go-materialize`](https://github.com/hollis-labs/go-materialize)
  (CW-20260918-0036): both packages had no dependency beyond stdlib on
  each other, so folio's scaffolding writer can now depend on the same
  write engine agentkit uses instead of hand-rolling its own. All former
  `agentkit/artifact` and `agentkit/materialize` imports now resolve to
  `go-materialize/artifact` and `go-materialize/materialize` — hard
  cutover, no compatibility aliases were kept.
- The persisted manifest path changed from the agentkit-branded
  `.agentkit/materialize-manifest.json` to `.materialize/manifest.json`
  as part of that move (breaking: a manifest written by a pre-cutover
  agentkit is not found by `Reconcile`/`Refresh` after upgrading; the
  next `Create` rewrites it at the new path).

### Dependencies

- go-materialize v0.1.0 (first tag; replaces the pseudo-version) and
  go-providers v0.28.0 (structured OpenCode run mode,
  `SessionLostClassifier`). go-runner v0.7.0 and go-sandbox v0.3.0 are
  unchanged.

## v0.6.1 — 2026-09-06

- Preserve buffered streaming-stdio events when a child exits before its
  reader runs, for both supervised and single-shot lifecycles. Own stdout
  independently of `exec.Cmd.Wait` and allow a bounded one-second drain for
  descendants that inherit the output pipe.
- Add deterministic regression coverage that delays the reader until the
  child has exited.

## v0.6.0 — 2026-09-06

### Shared materialization and canonical bootstrap

- Add neutral artifact trees, bounded filesystem/immutable sources,
  provenance and ownership metadata, and preservation of executable modes,
  binary content and empty directories.
- Add safe materialization, owned reconciliation and managed-document merges;
  extract reusable authored-recipe composition and document assembly.
- Add prepared execution with exact argv/env/cwd and independent project,
  boot, state and scratch roots. Legacy planting entry points delegate to
  the shared engine; session runtimes carry required sandbox policy.
- Add offline canonical session/bootstrap contracts, optional durable actors,
  provider-native ID mappings, lineage, publication and trace/work references.
- Update shared dependencies to go-providers v0.26.0, go-sandbox v0.3.0
  and go-runner v0.7.0.

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
