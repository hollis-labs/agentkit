package agentruntime

import "testing"

func TestFacadeConstants(t *testing.T) {
	if ModulePath != "github.com/hollis-labs/agentkit/agentruntime" {
		t.Fatalf("ModulePath = %q", ModulePath)
	}
	if Scope == "" {
		t.Fatal("Scope is empty")
	}
}
