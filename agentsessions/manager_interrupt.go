package agentsessions

import "context"

// InterruptTurn asks the registered session to end its current turn without
// stopping the session. It returns on the provider's acknowledgement; the
// turn's terminal event follows on the session's event surface. Callers must
// wait for that event before submitting a replacement turn.
//
// Missing sessions return ErrSessionNotRunning. Sessions or adapters without
// this capability return ErrInterruptUnsupported. Other errors, including
// caller cancellation and provider refusals, propagate unchanged.
//
// The registry and input locks are not held across the call: a running
// SendInput may be waiting for precisely the turn this request interrupts.
func (m *Manager) InterruptTurn(ctx context.Context, id string) error {
	m.mu.RLock()
	e, ok := m.registry[id]
	m.mu.RUnlock()
	if !ok {
		return ErrSessionNotRunning
	}
	interrupter, ok := e.sess.(TurnInterrupter)
	if !ok {
		return ErrInterruptUnsupported
	}
	return interrupter.InterruptTurn(ctx)
}
