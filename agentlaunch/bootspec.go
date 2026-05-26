package agentlaunch

import (
	"context"
	"fmt"
	"os"
	"time"
)

// RuntimeBinding is the frozen hot-path-readable runtime contract the
// orchestrators share. It contains only the fields a caller needs to
// pick a provider/model/runtime shape synchronously, without resolving
// the richer BootSpec blueprint.
//
// RuntimeKind is canonical here: consumers should converge on
// agentlaunch.RuntimeKind rather than carrying local runtime enums.
type RuntimeBinding struct {
	// Provider is the stable provider identifier (claude / codex /
	// opencode / future). Required.
	Provider string `yaml:"provider" json:"provider"`

	// Model is the provider-defined model selector. Optional.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`

	// RuntimeKind is the canonical shared runtime enum. Required.
	RuntimeKind RuntimeKind `yaml:"runtime_kind" json:"runtime_kind"`

	// Args is the provider/runtime argument vector overlay, excluding the
	// binary path. Consumer overlays still win on conflict for
	// runtime-critical fields at LaunchPlan/Prepare time.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

	// Timeout is the runtime-side execution timeout as a Go duration
	// string (for example "30s"). Optional.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Permission is the spawned agent's permission/approval posture, in
	// the provider's OWN vocabulary — interpreted against Provider:
	//
	//   - claude → permission_mode (default / acceptEdits / plan /
	//     bypassPermissions). Empty plants no `permissions.defaultMode`,
	//     leaving a headless claude in interactive `default` mode — it
	//     will hang on the first approval prompt. A headless claude
	//     runtime-binding should set this; acceptEdits is the safe
	//     non-interactive middle ground.
	//   - codex  → approval_policy (untrusted / on-failure / on-request /
	//     never). Empty is safe — go-providers defaults it to `never`.
	//
	// Carried verbatim onto LaunchPlan.Provider.Permission by
	// PlanFromLaunch and applied to the go-providers adapter by
	// providerplant.DefaultResolver. The adapter validates the value at
	// boot-dir render time. Optional.
	Permission string `yaml:"permission,omitempty" json:"permission,omitempty"`
}

// Validate enforces the frozen RuntimeBinding field contract.
func (r RuntimeBinding) Validate() error {
	if r.Provider == "" {
		return ErrRuntimeBindingMissingProvider
	}
	if !r.RuntimeKind.Valid() {
		return ErrUnknownRuntime
	}
	if r.Timeout != "" {
		if _, err := time.ParseDuration(r.Timeout); err != nil {
			return fmt.Errorf("%w: %v", ErrRuntimeBindingInvalidTimeout, err)
		}
	}
	return nil
}

// BootSpec is the frozen parameterized boot blueprint shared across
// Torque, Nanite, and Tether. It is distinct from RuntimeBinding: this
// type describes how boot material is assembled, while RuntimeBinding is
// the already-resolved runtime selection.
type BootSpec struct {
	// Inputs is the public typed input surface. Shape intentionally
	// matches the Hadron blueprint convention:
	// name / type / required / default / description.
	Inputs []BootInput `yaml:"inputs,omitempty" json:"inputs,omitempty"`

	// Files is the path-safe bootdir file set the library materializer
	// owns.
	Files []BootFileSpec `yaml:"files,omitempty" json:"files,omitempty"`

	// Vars is the derived-value layer. Vars resolve before render and may
	// read literals, files, calls, or commands subject to freshness,
	// secret, and trust-gate policy.
	Vars []VarSpec `yaml:"vars,omitempty" json:"vars,omitempty"`

	// Injections is the provider-native / overlay material the library
	// owns and writes via its materializer.
	Injections []BootInjectionSpec `yaml:"injections,omitempty" json:"injections,omitempty"`

	// Runtime is the resolved runtime contract associated with this boot
	// blueprint.
	Runtime RuntimeBinding `yaml:"runtime" json:"runtime"`
}

