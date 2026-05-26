package broker

import "context"

// DeterministicBroker is a [Broker] that applies a priority-ordered
// rule set over the signals in [Input].
//
// Rule priority (highest first; first match wins):
//
//  1. Reflex match with a non-empty [Input.ReflexAgentSlug] → that
//     slug. Confidence inherits from [Input.ReflexConfidence].
//     Reason="reflex:<id>".
//  2. Mode = [ModeWork] with confidence ≥ [ModeConfidenceHigh] →
//     [ProfileWorker]. Reason="mode=work".
//  3. Mode = [ModeWork] with confidence ≥ [ModeConfidenceLow] →
//     [ProfileWorker]. Reason="action-verb-work" (logged distinctly so
//     telemetry can isolate the lower-confidence band).
//  4. Mode = [ModePlan] with confidence ≥ [ModeConfidenceHigh] AND
//     ScopeTier = [TierOpen] → [ProfilePlanner].
//     Reason="mode=plan,tier=open". Plan-mode at any other tier falls
//     through to chat (planning conversation, not full decomposition).
//  5. ScopeTier = [TierOpen] AND ExecutionPattern = [PatternSubagent] →
//     [ProfilePlanner] (mode-independent).
//     Reason="tier=open,pattern=subagent".
//  6. Default → chat handles the turn directly ([ProfileChat]).
//     Reason="default-chat-handle". Confidence=0 to signal "no rule
//     matched, this is the fallback".
//
// The struct is concurrency-safe (it has no mutable state — [Decide] is
// a pure function over [Input]).
type DeterministicBroker struct{}

// New returns a [DeterministicBroker], the recommended [Broker]
// implementation for production wiring. [NewModeBroker] is retained as
// a smaller fallback / parity-test impl.
func New() *DeterministicBroker { return &DeterministicBroker{} }

// Decide implements [Broker] for [DeterministicBroker]. See the type
// doc for the full rule-priority list.
func (*DeterministicBroker) Decide(_ context.Context, in Input) (Decision, error) {
	// Rule 1 — reflex override. Highest priority because the caller's
	// reflex catalog is an explicit, hand-authored routing decision;
	// the rest of the rule set is statistical inference over the
	// message text.
	if in.ReflexMatchID != "" && in.ReflexAgentSlug != "" {
		return Decision{
			AgentProfile: in.ReflexAgentSlug,
			Reason:       "reflex:" + in.ReflexMatchID,
			Confidence:   in.ReflexConfidence,
		}, nil
	}

	// Rule 2 — high-confidence work mode → worker.
	// Slash commands and imperative-work phrasing typically clear the
	// high-confidence threshold; both dispatch with full confidence.
	if in.Mode == ModeWork && in.ModeConfidence >= ModeConfidenceHigh {
		return Decision{
			AgentProfile: ProfileWorker,
			Reason:       "mode=work",
			Confidence:   in.ModeConfidence,
		}, nil
	}

	// Rule 3 — action-verb work mode → worker (logged distinctly).
	// Action-verb classification is weaker than imperative phrasing
	// but still dispatches; the distinct Reason string lets telemetry
	// analysis isolate this band when tuning the threshold.
	if in.Mode == ModeWork && in.ModeConfidence >= ModeConfidenceLow {
		return Decision{
			AgentProfile: ProfileWorker,
			Reason:       "action-verb-work",
			Confidence:   in.ModeConfidence,
		}, nil
	}

	// Rule 4 — high-confidence plan mode AT TierOpen → planner.
	// Plan mode at any tier other than Open is a planning conversation
	// (chat handles that); only Open-tier plan inputs are large enough
	// to warrant a planner subagent decomposition.
	if in.Mode == ModePlan && in.ModeConfidence >= ModeConfidenceHigh && in.ScopeTier == TierOpen {
		return Decision{
			AgentProfile: ProfilePlanner,
			Reason:       "mode=plan,tier=open",
			Confidence:   in.ModeConfidence,
		}, nil
	}

	// Rule 5 — TierOpen × PatternSubagent → planner (mode-independent).
	// Catches inputs that classify as chat-mode but the scope / pattern
	// classifier flags as decomposition-shaped (e.g. "research the foo
	// landscape exhaustively" — chat-mode, but Open + Subagent).
	if in.ScopeTier == TierOpen && in.ExecutionPattern == PatternSubagent {
		return Decision{
			AgentProfile: ProfilePlanner,
			Reason:       "tier=open,pattern=subagent",
			Confidence:   confidenceForTierPattern(in),
		}, nil
	}

	// Rule 6 — default. Chat handles the turn directly; no dispatch.
	// Confidence is 0 to signal "no rule matched, this is the
	// fallback" — telemetry analysis on
	// confidence=0,reason=default-chat-handle measures how often the
	// fallback fires.
	return Decision{
		AgentProfile: ProfileChat,
		Reason:       "default-chat-handle",
		Confidence:   0,
	}, nil
}

// confidenceForTierPattern picks the broker's Confidence for the
// rule-5 ([TierOpen] × [PatternSubagent]) firing. The scope / pattern
// classification does not carry its own per-classification confidence
// the way mode classification does, so the broker uses a fixed
// mid-band confidence (0.75) to flag this as "rules-derived: not as
// strong as a high-confidence mode signal, but stronger than the
// default fallback."
//
// If [Input.ModeConfidence] is populated (e.g. a chat-mode classification
// at low confidence), we still keep the 0.75 floor — the rule-5 firing
// is the load-bearing signal here, not the mode classifier.
func confidenceForTierPattern(_ Input) float64 {
	return 0.75
}

// Compile-time assertion that *DeterministicBroker satisfies [Broker].
var _ Broker = (*DeterministicBroker)(nil)
