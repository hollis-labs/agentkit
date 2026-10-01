package agentsessions

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/go-runner/runner"
	"github.com/hollis-labs/go-sandbox/sandbox"
)

var (
	ErrPreparedExecutionInvalid  = errors.New("agentsessions: prepared execution is invalid")
	ErrPreparedRemoteRequired    = errors.New("agentsessions: required prepared confinement cannot be enforced for remote host")
	ErrSandboxPolicyConflict     = errors.New("agentsessions: sandbox policy inputs conflict")
	ErrPreparedAccessUnsupported = errors.New("agentsessions: prepared access requirement is unsupported")
	// ErrGUILaunchDenyUnsupported means StartOptions.DenyGUILaunch was set but
	// this platform or runtime cannot enforce it, so the launch is refused rather than run unconfined.
	ErrGUILaunchDenyUnsupported = errors.New("agentsessions: DenyGUILaunch cannot be enforced")
	// ErrProtectedPathsUnsupported means StartOptions.ProtectedPaths was set
	// but this platform or runtime cannot write-protect them, so the launch is
	// refused rather than run with the control plane writable.
	ErrProtectedPathsUnsupported = errors.New("agentsessions: ProtectedPaths cannot be enforced")
)

// SandboxOutcome is the session-runtime view of OS confinement for a child.
// It carries no argv/env values so callers can surface it without leaking
// secrets from prepared bindings.
type SandboxOutcome struct {
	PolicyID     string
	Mode         sandbox.ConfinementMode
	Backend      sandbox.BackendName
	State        sandbox.EnforcementState
	Enforced     bool
	Disabled     bool
	Unsupported  []string
	Diagnostics  []string
	BackendGOOS  string
	BackendReady bool
	Legacy       bool
}

// SandboxError reports sandbox setup failure before a child process was
// started. Required confinement failures stop launch at this boundary.
type SandboxError struct {
	Outcome SandboxOutcome
	Err     error
}

func (e *SandboxError) Error() string {
	if e == nil || e.Err == nil {
		return "agentsessions: sandbox setup"
	}
	return e.Err.Error()
}

func (e *SandboxError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func applyStartOptions(opts *StartOptions, prepared *agentlaunch.PreparedExecution) error {
	if prepared == nil {
		return nil
	}
	if err := prepared.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrPreparedExecutionInvalid, err)
	}
	bindings := prepared.Bindings
	if bindings.CWD != "" {
		opts.Workdir = bindings.CWD
	}
	// Prepared bindings are exact. A nil or empty prepared Env means the
	// child runs with an explicit empty environment, not the parent process
	// environment inherited through os/exec defaults.
	opts.Env = envVarsFromPrepared(bindings.Env)
	switch {
	case bindings.Launch != nil:
		// The template owns the provider argv and the launch's own flags;
		// Argv is only its first turn. Copying Argv[1:] into ExtraArgs is
		// what appended the provider argv a second time after BuildArgs.
		opts.Launch = bindings.Launch
		opts.ExtraArgs = nil
	case len(bindings.Argv) > 1:
		opts.ExtraArgs = append([]string(nil), bindings.Argv[1:]...)
	default:
		opts.ExtraArgs = nil
	}
	if prepared.Materialization != nil {
		opts.AutoPlantBootDir = false
	}
	// The boot fields travel on the prepared execution; a caller that set
	// its own keeps them.
	if opts.BootMode == "" && opts.BootPrompt == "" && opts.BootContent == "" {
		opts.BootMode = prepared.Boot.Mode
		opts.BootPrompt = prepared.Boot.Prompt
		opts.BootContent = prepared.Boot.Content
	}
	return nil
}

