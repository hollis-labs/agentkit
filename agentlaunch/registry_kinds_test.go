package agentlaunch

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

// kindMeta builds a coherent RegistryContractMeta for kind using the
// published schema-version and interface constants for that kind.
func kindMeta(kind RegistryKind, name string) RegistryContractMeta {
	sv, iface, _ := KindRegistrationSpec(kind)
	return RegistryContractMeta{
		Ref:           RegistryObjectRef{Kind: kind, Name: name},
		SchemaVersion: sv,
		Interface:     iface,
	}
}

// kindRecord builds a structurally-valid RegistrationRecord (coherent
// meta + local file pointer) for kind.
func kindRecord(kind RegistryKind, name string) RegistrationRecord {
	return RegistrationRecord{
		Meta:   kindMeta(kind, name),
		Source: RegistrationSource{FilePath: "/catalog/" + name + ".yaml"},
	}
}

// initialKinds is the S3.2 initial-kinds set that must validate on
// register and be fully covered by tests.
var initialKinds = []RegistryKind{
	RegistryKindAgentSource,
	RegistryKindSkillSource,
	RegistryKindMCPServer,
	RegistryKindExecutionTemplate,
	RegistryKindContractObject,
}

func TestPerKindRegistrationValidatorAcceptsEveryKind(t *testing.T) {
	// Every published kind, not just the five initial ones, validates
	// coherently — runtime-binding and boot-spec are in the vocabulary.
	allKinds := append([]RegistryKind{}, initialKinds...)
	allKinds = append(allKinds, RegistryKindRuntimeBinding, RegistryKindBootSpec)

	for _, kind := range allKinds {
		t.Run(string(kind), func(t *testing.T) {
			rec := kindRecord(kind, "obj."+string(kind))
			if err := PerKindRegistrationValidator(rec); err != nil {
				t.Fatalf("PerKindRegistrationValidator(%s) = %v, want nil", kind, err)
			}
		})
	}
}

