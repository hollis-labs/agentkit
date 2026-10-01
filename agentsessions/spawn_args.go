package agentsessions

import (
	"fmt"

	"github.com/hollis-labs/go-providers/provider"
)

// spawnArgs is a spawn's argv after the binary. With a launch template (a
// prepared launch's projected convention) it is the template resolved for
// this turn, ExtraArgs at the convention's extra-argument slot; the adapter's
// BuildArgs is not consulted, so the provider argv is never doubled and every
// turn carries its own prompt and resume id. Without one it is the adapter's
// argv with ExtraArgs at the same slot when the adapter can place them, and
// before its "--" otherwise (see adapterArgs).
func spawnArgs(adapter provider.CLIAdapter, opts StartOptions, prompt, systemPrompt, sessionID string) ([]string, error) {
	if opts.Launch != nil {
		args, err := opts.Launch.TurnArgv(provider.TurnInput{Prompt: prompt, SystemPrompt: systemPrompt, ResumeID: sessionID}, opts.ExtraArgs...)
		if err != nil {
			return nil, fmt.Errorf("agentsessions: resolve launch argv: %w", err)
		}
		return args, nil
	}
	return adapterArgs(adapter, prompt, systemPrompt, sessionID, opts.ExtraArgs), nil
}
