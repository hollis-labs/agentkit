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

	// ErrUnknownRenderer is returned by the default resolver when the
	// matrix descriptor names a BootDirRenderer this package has no
	// adapter for.
	ErrUnknownRenderer = errors.New("agentlaunch/providerplant: unknown bootdir renderer")
)
