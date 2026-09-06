package agentlaunch

import (
	"errors"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/artifact"
	"github.com/hollis-labs/agentkit/materialize"
)

var (
	ErrPreparedExecutionMissingArgv = errors.New("agentlaunch: prepared execution missing argv")
	ErrPreparedExecutionMissingCWD  = errors.New("agentlaunch: prepared execution missing cwd")
	ErrPrepareInputUnknown          = errors.New("agentlaunch: prepare input kind is unknown")
	ErrPrepareInputMissing          = errors.New("agentlaunch: prepare input is missing")
	ErrPrepareInputMismatch         = errors.New("agentlaunch: prepare input fields do not match kind")
)

type PrepareInputKind string

const (
	PrepareInputArtifacts           PrepareInputKind = "artifacts"
	PrepareInputResolvedComposition PrepareInputKind = "resolved_composition"
	PrepareInputAuthoredRecipe      PrepareInputKind = "authored_recipe"
)

func (k PrepareInputKind) Valid() bool {
	switch k {
	case PrepareInputArtifacts, PrepareInputResolvedComposition, PrepareInputAuthoredRecipe:
		return true
	default:
		return false
	}
}

// PrepareRequest is the contract-level input to launch preparation. Exactly
// one input family is active: raw artifacts, resolved composition, or authored
// recipe. The shared implementation added by later tasks will allow callers to
// enter at any of those levels without requiring a full runtime.
type PrepareRequest struct {
	Kind        PrepareInputKind                  `yaml:"kind" json:"kind"`
	Artifacts   *artifact.Tree                    `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Composition *agentcontext.ResolvedComposition `yaml:"composition,omitempty" json:"composition,omitempty"`
	Recipe      *agentcontext.AuthoredRecipe      `yaml:"recipe,omitempty" json:"recipe,omitempty"`
	Projection  ProviderProjection                `yaml:"projection,omitempty" json:"projection,omitempty"`
	Roots       ExecutionRoots                    `yaml:"roots" json:"roots"`
	Access      AccessRequirements                `yaml:"access,omitempty" json:"access,omitempty"`
	Legacy      LegacyCompatibility               `yaml:"legacy,omitempty" json:"legacy,omitempty"`
}

func (r PrepareRequest) Validate() error {
	if !r.Kind.Valid() {
		return ErrPrepareInputUnknown
	}
	switch r.Kind {
	case PrepareInputArtifacts:
		if r.Artifacts == nil {
			return ErrPrepareInputMissing
		}
		if r.Composition != nil || r.Recipe != nil {
			return ErrPrepareInputMismatch
		}
		return r.Artifacts.Validate()
	case PrepareInputResolvedComposition:
		if r.Composition == nil {
			return ErrPrepareInputMissing
		}
		if r.Artifacts != nil || r.Recipe != nil {
			return ErrPrepareInputMismatch
		}
	case PrepareInputAuthoredRecipe:
		if r.Recipe == nil {
			return ErrPrepareInputMissing
		}
		if r.Artifacts != nil || r.Composition != nil {
			return ErrPrepareInputMismatch
		}
	}
	return nil
}

type ProviderProjection struct {
	Provider    string                 `yaml:"provider,omitempty" json:"provider,omitempty"`
	Runtime     RuntimeKind            `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Artifacts   artifact.Tree          `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Bindings    ExecutionBindings      `yaml:"bindings,omitempty" json:"bindings,omitempty"`
	Effects     []RuntimeEffect        `yaml:"effects,omitempty" json:"effects,omitempty"`
	Diagnostics []CapabilityDiagnostic `yaml:"diagnostics,omitempty" json:"diagnostics,omitempty"`
}

// PreparedExecution is the lossless value handoff to sessions and wrapper
// code. It is distinct from the older PreparedLaunch compatibility struct,
// which M12/M14 will adapt onto this shape.
type PreparedExecution struct {
	InputKind       PrepareInputKind                  `yaml:"input_kind" json:"input_kind"`
	Composition     *agentcontext.ResolvedComposition `yaml:"composition,omitempty" json:"composition,omitempty"`
	Artifacts       artifact.Tree                     `yaml:"artifacts" json:"artifacts"`
	Materialization *materialize.Handle               `yaml:"materialization,omitempty" json:"materialization,omitempty"`
	Bindings        ExecutionBindings                 `yaml:"bindings" json:"bindings"`
	Roots           ExecutionRoots                    `yaml:"roots" json:"roots"`
	Access          AccessRequirements                `yaml:"access" json:"access"`
	Effects         []RuntimeEffect                   `yaml:"effects,omitempty" json:"effects,omitempty"`
	Diagnostics     []CapabilityDiagnostic            `yaml:"diagnostics,omitempty" json:"diagnostics,omitempty"`
	Legacy          LegacyCompatibility               `yaml:"legacy,omitempty" json:"legacy,omitempty"`
}

func (p PreparedExecution) Validate() error {
	if len(p.Bindings.Argv) == 0 {
		return ErrPreparedExecutionMissingArgv
	}
	if p.Bindings.CWD == "" {
		return ErrPreparedExecutionMissingCWD
	}
	return nil
}

type ExecutionBindings struct {
	Argv            []string          `yaml:"argv" json:"argv"`
	Env             map[string]EnvVar `yaml:"env,omitempty" json:"env,omitempty"`
	CWD             string            `yaml:"cwd" json:"cwd"`
	ConfigRoot      string            `yaml:"config_root,omitempty" json:"config_root,omitempty"`
	ProtocolContext string            `yaml:"protocol_context,omitempty" json:"protocol_context,omitempty"`
}

type EnvVar struct {
	Value      string `yaml:"value" json:"value"`
	Source     string `yaml:"source,omitempty" json:"source,omitempty"`
	Secret     bool   `yaml:"secret,omitempty" json:"secret,omitempty"`
	Precedence int    `yaml:"precedence,omitempty" json:"precedence,omitempty"`
}

type ExecutionRoots struct {
	ProjectRoot string `yaml:"project_root,omitempty" json:"project_root,omitempty"`
	BootRoot    string `yaml:"boot_root,omitempty" json:"boot_root,omitempty"`
	StateRoot   string `yaml:"state_root,omitempty" json:"state_root,omitempty"`
	ScratchRoot string `yaml:"scratch_root,omitempty" json:"scratch_root,omitempty"`
	CWD         string `yaml:"cwd,omitempty" json:"cwd,omitempty"`
}

type RuntimeEffectKind string

const (
	RuntimeEffectSecret     RuntimeEffectKind = "secret"
	RuntimeEffectHostConfig RuntimeEffectKind = "host_config"
	RuntimeEffectCredential RuntimeEffectKind = "credential"
	RuntimeEffectLoopback   RuntimeEffectKind = "loopback"
	RuntimeEffectSubprocess RuntimeEffectKind = "subprocess"
)

type RuntimeEffect struct {
	Kind           RuntimeEffectKind `yaml:"kind" json:"kind"`
	Name           string            `yaml:"name" json:"name"`
	ProviderEffect string            `yaml:"provider_effect,omitempty" json:"provider_effect,omitempty"`
	Destination    string            `yaml:"destination,omitempty" json:"destination,omitempty"`
	Required       bool              `yaml:"required,omitempty" json:"required,omitempty"`
	Redacted       bool              `yaml:"redacted,omitempty" json:"redacted,omitempty"`
	Owner          string            `yaml:"owner,omitempty" json:"owner,omitempty"`
	Diagnostic     string            `yaml:"diagnostic,omitempty" json:"diagnostic,omitempty"`
}

type AccessMode string

const (
	AccessDisabled AccessMode = "disabled"
	AccessOptional AccessMode = "optional"
	AccessRequired AccessMode = "required"
)

type AccessPathMode string

const (
	AccessRead  AccessPathMode = "read"
	AccessWrite AccessPathMode = "write"
	AccessDeny  AccessPathMode = "deny"
)

type RootKind string

const (
	RootProject RootKind = "project"
	RootBoot    RootKind = "boot"
	RootState   RootKind = "state"
	RootScratch RootKind = "scratch"
	RootRuntime RootKind = "runtime"
	RootOther   RootKind = "other"
)

type ExecutionHostKind string

const (
	ExecutionHostLocal  ExecutionHostKind = "local"
	ExecutionHostRemote ExecutionHostKind = "remote"
)

type AccessPath struct {
	Mode AccessPathMode `yaml:"mode" json:"mode"`
	Root RootKind       `yaml:"root" json:"root"`
	Path string         `yaml:"path" json:"path"`
}

type NetworkAccess struct {
	Disabled bool     `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	Loopback bool     `yaml:"loopback,omitempty" json:"loopback,omitempty"`
	Hosts    []string `yaml:"hosts,omitempty" json:"hosts,omitempty"`
}

