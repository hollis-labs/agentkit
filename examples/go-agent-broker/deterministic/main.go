// Package main demonstrates the DeterministicBroker's priority-ordered
// rule set. Each call below is shaped to fire a different rule, in
// priority order, plus the default-chat fallback.
//
// Run with: go run ./examples/deterministic
//
// Expected output:
//
//	rule=1 reflex                      → profile=deployer     reason=reflex:deploy_staging           confidence=0.95
//	rule=2 high-conf work mode         → profile=worker       reason=mode=work                       confidence=0.95
//	rule=3 action-verb work mode       → profile=worker       reason=action-verb-work                confidence=0.70
//	rule=4 high-conf plan @ open tier  → profile=planner      reason=mode=plan,tier=open             confidence=0.90
//	rule=5 open tier × subagent ptrn   → profile=planner      reason=tier=open,pattern=subagent      confidence=0.75
//	rule=6 default-chat fallback       → profile=             reason=default-chat-handle             confidence=0.00
package main

import (
	"context"
	"fmt"

	"github.com/hollis-labs/agentkit/broker"
)

func main() {
	b := broker.New()
	ctx := context.Background()

	type case_ struct {
		label string
		input broker.Input
	}

	cases := []case_{
		{
			label: "rule=1 reflex                     ",
			input: broker.Input{
				UserText:         "deploy staging",
				Mode:             broker.ModeWork, // would normally fire rule 2…
				ModeConfidence:   1.0,
				ReflexMatchID:    "deploy_staging", // …but reflex wins.
				ReflexAgentSlug:  "deployer",
				ReflexConfidence: 0.95,
			},
		},
		{
			label: "rule=2 high-conf work mode        ",
			input: broker.Input{
				UserText:       "implement the foo refactor",
				Mode:           broker.ModeWork,
				ModeConfidence: 0.95,
			},
		},
		{
			label: "rule=3 action-verb work mode      ",
			input: broker.Input{
				UserText:       "let's add a small helper for bar",
				Mode:           broker.ModeWork,
				ModeConfidence: 0.70,
			},
		},
		{
			label: "rule=4 high-conf plan @ open tier ",
			input: broker.Input{
				UserText:       "plan the migration of the entire pipeline",
				Mode:           broker.ModePlan,
				ModeConfidence: 0.90,
				ScopeTier:      broker.TierOpen,
			},
		},
		{
			label: "rule=5 open tier × subagent ptrn  ",
			input: broker.Input{
				UserText:         "research the foo landscape exhaustively",
				Mode:             broker.ModeChat, // chat-mode, but…
				ModeConfidence:   0.30,
				ScopeTier:        broker.TierOpen,        // …open scope and…
				ExecutionPattern: broker.PatternSubagent, // …subagent pattern still routes to planner.
			},
		},
		{
			label: "rule=6 default-chat fallback      ",
			input: broker.Input{
				UserText:       "what's the weather today?",
				Mode:           broker.ModeChat,
				ModeConfidence: 0.10,
			},
		},
	}

	for _, c := range cases {
		d, err := b.Decide(ctx, c.input)
		if err != nil {
			fmt.Printf("%s → error: %v\n", c.label, err)
			continue
		}
		fmt.Printf("%s → profile=%-12s reason=%-31s confidence=%.2f\n",
			c.label, d.AgentProfile, d.Reason, d.Confidence)
	}
}
