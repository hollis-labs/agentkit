package agentlaunch

// WorkspaceMode is the typed string enum that names the workspace
// reservation policy for a launched session. The mode describes intent;
// the preparer turns that intent into concrete absolute paths and an
// allocation strategy.
type WorkspaceMode string

const (
	// WorkspaceShared selects an existing workspace directory shared
	// across sessions for the same project (set via WorkspaceSpec.Workdir).
	// No fresh allocation; the preparer asserts the directory exists.
	WorkspaceShared WorkspaceMode = "shared"

	// WorkspaceTemp selects an ephemeral workspace allocated under
	// WorkspaceSpec.TempPrefix (defaulted by the preparer when empty).
	// The preparer removes the directory on session termination.
	WorkspaceTemp WorkspaceMode = "temp"

	// WorkspaceFresh selects a fresh workspace allocated at WorkspaceSpec
	// .WorkspaceDir for the lifetime of the session; the preparer creates
	// it if absent but does NOT delete it on termination. Useful for
	// post-mortem inspection.
	WorkspaceFresh WorkspaceMode = "fresh"

	// WorkspacePersistent selects a long-lived workspace pinned to
	// WorkspaceSpec.WorkspaceDir — created if absent, preserved across
	// session boundaries, and never removed by the preparer.
	WorkspacePersistent WorkspaceMode = "persistent"
)

// Valid reports whether the receiver is one of the four declared
// WorkspaceMode constants. The zero value ("") is not valid.
func (m WorkspaceMode) Valid() bool {
	switch m {
	case WorkspaceShared, WorkspaceTemp, WorkspaceFresh, WorkspacePersistent:
		return true
	default:
		return false
	}
}

// String returns the underlying token (e.g. "shared"). The zero value
// returns "".
func (m WorkspaceMode) String() string {
	return string(m)
}

// WorkspaceSpec declares the workspace reservation policy for a
// session, plus the optional concrete paths the policy resolves against.
// The preparer turns this into the final WorkspaceDir / Workdir handed
// to go-agent-sessions; the catalog port (CW-0003) populates it from
// YAML; the matrix (CW-0004) does not consult it.
type WorkspaceSpec struct {
	// Mode declares the reservation policy. Required; LaunchPlan.Validate
	// rejects an unset or unknown mode via ErrUnsupportedWorkspaceMode.
	Mode WorkspaceMode `yaml:"mode" json:"mode"`

	// Workdir is the absolute path used as the spawned process's working
	// directory. Optional at plan-time — the compiler may resolve a
	// project-relative default if empty. The preparer ultimately writes
	// this through to go-agent-sessions StartOptions.Workdir.
	Workdir string `yaml:"workdir,omitempty" json:"workdir,omitempty"`

	// WorkspaceDir is the absolute path of the per-session persistent
	// root for lib state (logs, planted bootdir parent, checkpoint
	// outputs). Distinct from Workdir per the two-dir convention shared
	// across the agent-boot portfolio.
	WorkspaceDir string `yaml:"workspace_dir,omitempty" json:"workspace_dir,omitempty"`

	// TempPrefix is the absolute directory under which WorkspaceTemp
	// allocates an ephemeral workspace. Ignored for non-temp modes.
	// Empty falls back to os.TempDir() at prepare time.
	TempPrefix string `yaml:"temp_prefix,omitempty" json:"temp_prefix,omitempty"`
}
