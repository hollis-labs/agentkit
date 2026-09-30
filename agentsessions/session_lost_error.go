package agentsessions

import (
	"fmt"

	"github.com/hollis-labs/go-providers/provider"
)

// SessionLostError is the error a resume turn fails with when the provider
// no longer has the session it was asked to continue. It carries the ids so a
// consumer can report the lost one without parsing the message.
//
// errors.Is(err, provider.ErrProviderSessionLost) is true, and so is a match
// against the underlying turn failure in Err. The message is the same text the
// wrapped sentinel produced before this type existed.
type SessionLostError struct {
	// RequestedID is the provider session id the turn asked to resume.
	RequestedID string
	// ActualID is the id of the session the provider used instead, when that
	// is known. It is empty when the turn failed rather than continuing in a
	// new session, which is the case for this error today.
	ActualID string
	// Err is the turn failure the session-lost detection accompanied.
	Err error
}

func (e *SessionLostError) Error() string {
	return fmt.Sprintf("agentsessions: provider session %q: %v: %v", e.RequestedID, provider.ErrProviderSessionLost, e.Err)
}

// Unwrap exposes both the sentinel and the underlying failure to errors.Is and
// errors.As.
func (e *SessionLostError) Unwrap() []error {
	return []error{provider.ErrProviderSessionLost, e.Err}
}
