package materialize

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/agentkit/artifact"
)

var (
	ErrUnknownOperation       = errors.New("materialize: unknown operation")
	ErrMissingTargetRoot      = errors.New("materialize: missing target root")
	ErrMissingArtifactEntries = errors.New("materialize: missing artifact entries")
	ErrUnsupportedOperation   = errors.New("materialize: unsupported operation")
	ErrTargetExists           = errors.New("materialize: target exists")
	ErrUnsafeTarget           = errors.New("materialize: unsafe target")
	ErrUnresolvedContent      = errors.New("materialize: file entry has unresolved content")
	ErrMissingManifest        = errors.New("materialize: current manifest required")
	ErrStaleGeneration        = errors.New("materialize: stale manifest generation")
	ErrConflict               = errors.New("materialize: ownership conflict")
	ErrMalformedDocument      = errors.New("materialize: malformed managed document")
)

type Operation string

const (
	OperationPlan      Operation = "plan"
	OperationCreate    Operation = "create"
	OperationReconcile Operation = "reconcile"
	OperationRefresh   Operation = "refresh"
)

func (o Operation) Valid() bool {
	switch o {
	case OperationPlan, OperationCreate, OperationReconcile, OperationRefresh:
		return true
	default:
		return false
	}
}

type ExistingTargetPolicy string

const (
	ExistingTargetRefuse    ExistingTargetPolicy = "refuse"
	ExistingTargetReplace   ExistingTargetPolicy = "replace_if_owned"
	ExistingTargetReconcile ExistingTargetPolicy = "reconcile"
)

type ConflictPolicy string

const (
	ConflictReport    ConflictPolicy = "report"
	ConflictOverwrite ConflictPolicy = "overwrite_owned"
)

// Selection narrows refresh/reconcile to ownership groups or exact entry IDs.
type Selection struct {
	Groups   []string `yaml:"groups,omitempty" json:"groups,omitempty"`
	EntryIDs []string `yaml:"entry_ids,omitempty" json:"entry_ids,omitempty"`
}

// ReconcilePolicy makes mixed-directory behavior explicit.
type ReconcilePolicy struct {
	Conflict    ConflictPolicy `yaml:"conflict,omitempty" json:"conflict,omitempty"`
	RemoveOwned bool           `yaml:"remove_owned,omitempty" json:"remove_owned,omitempty"`
}

// TargetRoots records destination roots materialization knows about. These are
// concrete directories, not necessarily the spawned process cwd.
type TargetRoots struct {
	ProjectRoot string `yaml:"project_root,omitempty" json:"project_root,omitempty"`
	BootRoot    string `yaml:"boot_root,omitempty" json:"boot_root,omitempty"`
	StateRoot   string `yaml:"state_root,omitempty" json:"state_root,omitempty"`
	ScratchRoot string `yaml:"scratch_root,omitempty" json:"scratch_root,omitempty"`
}

// Request is the write boundary input for the shared engine.
type Request struct {
	Operation          Operation            `yaml:"operation" json:"operation"`
	TargetRoot         string               `yaml:"target_root" json:"target_root"`
	Roots              TargetRoots          `yaml:"roots,omitempty" json:"roots,omitempty"`
	Artifacts          artifact.Tree        `yaml:"artifacts" json:"artifacts"`
	CurrentManifest    *Manifest            `yaml:"current_manifest,omitempty" json:"current_manifest,omitempty"`
	ExistingTarget     ExistingTargetPolicy `yaml:"existing_target,omitempty" json:"existing_target,omitempty"`
	Reconcile          ReconcilePolicy      `yaml:"reconcile,omitempty" json:"reconcile,omitempty"`
	Selection          Selection            `yaml:"selection,omitempty" json:"selection,omitempty"`
	Generation         string               `yaml:"generation,omitempty" json:"generation,omitempty"`
	ExpectedGeneration string               `yaml:"expected_generation,omitempty" json:"expected_generation,omitempty"`
}

