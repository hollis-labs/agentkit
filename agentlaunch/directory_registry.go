package agentlaunch

import (
	"fmt"
	"os"
	"time"
)

// RegistryKind is the stable directory-registry kind vocabulary.
//
// The initial set keeps runtime-binding and boot-spec distinct from
// execution-template so callers can cache or refresh them independently.
type RegistryKind string

const (
	RegistryKindAgentSource       RegistryKind = "agent-source"
	RegistryKindSkillSource       RegistryKind = "skill-source"
	RegistryKindMCPServer         RegistryKind = "mcp-server"
	RegistryKindRuntimeBinding    RegistryKind = "runtime-binding"
	RegistryKindBootSpec          RegistryKind = "boot-spec"
	RegistryKindExecutionTemplate RegistryKind = "execution-template"
	RegistryKindContractObject    RegistryKind = "contract-object"
	RegistryKindBusTopic          RegistryKind = "bus-topic"
)

// Valid reports whether k is one of the published registry kinds.
func (k RegistryKind) Valid() bool {
	switch k {
	case RegistryKindAgentSource,
		RegistryKindSkillSource,
		RegistryKindMCPServer,
		RegistryKindRuntimeBinding,
		RegistryKindBootSpec,
		RegistryKindExecutionTemplate,
		RegistryKindContractObject,
		RegistryKindBusTopic:
		return true
	default:
		return false
	}
}

const (
	AgentSourceSchemaVersionV1       = "v1alpha1"
	SkillSourceSchemaVersionV1       = "v1alpha1"
	MCPServerSchemaVersionV1         = "v1alpha1"
	RuntimeBindingSchemaVersionV1    = "v1alpha1"
	BootSpecSchemaVersionV1          = "v1alpha1"
	ExecutionTemplateSchemaVersionV1 = "v1alpha1"
	ContractObjectSchemaVersionV1    = "v1alpha1"
	BusTopicSchemaVersionV1          = "v1alpha1"
)

const (
	AgentSourceInterfaceV1       = "resolver-handle/v1"
	SkillSourceInterfaceV1       = "resolver-handle/v1"
	MCPServerInterfaceV1         = "mcp-server/v1"
	RuntimeBindingInterfaceV1    = "runtime-binding/v1"
	BootSpecInterfaceV1          = "boot-spec/v1"
	ExecutionTemplateInterfaceV1 = "execution-template/v1"
	ContractObjectInterfaceV1    = "harness-target-file/v1"
	BusTopicInterfaceV1          = "bus-topic/v1"
)

// RegistryContract is the common interface every published kind
// contract implements.
type RegistryContract interface {
	RegistryMeta() RegistryContractMeta
	Validate() error
}

// RegistryObjectRef is the stable identity handle used across register,
// deregister, query, and cross-kind references.
//
// Owner and Namespace are reserved on every kind from day one. They may
// be empty in single-tenant deployments but they are part of the stable
// address shape now to avoid later trust-boundary churn.
type RegistryObjectRef struct {
	Kind      RegistryKind `yaml:"kind" json:"kind"`
	Name      string       `yaml:"name" json:"name"`
	Owner     string       `yaml:"owner,omitempty" json:"owner,omitempty"`
	Namespace string       `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// Validate enforces the stable reference shape.
func (r RegistryObjectRef) Validate() error {
	if !r.Kind.Valid() {
		return ErrRegistryUnknownKind
	}
	if r.Name == "" {
		return ErrRegistryMissingName
	}
	return nil
}

// RegistryContractMeta carries the versioned schema and interface token
// for one published contract.
type RegistryContractMeta struct {
	Ref           RegistryObjectRef   `yaml:"ref" json:"ref"`
	SchemaVersion string              `yaml:"schema_version" json:"schema_version"`
	Interface     string              `yaml:"interface" json:"interface"`
	Labels        map[string]string   `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations   map[string]string   `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	DependsOn     []RegistryObjectRef `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
}