func TestPerKindRegistrationValidatorRejections(t *testing.T) {
	cases := []struct {
		name string
		rec  RegistrationRecord
		want error
	}{
		{
			name: "unknown kind",
			rec: RegistrationRecord{
				Meta: RegistryContractMeta{
					Ref:           RegistryObjectRef{Kind: RegistryKind("bus-topic"), Name: "x"},
					SchemaVersion: "v1alpha1",
					Interface:     "bus-topic/v1",
				},
				Source: RegistrationSource{FilePath: "/catalog/x.yaml"},
			},
			want: ErrRegistryUnknownKind,
		},
		{
			name: "wrong schema version",
			rec: func() RegistrationRecord {
				r := kindRecord(RegistryKindAgentSource, "a")
				r.Meta.SchemaVersion = "v0deprecated"
				return r
			}(),
			want: ErrRegistryKindSchemaMismatch,
		},
		{
			name: "wrong interface",
			rec: func() RegistrationRecord {
				r := kindRecord(RegistryKindMCPServer, "m")
				r.Meta.Interface = "resolver-handle/v1"
				return r
			}(),
			want: ErrRegistryKindInterfaceMismatch,
		},
		{
			name: "malformed meta: missing name",
			rec: func() RegistrationRecord {
				r := kindRecord(RegistryKindSkillSource, "s")
				r.Meta.Ref.Name = ""
				return r
			}(),
			want: ErrRegistryMissingName,
		},
		{
			name: "malformed meta: missing schema version",
			rec: func() RegistrationRecord {
				r := kindRecord(RegistryKindContractObject, "c")
				r.Meta.SchemaVersion = ""
				return r
			}(),
			want: ErrRegistryMissingSchemaVersion,
		},
		{
			name: "malformed meta: missing interface",
			rec: func() RegistrationRecord {
				r := kindRecord(RegistryKindExecutionTemplate, "e")
				r.Meta.Interface = ""
				return r
			}(),
			want: ErrRegistryMissingInterface,
		},
		{
			name: "malformed depends_on ref",
			rec: func() RegistrationRecord {
				r := kindRecord(RegistryKindExecutionTemplate, "e")
				r.Meta.DependsOn = []RegistryObjectRef{
					{Kind: RegistryKindAgentSource, Name: ""},
				}
				return r
			}(),
			want: ErrRegistryMissingName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := PerKindRegistrationValidator(tc.rec)
			if !errors.Is(err, tc.want) {
				t.Fatalf("PerKindRegistrationValidator() err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestKindRegistrationSpec(t *testing.T) {
	sv, iface, ok := KindRegistrationSpec(RegistryKindAgentSource)
	if !ok || sv != AgentSourceSchemaVersionV1 || iface != AgentSourceInterfaceV1 {
		t.Fatalf("KindRegistrationSpec(agent-source) = %q,%q,%v", sv, iface, ok)
	}
	if _, _, ok := KindRegistrationSpec(RegistryKind("nope")); ok {
		t.Fatalf("KindRegistrationSpec(nope) ok = true, want false")
	}
}

func TestRegistrarEnforcesPerKindValidationEndToEnd(t *testing.T) {
	r := NewInMemoryRegistrar(WithRecordValidator(PerKindRegistrationValidator))

	// A coherent record registers fine through Handle.
	good := kindRecord(RegistryKindAgentSource, "nanite.backend")
	if _, err := r.Handle(registerEnvelope(good, false)); err != nil {
		t.Fatalf("register coherent record: unexpected error %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}

	// A record with the wrong interface for its kind is rejected through
	// Handle and never reaches the store.
	bad := kindRecord(RegistryKindAgentSource, "nanite.frontend")
	bad.Meta.Interface = MCPServerInterfaceV1
	_, err := r.Handle(registerEnvelope(bad, false))
	if !errors.Is(err, ErrRegistryKindInterfaceMismatch) {
		t.Fatalf("register bad record: err = %v, want ErrRegistryKindInterfaceMismatch", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() after rejected register = %d, want 1", r.Len())
	}

	// A wrong schema version is also rejected end-to-end.
	badSchema := kindRecord(RegistryKindMCPServer, "torque-loopback")
	badSchema.Meta.SchemaVersion = "v0deprecated"
	if _, err := r.Handle(registerEnvelope(badSchema, false)); !errors.Is(err, ErrRegistryKindSchemaMismatch) {
		t.Fatalf("register bad schema: err = %v, want ErrRegistryKindSchemaMismatch", err)
	}
}

// --- DecodeContract -----------------------------------------------------

// marshalYAML renders a contract to YAML bytes so decode tests exercise a
// real round-trip without hand-writing documents.
func marshalYAML(t *testing.T, v any) []byte {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	return b
}

func validAgentSourceContract() AgentSourceContract {
	return AgentSourceContract{
		Meta:      kindMeta(RegistryKindAgentSource, "nanite.backend"),
		Resolver:  ResolverHandle{Protocol: ResolverProtocolLocal, Target: "app://nanite"},
		Freshness: SourceFreshnessFresh,
	}
}

func validSkillSourceContract() SkillSourceContract {
	return SkillSourceContract{
		Meta:      kindMeta(RegistryKindSkillSource, "review"),
		Resolver:  ResolverHandle{Protocol: ResolverProtocolLocal, Target: "app://nanite"},
		Freshness: SourceFreshnessLazy,
	}
}

func validMCPServerContract() MCPServerContract {
	return MCPServerContract{
		Meta:      kindMeta(RegistryKindMCPServer, "torque-loopback"),
		Transport: MCPTransportStdio,
		Command:   "torque",
	}
}

func validExecutionTemplateContract() ExecutionTemplateContract {
	return ExecutionTemplateContract{
		Meta:              kindMeta(RegistryKindExecutionTemplate, "nanite.backend.main"),
		AgentSourceRef:    RegistryObjectRef{Kind: RegistryKindAgentSource, Name: "nanite.backend"},
		RuntimeBindingRef: RegistryObjectRef{Kind: RegistryKindRuntimeBinding, Name: "codex.jsonrpc"},
		BootSpecRef:       RegistryObjectRef{Kind: RegistryKindBootSpec, Name: "nanite.backend.boot"},
	}
}

func validContractObjectContract() ContractObjectContract {
	return ContractObjectContract{
		Meta:   kindMeta(RegistryKindContractObject, "agents-md"),
		Target: HarnessTargetFile{ID: "agents-md", RelPath: "AGENTS.md", Phase: VarPhaseBuild},
		Object: ContractObject{Kind: ContractObjectSlot, Ref: "agents_md"},
	}
}

func TestDecodeContractEveryKind(t *testing.T) {
	cases := []struct {
		kind     RegistryKind
		contract RegistryContract
	}{
		{RegistryKindAgentSource, validAgentSourceContract()},
		{RegistryKindSkillSource, validSkillSourceContract()},
		{RegistryKindMCPServer, validMCPServerContract()},
		{RegistryKindExecutionTemplate, validExecutionTemplateContract()},
		{RegistryKindContractObject, validContractObjectContract()},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			raw := marshalYAML(t, tc.contract)
			got, err := DecodeContract(tc.kind, raw)
			if err != nil {
				t.Fatalf("DecodeContract(%s) = %v, want nil", tc.kind, err)
			}
			if got.RegistryMeta().Ref.Kind != tc.kind {
				t.Fatalf("decoded kind = %q, want %q", got.RegistryMeta().Ref.Kind, tc.kind)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("decoded contract Validate() = %v", err)
			}
		})
	}
}

// runtime-binding and boot-spec decode too even though they are not in
// the initial registerable-kinds set.
func TestDecodeContractVocabularyKinds(t *testing.T) {
	rb := RuntimeBindingContract{
		Meta:    kindMeta(RegistryKindRuntimeBinding, "codex.jsonrpc"),
		Binding: RuntimeBinding{Provider: "codex", RuntimeKind: RuntimeJsonRpcStdio},
	}
	if _, err := DecodeContract(RegistryKindRuntimeBinding, marshalYAML(t, rb)); err != nil {
		t.Fatalf("DecodeContract(runtime-binding) = %v", err)
	}

	bs := BootSpecContract{
		Meta: kindMeta(RegistryKindBootSpec, "nanite.backend.boot"),
		Spec: BootSpec{Runtime: RuntimeBinding{Provider: "codex", RuntimeKind: RuntimeJsonRpcStdio}},
	}
	if _, err := DecodeContract(RegistryKindBootSpec, marshalYAML(t, bs)); err != nil {
		t.Fatalf("DecodeContract(boot-spec) = %v", err)
	}
}

func TestDecodeContractRejections(t *testing.T) {
	t.Run("unknown kind", func(t *testing.T) {
		_, err := DecodeContract(RegistryKind("bus-topic"), []byte("{}"))
		if !errors.Is(err, ErrRegistryUnknownKind) {
			t.Fatalf("DecodeContract(unknown) err = %v, want ErrRegistryUnknownKind", err)
		}
	})

	t.Run("malformed yaml", func(t *testing.T) {
		_, err := DecodeContract(RegistryKindAgentSource, []byte("this: : not: yaml:"))
		if !errors.Is(err, ErrRegistryKindDecode) {
			t.Fatalf("DecodeContract(bad yaml) err = %v, want ErrRegistryKindDecode", err)
		}
	})

	t.Run("decoded contract fails Validate", func(t *testing.T) {
		// Valid YAML, but the contract body is incoherent: an MCP stdio
		// server with no command.
		bad := validMCPServerContract()
		bad.Command = ""
		_, err := DecodeContract(RegistryKindMCPServer, marshalYAML(t, bad))
		if !errors.Is(err, ErrRegistryResolverMissingTarget) {
			t.Fatalf("DecodeContract(incoherent body) err = %v, want ErrRegistryResolverMissingTarget", err)
		}
	})

	t.Run("decoded contract wrong schema version fails Validate", func(t *testing.T) {
		bad := validAgentSourceContract()
		bad.Meta.SchemaVersion = "v0deprecated"
		_, err := DecodeContract(RegistryKindAgentSource, marshalYAML(t, bad))
		if !errors.Is(err, ErrRegistryMissingSchemaVersion) {
			t.Fatalf("DecodeContract(wrong schema) err = %v, want ErrRegistryMissingSchemaVersion", err)
		}
	})
}
