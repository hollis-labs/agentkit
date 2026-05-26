package agentlaunch

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// registry_kinds.go ships the S3.2 per-kind contract implementations on
// top of the S1.3 contract types (directory_registry.go) and the S3.1
// live registrar core (registry_core.go).
//
// Two consumer surfaces live here:
//
//   - PerKindRegistrationValidator — a func(RegistrationRecord) error
//     that plugs straight into WithRecordValidator. It dispatches on the
//     record's kind and rejects registrations whose metadata is not
//     coherent for that kind (wrong schema version, wrong interface,
//     unknown kind, malformed meta/refs).
//
//   - DecodeContract — a standalone consumer utility that, given a kind
//     and a raw contract document (the YAML body a consumer would load
//     from RegistrationSource.FilePath), unmarshals it into the matching
//     contract struct and runs its Validate.
//
// Design constraints honored here:
//
//   - D1 (local-first). All validation is purely in-process. The
//     registration validator inspects only the RegistrationRecord value
//     it is handed; DecodeContract unmarshals bytes the caller already
//     holds. No code path makes a network call.
//
//   - D2 (the directory holds resolver handles, NOT content). The
//     registration validator operates exclusively on RegistrationRecord
//     (meta + local file pointer); it never decodes or stores a contract
//     body. DecodeContract is a SEPARATE consumer-facing utility — it
//     returns a decoded contract to its caller and does not feed the
//     registrar store. There is deliberately no path that snapshots a
//     decoded body back into the registrar.

// kindSpec pins the published schema-version and interface constants for
// one registry kind so the per-kind validator can check record metadata
// without re-deriving them.
type kindSpec struct {
	schemaVersion string
	iface         string
}

// kindSpecs maps every published RegistryKind to its S1.3 schema-version
// and interface constants. All seven published kinds are covered so the
// validator accepts the full vocabulary coherently; the five initial
// registerable kinds (agent-source, skill-source, mcp-server,
// execution-template, contract-object) are the ones S3.2 fully exercises.
var kindSpecs = map[RegistryKind]kindSpec{
	RegistryKindAgentSource:       {AgentSourceSchemaVersionV1, AgentSourceInterfaceV1},
	RegistryKindSkillSource:       {SkillSourceSchemaVersionV1, SkillSourceInterfaceV1},
	RegistryKindMCPServer:         {MCPServerSchemaVersionV1, MCPServerInterfaceV1},
	RegistryKindRuntimeBinding:    {RuntimeBindingSchemaVersionV1, RuntimeBindingInterfaceV1},
	RegistryKindBootSpec:          {BootSpecSchemaVersionV1, BootSpecInterfaceV1},
	RegistryKindExecutionTemplate: {ExecutionTemplateSchemaVersionV1, ExecutionTemplateInterfaceV1},
	RegistryKindContractObject:    {ContractObjectSchemaVersionV1, ContractObjectInterfaceV1},
}

// KindRegistrationSpec reports the published schema-version and interface
// constants a RegistrationRecord must carry for the given kind. ok is
// false when k is not a published registry kind.
//
// It is exported so callers building registrations can stamp the correct
// metadata without hard-coding the constants per kind.
func KindRegistrationSpec(k RegistryKind) (schemaVersion, iface string, ok bool) {
	spec, found := kindSpecs[k]
	if !found {
		return "", "", false
	}
	return spec.schemaVersion, spec.iface, true
}

// ValidateKindRegistration validates that one RegistrationRecord's
// metadata is coherent for its declared kind:
//
//   - the kind must be a published registry kind;
//   - Meta.SchemaVersion must equal the published constant for that kind;
//   - Meta.Interface must equal the published constant for that kind;
//   - Meta.Ref and every depends_on ref must satisfy the S1.3 shape rules
//     (RegistryContractMeta.Validate / RegistryObjectRef.Validate).
//
// It validates registration metadata only; it does not decode the
// contract body (that is DecodeContract's job, kept separate per D2).
func ValidateKindRegistration(rec RegistrationRecord) error {
	kind := rec.Meta.Ref.Kind
	spec, ok := kindSpecs[kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRegistryUnknownKind, kind)
	}

	// RegistryContractMeta.Validate enforces ref shape, kind agreement,
	// non-empty schema/interface, the expected-schema and
	// expected-interface match, and every depends_on ref shape. Wrap its
	// schema/interface mismatch outcomes with the S3.2-named sentinels so
	// callers can branch precisely on "wrong version for kind".
	if err := rec.Meta.Validate(kind, spec.schemaVersion, spec.iface); err != nil {
		return mapKindMetaError(err, kind, rec.Meta, spec)
	}
	return nil
}

