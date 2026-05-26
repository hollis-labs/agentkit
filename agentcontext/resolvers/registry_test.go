package resolvers

import (
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func TestDefault_RegisterAllSevenKinds(t *testing.T) {
	t.Parallel()
	got := Default()
	want := []agentcontext.SlotSourceKind{
		agentcontext.SlotSourceKindStaticFile,
		agentcontext.SlotSourceKindStaticDir,
		agentcontext.SlotSourceKindInline,
		agentcontext.SlotSourceKindCmd,
		agentcontext.SlotSourceKindHTTPText,
		agentcontext.SlotSourceKindHTTPJSON,
		agentcontext.SlotSourceKindRoleSummary,
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("Default() missing kind %q", k)
		}
	}
	if _, ok := got[agentcontext.SlotSourceKindSkillIndex]; ok {
		t.Errorf("Default() must NOT include skill_index (Subagent C territory)")
	}
	if len(got) != len(want) {
		t.Errorf("Default() returned %d kinds, want %d", len(got), len(want))
	}
}

func TestDefault_PluggableIntoProvider(t *testing.T) {
	t.Parallel()
	// Smoke: registry → NewProvider should succeed.
	if _, err := agentcontext.NewProvider(Default(), agentcontext.DefaultRenderer{}); err != nil {
		t.Fatalf("NewProvider with Default(): %v", err)
	}
}
