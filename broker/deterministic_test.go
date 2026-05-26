package broker

import (
	"context"
	"testing"
)

// TestDeterministic_Rule1_ReflexOverride asserts that a reflex match with
// a non-empty AgentSlug wins over every other signal — including a
// high-confidence mode signal that would otherwise route differently.
// This is the "reflex catalog is an explicit hand-authored routing
// decision" priority from agent_broker_v1.
func TestDeterministic_Rule1_ReflexOverride(t *testing.T) {
	b := New()
	cases := []struct {
		name       string
		input      Input
		wantSlug   string
		wantReason string
		wantConf   float64
	}{
		{
			name: "reflex beats high-confidence work-mode",
			input: Input{
				UserText:         "implement the foo",
				Mode:             ModeWork,
				ModeConfidence:   1.0,
				ReflexMatchID:    "deploy_staging",
				ReflexAgentSlug:  "deployer",
				ReflexConfidence: 0.95,
			},
			wantSlug:   "deployer",
			wantReason: "reflex:deploy_staging",
			wantConf:   0.95,
		},
		{
			name: "reflex beats TierOpen × PatternSubagent",
			input: Input{
				ScopeTier:        TierOpen,
				ExecutionPattern: PatternSubagent,
				ReflexMatchID:    "research_audit",
				ReflexAgentSlug:  "auditor",
				ReflexConfidence: 0.88,
			},
			wantSlug:   "auditor",
			wantReason: "reflex:research_audit",
			wantConf:   0.88,
		},
		{
			name: "reflex with empty AgentSlug does NOT override (falls through)",
			input: Input{
				Mode:            ModeWork,
				ModeConfidence:  0.9,
				ReflexMatchID:   "diagnostic_only",
				ReflexAgentSlug: "", // no routing slug → rule 1 skipped
			},
			wantSlug:   ProfileWorker,
			wantReason: "mode=work",
			wantConf:   0.9,
		},
		{
			name: "reflex with empty MatchID does NOT override even with slug set",
			input: Input{
				Mode:            ModeWork,
				ModeConfidence:  0.9,
				ReflexMatchID:   "",
				ReflexAgentSlug: "ghost", // dangling slug → rule 1 skipped
			},
			wantSlug:   ProfileWorker,
			wantReason: "mode=work",
			wantConf:   0.9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != tc.wantSlug {
				t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, tc.wantSlug)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Confidence != tc.wantConf {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tc.wantConf)
			}
		})
	}
}

// TestDeterministic_Rule2_WorkHighConfidence asserts that mode=work with
// confidence ≥ 0.85 routes to the worker with `Reason="mode=work"`.
func TestDeterministic_Rule2_WorkHighConfidence(t *testing.T) {
	b := New()
	cases := []struct {
		name string
		conf float64
	}{
		{"slash /work (1.0)", 1.0},
		{"imperative phrase (0.85)", 0.85},
		{"high explicit (0.9)", 0.9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), Input{
				Mode:           ModeWork,
				ModeConfidence: tc.conf,
			})
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != ProfileWorker {
				t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, ProfileWorker)
			}
			if got.Reason != "mode=work" {
				t.Errorf("Reason = %q, want %q", got.Reason, "mode=work")
			}
			if got.Confidence != tc.conf {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tc.conf)
			}
		})
	}
}

// TestDeterministic_Rule3_WorkActionVerb asserts that mode=work with
// confidence in [0.7, 0.85) still dispatches to worker but logs as
// `Reason="action-verb-work"` so v2 telemetry can isolate the band.
func TestDeterministic_Rule3_WorkActionVerb(t *testing.T) {
	b := New()
	cases := []struct {
		name string
		conf float64
	}{
		{"action-verb floor (0.7)", 0.7},
		{"mid action-verb (0.8)", 0.8},
		{"just below high (0.849)", 0.849},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), Input{
				Mode:           ModeWork,
				ModeConfidence: tc.conf,
			})
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != ProfileWorker {
				t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, ProfileWorker)
			}
			if got.Reason != "action-verb-work" {
				t.Errorf("Reason = %q, want %q", got.Reason, "action-verb-work")
			}
			if got.Confidence != tc.conf {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tc.conf)
			}
		})
	}
}

// TestDeterministic_WorkBelowThreshold asserts that mode=work below the
// 0.7 floor falls through to the default chat path — no dispatch.
func TestDeterministic_WorkBelowThreshold(t *testing.T) {
	b := New()
	got, err := b.Decide(context.Background(), Input{
		Mode:           ModeWork,
		ModeConfidence: 0.6,
	})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if got.AgentProfile != ProfileChat {
		t.Errorf("AgentProfile = %q, want chat (empty)", got.AgentProfile)
	}
	if got.Reason != "default-chat-handle" {
		t.Errorf("Reason = %q, want %q", got.Reason, "default-chat-handle")
	}
}

