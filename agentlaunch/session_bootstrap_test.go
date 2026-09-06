package agentlaunch

import (
	"errors"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Unix(123, 0).UTC() }

func TestPrepareSessionBootstrapPreassignedCanonicalSession(t *testing.T) {
	boot, err := PrepareSessionBootstrap(SessionBootstrapRequest{
		Intent:     SessionBootstrapPreassigned,
		SessionID:  "session-preassigned",
		Definition: DefinitionRef{Authority: "agent-setup", Name: "engineer", Revision: "r1"},
		Actor:      ActorRef{Authority: "nanite", ID: "planner", Durable: true},
		Runtime:    RuntimeAttemptRef{HostID: "host-a", BindingID: "binding-a", AttemptID: "attempt-a", BindingGeneration: 7},
		WorkRefs:   []WorkRef{{System: "torque", ID: "CW-1"}},
		TraceRefs:  []TraceRef{{Kind: "commit", ID: "abc123"}},
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("PrepareSessionBootstrap: %v", err)
	}
	if boot.SessionID != "session-preassigned" || boot.SessionEnvKey != CanonicalSessionEnv {
		t.Fatalf("session bootstrap identity = %+v", boot)
	}
	if boot.Publication != PublicationPrivateLocal {
		t.Fatalf("publication = %q, want private-local", boot.Publication)
	}
	if err := boot.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestPrepareSessionBootstrapAllowsMissingNativeIDsAndOwnerScopedMappings(t *testing.T) {
	boot, err := PrepareSessionBootstrap(SessionBootstrapRequest{
		Intent:    SessionBootstrapPreassigned,
		SessionID: "session-native-optional",
		ProviderMappings: []ProviderSessionMapping{
			{Owner: "local-host", Provider: "codex"},
			{Owner: "other-host", Provider: "codex", NativeSessionID: "provider-same"},
			{Owner: "local-host", Provider: "codex", NativeSessionID: "provider-same"},
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("PrepareSessionBootstrap: %v", err)
	}
	if got := boot.ProviderMappings[0].NativeSessionID; got != "" {
		t.Fatalf("missing native id fabricated as %q", got)
	}
}

func TestSessionBootstrapRejectsProviderMappingCollisionInsideOwnerScope(t *testing.T) {
	_, err := PrepareSessionBootstrap(SessionBootstrapRequest{
		Intent:    SessionBootstrapPreassigned,
		SessionID: "session-collision",
		ProviderMappings: []ProviderSessionMapping{
			{Owner: "local-host", Provider: "codex", NativeSessionID: "native-1"},
			{Owner: "local-host", Provider: "codex", NativeSessionID: "native-1"},
		},
		Now: fixedNow,
	})
	if !errors.Is(err, ErrBootstrapProviderMappingCollision) {
		t.Fatalf("error = %v, want provider mapping collision", err)
	}
}

func TestPrepareSessionBootstrapResumeAndCompactPreservePrevious(t *testing.T) {
	previous, err := PrepareSessionBootstrap(SessionBootstrapRequest{
		Intent:    SessionBootstrapPreassigned,
		SessionID: "session-existing",
		Now:       fixedNow,
	})
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	for _, intent := range []SessionBootstrapIntent{SessionBootstrapResume, SessionBootstrapCompact} {
		t.Run(string(intent), func(t *testing.T) {
			boot, err := PrepareSessionBootstrap(SessionBootstrapRequest{Intent: intent, Previous: &previous, Now: fixedNow})
			if err != nil {
				t.Fatalf("PrepareSessionBootstrap: %v", err)
			}
			if boot.SessionID != previous.SessionID {
				t.Fatalf("session = %q, want %q", boot.SessionID, previous.SessionID)
			}
		})
	}
}

func TestPrepareSessionBootstrapFreshAndForkMintNew(t *testing.T) {
	previous, err := PrepareSessionBootstrap(SessionBootstrapRequest{Intent: SessionBootstrapPreassigned, SessionID: "session-parent", Now: fixedNow})
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	seq := []string{"session-fresh", "session-child"}
	mint := func() (string, error) {
		out := seq[0]
		seq = seq[1:]
		return out, nil
	}
	fresh, err := PrepareSessionBootstrap(SessionBootstrapRequest{Intent: SessionBootstrapFresh, Previous: &previous, Mint: mint, Now: fixedNow})
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	fork, err := PrepareSessionBootstrap(SessionBootstrapRequest{Intent: SessionBootstrapFork, Previous: &previous, Mint: mint, Now: fixedNow})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if fresh.SessionID == previous.SessionID || fork.SessionID == previous.SessionID || fork.ParentSessionID != previous.SessionID {
		t.Fatalf("fresh/fork identities wrong: previous=%s fresh=%s fork=%+v", previous.SessionID, fresh.SessionID, fork)
	}
}

func TestPrepareSessionBootstrapRetriesMintCollision(t *testing.T) {
	previous, err := PrepareSessionBootstrap(SessionBootstrapRequest{Intent: SessionBootstrapPreassigned, SessionID: "session-parent", Now: fixedNow})
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	seq := []string{"session-parent", "session-new"}
	boot, err := PrepareSessionBootstrap(SessionBootstrapRequest{
		Intent:   SessionBootstrapFresh,
		Previous: &previous,
		Mint: func() (string, error) {
			out := seq[0]
			seq = seq[1:]
			return out, nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("PrepareSessionBootstrap: %v", err)
	}
	if boot.SessionID != "session-new" {
		t.Fatalf("session = %q, want session-new", boot.SessionID)
	}
}

func TestBootstrapRejectsRendererArtifactIdentity(t *testing.T) {
	for _, bad := range []string{"current", "latest", "../session", "session/child", "session child"} {
		t.Run(bad, func(t *testing.T) {
			_, err := PrepareSessionBootstrap(SessionBootstrapRequest{Intent: SessionBootstrapPreassigned, SessionID: bad, Now: fixedNow})
			if !errors.Is(err, ErrBootstrapUnsafeIdentity) {
				t.Fatalf("error = %v, want unsafe identity", err)
			}
		})
	}
}

func TestSessionBootstrapIsOfflineAndTetherIndependent(t *testing.T) {
	boot, err := PrepareSessionBootstrap(SessionBootstrapRequest{
		Intent:    SessionBootstrapFresh,
		Mint:      func() (string, error) { return "session-offline", nil },
		Now:       fixedNow,
		Runtime:   RuntimeAttemptRef{HostID: "host", BindingID: "binding", BindingGeneration: 1},
		WorkRefs:  []WorkRef{{System: "torque", ID: "CW-20260906-0026"}},
		TraceRefs: []TraceRef{{Kind: "message-contract", ID: "g02"}},
	})
	if err != nil {
		t.Fatalf("PrepareSessionBootstrap: %v", err)
	}
	if boot.Publication != PublicationPrivateLocal {
		t.Fatalf("publication = %q", boot.Publication)
	}
}

func TestDeliveryObservationVocabulary(t *testing.T) {
	obs := DeliveryObservation{
		MessageID: "message-1", DeliveryID: "delivery-1", AttemptID: "attempt-1",
		Stage: DeliveryObservedTurnSubmitted, SessionID: "session-1",
		RuntimeBindingID: "binding-1", BindingGeneration: 3,
		Provider: "codex", ObservedAt: fixedNow(),
	}
	if err := obs.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	obs.NativeSessionID = ""
	if err := obs.Validate(); err != nil {
		t.Fatalf("Validate without native id: %v", err)
	}
	obs.Stage = DeliveryObservationStage("understood")
	if !errors.Is(obs.Validate(), ErrDeliveryObservationUnknownStage) {
		t.Fatalf("unknown stage error = %v", obs.Validate())
	}
}

func TestDeliveryCapabilitiesAreExplicit(t *testing.T) {
	for _, capability := range []DeliveryCapability{
		DeliveryCapabilityPullOnly,
		DeliveryCapabilityNextTurn,
		DeliveryCapabilityBetweenToolCall,
		DeliveryCapabilityInterrupt,
		DeliveryCapabilityCancelTurn,
		DeliveryCapabilityLifecycleStop,
		DeliveryCapabilityDurableHostHandoff,
	} {
		if !capability.Valid() {
			t.Fatalf("capability %q should be valid", capability)
		}
	}
	if DeliveryCapability("magic").Valid() {
		t.Fatal("unknown capability should be invalid")
	}
}
