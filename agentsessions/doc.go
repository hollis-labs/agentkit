// Package agentsessions provides a long-lived agent-session abstraction
// over the hollis-labs Go primitive libraries.
//
// A Session is the running handle for one agent. Six lifecycle shapes
// are supported. NewFromAdapter selects one of the first five from the
// Capabilities flags on AdapterRuntimeConfig (the runtimes.Mode each one
// drives is in parentheses):
//
//   - turn-based subprocess (default; subprocess-per-turn) — runner.Run
//     per SendInput
//   - long-lived PTY (Caps.PTY; pty) — TUI driven over a creack/pty master
//   - long-lived streaming stdio (Caps.StreamingStdio; streaming-stdio) —
//     NDJSON over stdin/stdout (Claude `claude -p --input-format stream-json`)
//   - long-lived JSON-RPC stdio (Caps.JsonRpcStdio; jsonrpc-stdio) —
//     JSON-RPC 2.0 over stdin/stdout (Codex `app-server`)
//     (Codex `app-server`). The session reports a Codex turn's agent messages,
//     commands, file changes and end on EventFanout and TypedEventCallback like
//     every other runtime: item/completed for an agentMessage is a delta (phase
//     final for the final answer, narration for commentary, block id the item
//     id), for a commandExecution or fileChange a tool use (and, on the typed
//     surface, a tool result, and a PermissionDenied for a declined one), for a
//     refused item/tool/requestUserInput request a request_user_input tool use
//     carrying the questions, and turn/completed is a done (stop reason
//     end_turn, or cancelled when interrupted) or, for a failed turn, an error.
//     A repeated item id is reported once. Prefer TypedEventCallback: EventFanout
//     drops events when its channel is full (see StartOptions.EventFanout), and a
//     dropped final message or done corrupts a consumer's view of the turn.
//   - long-lived HTTP server (Caps.ServeHTTP; http-sse) — child-owned HTTP
//     API with server-sent events (opencode `serve`)
//   - HTTP-streamed (llmcontracts.Provider directly, via NewFromProvider)
//
// PTY / StreamingStdio / JsonRpcStdio / ServeHTTP are mutually exclusive
// — at most one lifecycle flag may be true on a single Capabilities value. All
// shapes expose the same Session interface — Wait, Stop, SendInput,
// Resize, Health, CheckpointHints — so consumers (agent-mux, clockwork,
// nanite) can drive any of them uniformly.
//
// Optional capabilities are separate interfaces a caller type-asserts:
//
//   - JsonRpcCaller (JSON-RPC runtime): Call(method, params) → result for
//     typed request/response correlation; SendInput remains the raw-bytes
//     escape hatch. The session's own request ids start at 2^32, so they
//     never collide with raw frames a host numbers from 1.
//   - TurnInterrupter: InterruptTurn ends the turn in flight and keeps the
//     process, so the next SendInput runs on the same process. The
//     streaming-stdio (Claude control_request), JSON-RPC (Codex
//     turn/interrupt) and serve-http (OpenCode abort) sessions implement it;
//     an adapter with no interrupt returns ErrInterruptUnsupported.
//   - SessionIDer, CheckpointHinter, PIDReporter, SandboxOutcomeReporter.
//
// A Manager registers Sessions, persists state transitions through
// caller-supplied sinks, watches for terminal exits, and broadcasts
// session output to attach subscribers via an in-memory ring buffer.
//
// # Composition
//
//   - go-providers / go-llm-contracts — llmcontracts.Provider (an HTTP
//     API, via NewFromProvider) or provider.CLIAdapter (every CLI runtime,
//     per-turn or long-lived, via NewFromAdapter).
//   - go-runner — runner.Run drives the per-turn subprocess case.
//   - go-sandbox — the effective sandbox comes from
//     StartOptions.PreparedExecution (its Access), SandboxPolicy, or the
//     legacy Profile, and is applied at every spawn. DenyGUILaunch and
//     ProtectedPaths are merged into it and fail the launch where they
//     cannot be enforced rather than running unconfined.
//   - agentlaunch — StartOptions.PreparedExecution and StartOptions.Launch
//     carry a prepared launch's exact bindings and per-turn argv template.
//   - go-egress-proxy — NOT a dep. Consumers wanting allowlisted egress
//     start an egress.Proxy themselves and merge its env vars into
//     StartOptions.Env before calling Manager.Start.
//
// Persistence is consumer-owned: this library defines StateSink,
// AttachmentSink, and EventSink interfaces and ships none of their
// implementations.
//
// # Output and session logs
//
// The long-lived runtimes (PTY, streaming stdio, JSON-RPC stdio, serve-http)
// read child output line by line. A line longer than 64 MiB is read
// through, skipped and logged rather than routed, so the child never blocks
// on a full pipe. In the streaming-stdio and JSON-RPC runtimes a reader that
// fails for any other reason marks the session unusable: Health reports it
// not alive and input fails at once.
//
// The long-lived runtimes also require StartOptions.LogPath or WorkspaceDir
// (then <WorkspaceDir>/logs/session.log). The log is opened for appending
// and never truncated, so a host's own writer on the same file keeps its
// lines; a host that wants a fresh log per session passes a fresh path.
//
// # Turn failures on the subprocess runtime
//
// With an adapter that classifies them, a resume turn whose provider
// session is gone fails with *SessionLostError (errors.Is
// provider.ErrProviderSessionLost), one that resumed into a new session
// reports events.SessionLost and OnProviderSessionLost, and a sign-in
// failure wraps provider.ErrProviderNotAuthenticated and reports
// events.AuthFailed (EndTurnOnAuthFailure ends that turn early). Permission
// denials parsed from the stream are marked on the byte Fanout.
//
// # Usage on the serve-http runtime
//
// OpenCode reports each step's tokens, cost and reason in a step-finish part.
// The serve-http runtime sends one llmtypes.EventUsage on EventFanout per
// step, as OpenCode's run mode does for its step_finish line: the tokens and
// CostUSD are that step's own, never a running total, so a consumer sums them
// per turn; output tokens include reasoning tokens; the stop reason is the
// step's, normalized. A usage event is not terminal: a turn is one or more
// steps, and session.idle still ends it. The compaction summary's step is
// reported too, since it is real spend. EventFanout drops events when its
// channel is full, usage included, so size it for the consumer.
//
// # A lost session on the streaming-stdio runtime
//
// A streaming-stdio child started to resume a provider session the provider
// no longer has exits at once, and its stdout says only that it failed. When
// the adapter classifies that (provider.SessionLostClassifier) and the attempt
// resumed an id, the session keeps a bounded tail of that attempt's stderr and
// classifies it when the child exits abnormally, unless the attempt got going
// (it emitted a session id or finished a turn) or the session or its
// supervisor ended it (Stop, a canceled ctx, a supervisor kill). It then reports
// events.SessionLost once, after the child's final output, with the same
// "[session_lost]" marker on the byte Fanout; the dead id is no longer the
// session's ProviderSessionID; SendInput fails with *SessionLostError (which
// still matches ErrNoInputChannel); and the session is not restarted, since a
// restart would resume the same id. A SendInput whose write fails because the
// child just died waits briefly for the classification (ending with its ctx and
// with Stop), so it reports the loss rather than a broken pipe. Wait's error is
// unchanged. StartOptions.Stderr, or the session log, still receives every byte
// of stderr the child wrote before it exited, bar a descendant that keeps
// stderr open and is cut off after a one-second drain; an attempt that is not
// a classifiable resume keeps its plain stderr routing.
//
// # Process-level State enum
//
// The library defines a fixed four-value State enum: launching, running,
// done, failed. Consumers map their domain FSM (clockwork tasks, mux
// logical agents) on top — the library only tracks process-level state.
//
// # Single-turn-in-flight
//
// SendInput is serialized per-Session by the Manager via a per-entry lock,
// matching the mux runtime contract. The subprocess-per-turn, serve-http and
// NewFromProvider runtimes additionally surface ErrTurnInFlight if a second
// SendInput arrives before the first turn's terminal event has flushed. The
// PTY, streaming-stdio and JSON-RPC runtimes write input straight to the
// long-lived child, which owns its own turn boundaries.
package agentsessions
