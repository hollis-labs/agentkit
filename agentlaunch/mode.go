package agentlaunch

// LaunchMode is the typed string enum that names the lifecycle stance
// the caller wants for the session: interactive (long-lived, attach-
// enabled), background (long-lived, no attach), or ephemeral (one-shot,
// terminates after first turn completes).
type LaunchMode string

const (
	// LaunchInteractive selects a long-lived session with attach enabled.
	// The Manager wires the attach broker and accepts SendInput from
	// connected viewers.
	LaunchInteractive LaunchMode = "interactive"

	// LaunchBackground selects a long-lived session WITHOUT attach
	// support — the orchestrator drives SendInput programmatically and
	// no viewer attaches. Lower overhead per session.
	LaunchBackground LaunchMode = "background"

	// LaunchEphemeral selects a one-shot session that terminates after
	// its first turn completes. Used for one-off task execution where a
	// long-lived process is wasteful.
	LaunchEphemeral LaunchMode = "ephemeral"
)

// Valid reports whether the receiver is one of the three declared
// LaunchMode constants. The zero value ("") is not valid.
func (m LaunchMode) Valid() bool {
	switch m {
	case LaunchInteractive, LaunchBackground, LaunchEphemeral:
		return true
	default:
		return false
	}
}

// String returns the underlying token (e.g. "interactive"). The zero
// value returns "".
func (m LaunchMode) String() string {
	return string(m)
}
