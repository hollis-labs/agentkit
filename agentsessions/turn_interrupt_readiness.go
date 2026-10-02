package agentsessions

import (
	"errors"

	"github.com/hollis-labs/go-providers/provider"
)

// ErrTurnNotStarted means the session can cancel turns but has not yet tracked
// the runtime handle needed to target one. A host may retry after the runtime's
// start notification, with a bounded deadline.
var ErrTurnNotStarted = errors.New("agentsessions: interrupt turn has not started")

// TurnInterruptReadiness is an optional precondition for Manager.InterruptTurn.
// A session reports whether it has the handle required to address cancellation.
// Unsupported adapters return ErrInterruptUnsupported. The query does no I/O;
// readiness can change before cancellation, so terminal events remain authoritative.
type TurnInterruptReadiness interface {
	TurnInterruptReady() (bool, error)
}

// TurnInterruptReady reports the handle observed in a turn-start notification.
// Manager uses it to avoid acknowledging a cancellation before that notification;
// the session's direct InterruptTurn no-turn contract remains unchanged.
func (s *jsonRpcStdioSession) TurnInterruptReady() (bool, error) {
	if _, ok := s.adapter.(provider.RPCTurnInterrupter); !ok {
		return false, ErrInterruptUnsupported
	}
	return s.rpcTurn.Load() != nil, nil
}
