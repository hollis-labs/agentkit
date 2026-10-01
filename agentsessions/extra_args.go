package agentsessions

import (
	"slices"

	"github.com/hollis-labs/go-providers/provider"
)

// adapterArgs is an adapter's argv for one turn with extra added. An adapter
// that implements provider.ExtraArgsBuilder places extra at its launch
// convention's extra-argument slot, where a Launch template puts it too: for a
// codex exec resume turn that is in front of `resume <id>`, the only place
// codex takes exec options (CW-20261001-0197). Otherwise, or when extra
// carries its own "--" (a whole argv tail), extra is spliced into BuildArgs'
// output by withExtraArgs.
func adapterArgs(adapter provider.CLIAdapter, prompt, systemPrompt, sessionID string, extra []string) []string {
	if eb, ok := adapter.(provider.ExtraArgsBuilder); ok && len(extra) > 0 && !slices.Contains(extra, "--") {
		return eb.BuildArgsWithExtras(prompt, systemPrompt, sessionID, extra)
	}
	return withExtraArgs(adapter.BuildArgs(prompt, systemPrompt, sessionID), extra)
}

// withExtraArgs returns args with ExtraArgs spliced in. Since go-providers
// v0.34.1 an adapter's argv with a prompt ends in "-- <prompt>"; flags
// appended after that "--" reach the agent as prompt text
// (CW-20261001-0102). So extra goes immediately before args' first "--", or
// is appended when there is none. An extra that carries its own "--" is a
// whole argv tail whose positionals its caller placed, and is appended
// unchanged as before.
func withExtraArgs(args, extra []string) []string {
	if len(extra) == 0 {
		return args
	}
	at := slices.Index(args, "--")
	if at < 0 || slices.Contains(extra, "--") {
		return append(args, extra...)
	}
	out := make([]string, 0, len(args)+len(extra))
	out = append(out, args[:at]...)
	out = append(out, extra...)
	return append(out, args[at:]...)
}