func normalizeStartOptions(opts StartOptions) (StartOptions, error) {
	if opts.PreparedExecution != nil {
		if err := applyStartOptions(&opts, opts.PreparedExecution); err != nil {
			return StartOptions{}, err
		}
	}
	if opts.Launch != nil {
		// The session keeps its own copy: editing the caller's template
		// after Start must not rewrite later turns' argv.
		opts.Launch = opts.Launch.Clone()
		if err := DeliverStreamingBoot(&opts, opts.Launch.Convention.Mode); err != nil {
			return StartOptions{}, fmt.Errorf("agentsessions: deliver boot prompt: %w", err)
		}
	}
	if opts.PreparedExecution != nil {
		policy, err := sandboxPolicyFromPrepared(opts.PreparedExecution, opts.Workdir)
		if err != nil {
			return StartOptions{}, err
		}
		if policy != nil {
			if opts.SandboxPolicy != nil {
				return StartOptions{}, fmt.Errorf("%w: PreparedExecution.Access and StartOptions.SandboxPolicy are mutually exclusive", ErrSandboxPolicyConflict)
			}
			opts.SandboxPolicy = policy
		}
	}
	if opts.SandboxPolicy != nil && opts.Profile.ID != "" {
		return StartOptions{}, fmt.Errorf("%w: StartOptions.SandboxPolicy and StartOptions.Profile are mutually exclusive", ErrSandboxPolicyConflict)
	}
	if opts.SandboxPolicy != nil && opts.SandboxPolicy.ID == "" {
		return StartOptions{}, errors.New("agentsessions: StartOptions.SandboxPolicy.ID is required")
	}
	opts, err := applyProtectedPaths(opts)
	if err != nil {
		return StartOptions{}, err
	}
	return applyGUILaunchDeny(opts)
}

// minimalProtectProfileID names the host-filesystem profile that carries only
// the protected paths when the caller supplied no sandbox of its own.
const minimalProtectProfileID = "protect-control-plane"

// applyProtectedPaths folds StartOptions.ProtectedPaths into the one sandbox
// that will wrap the child, as applyGUILaunchDeny does for its knob: onto an
// existing SandboxPolicy or Profile (copies, so the caller's values are not
// mutated), and only when there is neither (or the policy is disabled) does it
// install a minimal host-filesystem profile. It runs before
// applyGUILaunchDeny, so that knob lands on this profile rather than replacing
// it. It is idempotent.
func applyProtectedPaths(opts StartOptions) (StartOptions, error) {
	if len(opts.ProtectedPaths) == 0 {
		return opts, nil
	}
	for _, path := range opts.ProtectedPaths {
		if !filepath.IsAbs(path) {
			return StartOptions{}, fmt.Errorf("agentsessions: StartOptions.ProtectedPaths entry %q must be absolute", path)
		}
	}
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		return StartOptions{}, fmt.Errorf("%w: the %s sandbox backend on %s cannot write-protect paths", ErrProtectedPathsUnsupported, caps.Backend, caps.GOOS)
	}
	switch {
	case opts.SandboxPolicy != nil && opts.SandboxPolicy.Mode == sandbox.ConfinementDisabled:
		// An unconfined policy has nothing to merge into; protection still
		// needs a sandbox, so the child gets the minimal profile.
		opts.SandboxPolicy = nil
		opts.Profile = minimalProtectProfile(opts.ProtectedPaths)
	case opts.SandboxPolicy != nil:
		merged, err := opts.SandboxPolicy.WithProtected(opts.ProtectedPaths...)
		if err != nil {
			return StartOptions{}, fmt.Errorf("agentsessions: StartOptions.ProtectedPaths: %w", err)
		}
		opts.SandboxPolicy = &merged
	case opts.Profile.ID != "":
		opts.Profile.FS.Protect = appendMissing(slices.Clone(opts.Profile.FS.Protect), opts.ProtectedPaths)
	default:
		opts.Profile = minimalProtectProfile(opts.ProtectedPaths)
	}
	return opts, nil
}

// minimalProtectProfile is a default-allow profile whose only effect is the
// write protection: on Linux the host filesystem, writable, with the
// protected paths read-only; on macOS go-sandbox's default-allow seatbelt
// with write denies.
func minimalProtectProfile(paths []string) sandbox.Profile {
	return sandbox.Profile{
		ID:             minimalProtectProfileID,
		Net:            true,
		Subprocess:     true,
		HostFilesystem: true,
		FS:             sandbox.FSSpec{Protect: appendMissing(nil, paths)},
	}
}