// mapKindMetaError re-labels the schema-version / interface mismatch
// outcomes of RegistryContractMeta.Validate with the S3.2 named
// sentinels. RegistryContractMeta.Validate reports both the
// missing-token case and the wrong-value case with the same underlying
// sentinel; S3.2 callers want a distinct, kind-aware error for a value
// that is present but wrong, so detect that case explicitly here.
func mapKindMetaError(err error, kind RegistryKind, meta RegistryContractMeta, spec kindSpec) error {
	switch {
	case meta.SchemaVersion != "" && meta.SchemaVersion != spec.schemaVersion:
		return fmt.Errorf("%w: kind %q got %q want %q",
			ErrRegistryKindSchemaMismatch, kind, meta.SchemaVersion, spec.schemaVersion)
	case meta.Interface != "" && meta.Interface != spec.iface:
		return fmt.Errorf("%w: kind %q got %q want %q",
			ErrRegistryKindInterfaceMismatch, kind, meta.Interface, spec.iface)
	default:
		return err
	}
}

// PerKindRegistrationValidator is the per-kind registration validator. Its
// signature matches WithRecordValidator exactly, so a registrar that
// enforces per-kind metadata coherence is constructed with:
//
//	r := NewInMemoryRegistrar(WithRecordValidator(PerKindRegistrationValidator))
//
// It is a thin package-level adapter over ValidateKindRegistration kept
// as a value so it can be passed directly without a closure.
func PerKindRegistrationValidator(rec RegistrationRecord) error {
	return ValidateKindRegistration(rec)
}

// compile-time assertion that PerKindRegistrationValidator satisfies the
// WithRecordValidator hook signature.
var _ func(RegistrationRecord) error = PerKindRegistrationValidator

// DecodeContract unmarshals a raw contract document into the contract
// struct for kind and runs its Validate. It is the consumer-facing way to
// validate a full contract body on demand — the body a consumer would
// load from RegistrationSource.FilePath.
//
// D2: DecodeContract is a standalone utility. It does not touch the
// registrar and the registrar never calls it; decoded bodies are returned
// to the caller, never stored. raw is YAML (the catalog document format);
// the returned RegistryContract is concrete per kind.
func DecodeContract(kind RegistryKind, raw []byte) (RegistryContract, error) {
	switch kind {
	case RegistryKindAgentSource:
		return decodeInto[AgentSourceContract](raw)
	case RegistryKindSkillSource:
		return decodeInto[SkillSourceContract](raw)
	case RegistryKindMCPServer:
		return decodeInto[MCPServerContract](raw)
	case RegistryKindRuntimeBinding:
		return decodeInto[RuntimeBindingContract](raw)
	case RegistryKindBootSpec:
		return decodeInto[BootSpecContract](raw)
	case RegistryKindExecutionTemplate:
		return decodeInto[ExecutionTemplateContract](raw)
	case RegistryKindContractObject:
		return decodeInto[ContractObjectContract](raw)
	default:
		return nil, fmt.Errorf("%w: %q", ErrRegistryUnknownKind, kind)
	}
}

// decodeInto unmarshals raw YAML into a value of contract type T, runs its
// Validate, and returns it as a RegistryContract. A decode failure is
// wrapped with ErrRegistryKindDecode; a validation failure is returned as
// the contract's own sentinel so callers branch on it directly.
func decodeInto[T interface {
	RegistryContract
}](raw []byte) (RegistryContract, error) {
	var c T
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistryKindDecode, err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