// Validate enforces field-shape correctness on the frozen BootSpec
// contract.
func (s BootSpec) Validate() error {
	seenInputs := make(map[string]struct{}, len(s.Inputs))
	for i := range s.Inputs {
		in := s.Inputs[i]
		if err := in.Validate(); err != nil {
			return err
		}
		if _, dup := seenInputs[in.Name]; dup {
			return fmt.Errorf("%w: %s", ErrBootSpecDuplicateInput, in.Name)
		}
		seenInputs[in.Name] = struct{}{}
	}
	for i := range s.Files {
		if err := s.Files[i].Validate(); err != nil {
			return err
		}
	}
	for i := range s.Vars {
		if err := s.Vars[i].Validate(); err != nil {
			return err
		}
	}
	for i := range s.Injections {
		if err := s.Injections[i].Validate(); err != nil {
			return err
		}
	}
	return s.Runtime.Validate()
}

// BootInput is one typed parameter exposed by a BootSpec. Type is an
// open token so the shared library freezes the shape without forcing all
// consumers to adopt one closed enum prematurely.
type BootInput struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Default     any    `yaml:"default,omitempty" json:"default,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Validate enforces the Hadron-style input shape.
func (i BootInput) Validate() error {
	if i.Name == "" {
		return ErrBootInputMissingName
	}
	if i.Type == "" {
		return ErrBootInputMissingType
	}
	return nil
}

// ContractObjectKind is the frozen vocabulary the library materializer
// understands without a consumer-specific callback.
type ContractObjectKind string

const (
	// ContractObjectLiteral is inline persisted content. Avoid it for
	// secret-bearing material.
	ContractObjectLiteral ContractObjectKind = "literal"

	// ContractObjectInput projects one named BootSpec input directly.
	ContractObjectInput ContractObjectKind = "input"

	// ContractObjectVar projects one resolved BootSpec var directly.
	ContractObjectVar ContractObjectKind = "var"

	// ContractObjectSlot delegates rendering to the consumer-pluggable
	// slot renderer.
	ContractObjectSlot ContractObjectKind = "slot"
)

// Valid reports whether k is a known contract object kind.
func (k ContractObjectKind) Valid() bool {
	switch k {
	case ContractObjectLiteral, ContractObjectInput, ContractObjectVar, ContractObjectSlot:
		return true
	default:
		return false
	}
}

// ContractObject points at the content a file/injection should
// materialize. Slot objects are rendered through a consumer-pluggable
// renderer; the other kinds are library-resolved directly.
type ContractObject struct {
	Kind ContractObjectKind `yaml:"kind" json:"kind"`
	Ref  string             `yaml:"ref,omitempty" json:"ref,omitempty"`
	Text string             `yaml:"text,omitempty" json:"text,omitempty"`
}

// Validate enforces the contract-object vocabulary.
func (o ContractObject) Validate() error {
	if !o.Kind.Valid() {
		return ErrContractObjectUnknownKind
	}
	switch o.Kind {
	case ContractObjectLiteral:
		return nil
	case ContractObjectInput, ContractObjectVar, ContractObjectSlot:
		if o.Ref == "" {
			return ErrContractObjectMissingRef
		}
	}
	return nil
}

// MaterializationPhase is the boot-assembly phase a file/injection/var
// resolves in. VarPhaseAuto derives from the var's sink: files and
// injections default to build, while runtime is session-start.
type MaterializationPhase string

const (
	VarPhaseAuto         MaterializationPhase = "auto"
	VarPhaseBuild        MaterializationPhase = "build"
	VarPhaseSessionStart MaterializationPhase = "session-start"
)

// Valid reports whether p is a known materialization phase.
func (p MaterializationPhase) Valid() bool {
	switch p {
	case VarPhaseAuto, VarPhaseBuild, VarPhaseSessionStart:
		return true
	default:
		return false
	}
}

// BootFileSpec is one path-safe file target inside the bootdir.
type BootFileSpec struct {
	// ID is the stable file handle used for partial replant.
	ID string `yaml:"id" json:"id"`

	// RelPath is the bootdir-relative target path.
	RelPath string `yaml:"rel_path" json:"rel_path"`

	// Object names the content to materialize at RelPath.
	Object ContractObject `yaml:"object" json:"object"`

	// Mode is the final file mode. Zero falls back to 0o644.
	Mode os.FileMode `yaml:"mode,omitempty" json:"mode,omitempty"`

	// Phase identifies when the file is resolved. Empty is treated as
	// build for file sinks.
	Phase MaterializationPhase `yaml:"phase,omitempty" json:"phase,omitempty"`
}

// Validate enforces the file-shape contract.
func (f BootFileSpec) Validate() error {
	if f.ID == "" {
		return ErrBootFileMissingID
	}
	if f.RelPath == "" {
		return ErrBootFileMissingRelPath
	}
	if err := ValidateBootDirRelPath(f.RelPath); err != nil {
		return err
	}
	if err := f.Object.Validate(); err != nil {
		return err
	}
	if f.Phase != "" && !f.Phase.Valid() {
		return ErrBootSpecVarUnknownPhase
	}
	return nil
}

// BootInjectionSpec is one provider-native or raw injection target. It
// is separate from LaunchPlan.Injection: this type is boot-blueprint
// contract data, while LaunchPlan.Injection is caller-supplied runtime
// overlay.
type BootInjectionSpec struct {
	// ID is the stable injection handle used for partial replant.
	ID string `yaml:"id" json:"id"`

	// Kind selects the native planting convention.
	Kind NativeFileKind `yaml:"kind" json:"kind"`

	// Name identifies a skill/native target. Required for skill
	// injections and ignored for raw ones.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// RelPath is the bootdir-relative target for raw injections.
	RelPath string `yaml:"rel_path,omitempty" json:"rel_path,omitempty"`

	// Object names the content to materialize.
	Object ContractObject `yaml:"object" json:"object"`

	// Mode is the final file mode. Zero falls back to 0o644.
	Mode os.FileMode `yaml:"mode,omitempty" json:"mode,omitempty"`

	// Phase identifies when the injection is resolved. Empty is treated
	// as build for injection sinks.
	Phase MaterializationPhase `yaml:"phase,omitempty" json:"phase,omitempty"`
}

// Validate enforces the injection-shape contract.
func (i BootInjectionSpec) Validate() error {
	if i.ID == "" {
		return ErrBootInjectionMissingID
	}
	if !i.Kind.Valid() {
		return ErrUnknownNativeFileKind
	}
	switch i.Kind {
	case NativeFileSkill:
		if i.Name == "" {
			return ErrBootInjectionMissingName
		}
		if !safePathSegment(i.Name) {
			return ErrNativeFileUnsafeID
		}
	case NativeFileRaw:
		if i.RelPath == "" {
			return ErrNativeFileMissingRelPath
		}
		if err := ValidateBootDirRelPath(i.RelPath); err != nil {
			return err
		}
	}
	if err := i.Object.Validate(); err != nil {
		return err
	}
	if i.Phase != "" && !i.Phase.Valid() {
		return ErrBootSpecVarUnknownPhase
	}
	return nil
}

// VarFreshness is the cache policy attached to a derived var.
type VarFreshness string

const (
	VarFreshnessCacheOK    VarFreshness = "cache_ok"
	VarFreshnessFresh      VarFreshness = "fresh"
	VarFreshnessBestEffort VarFreshness = "best_effort"
)

// Valid reports whether f is a known freshness policy.
func (f VarFreshness) Valid() bool {
	switch f {
	case VarFreshnessCacheOK, VarFreshnessFresh, VarFreshnessBestEffort:
		return true
	default:
		return false
	}
}

// VarOnError is the failure policy attached to a derived var.
type VarOnError string

const (
	VarOnErrorAbort         VarOnError = "abort"
	VarOnErrorFallback      VarOnError = "fallback"
	VarOnErrorWarn          VarOnError = "warn"
	VarOnErrorPermanentFail VarOnError = "permanent-fail"
)

// Valid reports whether o is a known on_error policy.
func (o VarOnError) Valid() bool {
	switch o {
	case VarOnErrorAbort, VarOnErrorFallback, VarOnErrorWarn, VarOnErrorPermanentFail:
		return true
	default:
		return false
	}
}

// VarSourceKind is the allowed resolver family for a derived var.
type VarSourceKind string

const (
	VarSourceLiteral VarSourceKind = "literal"
	VarSourceFile    VarSourceKind = "file"
	VarSourceCall    VarSourceKind = "call"
	VarSourceCmd     VarSourceKind = "cmd"
)

// Valid reports whether k is a known var source kind.
func (k VarSourceKind) Valid() bool {
	switch k {
	case VarSourceLiteral, VarSourceFile, VarSourceCall, VarSourceCmd:
		return true
	default:
		return false
	}
}

// VarSpec is one derived boot variable.
type VarSpec struct {
	Name      string               `yaml:"name" json:"name"`
	Source    VarSource            `yaml:"source" json:"source"`
	Freshness VarFreshness         `yaml:"freshness" json:"freshness"`
	Fallback  any                  `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	OnError   VarOnError           `yaml:"on_error" json:"on_error"`
	Phase     MaterializationPhase `yaml:"phase" json:"phase"`

	// Secret marks the var as secret-bearing. Persisted blueprints must
	// not inline secret values at rest; use file/call/cmd indirection
	// instead.
	Secret bool `yaml:"secret,omitempty" json:"secret,omitempty"`
}