func (r Request) Validate() error {
	if !r.Operation.Valid() {
		return ErrUnknownOperation
	}
	if r.TargetRoot == "" {
		return ErrMissingTargetRoot
	}
	if len(r.Artifacts.Entries) == 0 {
		return ErrMissingArtifactEntries
	}
	if err := r.Artifacts.Validate(); err != nil {
		return err
	}
	return nil
}

type ChangeKind string

const (
	ChangeCreate    ChangeKind = "create"
	ChangeUpdate    ChangeKind = "update"
	ChangeMode      ChangeKind = "mode"
	ChangeRemove    ChangeKind = "remove"
	ChangeUnchanged ChangeKind = "unchanged"
	ChangeConflict  ChangeKind = "conflict"
)

type Change struct {
	Path     string            `yaml:"path" json:"path"`
	Kind     ChangeKind        `yaml:"kind" json:"kind"`
	EntryID  string            `yaml:"entry_id,omitempty" json:"entry_id,omitempty"`
	GroupID  string            `yaml:"group_id,omitempty" json:"group_id,omitempty"`
	Before   artifact.Digest   `yaml:"before,omitempty" json:"before,omitempty"`
	After    artifact.Digest   `yaml:"after,omitempty" json:"after,omitempty"`
	Conflict string            `yaml:"conflict,omitempty" json:"conflict,omitempty"`
	Redacted map[string]bool   `yaml:"redacted,omitempty" json:"redacted,omitempty"`
	Meta     map[string]string `yaml:"meta,omitempty" json:"meta,omitempty"`
}

type Report struct {
	Operation Operation `yaml:"operation" json:"operation"`
	Changes   []Change  `yaml:"changes,omitempty" json:"changes,omitempty"`
	Complete  bool      `yaml:"complete" json:"complete"`
}

type ManifestEntry struct {
	Path       string              `yaml:"path" json:"path"`
	Kind       artifact.EntryKind  `yaml:"kind" json:"kind"`
	Mode       uint32              `yaml:"mode,omitempty" json:"mode,omitempty"`
	Digest     artifact.Digest     `yaml:"digest,omitempty" json:"digest,omitempty"`
	Ownership  artifact.Ownership  `yaml:"ownership,omitempty" json:"ownership,omitempty"`
	Provenance artifact.Provenance `yaml:"provenance,omitempty" json:"provenance,omitempty"`
}

type Manifest struct {
	SchemaVersion string          `yaml:"schema_version" json:"schema_version"`
	Generation    string          `yaml:"generation" json:"generation"`
	Roots         TargetRoots     `yaml:"roots,omitempty" json:"roots,omitempty"`
	Entries       []ManifestEntry `yaml:"entries" json:"entries"`
	CreatedAt     time.Time       `yaml:"created_at,omitempty" json:"created_at,omitempty"`
}

type Handle struct {
	TargetRoot string   `yaml:"target_root" json:"target_root"`
	Manifest   Manifest `yaml:"manifest" json:"manifest"`
	Report     Report   `yaml:"report" json:"report"`
}

type Plan struct {
	Request    Request  `yaml:"request" json:"request"`
	Manifest   Manifest `yaml:"manifest" json:"manifest"`
	Report     Report   `yaml:"report" json:"report"`
	WillMutate bool     `yaml:"will_mutate" json:"will_mutate"`
}

type Engine interface {
	Plan(ctx context.Context, req Request) (Plan, error)
	Apply(ctx context.Context, req Request) (Handle, error)
}

func ValidatePlan(p Plan) error {
	if err := p.Request.Validate(); err != nil {
		return err
	}
	if p.Report.Operation != "" && p.Report.Operation != p.Request.Operation {
		return fmt.Errorf("materialize: report operation %q does not match request %q", p.Report.Operation, p.Request.Operation)
	}
	return nil
}