// TestDeterministic_Rule4_PlanOpen asserts that high-confidence plan mode
// at TierOpen routes to planner. Lower tiers stay in chat (per the
// "planning conversation, not full decomposition" carve-out).
func TestDeterministic_Rule4_PlanOpen(t *testing.T) {
	b := New()
	cases := []struct {
		name        string
		input       Input
		wantProfile string
		wantReason  string
	}{
		{
			name:        "plan + tier=open + high conf → planner",
			input:       Input{Mode: ModePlan, ModeConfidence: 0.9, ScopeTier: TierOpen},
			wantProfile: ProfilePlanner,
			wantReason:  "mode=plan,tier=open",
		},
		{
			name:        "plan + tier=open at threshold (0.85) → planner",
			input:       Input{Mode: ModePlan, ModeConfidence: 0.85, ScopeTier: TierOpen},
			wantProfile: ProfilePlanner,
			wantReason:  "mode=plan,tier=open",
		},
		{
			name:        "plan + tier=large (≤ Large) → chat (planning conversation)",
			input:       Input{Mode: ModePlan, ModeConfidence: 0.9, ScopeTier: TierLarge},
			wantProfile: ProfileChat,
			wantReason:  "default-chat-handle",
		},
		{
			name:        "plan + tier=medium → chat",
			input:       Input{Mode: ModePlan, ModeConfidence: 0.9, ScopeTier: TierMedium},
			wantProfile: ProfileChat,
			wantReason:  "default-chat-handle",
		},
		{
			name:        "plan + tier=open but low conf (0.7) → chat",
			input:       Input{Mode: ModePlan, ModeConfidence: 0.7, ScopeTier: TierOpen},
			wantProfile: ProfileChat,
			wantReason:  "default-chat-handle",
		},
		{
			name:        "plan + missing tier → chat",
			input:       Input{Mode: ModePlan, ModeConfidence: 0.9, ScopeTier: ""},
			wantProfile: ProfileChat,
			wantReason:  "default-chat-handle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != tc.wantProfile {
				t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, tc.wantProfile)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestDeterministic_Rule5_TierOpenSubagent asserts that TierOpen ×
// PatternSubagent dispatches to planner regardless of mode (catches
// chat-classified inputs that the scope/pattern classifier flags as
// decomposition-shaped).
func TestDeterministic_Rule5_TierOpenSubagent(t *testing.T) {
	b := New()
	cases := []struct {
		name        string
		input       Input
		wantProfile string
		wantReason  string
	}{
		{
			name:        "chat-mode + tier=open + subagent → planner",
			input:       Input{Mode: ModeChat, ModeConfidence: 0.0, ScopeTier: TierOpen, ExecutionPattern: PatternSubagent},
			wantProfile: ProfilePlanner,
			wantReason:  "tier=open,pattern=subagent",
		},
		{
			name:        "no-mode + tier=open + subagent → planner",
			input:       Input{ScopeTier: TierOpen, ExecutionPattern: PatternSubagent},
			wantProfile: ProfilePlanner,
			wantReason:  "tier=open,pattern=subagent",
		},
		{
			name:        "tier=open + pattern=inline → chat (rule 5 needs subagent)",
			input:       Input{ScopeTier: TierOpen, ExecutionPattern: PatternInline},
			wantProfile: ProfileChat,
			wantReason:  "default-chat-handle",
		},
		{
			name:        "tier=large + pattern=subagent → chat (rule 5 needs Open)",
			input:       Input{ScopeTier: TierLarge, ExecutionPattern: PatternSubagent},
			wantProfile: ProfileChat,
			wantReason:  "default-chat-handle",
		},
		{
			name:        "tier=open + pattern=background → chat (rule 5 needs subagent)",
			input:       Input{ScopeTier: TierOpen, ExecutionPattern: PatternBackground},
			wantProfile: ProfileChat,
			wantReason:  "default-chat-handle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != tc.wantProfile {
				t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, tc.wantProfile)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestDeterministic_Rule5_FixedConfidence asserts the rule-5 firing emits
// the documented mid-band confidence (0.75) rather than inheriting from
// the (potentially zero) ModeConfidence.
func TestDeterministic_Rule5_FixedConfidence(t *testing.T) {
	b := New()
	got, err := b.Decide(context.Background(), Input{
		Mode:             ModeChat,
		ModeConfidence:   0.0, // chat default fallback in classifier
		ScopeTier:        TierOpen,
		ExecutionPattern: PatternSubagent,
	})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if got.Confidence != 0.75 {
		t.Errorf("Confidence = %v, want 0.75 (rule-5 mid-band)", got.Confidence)
	}
}

// TestDeterministic_Rule6_DefaultChat asserts the default-chat fallback
// fires for inputs that match no rule. Reason and confidence are exact.
func TestDeterministic_Rule6_DefaultChat(t *testing.T) {
	b := New()
	cases := []struct {
		name  string
		input Input
	}{
		{"empty input", Input{}},
		{"plain chat", Input{Mode: ModeChat, ModeConfidence: 0.0}},
		{"low-conf work (below floor)", Input{Mode: ModeWork, ModeConfidence: 0.5}},
		{"plan but tier=small", Input{Mode: ModePlan, ModeConfidence: 0.9, ScopeTier: TierSmall}},
		{"tier=trivial pattern=inline", Input{ScopeTier: TierTrivial, ExecutionPattern: PatternInline}},
		{
			name: "session-mode set but no per-turn signals (deterministic ignores SessionMode)",
			input: Input{
				SessionMode: ModeWork, // would dispatch under no-op ModeBroker; deterministic ignores
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != ProfileChat {
				t.Errorf("AgentProfile = %q, want chat (empty)", got.AgentProfile)
			}
			if got.Reason != "default-chat-handle" {
				t.Errorf("Reason = %q, want %q", got.Reason, "default-chat-handle")
			}
			if got.Confidence != 0 {
				t.Errorf("Confidence = %v, want 0", got.Confidence)
			}
		})
	}
}

// TestDeterministic_PriorityOrder is a property-style test asserting that
// when multiple rules COULD fire for the same input, the higher-priority
// rule wins. This is the load-bearing guarantee — agent_broker_v1's whole
// design is "priority-ordered rule set."
func TestDeterministic_PriorityOrder(t *testing.T) {
	b := New()
	cases := []struct {
		name        string
		input       Input
		wantProfile string
		wantReason  string
		// note: each input intentionally satisfies the conditions of
		// MULTIPLE rules; the test asserts the highest-priority one fires.
	}{
		{
			// Reflex (rule 1) AND work-high (rule 2) both fire → rule 1 wins.
			name: "reflex + work-high → rule 1 (reflex)",
			input: Input{
				Mode:             ModeWork,
				ModeConfidence:   1.0,
				ReflexMatchID:    "x",
				ReflexAgentSlug:  "custom",
				ReflexConfidence: 0.5,
			},
			wantProfile: "custom",
			wantReason:  "reflex:x",
		},
		{
			// Reflex (rule 1) AND tier-open-subagent (rule 5) both fire → rule 1 wins.
			name: "reflex + tier-open-subagent → rule 1 (reflex)",
			input: Input{
				ScopeTier:        TierOpen,
				ExecutionPattern: PatternSubagent,
				ReflexMatchID:    "x",
				ReflexAgentSlug:  "custom",
				ReflexConfidence: 0.5,
			},
			wantProfile: "custom",
			wantReason:  "reflex:x",
		},
		{
			// Work-high (rule 2) AND tier-open-subagent (rule 5) both
			// fire (mode=work, conf 0.9, tier=open, pattern=subagent) →
			// rule 2 wins because mode-work is higher priority than the
			// mode-independent rule 5.
			name: "work-high + tier-open-subagent → rule 2 (work)",
			input: Input{
				Mode:             ModeWork,
				ModeConfidence:   0.9,
				ScopeTier:        TierOpen,
				ExecutionPattern: PatternSubagent,
			},
			wantProfile: ProfileWorker,
			wantReason:  "mode=work",
		},
		{
			// Work-action-verb (rule 3) AND tier-open-subagent (rule 5)
			// both fire (mode=work conf 0.7, tier=open, pattern=subagent)
			// → rule 3 wins (mode is higher priority than tier×pattern).
			name: "work-action-verb + tier-open-subagent → rule 3 (action-verb)",
			input: Input{
				Mode:             ModeWork,
				ModeConfidence:   0.7,
				ScopeTier:        TierOpen,
				ExecutionPattern: PatternSubagent,
			},
			wantProfile: ProfileWorker,
			wantReason:  "action-verb-work",
		},
		{
			// Plan-open (rule 4) AND tier-open-subagent (rule 5) both
			// fire (mode=plan conf 0.9, tier=open, pattern=subagent) →
			// rule 4 wins.
			name: "plan-open + tier-open-subagent → rule 4 (plan)",
			input: Input{
				Mode:             ModePlan,
				ModeConfidence:   0.9,
				ScopeTier:        TierOpen,
				ExecutionPattern: PatternSubagent,
			},
			wantProfile: ProfilePlanner,
			wantReason:  "mode=plan,tier=open",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != tc.wantProfile {
				t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, tc.wantProfile)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestDeterministic_ContextCancelled asserts the broker honors a cancelled
// context (nice-to-have for downstream callers that pass a deadline).
// Since Decide is pure compute today this is informational; we still
// document the contract via this test so v2 (which may call out to a
// peer-agent) inherits the expectation.
func TestDeterministic_ContextCancelled(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// v1 deterministic broker is pure compute; cancellation is best-effort.
	// We assert it returns *some* decision without panicking, not that it
	// errors — the contract leaves the choice to the impl, and v1 chooses
	// not to plumb cancellation checks into a sub-microsecond path.
	got, err := b.Decide(ctx, Input{Mode: ModeWork, ModeConfidence: 1.0})
	if err != nil {
		// Acceptable: future impls may return ctx.Err() on cancellation.
		// v1 currently does not.
		return
	}
	if got.AgentProfile != ProfileWorker {
		t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, ProfileWorker)
	}
}

// TestDeterministic_BrokerInterface confirms *DeterministicBroker satisfies
// Broker (compile-time assertion duplicated as runtime test for clarity).
func TestDeterministic_BrokerInterface(t *testing.T) {
	var _ Broker = New()
	var _ Broker = (*DeterministicBroker)(nil)
}