func appendMissing(dst, values []string) []string {
	for _, value := range values {
		if !slices.Contains(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}

// minimalGUIDenyProfileID names the default-allow profile that carries only
// the DenyGUILaunch knob when the caller supplied no sandbox of its own.
const minimalGUIDenyProfileID = "deny-gui-launch"

// applyGUILaunchDeny folds StartOptions.DenyGUILaunch into the one sandbox
// that will wrap the child. It composes instead of nesting: the knob goes onto
// an existing SandboxPolicy or Profile (copies, so the caller's values are not
// mutated), and only when there is neither does it install a minimal
// default-allow profile. It is idempotent.
func applyGUILaunchDeny(opts StartOptions) (StartOptions, error) {
	if !opts.DenyGUILaunch {
		return opts, nil
	}
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapGUILaunchDeny) {
		return StartOptions{}, fmt.Errorf("%w: the %s sandbox backend on %s cannot deny GUI launch", ErrGUILaunchDenyUnsupported, caps.Backend, caps.GOOS)
	}
	switch {
	case opts.SandboxPolicy != nil && opts.SandboxPolicy.Mode == sandbox.ConfinementDisabled:
		// An unconfined policy has nothing to merge into. The knob still asks
		// for one denial, so the child gets the minimal profile and nothing else.
		opts.SandboxPolicy = nil
		opts.Profile = sandbox.Profile{ID: minimalGUIDenyProfileID, Net: true, Subprocess: true, DenyGUILaunch: true}
	case opts.SandboxPolicy != nil:
		merged := *opts.SandboxPolicy
		merged.DenyGUILaunch = true
		opts.SandboxPolicy = &merged
	case opts.Profile.ID != "":
		opts.Profile.DenyGUILaunch = true
	default:
		opts.Profile = sandbox.Profile{ID: minimalGUIDenyProfileID, Net: true, Subprocess: true, DenyGUILaunch: true}
	}
	return opts, nil
}

func normalizeProviderStartOptions(opts StartOptions) (StartOptions, error) {
	if opts.DenyGUILaunch {
		return StartOptions{}, fmt.Errorf("%w: a provider-native runtime applies no OS sandbox", ErrGUILaunchDenyUnsupported)
	}
	if len(opts.ProtectedPaths) > 0 {
		return StartOptions{}, fmt.Errorf("%w: a provider-native runtime applies no OS sandbox", ErrProtectedPathsUnsupported)
	}
	opts, err := normalizeStartOptions(opts)
	if err != nil {
		return StartOptions{}, err
	}
	if opts.SandboxPolicy != nil {
		if opts.SandboxPolicy.Mode == sandbox.ConfinementRequired {
			return StartOptions{}, errors.New("agentsessions: provider runtime cannot enforce required OS sandbox policy")
		}
		opts.SandboxPolicy = nil
	}
	if opts.Profile.ID != "" {
		return StartOptions{}, errors.New("agentsessions: provider runtime cannot enforce legacy sandbox profile")
	}
	return opts, nil
}

func envVarsFromPrepared(env map[string]agentlaunch.EnvVar) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key].Value)
	}
	return out
}

