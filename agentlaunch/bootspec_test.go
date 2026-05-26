package agentlaunch

import (
	"errors"
	"testing"
)

func TestRuntimeBindingValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		b := RuntimeBinding{
			Provider:    "codex",
			Model:       "gpt-5",
			RuntimeKind: RuntimeJsonRpcStdio,
			Args:        []string{"--fast"},
			Timeout:     "30s",
		}
		if err := b.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("missing provider", func(t *testing.T) {
		err := (RuntimeBinding{RuntimeKind: RuntimePTY}).Validate()
		if !errors.Is(err, ErrRuntimeBindingMissingProvider) {
			t.Fatalf("Validate() error = %v, want ErrRuntimeBindingMissingProvider", err)
		}
	})

	t.Run("invalid timeout", func(t *testing.T) {
		err := (RuntimeBinding{
			Provider:    "codex",
			RuntimeKind: RuntimePTY,
			Timeout:     "later",
		}).Validate()
		if !errors.Is(err, ErrRuntimeBindingInvalidTimeout) {
			t.Fatalf("Validate() error = %v, want ErrRuntimeBindingInvalidTimeout", err)
		}
	})
}

func TestBootSpecValidate(t *testing.T) {
	spec := BootSpec{
		Inputs: []BootInput{
			{Name: "ticket", Type: "string", Required: true, Description: "task id"},
		},
		Files: []BootFileSpec{
			{
				ID:      "agents-md",
				RelPath: "AGENTS.md",
				Object:  ContractObject{Kind: ContractObjectSlot, Ref: "agents_md"},
				Phase:   VarPhaseBuild,
			},
		},
		Vars: []VarSpec{
			{
				Name:      "role_summary",
				Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "roles/backend.md", TrimSpace: true}},
				Freshness: VarFreshnessCacheOK,
				OnError:   VarOnErrorAbort,
				Phase:     VarPhaseAuto,
			},
			{
				Name: "task_title",
				Source: VarSource{
					Kind: VarSourceCall,
					Call: &VarCallRef{
						Transport: CallTransportMCP,
						Target:    "torque.task.get",
						Args:      map[string]any{"id": "CW-1"},
						Timeout:   "5s",
						Gate:      TrustGate{Authorization: "task-metadata-read"},
					},
				},
				Freshness: VarFreshnessFresh,
				OnError:   VarOnErrorFallback,
				Fallback:  "unknown",
				Phase:     VarPhaseSessionStart,
			},
		},
		Injections: []BootInjectionSpec{
			{
				ID:     "skill-review",
				Kind:   NativeFileSkill,
				Name:   "review",
				Object: ContractObject{Kind: ContractObjectVar, Ref: "role_summary"},
				Phase:  VarPhaseBuild,
			},
		},
		Runtime: RuntimeBinding{
			Provider:    "codex",
			Model:       "gpt-5",
			RuntimeKind: RuntimeJsonRpcStdio,
			Args:        []string{"serve"},
			Timeout:     "30s",
		},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestBootSpecRejectsDuplicateInputs(t *testing.T) {
	spec := BootSpec{
		Inputs: []BootInput{
			{Name: "ticket", Type: "string"},
			{Name: "ticket", Type: "string"},
		},
		Runtime: RuntimeBinding{
			Provider:    "codex",
			RuntimeKind: RuntimePTY,
		},
	}
	err := spec.Validate()
	if !errors.Is(err, ErrBootSpecDuplicateInput) {
		t.Fatalf("Validate() error = %v, want ErrBootSpecDuplicateInput", err)
	}
}

func TestVarSpecRejectsSecretInlineValue(t *testing.T) {
	v := VarSpec{
		Name:      "api_key",
		Source:    VarSource{Kind: VarSourceLiteral, Literal: "secret"},
		Freshness: VarFreshnessBestEffort,
		OnError:   VarOnErrorPermanentFail,
		Phase:     VarPhaseSessionStart,
		Secret:    true,
	}
	err := v.Validate()
	if !errors.Is(err, ErrBootSpecVarSecretInlineValue) {
		t.Fatalf("Validate() error = %v, want ErrBootSpecVarSecretInlineValue", err)
	}
}

func TestVarSourceRequiresTrustGateForCallAndCmd(t *testing.T) {
	t.Run("call", func(t *testing.T) {
		err := (VarSource{
			Kind: VarSourceCall,
			Call: &VarCallRef{
				Transport: CallTransportHTTP,
				Target:    "https://example.com/value",
			},
		}).Validate()
		if !errors.Is(err, ErrBootSpecVarTrustGateRequired) {
			t.Fatalf("Validate() error = %v, want ErrBootSpecVarTrustGateRequired", err)
		}
	})

	t.Run("cmd", func(t *testing.T) {
		err := (VarSource{
			Kind: VarSourceCmd,
			Cmd: &VarCmdRef{
				Argv: []string{"git", "rev-parse", "HEAD"},
			},
		}).Validate()
		if !errors.Is(err, ErrBootSpecVarTrustGateRequired) {
			t.Fatalf("Validate() error = %v, want ErrBootSpecVarTrustGateRequired", err)
		}
	})
}