type SubprocessAccess struct {
	Allowed bool `yaml:"allowed" json:"allowed"`
}

// AccessRequirements is the agentkit handoff contract. M08 will define the
// resolved go-sandbox policy that enforces these requirements.
type AccessRequirements struct {
	Mode        AccessMode        `yaml:"mode" json:"mode"`
	Host        ExecutionHostKind `yaml:"host" json:"host"`
	Roots       ExecutionRoots    `yaml:"roots" json:"roots"`
	Filesystem  []AccessPath      `yaml:"filesystem,omitempty" json:"filesystem,omitempty"`
	Network     NetworkAccess     `yaml:"network,omitempty" json:"network,omitempty"`
	Subprocess  SubprocessAccess  `yaml:"subprocess,omitempty" json:"subprocess,omitempty"`
	RuntimeRead []string          `yaml:"runtime_read,omitempty" json:"runtime_read,omitempty"`
}

type EnforcementOutcomeKind string

const (
	EnforcementDisabled    EnforcementOutcomeKind = "disabled"
	EnforcementUnsupported EnforcementOutcomeKind = "unsupported"
	EnforcementConfigured  EnforcementOutcomeKind = "configured"
	EnforcementEnforced    EnforcementOutcomeKind = "enforced"
	EnforcementFailed      EnforcementOutcomeKind = "failed"
)

type CapabilityDiagnostic struct {
	Code     string                 `yaml:"code" json:"code"`
	Severity string                 `yaml:"severity" json:"severity"`
	Message  string                 `yaml:"message" json:"message"`
	Outcome  EnforcementOutcomeKind `yaml:"outcome,omitempty" json:"outcome,omitempty"`
	Feature  string                 `yaml:"feature,omitempty" json:"feature,omitempty"`
	Provider string                 `yaml:"provider,omitempty" json:"provider,omitempty"`
	Runtime  RuntimeKind            `yaml:"runtime,omitempty" json:"runtime,omitempty"`
}

type LegacyCompatibility struct {
	BootSpec       bool     `yaml:"boot_spec,omitempty" json:"boot_spec,omitempty"`
	NativeFile     bool     `yaml:"native_file,omitempty" json:"native_file,omitempty"`
	BootDirOverlay bool     `yaml:"boot_dir_overlay,omitempty" json:"boot_dir_overlay,omitempty"`
	PreparedLaunch bool     `yaml:"prepared_launch,omitempty" json:"prepared_launch,omitempty"`
	Losses         []string `yaml:"losses,omitempty" json:"losses,omitempty"`
	Unsupported    []string `yaml:"unsupported,omitempty" json:"unsupported,omitempty"`
}
