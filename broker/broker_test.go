package broker

import (
	"context"
	"testing"
)

func TestModeBroker_Decide(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		wantProfile string
		wantReason  string
	}{
		{"chat", "chat", "default-chat", "mode=chat"},
		{"work", "work", "worker", "mode=work"},
		{"plan", "plan", "planner", "mode=plan"},
		{"empty falls back to chat", "", "default-chat", "mode=chat"},
		{"unknown falls back to chat", "novel", "default-chat", "mode=chat"},
	}
	b := NewModeBroker()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Decide(context.Background(), Input{SessionMode: tc.mode})
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if got.AgentProfile != tc.wantProfile {
				t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, tc.wantProfile)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Confidence != 1.0 {
				t.Errorf("Confidence = %v, want 1.0", got.Confidence)
			}
		})
	}
}

// TestModeBroker_IgnoresExtraFields confirms callers can populate the v1
// fields (UserText, ScopeTier, ReflexMatchID) without affecting the no-op
// decision. Future v1 impl uses these; the scaffold must accept them.
func TestModeBroker_IgnoresExtraFields(t *testing.T) {
	b := NewModeBroker()
	got, err := b.Decide(context.Background(), Input{
		UserText:      "deploy the staging cluster",
		SessionMode:   "work",
		ScopeTier:     "open",
		ReflexMatchID: "reflex_deploy_001",
	})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if got.AgentProfile != "worker" {
		t.Errorf("AgentProfile = %q, want %q", got.AgentProfile, "worker")
	}
}

// Compile-time assertion that *ModeBroker satisfies Broker.
var _ Broker = (*ModeBroker)(nil)