func sandboxPolicyFromPrepared(prepared *agentlaunch.PreparedExecution, fallbackCWD string) (*sandbox.ResolvedAccessPolicy, error) {
	if prepared == nil {
		return nil, nil
	}
	access := prepared.Access
	if access.Mode == "" || access.Mode == agentlaunch.AccessOptional {
		return nil, nil
	}
	if access.Mode == agentlaunch.AccessDisabled {
		policy, err := sandbox.ResolveAccessPolicy(sandbox.AccessPolicy{
			ID:      preparedPolicyID(prepared),
			Mode:    sandbox.ConfinementDisabled,
			Backend: sandbox.BackendAuto,
			Roots:   sandboxRoots(preparedAccessRoots(prepared), fallbackCWD),
		})
		if err != nil {
			return nil, err
		}
		return &policy, nil
	}
	if access.Mode != agentlaunch.AccessRequired {
		return nil, fmt.Errorf("agentsessions: unknown prepared access mode %q", access.Mode)
	}
	if access.Host == agentlaunch.ExecutionHostRemote {
		return nil, ErrPreparedRemoteRequired
	}
	roots := sandboxRoots(preparedAccessRoots(prepared), fallbackCWD)
	fs := sandbox.FilesystemAccess{}
	for _, item := range access.Filesystem {
		ref, err := accessPathRef(item)
		if err != nil {
			return nil, err
		}
		switch item.Mode {
		case agentlaunch.AccessRead:
			fs.Read = append(fs.Read, ref)
		case agentlaunch.AccessWrite:
			fs.Write = append(fs.Write, ref)
		case agentlaunch.AccessDeny:
			fs.Deny = append(fs.Deny, ref)
		default:
			return nil, fmt.Errorf("agentsessions: unknown prepared filesystem access mode %q", item.Mode)
		}
	}
	runtime := sandbox.RuntimeAccess{}
	if len(prepared.Bindings.Argv) > 0 && filepath.IsAbs(prepared.Bindings.Argv[0]) {
		runtime.Executable = sandbox.PathRef{Path: prepared.Bindings.Argv[0]}
	}
	for _, path := range access.RuntimeRead {
		runtime.Read = append(runtime.Read, sandbox.PathRef{Path: path})
	}
	network := sandbox.NetworkAccess{Mode: sandbox.NetworkDeny}
	if access.Network.Disabled {
		network.Mode = sandbox.NetworkDeny
	} else if access.Network.Loopback {
		network.Mode = sandbox.NetworkLoopback
	} else if len(access.Network.Hosts) > 0 {
		return nil, fmt.Errorf("%w: network host allowlists are not enforceable by go-sandbox", ErrPreparedAccessUnsupported)
	}
	subprocess := sandbox.SubprocessDeny
	if access.Subprocess.Allowed {
		subprocess = sandbox.SubprocessAllow
	}
	policy, err := sandbox.ResolveAccessPolicy(sandbox.AccessPolicy{
		ID:         preparedPolicyID(prepared),
		Mode:       sandbox.ConfinementRequired,
		Backend:    sandbox.BackendAuto,
		Roots:      roots,
		FS:         fs,
		Runtime:    runtime,
		Network:    network,
		Subprocess: subprocess,
	})
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func preparedAccessRoots(prepared *agentlaunch.PreparedExecution) agentlaunch.ExecutionRoots {
	roots := prepared.Access.Roots
	if roots.ProjectRoot == "" {
		roots.ProjectRoot = prepared.Roots.ProjectRoot
	}
	if roots.BootRoot == "" {
		roots.BootRoot = prepared.Roots.BootRoot
	}
	if roots.StateRoot == "" {
		roots.StateRoot = prepared.Roots.StateRoot
	}
	if roots.ScratchRoot == "" {
		roots.ScratchRoot = prepared.Roots.ScratchRoot
	}
	if roots.CWD == "" {
		roots.CWD = prepared.Roots.CWD
	}
	return roots
}

func rootedPathRef(root sandbox.RootName, path string) sandbox.PathRef {
	if filepath.IsAbs(path) {
		return sandbox.PathRef{Path: path}
	}
	return sandbox.PathRef{Root: root, Relative: path}
}

func accessPathRef(item agentlaunch.AccessPath) (sandbox.PathRef, error) {
	switch item.Root {
	case "":
		if !filepath.IsAbs(item.Path) {
			return sandbox.PathRef{}, fmt.Errorf("%w: filesystem path %q needs a known root or absolute path", ErrPreparedAccessUnsupported, item.Path)
		}
		return sandbox.PathRef{Path: item.Path}, nil
	case agentlaunch.RootProject:
		return rootedPathRef(sandbox.ProjectRoot, item.Path), nil
	case agentlaunch.RootBoot:
		return rootedPathRef(sandbox.BootRoot, item.Path), nil
	case agentlaunch.RootState:
		return rootedPathRef(sandbox.StateRoot, item.Path), nil
	case agentlaunch.RootScratch:
		return rootedPathRef(sandbox.ScratchRoot, item.Path), nil
	case agentlaunch.RootRuntime, agentlaunch.RootOther:
		if !filepath.IsAbs(item.Path) {
			return sandbox.PathRef{}, fmt.Errorf("%w: %s filesystem path %q must be absolute", ErrPreparedAccessUnsupported, item.Root, item.Path)
		}
		return sandbox.PathRef{Path: item.Path}, nil
	default:
		return sandbox.PathRef{}, fmt.Errorf("%w: unknown filesystem root %q", ErrPreparedAccessUnsupported, item.Root)
	}
}

func sandboxRoots(roots agentlaunch.ExecutionRoots, fallbackCWD string) sandbox.Roots {
	project := firstNonEmpty(roots.ProjectRoot, roots.CWD, fallbackCWD)
	cwd := firstNonEmpty(roots.CWD, fallbackCWD, project)
	return sandbox.Roots{
		Project: project,
		Boot:    roots.BootRoot,
		State:   roots.StateRoot,
		Scratch: roots.ScratchRoot,
		CWD:     cwd,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func preparedPolicyID(prepared *agentlaunch.PreparedExecution) string {
	if prepared == nil {
		return ""
	}
	if prepared.Access.Roots.ProjectRoot != "" || prepared.Access.Roots.BootRoot != "" {
		return "prepared:" + string(prepared.InputKind)
	}
	return "prepared-execution"
}

func prepareSandboxForCommand(cmd *exec.Cmd, opts StartOptions) (SandboxOutcome, func(), error) {
	if opts.SandboxPolicy != nil {
		out, cleanup, err := sandbox.ApplyResolved(cmd, *opts.SandboxPolicy)
		mapped := sessionSandboxOutcome(out, false)
		if err != nil {
			return mapped, func() {}, &SandboxError{Outcome: mapped, Err: fmt.Errorf("agentsessions: sandbox apply resolved: %w", err)}
		}
		if cleanup == nil {
			cleanup = func() {}
		}
		return mapped, cleanup, nil
	}
	if opts.Profile.ID == "" {
		return SandboxOutcome{State: sandbox.EnforcementDisabled, Disabled: true}, func() {}, nil
	}
	cleanup, err := sandbox.Apply(cmd, opts.Profile, opts.Workdir)
	out := SandboxOutcome{PolicyID: opts.Profile.ID, Mode: sandbox.ConfinementRequired, Backend: sandbox.BackendAuto, State: sandbox.EnforcementApplied, Enforced: true, Legacy: true, Diagnostics: []string{"legacy Profile adapter: default-allow compatibility semantics"}}
	if err != nil {
		out.State = sandbox.EnforcementFailed
		out.Enforced = false
		out.Diagnostics = append(out.Diagnostics, err.Error())
		return out, func() {}, &SandboxError{Outcome: out, Err: fmt.Errorf("agentsessions: sandbox apply legacy profile: %w", err)}
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	return out, cleanup, nil
}

func sessionSandboxOutcomeFromRunner(out runner.SandboxOutcome) SandboxOutcome {
	state := sandbox.EnforcementFailed
	switch out.State {
	case runner.SandboxStateDisabled:
		state = sandbox.EnforcementDisabled
	case runner.SandboxStateUnsupported:
		state = sandbox.EnforcementUnsupported
	case runner.SandboxStateConfigured:
		state = sandbox.EnforcementConfigured
	case runner.SandboxStateLaunched:
		state = sandbox.EnforcementApplied
	case runner.SandboxStateFailed:
		state = sandbox.EnforcementFailed
	}
	return SandboxOutcome{
		PolicyID:     out.PolicyID,
		Mode:         out.Mode,
		Backend:      out.Backend,
		State:        state,
		Enforced:     out.Enforced,
		Disabled:     out.Disabled,
		Unsupported:  append([]string(nil), out.Unsupported...),
		Diagnostics:  append([]string(nil), out.Diagnostics...),
		BackendGOOS:  out.BackendGOOS,
		BackendReady: out.BackendReady,
		Legacy:       out.Legacy,
	}
}

func sessionSandboxOutcome(out sandbox.EnforcementOutcome, legacy bool) SandboxOutcome {
	mapped := SandboxOutcome{
		PolicyID:     out.PolicyID,
		Mode:         out.Mode,
		Backend:      out.Backend,
		State:        out.State,
		Enforced:     out.Enforced,
		Disabled:     out.Disabled,
		Diagnostics:  append([]string(nil), out.Diagnostics...),
		BackendGOOS:  out.BackendGOOS,
		BackendReady: out.BackendReady,
		Legacy:       legacy,
	}
	for _, cap := range out.Unsupported {
		mapped.Unsupported = append(mapped.Unsupported, string(cap))
	}
	return mapped
}

// PreparedSandboxPolicy derives the resolved go-sandbox policy represented by a
// prepared execution's access requirements. It is exported for host runtimes
// outside agentsessions, such as go-agent-wrapper's ACP client lifecycle, that
// must enforce the same prepared access contract at their own spawn boundary.
func PreparedSandboxPolicy(prepared *agentlaunch.PreparedExecution, fallbackCWD string) (*sandbox.ResolvedAccessPolicy, error) {
	return sandboxPolicyFromPrepared(prepared, fallbackCWD)
}