// Validate enforces the derived-var contract.
func (v VarSpec) Validate() error {
	if v.Name == "" {
		return ErrBootSpecVarMissingName
	}
	if err := v.Source.Validate(); err != nil {
		return err
	}
	if !v.Freshness.Valid() {
		return ErrBootSpecVarUnknownFreshness
	}
	if !v.OnError.Valid() {
		return ErrBootSpecVarUnknownOnError
	}
	if !v.Phase.Valid() {
		return ErrBootSpecVarUnknownPhase
	}
	if v.Secret {
		if v.Source.Kind == VarSourceLiteral {
			return ErrBootSpecVarSecretInlineValue
		}
		if v.Fallback != nil {
			return ErrBootSpecVarSecretInlineValue
		}
	}
	return nil
}

// VarSource is the source block on a VarSpec. Exactly one config branch
// must be populated for the selected Kind.
type VarSource struct {
	Kind    VarSourceKind `yaml:"kind" json:"kind"`
	Literal any           `yaml:"literal,omitempty" json:"literal,omitempty"`
	File    *VarFileRef   `yaml:"file,omitempty" json:"file,omitempty"`
	Call    *VarCallRef   `yaml:"call,omitempty" json:"call,omitempty"`
	Cmd     *VarCmdRef    `yaml:"cmd,omitempty" json:"cmd,omitempty"`
}

