package providerplant

import (
	"context"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

// prepareAndPlantConfig collects the two option families PrepareAndPlant
// threads through to launcher.Prepare and Plant respectively.
type prepareAndPlantConfig struct {
	prepareOpts []launcher.PrepareOption
	plantOpts   []Option
}

// PrepareAndPlantOption configures a PrepareAndPlant call. Wrap a
// launcher.PrepareOption with WithPrepareOption and a Plant Option with
// WithPlantOption — PrepareAndPlant cannot take two variadic parameters,
// so both families funnel through this single option type.
type PrepareAndPlantOption func(*prepareAndPlantConfig)

// WithPrepareOption forwards a launcher.PrepareOption (WithContextHook,
// WithWorkspaceHook, …) to the Prepare stage of PrepareAndPlant.
func WithPrepareOption(o launcher.PrepareOption) PrepareAndPlantOption {
	return func(c *prepareAndPlantConfig) { c.prepareOpts = append(c.prepareOpts, o) }
}

// WithPlantOption forwards a Plant Option (WithAdapter, WithResolver) to
// the Plant stage of PrepareAndPlant.
func WithPlantOption(o Option) PrepareAndPlantOption {
	return func(c *prepareAndPlantConfig) { c.plantOpts = append(c.plantOpts, o) }
}

// PrepareAndPlant is the one-call launch API: it runs launcher.Prepare
// to materialize the workspace and bootdir, then Plant to write the
// provider boot files, native files, and injection overlay into that
// bootdir. The returned PreparedLaunch is fully materialized — bootdir
// planted, Env/Argv/Workdir rewired — and ready for the sessionshim
// conversion to go-agent-sessions StartOptions.
//
// On a Plant failure the partially materialized bootdir is NOT removed;
// cleanup of PreparedLaunch.PlantedBootDir remains the caller's
// responsibility either way (see launcher.Prepare docs).
func PrepareAndPlant(ctx context.Context, compiled *agentlaunch.CompiledLaunch, opts ...PrepareAndPlantOption) (*agentlaunch.PreparedLaunch, error) {
	var cfg prepareAndPlantConfig
	for _, o := range opts {
		o(&cfg)
	}
	prepared, err := launcher.Prepare(ctx, compiled, cfg.prepareOpts...)
	if err != nil {
		return nil, err
	}
	if err := Plant(ctx, prepared, cfg.plantOpts...); err != nil {
		return nil, err
	}
	return prepared, nil
}
