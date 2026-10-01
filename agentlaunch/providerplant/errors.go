package providerplant

import "errors"

// Sentinel errors returned by this package. All are errors.Is-comparable;
// render / filesystem failures are wrapped with fmt.Errorf("%w: …") so the
// underlying cause is still reachable via errors.Unwrap.
var (
	// ErrNilPrepared is returned by Plant when the *PreparedLaunch
	// argument is nil.
	ErrNilPrepared = errors.New("agentlaunch/providerplant: nil prepared launch")

	// ErrNilCompiled is returned when the PreparedLaunch carries no
	// embedded CompiledLaunch (or that CompiledLaunch has a nil Plan) —
	// the planter cannot resolve a provider without one.
	ErrNilCompiled = errors.New("agentlaunch/providerplant: prepared launch has no compiled plan")

	// ErrAdapterResolution is returned when no provider adapter could be
	// resolved for the launch's provider×runtime pair.
	ErrAdapterResolution = errors.New("agentlaunch/providerplant: could not resolve provider adapter")

	// ErrNoBootDirSpec is returned when the resolved adapter does not
	// implement provider.BootDirProvider — it has no bootdir layout to
	// plant.
	ErrNoBootDirSpec = errors.New("agentlaunch/providerplant: adapter does not provide a BootDirSpec")

	// ErrPositionalAfterProjection is returned when the first of the
	// plan's Provider.Flags and Injection.Args is not an option, or when one
	// of them is "--". They follow the projected flags, which can end in a
	// variadic flag (Claude's --add-dir) that would swallow a positional, and
	// a "--" among them would make every later one a positional.
	ErrPositionalAfterProjection = errors.New("agentlaunch/providerplant: a positional argument would follow the projected argv")

	// ErrNoNativeAdapter is returned by the default resolver when the
	// launch runs over ACP (no boot dir to plant) or DefaultResolver has no
	// constructor for the runtime's go-providers adapter. A caller with its
	// own adapter passes WithAdapter or WithResolver.
	ErrNoNativeAdapter = errors.New("agentlaunch/providerplant: no native adapter for the runtime")
)