// Validate enforces the var-source union contract.
func (s VarSource) Validate() error {
	if !s.Kind.Valid() {
		return ErrBootSpecVarUnknownSourceKind
	}
	switch s.Kind {
	case VarSourceLiteral:
		return nil
	case VarSourceFile:
		if s.File == nil || s.File.Path == "" {
			return ErrBootSpecVarMissingSourceConfig
		}
	case VarSourceCall:
		if s.Call == nil {
			return ErrBootSpecVarMissingSourceConfig
		}
		if err := s.Call.Validate(); err != nil {
			return err
		}
	case VarSourceCmd:
		if s.Cmd == nil {
			return ErrBootSpecVarMissingSourceConfig
		}
		if err := s.Cmd.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// VarFileRef reads a var from a file.
type VarFileRef struct {
	Path      string `yaml:"path" json:"path"`
	TrimSpace bool   `yaml:"trim_space,omitempty" json:"trim_space,omitempty"`
}

// CallTransport is the allowed remote call substrate for a var source.
type CallTransport string

const (
	CallTransportMCP  CallTransport = "mcp"
	CallTransportHTTP CallTransport = "http"
)

// Valid reports whether t is a known call transport.
func (t CallTransport) Valid() bool {
	switch t {
	case CallTransportMCP, CallTransportHTTP:
		return true
	default:
		return false
	}
}

// TrustGate is the explicit authorization guard required for call/cmd
// var sources. At least one of Trust or Authorization must be set.
type TrustGate struct {
	Trust         string `yaml:"trust,omitempty" json:"trust,omitempty"`
	Authorization string `yaml:"authorization,omitempty" json:"authorization,omitempty"`
}

// Valid reports whether the gate is actionable.
func (g TrustGate) Valid() bool {
	return g.Trust != "" || g.Authorization != ""
}

// VarCallRef resolves a var through an MCP or HTTP call.
type VarCallRef struct {
	Transport CallTransport  `yaml:"transport" json:"transport"`
	Target    string         `yaml:"target" json:"target"`
	Operation string         `yaml:"operation,omitempty" json:"operation,omitempty"`
	Args      map[string]any `yaml:"args,omitempty" json:"args,omitempty"`
	Timeout   string         `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Gate      TrustGate      `yaml:"gate" json:"gate"`
}

// Validate enforces the call-source contract.
func (c VarCallRef) Validate() error {
	if !c.Transport.Valid() {
		return ErrBootSpecVarMissingSourceConfig
	}
	if c.Target == "" {
		return ErrBootSpecVarMissingSourceConfig
	}
	if !c.Gate.Valid() {
		return ErrBootSpecVarTrustGateRequired
	}
	if c.Timeout != "" {
		if _, err := time.ParseDuration(c.Timeout); err != nil {
			return fmt.Errorf("%w: %v", ErrBootSpecVarInvalidTimeout, err)
		}
	}
	return nil
}

// VarCmdRef resolves a var by executing a command. The command is split
// as argv, not shell text, so authorization applies to the exact
// executable path + args.
type VarCmdRef struct {
	Argv    []string  `yaml:"argv" json:"argv"`
	Workdir string    `yaml:"workdir,omitempty" json:"workdir,omitempty"`
	Timeout string    `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Gate    TrustGate `yaml:"gate" json:"gate"`
}

// Validate enforces the command-source contract.
func (c VarCmdRef) Validate() error {
	if len(c.Argv) == 0 {
		return ErrBootSpecVarMissingSourceConfig
	}
	if !c.Gate.Valid() {
		return ErrBootSpecVarTrustGateRequired
	}
	if c.Timeout != "" {
		if _, err := time.ParseDuration(c.Timeout); err != nil {
			return fmt.Errorf("%w: %v", ErrBootSpecVarInvalidTimeout, err)
		}
	}
	return nil
}

// MaterializeRequest is the caller-provided input bag for one bootdir
// populate/replant action.
type MaterializeRequest struct {
	Spec   *BootSpec      `yaml:"-" json:"-"`
	Inputs map[string]any `yaml:"inputs,omitempty" json:"inputs,omitempty"`
}

// ReplantSelector narrows a replant operation to specific files,
// injections, or slot refs. Empty means "reconcile every declared
// object".
type ReplantSelector struct {
	FileIDs      []string `yaml:"file_ids,omitempty" json:"file_ids,omitempty"`
	InjectionIDs []string `yaml:"injection_ids,omitempty" json:"injection_ids,omitempty"`
	SlotRefs     []string `yaml:"slot_refs,omitempty" json:"slot_refs,omitempty"`
}

// MaterializeResult is the stable report shape returned by the
// materializer after Populate or Replant.
type MaterializeResult struct {
	FilesWritten      []string       `yaml:"files_written,omitempty" json:"files_written,omitempty"`
	InjectionsWritten []string       `yaml:"injections_written,omitempty" json:"injections_written,omitempty"`
	SlotsRendered     []string       `yaml:"slots_rendered,omitempty" json:"slots_rendered,omitempty"`
	Runtime           RuntimeBinding `yaml:"runtime" json:"runtime"`
}

// ContractRenderRequest is the render-time input handed to a
// consumer-pluggable slot renderer.
type ContractRenderRequest struct {
	Spec   *BootSpec
	Inputs map[string]any
	Vars   map[string]any
	Object ContractObject
}

// ContractRenderer is the consumer-pluggable rendering seam. The shared
// library owns path-safe writing and overwrite order; consumers own the
// content rendering for slot objects.
type ContractRenderer interface {
	RenderContractObject(ctx context.Context, req ContractRenderRequest) (string, error)
}

// Materializer is the frozen bootdir materialization API. Populate must
// be idempotent against an existing directory for crash recovery.
// Replant must support partial reconciliation by file ID, injection ID,
// and slot ref.
type Materializer interface {
	Populate(ctx context.Context, bootDir string, req MaterializeRequest, renderer ContractRenderer) (*MaterializeResult, error)
	Replant(ctx context.Context, bootDir string, req MaterializeRequest, sel ReplantSelector, renderer ContractRenderer) (*MaterializeResult, error)
}