// Validate enforces the common contract metadata shape.
func (m RegistryContractMeta) Validate(expectedKind RegistryKind, expectedSchema, expectedInterface string) error {
	if err := m.Ref.Validate(); err != nil {
		return err
	}
	if m.Ref.Kind != expectedKind {
		return fmt.Errorf("%w: got %q want %q", ErrRegistryUnknownKind, m.Ref.Kind, expectedKind)
	}
	if m.SchemaVersion == "" {
		return ErrRegistryMissingSchemaVersion
	}
	if expectedSchema != "" && m.SchemaVersion != expectedSchema {
		return fmt.Errorf("%w: got %q want %q", ErrRegistryMissingSchemaVersion, m.SchemaVersion, expectedSchema)
	}
	if m.Interface == "" {
		return ErrRegistryMissingInterface
	}
	if expectedInterface != "" && m.Interface != expectedInterface {
		return fmt.Errorf("%w: got %q want %q", ErrRegistryMissingInterface, m.Interface, expectedInterface)
	}
	for i := range m.DependsOn {
		if err := m.DependsOn[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ResolverProtocol is the callback/resolution family used by resolver
// handles. agent-source and skill-source publish handles, never inline
// profile bodies.
type ResolverProtocol string

const (
	ResolverProtocolLocal ResolverProtocol = "local"
	ResolverProtocolMCP   ResolverProtocol = "mcp"
	ResolverProtocolHTTP  ResolverProtocol = "http"
	ResolverProtocolCmd   ResolverProtocol = "cmd"
)

// Valid reports whether p is a known resolver protocol.
func (p ResolverProtocol) Valid() bool {
	switch p {
	case ResolverProtocolLocal, ResolverProtocolMCP, ResolverProtocolHTTP, ResolverProtocolCmd:
		return true
	default:
		return false
	}
}

// ResolverHandle is the callback contract carried by resolver-backed
// kinds such as agent-source and skill-source.
type ResolverHandle struct {
	Protocol  ResolverProtocol `yaml:"protocol" json:"protocol"`
	Target    string           `yaml:"target,omitempty" json:"target,omitempty"`
	Operation string           `yaml:"operation,omitempty" json:"operation,omitempty"`
	Command   []string         `yaml:"command,omitempty" json:"command,omitempty"`
	Timeout   string           `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Gate      TrustGate        `yaml:"gate,omitempty" json:"gate,omitempty"`
}

// Validate enforces the callback-handle shape, including trust gates
// for remote/command execution surfaces.
func (h ResolverHandle) Validate() error {
	if !h.Protocol.Valid() {
		return ErrRegistryResolverUnknownProtocol
	}
	switch h.Protocol {
	case ResolverProtocolLocal:
		if h.Target == "" {
			return ErrRegistryResolverMissingTarget
		}
	case ResolverProtocolMCP:
		if h.Target == "" {
			return ErrRegistryResolverMissingTarget
		}
		if h.Operation == "" {
			return ErrRegistryResolverMissingTarget
		}
		if !h.Gate.Valid() {
			return ErrBootSpecVarTrustGateRequired
		}
	case ResolverProtocolHTTP:
		if h.Target == "" {
			return ErrRegistryResolverMissingTarget
		}
		if !h.Gate.Valid() {
			return ErrBootSpecVarTrustGateRequired
		}
	case ResolverProtocolCmd:
		if len(h.Command) == 0 {
			return ErrRegistryResolverMissingTarget
		}
		if !h.Gate.Valid() {
			return ErrBootSpecVarTrustGateRequired
		}
	}
	if h.Timeout != "" {
		if _, err := time.ParseDuration(h.Timeout); err != nil {
			return fmt.Errorf("%w: %v", ErrBootSpecVarInvalidTimeout, err)
		}
	}
	return nil
}

// SourceFreshness is the refresh stance for resolver-backed directory
// sources.
type SourceFreshness string

const (
	SourceFreshnessPinned SourceFreshness = "pinned"
	SourceFreshnessLazy   SourceFreshness = "lazy"
	SourceFreshnessFresh  SourceFreshness = "fresh"
)

// Valid reports whether f is a known source freshness policy.
func (f SourceFreshness) Valid() bool {
	switch f {
	case SourceFreshnessPinned, SourceFreshnessLazy, SourceFreshnessFresh:
		return true
	default:
		return false
	}
}

// AgentSourceContract publishes a resolver handle for composing an
// agent profile. The directory never ingests the body itself.
type AgentSourceContract struct {
	Meta       RegistryContractMeta `yaml:"meta" json:"meta"`
	Resolver   ResolverHandle       `yaml:"resolver" json:"resolver"`
	Freshness  SourceFreshness      `yaml:"freshness" json:"freshness"`
	AgentType  string               `yaml:"agent_type,omitempty" json:"agent_type,omitempty"`
	RuntimeRef *RegistryObjectRef   `yaml:"runtime_ref,omitempty" json:"runtime_ref,omitempty"`
}

func (c AgentSourceContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the resolver-handle contract for agent sources.
func (c AgentSourceContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindAgentSource, AgentSourceSchemaVersionV1, AgentSourceInterfaceV1); err != nil {
		return err
	}
	if err := c.Resolver.Validate(); err != nil {
		return err
	}
	if !c.Freshness.Valid() {
		return ErrBootSpecVarUnknownFreshness
	}
	if c.RuntimeRef != nil {
		if err := c.RuntimeRef.Validate(); err != nil {
			return err
		}
		if c.RuntimeRef.Kind != RegistryKindRuntimeBinding {
			return fmt.Errorf("%w: agent-source runtime_ref must target %q", ErrRegistryUnknownKind, RegistryKindRuntimeBinding)
		}
	}
	return nil
}

// SkillSourceContract publishes a resolver handle for composing a skill
// body at resolve time. Static snapshots are intentionally not the
// contract model.
type SkillSourceContract struct {
	Meta      RegistryContractMeta `yaml:"meta" json:"meta"`
	Resolver  ResolverHandle       `yaml:"resolver" json:"resolver"`
	Freshness SourceFreshness      `yaml:"freshness" json:"freshness"`
	SkillType string               `yaml:"skill_type,omitempty" json:"skill_type,omitempty"`
}

func (c SkillSourceContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the resolver-handle contract for skill sources.
func (c SkillSourceContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindSkillSource, SkillSourceSchemaVersionV1, SkillSourceInterfaceV1); err != nil {
		return err
	}
	if err := c.Resolver.Validate(); err != nil {
		return err
	}
	if !c.Freshness.Valid() {
		return ErrBootSpecVarUnknownFreshness
	}
	return nil
}

// MCPTransport is the server transport published for one MCP server
// contract.
type MCPTransport string

const (
	MCPTransportStdio          MCPTransport = "stdio"
	MCPTransportSSE            MCPTransport = "sse"
	MCPTransportStreamableHTTP MCPTransport = "streamable-http"
)

// Valid reports whether t is a known MCP transport.
func (t MCPTransport) Valid() bool {
	switch t {
	case MCPTransportStdio, MCPTransportSSE, MCPTransportStreamableHTTP:
		return true
	default:
		return false
	}
}

// MCPServerContract publishes one MCP server definition. It is safe to
// register locally only; the directory copy is optional enrichment.
type MCPServerContract struct {
	Meta        RegistryContractMeta `yaml:"meta" json:"meta"`
	Transport   MCPTransport         `yaml:"transport" json:"transport"`
	Command     string               `yaml:"command,omitempty" json:"command,omitempty"`
	Args        []string             `yaml:"args,omitempty" json:"args,omitempty"`
	URL         string               `yaml:"url,omitempty" json:"url,omitempty"`
	Env         map[string]string    `yaml:"env,omitempty" json:"env,omitempty"`
	ToolPrefix  string               `yaml:"tool_prefix,omitempty" json:"tool_prefix,omitempty"`
	HealthCheck *ResolverHandle      `yaml:"health_check,omitempty" json:"health_check,omitempty"`
}

func (c MCPServerContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the MCP server contract shape.
func (c MCPServerContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindMCPServer, MCPServerSchemaVersionV1, MCPServerInterfaceV1); err != nil {
		return err
	}
	if !c.Transport.Valid() {
		return ErrRegistryTransportUnknown
	}
	switch c.Transport {
	case MCPTransportStdio:
		if c.Command == "" {
			return ErrRegistryResolverMissingTarget
		}
	default:
		if c.URL == "" {
			return ErrRegistryResolverMissingTarget
		}
	}
	if c.HealthCheck != nil {
		if err := c.HealthCheck.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// RuntimeBindingContract publishes a runtime-binding record as its own
// independently-refreshable kind.
type RuntimeBindingContract struct {
	Meta    RegistryContractMeta `yaml:"meta" json:"meta"`
	Binding RuntimeBinding       `yaml:"binding" json:"binding"`
}

func (c RuntimeBindingContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the published runtime-binding contract.
func (c RuntimeBindingContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindRuntimeBinding, RuntimeBindingSchemaVersionV1, RuntimeBindingInterfaceV1); err != nil {
		return err
	}
	return c.Binding.Validate()
}

// BootSpecContract publishes a boot-spec record as its own
// independently-refreshable kind.
type BootSpecContract struct {
	Meta RegistryContractMeta `yaml:"meta" json:"meta"`
	Spec BootSpec             `yaml:"spec" json:"spec"`
}

func (c BootSpecContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the published boot-spec contract.
func (c BootSpecContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindBootSpec, BootSpecSchemaVersionV1, BootSpecInterfaceV1); err != nil {
		return err
	}
	return c.Spec.Validate()
}

// ExecutionTemplateContract is the composition layer that ties together
// an agent source, optional skill sources, boot spec, runtime binding,
// and MCP server references without collapsing those sub-kinds.
type ExecutionTemplateContract struct {
	Meta               RegistryContractMeta `yaml:"meta" json:"meta"`
	AgentSourceRef     RegistryObjectRef    `yaml:"agent_source_ref" json:"agent_source_ref"`
	SkillSourceRefs    []RegistryObjectRef  `yaml:"skill_source_refs,omitempty" json:"skill_source_refs,omitempty"`
	MCPServerRefs      []RegistryObjectRef  `yaml:"mcp_server_refs,omitempty" json:"mcp_server_refs,omitempty"`
	RuntimeBindingRef  RegistryObjectRef    `yaml:"runtime_binding_ref" json:"runtime_binding_ref"`
	BootSpecRef        RegistryObjectRef    `yaml:"boot_spec_ref" json:"boot_spec_ref"`
	ContractObjectRefs []RegistryObjectRef  `yaml:"contract_object_refs,omitempty" json:"contract_object_refs,omitempty"`
}

func (c ExecutionTemplateContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the composition contract and keeps runtime-binding
// and boot-spec as distinct referenced kinds.
func (c ExecutionTemplateContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindExecutionTemplate, ExecutionTemplateSchemaVersionV1, ExecutionTemplateInterfaceV1); err != nil {
		return err
	}
	if err := c.AgentSourceRef.Validate(); err != nil {
		return err
	}
	if c.AgentSourceRef.Kind != RegistryKindAgentSource {
		return fmt.Errorf("%w: execution-template agent_source_ref must target %q", ErrRegistryUnknownKind, RegistryKindAgentSource)
	}
	if err := c.RuntimeBindingRef.Validate(); err != nil {
		return err
	}
	if c.RuntimeBindingRef.Kind != RegistryKindRuntimeBinding {
		return fmt.Errorf("%w: execution-template runtime_binding_ref must target %q", ErrRegistryUnknownKind, RegistryKindRuntimeBinding)
	}
	if err := c.BootSpecRef.Validate(); err != nil {
		return err
	}
	if c.BootSpecRef.Kind != RegistryKindBootSpec {
		return fmt.Errorf("%w: execution-template boot_spec_ref must target %q", ErrRegistryUnknownKind, RegistryKindBootSpec)
	}
	for i := range c.SkillSourceRefs {
		if err := c.SkillSourceRefs[i].Validate(); err != nil {
			return err
		}
		if c.SkillSourceRefs[i].Kind != RegistryKindSkillSource {
			return fmt.Errorf("%w: execution-template skill_source_refs must target %q", ErrRegistryUnknownKind, RegistryKindSkillSource)
		}
	}
	for i := range c.MCPServerRefs {
		if err := c.MCPServerRefs[i].Validate(); err != nil {
			return err
		}
		if c.MCPServerRefs[i].Kind != RegistryKindMCPServer {
			return fmt.Errorf("%w: execution-template mcp_server_refs must target %q", ErrRegistryUnknownKind, RegistryKindMCPServer)
		}
	}
	for i := range c.ContractObjectRefs {
		if err := c.ContractObjectRefs[i].Validate(); err != nil {
			return err
		}
		if c.ContractObjectRefs[i].Kind != RegistryKindContractObject {
			return fmt.Errorf("%w: execution-template contract_object_refs must target %q", ErrRegistryUnknownKind, RegistryKindContractObject)
		}
	}
	return nil
}

// HarnessTargetFile is the target-file surface for the contract-object
// registry kind.
type HarnessTargetFile struct {
	ID      string               `yaml:"id" json:"id"`
	RelPath string               `yaml:"rel_path" json:"rel_path"`
	Mode    os.FileMode          `yaml:"mode,omitempty" json:"mode,omitempty"`
	Phase   MaterializationPhase `yaml:"phase,omitempty" json:"phase,omitempty"`
}

// Validate enforces the harness-target file shape.
func (t HarnessTargetFile) Validate() error {
	if t.ID == "" {
		return ErrBootFileMissingID
	}
	if t.RelPath == "" {
		return ErrBootFileMissingRelPath
	}
	if err := ValidateBootDirRelPath(t.RelPath); err != nil {
		return err
	}
	if t.Phase != "" && !t.Phase.Valid() {
		return ErrBootSpecVarUnknownPhase
	}
	return nil
}

// ContractObjectContract publishes one harness target file plus the
// contract object that should materialize there.
type ContractObjectContract struct {
	Meta   RegistryContractMeta `yaml:"meta" json:"meta"`
	Target HarnessTargetFile    `yaml:"target" json:"target"`
	Object ContractObject       `yaml:"object" json:"object"`
}

func (c ContractObjectContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the harness-target file contract.
func (c ContractObjectContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindContractObject, ContractObjectSchemaVersionV1, ContractObjectInterfaceV1); err != nil {
		return err
	}
	if err := c.Target.Validate(); err != nil {
		return err
	}
	return c.Object.Validate()
}

// RegistrationSource is the local-first pointer model used by the
// directory registry envelope. The file-backed registrar is a permanent
// first-class mode, not migration scaffolding.
type RegistrationSource struct {
	FilePath  string `yaml:"file_path" json:"file_path"`
	Digest    string `yaml:"digest,omitempty" json:"digest,omitempty"`
	Directory string `yaml:"directory,omitempty" json:"directory,omitempty"`
}

// Validate enforces the source-of-truth pointer shape.
func (s RegistrationSource) Validate() error {
	if s.FilePath == "" {
		return ErrRegistryMissingLocalRef
	}
	return nil
}

// RegistrationRecord binds a stable object handle to a local file-backed
// contract document plus optional directory enrichment metadata.
type RegistrationRecord struct {
	Meta   RegistryContractMeta `yaml:"meta" json:"meta"`
	Source RegistrationSource   `yaml:"source" json:"source"`
}

// Validate enforces the registration pointer model.
func (r RegistrationRecord) Validate() error {
	if err := r.Meta.Ref.Validate(); err != nil {
		return err
	}
	if r.Meta.SchemaVersion == "" {
		return ErrRegistryMissingSchemaVersion
	}
	if r.Meta.Interface == "" {
		return ErrRegistryMissingInterface
	}
	return r.Source.Validate()
}

// RegistryResolutionPolicy names how consumers should resolve records.
type RegistryResolutionPolicy string

const (
	RegistryResolutionLocalFirst RegistryResolutionPolicy = "local-first"
)

// Valid reports whether p is a known resolution policy.
func (p RegistryResolutionPolicy) Valid() bool {
	switch p {
	case RegistryResolutionLocalFirst:
		return true
	default:
		return false
	}
}

// RegistryOperation is the request/response envelope verb.
type RegistryOperation string

const (
	RegistryOperationRegister   RegistryOperation = "register"
	RegistryOperationDeregister RegistryOperation = "deregister"
	RegistryOperationQuery      RegistryOperation = "query"
	RegistryOperationHealth     RegistryOperation = "health"
)

// Valid reports whether o is a known registry operation.
func (o RegistryOperation) Valid() bool {
	switch o {
	case RegistryOperationRegister, RegistryOperationDeregister, RegistryOperationQuery, RegistryOperationHealth:
		return true
	default:
		return false
	}
}

// RegistrarMode describes the concrete registrar implementation serving
// the envelope.
type RegistrarMode string

const (
	RegistrarModeFileBacked RegistrarMode = "file-backed"
	RegistrarModeDirectory  RegistrarMode = "directory"
)

// Valid reports whether m is a known registrar mode.
func (m RegistrarMode) Valid() bool {
	switch m {
	case RegistrarModeFileBacked, RegistrarModeDirectory:
		return true
	default:
		return false
	}
}

// RegistryRegistrar identifies the concrete registrar handling the
// envelope.
type RegistryRegistrar struct {
	Mode         RegistrarMode `yaml:"mode" json:"mode"`
	FileRoot     string        `yaml:"file_root,omitempty" json:"file_root,omitempty"`
	DirectoryURL string        `yaml:"directory_url,omitempty" json:"directory_url,omitempty"`
}

// Validate enforces the registrar descriptor shape.
func (r RegistryRegistrar) Validate() error {
	if !r.Mode.Valid() {
		return ErrRegistryUnknownResolution
	}
	switch r.Mode {
	case RegistrarModeFileBacked:
		if r.FileRoot == "" {
			return ErrRegistryMissingLocalRef
		}
	case RegistrarModeDirectory:
		if r.DirectoryURL == "" {
			return ErrRegistryResolverMissingTarget
		}
	}
	return nil
}

// RegisterPayload is the envelope body for register.
type RegisterPayload struct {
	Record RegistrationRecord `yaml:"record" json:"record"`
	Upsert bool               `yaml:"upsert,omitempty" json:"upsert,omitempty"`
}

// DeregisterPayload is the envelope body for deregister.
type DeregisterPayload struct {
	Ref RegistryObjectRef `yaml:"ref" json:"ref"`
}

// QueryPayload is the envelope body for query.
type QueryPayload struct {
	Kinds        []RegistryKind    `yaml:"kinds,omitempty" json:"kinds,omitempty"`
	Owner        string            `yaml:"owner,omitempty" json:"owner,omitempty"`
	Namespace    string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Name         string            `yaml:"name,omitempty" json:"name,omitempty"`
	Interface    string            `yaml:"interface,omitempty" json:"interface,omitempty"`
	Labels       map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	IncludeLocal bool              `yaml:"include_local,omitempty" json:"include_local,omitempty"`
}

// HealthStatus is the registry-health result vocabulary.
type HealthStatus string

const (
	HealthStatusOK       HealthStatus = "ok"
	HealthStatusDegraded HealthStatus = "degraded"
	HealthStatusDown     HealthStatus = "down"
)

// Valid reports whether s is a known health status.
func (s HealthStatus) Valid() bool {
	switch s {
	case HealthStatusOK, HealthStatusDegraded, HealthStatusDown:
		return true
	default:
		return false
	}
}

// HealthPayload is the envelope body for health. It is request-safe and
// response-safe: callers may send it empty; responders may fill status
// and detail fields.
type HealthPayload struct {
	Status             HealthStatus `yaml:"status,omitempty" json:"status,omitempty"`
	DirectoryReachable bool         `yaml:"directory_reachable,omitempty" json:"directory_reachable,omitempty"`
	LastSyncAt         string       `yaml:"last_sync_at,omitempty" json:"last_sync_at,omitempty"`
	Detail             string       `yaml:"detail,omitempty" json:"detail,omitempty"`
}

// RegistryEnvelope is the local-first register / deregister / query /
// health envelope.
type RegistryEnvelope struct {
	Version    string                   `yaml:"version" json:"version"`
	Resolution RegistryResolutionPolicy `yaml:"resolution" json:"resolution"`
	Operation  RegistryOperation        `yaml:"operation" json:"operation"`
	Registrar  RegistryRegistrar        `yaml:"registrar" json:"registrar"`
	Register   *RegisterPayload         `yaml:"register,omitempty" json:"register,omitempty"`
	Deregister *DeregisterPayload       `yaml:"deregister,omitempty" json:"deregister,omitempty"`
	Query      *QueryPayload            `yaml:"query,omitempty" json:"query,omitempty"`
	Health     *HealthPayload           `yaml:"health,omitempty" json:"health,omitempty"`
}

// Validate enforces the operation-to-payload mapping for the registry
// envelope.
func (e RegistryEnvelope) Validate() error {
	if e.Version == "" {
		return ErrRegistryMissingSchemaVersion
	}
	if !e.Resolution.Valid() {
		return ErrRegistryUnknownResolution
	}
	if !e.Operation.Valid() {
		return ErrRegistryUnknownOperation
	}
	if err := e.Registrar.Validate(); err != nil {
		return err
	}
	switch e.Operation {
	case RegistryOperationRegister:
		if e.Register == nil {
			return ErrRegistryMissingPayload
		}
		return e.Register.Record.Validate()
	case RegistryOperationDeregister:
		if e.Deregister == nil {
			return ErrRegistryMissingPayload
		}
		return e.Deregister.Ref.Validate()
	case RegistryOperationQuery:
		if e.Query == nil {
			return ErrRegistryMissingPayload
		}
		for i := range e.Query.Kinds {
			if !e.Query.Kinds[i].Valid() {
				return ErrRegistryUnknownKind
			}
		}
	case RegistryOperationHealth:
		if e.Health == nil {
			return ErrRegistryMissingPayload
		}
		if e.Health.Status != "" && !e.Health.Status.Valid() {
			return ErrRegistryUnknownHealthStatus
		}
	}
	return nil
}
