package agentsessions

import "slices"

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
