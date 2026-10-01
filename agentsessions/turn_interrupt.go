package agentsessions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/go-providers/provider"
)

// interrupts pairs a session's interrupt requests with the CLI's answers,
// which the output reader sees.
type interrupts struct {
	seq     atomic.Int64
	mu      sync.Mutex
	waiting map[string]chan error
}

func (in *interrupts) register() (string, chan error) {
	id := fmt.Sprintf("agentkit-interrupt-%d", in.seq.Add(1))
	ch := make(chan error, 1)
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.waiting == nil {
		in.waiting = map[string]chan error{}
	}
	in.waiting[id] = ch
	return id, ch
}

func (in *interrupts) forget(id string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	delete(in.waiting, id)
}

// answer delivers the CLI's answer to the request it names, if one is
// waiting.
func (in *interrupts) answer(id string, err error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if ch, ok := in.waiting[id]; ok {
		ch <- err
		delete(in.waiting, id)
	}
}

// failAll answers every waiting request with err: the output they would be
// answered on has ended.
func (in *interrupts) failAll(err error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	for id, ch := range in.waiting {
		ch <- err
		delete(in.waiting, id)
	}
}

// observe recognises an interrupt answer on an output line.
func (in *interrupts) observe(adapter provider.TurnInterrupter, line []byte) {
	if !bytes.Contains(line, []byte("control_response")) {
		return
	}
	if id, ok, err := adapter.InterruptResponse(line); ok {
		in.answer(id, err)
	}
}

// InterruptTurn asks the CLI to end the turn in flight and keep its process
// (Claude's stream-json control_request interrupt, through the adapter's
// provider.TurnInterrupter). It returns when the CLI acknowledges, with the
// CLI's refusal if it refused, or ctx's error. The interrupted turn ends on
// the event stream the CLI's own way: for Claude, an error result whose
// terminal_reason is aborted_tools or aborted_streaming. The process stays
// up and SendInput starts the next turn. With no turn in flight, Claude
// acknowledges and nothing else happens.
func (s *streamingStdioSession) InterruptTurn(ctx context.Context) error {
	interrupter, ok := s.adapter.(provider.TurnInterrupter)
	if !ok {
		return ErrInterruptUnsupported
	}
	if err := s.readerFault.get(); err != nil {
		return err
	}
	if !s.alive.Load() {
		return ErrNoInputChannel
	}
	id, answer := s.interrupts.register()
	frame := append(interrupter.InterruptRequest(id), '\n')
	s.ioLock.Lock()
	stdin := s.stdin
	var werr error
	if stdin == nil {
		werr = ErrNoInputChannel
	} else {
		_, werr = stdin.Write(frame)
	}
	s.ioLock.Unlock()
	if werr != nil {
		s.interrupts.forget(id)
		return werr
	}
	select {
	case err := <-answer:
		return err
	case <-ctx.Done():
		s.interrupts.forget(id)
		return ctx.Err()
	}
}

// followTurn tracks the open turn through the adapter's
// provider.RPCTurnInterrupter, for InterruptTurn.
func (s *jsonRpcStdioSession) followTurn(method string, params json.RawMessage) {
	ti, ok := s.adapter.(provider.RPCTurnInterrupter)
	if !ok {
		return
	}
	handle, started, ended := ti.TurnNotification(method, params)
	switch {
	case started:
		s.rpcTurn.Store(&handle)
	case ended:
		if cur := s.rpcTurn.Load(); cur != nil && bytes.Equal(*cur, handle) {
			s.rpcTurn.Store(nil)
		}
	}
}

// InterruptTurn interrupts the open turn with the adapter's interrupt
// request (provider.RPCTurnInterrupter: Codex app-server's turn/interrupt)
// and returns on its response. The turn then ends the runtime's usual way
// (Codex: turn/completed, status "interrupted") and the next turn runs on the
// same process. With no turn open it does nothing; a turn that ends while the
// request is in flight is not an error.
func (s *jsonRpcStdioSession) InterruptTurn(ctx context.Context) error {
	ti, ok := s.adapter.(provider.RPCTurnInterrupter)
	if !ok {
		return ErrInterruptUnsupported
	}
	turn := s.rpcTurn.Load()
	if turn == nil {
		return nil
	}
	method, params := ti.InterruptCall(*turn)
	_, err := s.Call(ctx, method, params)
	if err != nil && s.rpcTurn.Load() != turn {
		return nil
	}
	return err
}

// InterruptTurn aborts the session's turn (OpenCode serve's
// POST /session/{id}/abort) and returns on the server's answer. The turn
// ends with session.error (MessageAbortedError), which the session reports as
// an error event, and the server and session stay up for the next SendInput.
// With no turn in flight OpenCode answers true and nothing else happens.
func (s *serveHTTPSession) InterruptTurn(ctx context.Context) error {
	if !s.alive.Load() || s.sessionID == "" {
		return ErrNoInputChannel
	}
	s.turnMu.Lock()
	marked := s.turnInFlight
	if marked {
		s.interruptedTurn = true
	}
	s.turnMu.Unlock()
	endpoint := s.withWorkdirQuery(s.baseURL + "/session/" + url.PathEscape(s.sessionID) + "/abort")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err == nil {
		var resp *http.Response
		if resp, err = s.httpClient.Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				err = fmt.Errorf("agentsessions: serve-http abort: status %d: %s", resp.StatusCode, body)
			}
		}
	}
	if err != nil && marked {
		s.turnMu.Lock()
		s.interruptedTurn = false
		s.turnMu.Unlock()
	}
	return err
}
