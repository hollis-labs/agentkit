package broker

import "context"

// Input is the per-turn signal bundle a [Broker] consults to make a
// decision. All fields are zero-value-safe — callers that only populate
// a subset get well-defined (degenerate) behavior. Adding fields in the
// future is non-breaking under the same contract.
//
// Field projections (string / float64 shapes rather than concrete
// classifier or reflex types) keep the broker module dependency-free:
// callers project their classifier / reflex output into these
// primitives at the call site.
type Input struct {
	// UserText is the raw user-facing prompt for the turn.
	UserText string

	// SessionMode is the active workspace mode slug ("chat", "plan",
	// "work"). This is the persistent per-session mode, NOT the
	// per-turn classified mode. Empty means unknown.
	//
	// Read by [ModeBroker] (mode pass-through). [DeterministicBroker]
	// does not consult SessionMode directly — its rules key off
	// [Input.Mode] (per-turn classified) instead, but SessionMode
	// remains in the input for telemetry and future rule extensions.
	SessionMode string

	// Mode is the per-turn classified mode produced by the caller's
	// mode classifier: one of "chat", "plan", "work" (see [ModeChat],
	// [ModePlan], [ModeWork]). Empty means the caller did not classify
	// (the deterministic broker treats this as no-signal and falls
	// through).
	Mode string

	// ModeConfidence is the classifier's confidence in [Input.Mode], in
	// [0, 1]. The deterministic broker's mode rules apply confidence
	// thresholds: see [ModeConfidenceHigh] and [ModeConfidenceLow].
	ModeConfidence float64

	// ScopeTier is the caller's scope-tier projection — one of
	// [TierTrivial], [TierSmall], [TierMedium], [TierLarge], [TierOpen].
	// Empty means no classification.
	ScopeTier string

	// ExecutionPattern is the caller's execution-pattern projection —
	// one of [PatternInline], [PatternSubagent], [PatternBackground].
	// Empty means no classification.
	ExecutionPattern string

	// ReflexMatchID is the matched reflex's ID when the caller's reflex
	// matcher fired upstream. Empty means no match.
	ReflexMatchID string

	// ReflexAgentSlug is the agent slug the matched reflex specified.
	// The deterministic broker's highest-priority rule routes to this
	// slug when ReflexMatchID is set AND ReflexAgentSlug is non-empty.
	ReflexAgentSlug string

	// ReflexConfidence is the matched reflex's confidence in [0, 1].
	// When reflex routing fires, [Decision.Confidence] inherits this
	// value.
	ReflexConfidence float64
}

// Decision is the [Broker]'s per-turn output. AgentProfile is the slug
// the caller should route to — empty string ([ProfileChat]) means "chat
// handles the turn directly, no dispatch"; a non-empty slug means
// "dispatch with this profile." Reason is a human-readable explanation
// suitable for surfacing in the caller's telemetry / decision-review
// surfaces. Confidence is in [0, 1].
type Decision struct {
	AgentProfile string
	Reason       string
	Confidence   float64
}

// Broker is the narrow surface host call-sites consult before dispatch.
// Implementations must be safe for concurrent use.
type Broker interface {
	Decide(ctx context.Context, input Input) (Decision, error)
}

// Agent-profile slug constants the broker emits. Callers may dispatch
// other slugs via reflex / configuration; these are the default routes.
const (
	ProfileWorker  = "worker"
	ProfilePlanner = "planner"
	// ProfileChat is the empty AgentProfile — chat handles the turn
	// directly, no dispatch. Defined as a named constant so callers can
	// compare against `decision.AgentProfile == broker.ProfileChat`
	// rather than `== ""`.
	ProfileChat = ""
)

// Confidence thresholds applied by the deterministic broker's mode
// rules. The defaults match common classifier output tiers (slash
// commands at ~1.0, imperative phrasing at ~0.85, action-verb phrasing
// at ~0.7, default at 0.0).
const (
	ModeConfidenceHigh = 0.85
	ModeConfidenceLow  = 0.7
)

// Scope-tier and execution-pattern wire strings. These are the values
// the broker compares against [Input.ScopeTier] and
// [Input.ExecutionPattern]; callers' classifiers should produce
// matching strings (or callers should translate at the seam).
const (
	TierTrivial = "trivial"
	TierSmall   = "small"
	TierMedium  = "medium"
	TierLarge   = "large"
	TierOpen    = "open"

	PatternInline     = "inline"
	PatternSubagent   = "subagent"
	PatternBackground = "background"
)

// Mode wire strings — values the broker compares against [Input.Mode].
const (
	ModeChat = "chat"
	ModePlan = "plan"
	ModeWork = "work"
)

// ModeBroker maps [Input.SessionMode] directly to an agent profile —
// "work" → [ProfileWorker], "plan" → [ProfilePlanner], any other value
// (including "" and unknown modes) → "default-chat" — and always
// returns Confidence=1.0. It is the simplest useful [Broker]; new
// callers should generally prefer [DeterministicBroker] returned by
// [New], which composes richer signals.
type ModeBroker struct{}

// NewModeBroker returns a [ModeBroker].
func NewModeBroker() *ModeBroker { return &ModeBroker{} }

// Decide implements [Broker] for [ModeBroker].
func (*ModeBroker) Decide(_ context.Context, input Input) (Decision, error) {
	switch input.SessionMode {
	case ModeWork:
		return Decision{AgentProfile: ProfileWorker, Reason: "mode=work", Confidence: 1.0}, nil
	case ModePlan:
		return Decision{AgentProfile: ProfilePlanner, Reason: "mode=plan", Confidence: 1.0}, nil
	default:
		return Decision{AgentProfile: "default-chat", Reason: "mode=chat", Confidence: 1.0}, nil
	}
}

// Compile-time assertion that *ModeBroker satisfies [Broker].
var _ Broker = (*ModeBroker)(nil)
