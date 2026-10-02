//go:build !windows

package agentsessions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
)

// notifyPipe reports entry to Write before using the real pollable stdin pipe.
type notifyPipe struct {
	*os.File
	entered chan struct{}
	once    sync.Once
}

func (p *notifyPipe) Write(b []byte) (int, error) {
	p.once.Do(func() { close(p.entered) })
	return p.File.Write(b)
}

func interruptSession(stdin io.WriteCloser) *streamingStdioSession {
	s := &streamingStdioSession{adapter: provider.NewClaudeAdapterStreamingStdio(), stdin: stdin}
	s.alive.Store(true)
	return s
}

func assertNoInterruptWaiters(t *testing.T, s *streamingStdioSession) {
	t.Helper()
	s.interrupts.mu.Lock()
	defer s.interrupts.mu.Unlock()
	if len(s.interrupts.waiting) != 0 {
		t.Fatalf("retained interrupt waiters = %d", len(s.interrupts.waiting))
	}
}

func TestStreamingInterruptCancelledWhileInputLocked(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	p := &notifyPipe{File: w, entered: make(chan struct{})}
	s := interruptSession(p)
	s.ioLock.Lock()
	// Hold the input lock until the caller has returned. Releasing it must not
	// let any abandoned waiter send an interrupt to a successor turn.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.InterruptTurn(ctx) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		s.ioLock.Unlock()
		t.Fatal("input-lock wait ignored context")
	}
	s.ioLock.Unlock()
	assertNoInterruptWaiters(t, s)
	if err := s.SendInput(context.Background(), []byte("successor")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("successor\n"))
	if _, err := io.ReadFull(r, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "successor\n" {
		t.Fatalf("late interrupt: %q", got)
	}
}

func TestStreamingInterruptNonReadingStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	// Fill the real nonblocking pipe without a reader, deterministically reaching
	// EAGAIN. SyscallConn preserves os.File's pollable state (unlike File.Fd).
	raw, err := w.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	filled := 0
	var fillErr error
	if err := raw.Control(func(fd uintptr) {
		chunk := make([]byte, 4096)
		for {
			n, err := syscall.Write(int(fd), chunk)
			if n > 0 {
				filled += n
			}
			if errors.Is(err, syscall.EAGAIN) {
				return
			}
			if err != nil {
				fillErr = err
				return
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if fillErr != nil {
		t.Fatal(fillErr)
	}
	p := &notifyPipe{File: w, entered: make(chan struct{})}
	s := interruptSession(p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.InterruptTurn(ctx) }()
	select {
	case <-p.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("write never entered")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = w.Close()
		t.Fatal("stdin write ignored cancellation")
	}
	assertNoInterruptWaiters(t, s)
	// Drain only the filler. A canceled write must not be waiting to enter when
	// room appears, and its expired deadline must not poison the next SendInput.
	if _, err := io.CopyN(io.Discard, r, int64(filled)); err != nil {
		t.Fatal(err)
	}
	if err := s.SendInput(context.Background(), []byte("successor")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("successor\n"))
	if _, err := io.ReadFull(r, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "successor\n" {
		t.Fatalf("late interrupt: %q", got)
	}
}

func TestStreamingInterruptPreCancelledAndMissingInput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	p := &notifyPipe{File: w, entered: make(chan struct{})}
	s := interruptSession(p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.InterruptTurn(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	select {
	case <-p.entered:
		t.Fatal("canceled interrupt wrote to stdin")
	default:
	}
	assertNoInterruptWaiters(t, s)
	s.stdin = nil
	if err := s.InterruptTurn(context.Background()); !errors.Is(err, ErrNoInputChannel) {
		t.Fatalf("error = %v", err)
	}
	assertNoInterruptWaiters(t, s)
	// A failed write releases the same lock used by subsequent input.
	s.ioLock.Lock()
	if s.stdin != nil {
		t.Error("missing input changed")
	}
	s.ioLock.Unlock()
}

// delayedDeadlinePipe forces cancellation's deadline callback to finish after
// Write has succeeded. Waiting for entered makes the callback race deterministic.
type delayedDeadlinePipe struct {
	*os.File
	cancel   context.CancelFunc
	entered  chan struct{}
	finished chan struct{}
}

func (p *delayedDeadlinePipe) Write(b []byte) (int, error) {
	n, err := p.File.Write(b)
	p.cancel()
	<-p.entered
	return n, err
}
func (p *delayedDeadlinePipe) SetWriteDeadline(at time.Time) error {
	if at.IsZero() {
		return p.File.SetWriteDeadline(at)
	}
	close(p.entered)
	time.Sleep(80 * time.Millisecond)
	err := p.File.SetWriteDeadline(at)
	close(p.finished)
	return err
}

func TestStreamingInterruptJoinsDeadlineBeforeNextInput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &delayedDeadlinePipe{File: w, cancel: cancel, entered: make(chan struct{}), finished: make(chan struct{})}
	s := interruptSession(p)
	if err := s.InterruptTurn(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	select {
	case <-p.finished:
	default:
		// Join even in a failing implementation so the fixture leaves no callback.
		<-p.finished
		t.Fatal("interrupt returned before deadline callback finished")
	}
	// Swap only the wrapper out after cancellation; reuse the same os.File and
	// deadline. A non-joined callback or an uncleared deadline breaks this write.
	s.stdin = w
	if err := s.SendInput(context.Background(), []byte("successor")); err != nil {
		t.Fatal(err)
	}
	// The successful interrupt frame remains a complete line, followed by input.
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(buf[:n], []byte("\nsuccessor\n")) {
		t.Fatalf("stdin = %q", buf[:n])
	}
	assertNoInterruptWaiters(t, s)
}

type customInterruptAdapter struct {
	provider.CLIAdapter
	provider.TurnInterrupter
	frame        []byte
	panicRequest bool
}

func (a customInterruptAdapter) InterruptRequest(string) []byte {
	if a.panicRequest {
		panic("bad interrupt adapter")
	}
	return a.frame
}

func TestStreamingInterruptOversizedFrameAndAdapterPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprint(panics), func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			defer func() { _ = w.Close() }()
			if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			p := &notifyPipe{File: w, entered: make(chan struct{})}
			s := interruptSession(p)
			a := provider.NewClaudeAdapterStreamingStdio()
			s.adapter = customInterruptAdapter{CLIAdapter: a, TurnInterrupter: a, frame: bytes.Repeat([]byte("x"), 4096), panicRequest: panics}
			if panics {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("adapter did not panic")
						}
					}()
					_ = s.InterruptTurn(context.Background())
				}()
			} else {
				if err := s.InterruptTurn(context.Background()); !errors.Is(err, ErrInterruptFrameTooLarge) {
					t.Fatalf("error = %v", err)
				}
			}
			select {
			case <-p.entered:
				t.Fatal("rejected interrupt wrote bytes")
			default:
			}
			assertNoInterruptWaiters(t, s)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.ioLock.LockContext(ctx); err != nil {
				t.Fatalf("lock retained: %v", err)
			}
			s.stdin = w
			s.ioLock.Unlock()
			if err := s.SendInput(context.Background(), []byte("successor")); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len("successor\n"))
			if _, err := io.ReadFull(r, got); err != nil {
				t.Fatal(err)
			}
			if string(got) != "successor\n" {
				t.Fatalf("corrupted input: %q", got)
			}
		})
	}
}

func TestStreamingInterruptClosedPipePreservesErrorIdentity(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	s := interruptSession(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.InterruptTurn(ctx); !errors.Is(err, os.ErrClosed) || !errors.Is(err, ErrNoInputChannel) {
		t.Fatalf("closed input error = %v", err)
	}
	assertNoInterruptWaiters(t, s)
}

func TestInterruptFrameAtomicSizeBoundary(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	if err := r.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	frame := append(bytes.Repeat([]byte("x"), 4095), '\n')
	if err := writeInterrupt(context.Background(), w, frame); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(frame))
	if _, err := io.ReadFull(r, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatal("atomic-limit frame changed")
	}
}
