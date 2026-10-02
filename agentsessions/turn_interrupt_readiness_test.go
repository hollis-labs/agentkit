package agentsessions

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/providertest"
)

type readinessSession struct {
	managerInterruptSession
	ready func() (bool, error)
}

func (s readinessSession) TurnInterruptReady() (bool, error) { return s.ready() }

type readinessRuntime struct {
	*fakeRuntime
	ready func() (bool, error)
	calls *atomic.Int32
}

func (r readinessRuntime) Start(ctx context.Context, opts StartOptions) (Session, error) {
	s, err := r.fakeRuntime.Start(ctx, opts)
	if err != nil {
		return nil, err
	}
	return readinessSession{managerInterruptSession: managerInterruptSession{Session: s, interrupt: func(context.Context) error { r.calls.Add(1); return nil }}, ready: r.ready}, nil
}

func TestManagerInterruptTurnReadiness(t *testing.T) {
	queryErr := errors.New("readiness query failed")
	for _, tc := range []struct {
		name           string
		ready          bool
		queryErr, want error
		calls          int32
	}{
		{"waiting for handle", false, nil, ErrTurnNotStarted, 0},
		{"unsupported", false, ErrInterruptUnsupported, ErrInterruptUnsupported, 0},
		{"query error", false, queryErr, queryErr, 0},
		{"ready", true, nil, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(nil)
			defer func() { _ = m.Stop(context.Background(), "s"); _ = m.Shutdown(context.Background()) }()
			var calls atomic.Int32
			rt := readinessRuntime{fakeRuntime: newFakeRuntime("ready", "test"), ready: func() (bool, error) { return tc.ready, tc.queryErr }, calls: &calls}
			if err := m.Start(context.Background(), StartRequest{ID: "s", Runtime: rt}); err != nil {
				t.Fatal(err)
			}
			if err := m.InterruptTurn(context.Background(), "s"); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if calls.Load() != tc.calls {
				t.Fatalf("cancel calls=%d, want %d", calls.Load(), tc.calls)
			}
		})
	}
}

func TestJSONRPCDirectInterruptNoTurnContract(t *testing.T) {
	s := &jsonRpcStdioSession{adapter: provider.NewCodexAdapterAppServer()}
	if ready, err := s.TurnInterruptReady(); ready || err != nil {
		t.Fatalf("readiness=%v,%v", ready, err)
	}
	if err := s.InterruptTurn(context.Background()); err != nil {
		t.Fatalf("direct no-turn interruption changed: %v", err)
	}
	s.adapter = provider.NewAntigravityAdapter()
	if _, err := s.TurnInterruptReady(); !errors.Is(err, ErrInterruptUnsupported) {
		t.Fatal(err)
	}
}

// The capture sends turn/start's response before turn/started. A scripted
// release frame holds that exact gap open deterministically; no model runs.
func TestManagerInterruptTurnResponseNotificationGap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := providertest.New(t, runtimes.Codex, providertest.Script(
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"turn/start"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":1,"result":{"turn":{"id":"gap-turn"}}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":2,"method":"test/release-start"}`),
		providertest.Send(`{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"gap-thread","turn":{"id":"gap-turn"}}}`),
		providertest.Send(`{"jsonrpc":"2.0","id":2,"result":{}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":3,"method":"turn/interrupt"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":3,"result":{}}`),
		providertest.Send(`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"gap-thread","turn":{"id":"gap-turn","status":"interrupted"}}}`),
		providertest.AwaitEOF(), providertest.Exit(0),
	))
	a := provider.NewCodexAdapterAppServer()
	a.Binary = fake.Path
	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "gap", Adapter: a, Caps: Capabilities{JsonRpcStdio: true, BinaryRequired: true}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(nil)
	defer func() { _ = m.Stop(context.Background(), "s"); _ = m.Shutdown(context.Background()) }()
	dir := t.TempDir()
	if err := m.Start(context.Background(), StartRequest{ID: "s", Runtime: rt, Options: StartOptions{Workdir: dir, LogPath: filepath.Join(dir, "session.log")}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.JsonRpcCall(ctx, "s", "turn/start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := m.InterruptTurn(ctx, "s"); !errors.Is(err, ErrTurnNotStarted) {
		t.Fatalf("untracked accepted turn acknowledged: %v", err)
	}
	if _, err := m.JsonRpcCall(ctx, "s", "test/release-start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := m.InterruptTurn(ctx, "s"); err != nil {
		t.Fatalf("tracked turn could not cancel: %v", err)
	}
}
