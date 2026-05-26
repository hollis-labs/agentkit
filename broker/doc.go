// Package broker is the intent-based agent-router primitive used by host
// applications to decide which agent profile should handle a user turn —
// chat handles it directly, dispatch to a worker, dispatch to a planner,
// etc.
//
// A [Broker] consumes a per-turn [Input] (raw user text plus session
// context — mode, scope tier, execution pattern, an optional reflex
// match) and returns a [Decision] carrying the chosen agent-profile slug,
// a human-readable reason for telemetry, and a confidence score in
// [0, 1]. Implementations must be safe for concurrent use.
//
// The package ships two implementations of [Broker]:
//
//   - [DeterministicBroker], returned by [New], is the recommended
//     implementation. It applies a priority-ordered rule set over
//     reflex / classified-mode / scope-tier × execution-pattern signals
//     (see [DeterministicBroker.Decide] for the full priority list).
//
//   - [ModeBroker], returned by [NewModeBroker], is a small fallback
//     that maps [Input.SessionMode] directly to an agent profile —
//     "work" → [ProfileWorker], "plan" → [ProfilePlanner], any other
//     value → "default-chat" — and always returns Confidence=1.0. It
//     is retained for parity testing during call-site cutovers and as
//     a minimal example impl.
//
// Both implementations are pure functions over [Input] (no mutable
// state) and are safe for concurrent use.
//
// [Input] uses primitive-typed fields (string / float64 projections of
// classifier and reflex outputs) so the broker module stays
// dependency-free; callers project their internal classifier / reflex
// types into these primitives at the call site.
//
// The package also exposes a small set of named constants for the
// agent-profile slugs ([ProfileWorker], [ProfilePlanner],
// [ProfileChat]), confidence-threshold tiers ([ModeConfidenceHigh],
// [ModeConfidenceLow]), scope-tier wire strings ([TierTrivial] …
// [TierOpen]), execution-pattern wire strings ([PatternInline] …
// [PatternBackground]), and per-turn classified-mode wire strings
// ([ModeChat], [ModePlan], [ModeWork]) so callers don't have to repeat
// magic strings.
package broker
