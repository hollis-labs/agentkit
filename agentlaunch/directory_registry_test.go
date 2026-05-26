package agentlaunch

import (
	"errors"
	"testing"
)

func TestAgentSourceContractValidate(t *testing.T) {
	contract := AgentSourceContract{
		Meta: RegistryContractMeta{
			Ref: RegistryObjectRef{
				Kind:      RegistryKindAgentSource,
				Name:      "nanite.backend",
				Owner:     "nanite",
				Namespace: "prod",
			},
			SchemaVersion: AgentSourceSchemaVersionV1,
			Interface:     AgentSourceInterfaceV1,
		},
		Resolver: ResolverHandle{
			Protocol:  ResolverProtocolMCP,
			Target:    "app://nanite",
			Operation: "compose_agent",
			Timeout:   "5s",
			Gate:      TrustGate{Authorization: "agent-compose"},
		},
		Freshness: SourceFreshnessFresh,
		RuntimeRef: &RegistryObjectRef{
			Kind:      RegistryKindRuntimeBinding,
			Name:      "codex.jsonrpc",
			Owner:     "nanite",
			Namespace: "prod",
		},
	}
	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSkillSourceContractRequiresResolverGate(t *testing.T) {
	contract := SkillSourceContract{
		Meta: RegistryContractMeta{
			Ref: RegistryObjectRef{
				Kind: RegistryKindSkillSource,
				Name: "review",
			},
			SchemaVersion: SkillSourceSchemaVersionV1,
			Interface:     SkillSourceInterfaceV1,
		},
		Resolver: ResolverHandle{
			Protocol:  ResolverProtocolMCP,
			Target:    "app://nanite",
			Operation: "compose_skill",
		},
		Freshness: SourceFreshnessLazy,
	}
	err := contract.Validate()
	if !errors.Is(err, ErrBootSpecVarTrustGateRequired) {
		t.Fatalf("Validate() error = %v, want ErrBootSpecVarTrustGateRequired", err)
	}
}

func TestExecutionTemplateKeepsBootSpecAndRuntimeBindingDistinct(t *testing.T) {
	contract := ExecutionTemplateContract{
		Meta: RegistryContractMeta{
			Ref: RegistryObjectRef{
				Kind: RegistryKindExecutionTemplate,
				Name: "nanite.backend.main",
			},
			SchemaVersion: ExecutionTemplateSchemaVersionV1,
			Interface:     ExecutionTemplateInterfaceV1,
		},
		AgentSourceRef: RegistryObjectRef{
			Kind: RegistryKindAgentSource,
			Name: "nanite.backend",
		},
		RuntimeBindingRef: RegistryObjectRef{
			Kind: RegistryKindRuntimeBinding,
			Name: "codex.jsonrpc",
		},
		BootSpecRef: RegistryObjectRef{
			Kind: RegistryKindBootSpec,
			Name: "nanite.backend.boot",
		},
		SkillSourceRefs: []RegistryObjectRef{
			{Kind: RegistryKindSkillSource, Name: "review"},
		},
		MCPServerRefs: []RegistryObjectRef{
			{Kind: RegistryKindMCPServer, Name: "torque-loopback"},
		},
		ContractObjectRefs: []RegistryObjectRef{
			{Kind: RegistryKindContractObject, Name: "agents-md"},
		},
	}
	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestContractObjectContractValidate(t *testing.T) {
	contract := ContractObjectContract{
		Meta: RegistryContractMeta{
			Ref: RegistryObjectRef{
				Kind: RegistryKindContractObject,
				Name: "agents-md",
			},
			SchemaVersion: ContractObjectSchemaVersionV1,
			Interface:     ContractObjectInterfaceV1,
		},
		Target: HarnessTargetFile{
			ID:      "agents-md",
			RelPath: "AGENTS.md",
			Phase:   VarPhaseBuild,
		},
		Object: ContractObject{
			Kind: ContractObjectSlot,
			Ref:  "agents_md",
		},
	}
	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRegistryEnvelopeRequiresLocalFileBackedRegistration(t *testing.T) {
	envelope := RegistryEnvelope{
		Version:    "v1alpha1",
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationRegister,
		Registrar: RegistryRegistrar{
			Mode:     RegistrarModeFileBacked,
			FileRoot: "/catalog",
		},
		Register: &RegisterPayload{
			Record: RegistrationRecord{
				Meta: RegistryContractMeta{
					Ref: RegistryObjectRef{
						Kind:      RegistryKindAgentSource,
						Name:      "nanite.backend",
						Owner:     "nanite",
						Namespace: "prod",
					},
					SchemaVersion: AgentSourceSchemaVersionV1,
					Interface:     AgentSourceInterfaceV1,
				},
			},
		},
	}
	err := envelope.Validate()
	if !errors.Is(err, ErrRegistryMissingLocalRef) {
		t.Fatalf("Validate() error = %v, want ErrRegistryMissingLocalRef", err)
	}
}
