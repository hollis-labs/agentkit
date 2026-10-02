package agentsessions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestManagerInterruptTurnUnsupported(t *testing.T) {
	m := NewManager(nil)
	defer func() {
		_ = m.Stop(context.Background(), "s")
		_ = m.Shutdown(context.Background())
	}()
	if err := m.InterruptTurn(context.Background(), "missing"); !errors.Is(err, ErrSessionNotRunning) {
		t.Fatalf("missing session: %v", err)
	}
	if err := m.Start(context.Background(), StartRequest{ID: "s", Runtime: newFakeRuntime("fake", "test")}); err != nil {
		t.Fatal(err)
	}
	if err := m.InterruptTurn(context.Background(), "s"); !errors.Is(err, ErrInterruptUnsupported) {
		t.Fatalf("unsupported session: %v", err)
	}
}

type managerInterruptRuntime struct {
	*fakeRuntime
	interrupt func(context.Context) error
	input     func(context.Context, []byte) error
}

func (r managerInterruptRuntime) Start(ctx context.Context, opts StartOptions) (Session, error) {
	s, err := r.fakeRuntime.Start(ctx, opts)
	if err != nil {
		return nil, err
	}
	return managerInterruptSession{Session: s, interrupt: r.interrupt, input: r.input}, nil
}

type managerInterruptSession struct {
	Session
	interrupt func(context.Context) error
	input     func(context.Context, []byte) error
}

func (s managerInterruptSession) InterruptTurn(ctx context.Context) error { return s.interrupt(ctx) }
func (s managerInterruptSession) SendInput(ctx context.Context, data []byte) error {
	if s.input != nil {
		return s.input(ctx, data)
	}
	return s.Session.SendInput(ctx, data)
}

func TestManagerInterruptTurnPropagatesErrors(t *testing.T) {
	refused := errors.New("provider refused")
	for _, want := range []error{ErrInterruptUnsupported, refused, context.Canceled} {
		t.Run(want.Error(), func(t *testing.T) {
			m := NewManager(nil)
			defer func() {
				_ = m.Stop(context.Background(), "s")
				_ = m.Shutdown(context.Background())
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if want == context.Canceled {
				cancel()
			}
			rt := managerInterruptRuntime{fakeRuntime: newFakeRuntime("interrupt", "test"), interrupt: func(got context.Context) error {
				if got != ctx {
					t.Error("caller context was replaced")
				}
				if want == context.Canceled {
					return got.Err()
				}
				return want
			}}
			if err := m.Start(context.Background(), StartRequest{ID: "s", Runtime: rt}); err != nil {
				t.Fatal(err)
			}
			if err := m.InterruptTurn(ctx, "s"); !errors.Is(err, want) {
				t.Fatalf("error %v, want %v", err, want)
			}
		})
	}
}

func TestManagerInterruptTurnDoesNotWaitForSendInput(t *testing.T) {
	m := NewManager(nil)
	defer func() {
		_ = m.Stop(context.Background(), "s")
		_ = m.Shutdown(context.Background())
	}()
	started, released := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	defer release()
	rt := managerInterruptRuntime{fakeRuntime: newFakeRuntime("interrupt", "test"), input: func(context.Context, []byte) error {
		close(started)
		<-released
		return nil
	}, interrupt: func(context.Context) error {
		// An interrupt must be able to unblock a provider whose SendInput
		// waits for its turn. Taking inputMu here would deadlock both.
		release()
		return nil
	}}
	if err := m.Start(context.Background(), StartRequest{ID: "s", Runtime: rt}); err != nil {
		t.Fatal(err)
	}
	inputDone := make(chan error, 1)
	go func() { inputDone <- m.SendInput("s", []byte("running")) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	interruptDone := make(chan error, 1)
	go func() { interruptDone <- m.InterruptTurn(ctx, "s") }()
	select {
	case err := <-interruptDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("interrupt blocked behind SendInput")
	}
	select {
	case err := <-inputDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, ok := m.Get("s"); !ok {
		t.Fatal("interrupt stopped the session")
	}
}
