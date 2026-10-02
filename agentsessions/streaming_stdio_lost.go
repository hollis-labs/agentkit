//go:build !windows

package agentsessions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hollis-labs/go-providers/provider"
)

// A streaming-stdio child that is started to resume a provider session the
// provider no longer has dies at once: Claude writes "No conversation found
// with session ID: <id>" to stderr, answers the first turn with an empty
// error result and exits 1. The stdout stream alone cannot say why, so the
// session keeps a bounded tail of each resume attempt's stderr and, when the
// attempt exits abnormally, asks the adapter's SessionLostClassifier about it
// (CW-20261001-0222). The adapter-per-turn runtime does the same per turn
// (from_adapter.go).
//
// Only an attempt that never got going is classified. A resumed Claude that
// is healthy emits its init (a session id) before anything else, and its
// stderr can legitimately mention the same words, for instance in a tool's
// error line, so the tail of an attempt that produced a session id or a
// finished turn says nothing about the provider having lost the session. Nor
// is an attempt the session or its supervisor ended (Stop, a canceled ctx, an
// idle or watchdog kill) classified: its abnormal exit is the cause.

// sessionLostClassifyWait bounds how long a failed SendInput waits for the
// exit of the attempt it wrote to to be classified, so a write that fails
// because the child just died is reported as a lost session rather than as a
// broken pipe. The wait also ends with the caller's ctx and with Stop.
const sessionLostClassifyWait = 3 * time.Second

// stderrCapture is one attempt's stderr, routed through a pipe the session
// owns so a bounded tail can be classified when the child exits. Every byte
// the child wrote before it exited still reaches the destination its stderr
// had before: the caller's StartOptions.Stderr, or the session log. One bound
// applies: a descendant that keeps stderr open after the child exits is cut
// off after the same one-second drain as stdout (drainChildOutput). The two
// drains run one after the other, so such a descendant can hold the exit up
// for up to two seconds.
//
// A capture exists only for an attempt that resumes a provider session
// (a non-empty id in its argv) of an adapter that implements
// provider.SessionLostClassifier. Every other attempt keeps the plain
// routing and is never classified.
type stderrCapture struct {
	classifier  provider.SessionLostClassifier
	requestedID string
	tail        *tailWriter
	read        *os.File
	// copied is closed when the copier has read stderr to EOF (or the read
	// end was closed to cut off a descendant that kept stderr open).
	copied chan struct{}
	// classified is closed once the attempt's exit has been classified,
	// whatever the outcome.
	classified chan struct{}
	// progressed is set when the attempt's own stdout shows it got going: a
	// session id (the CLI's init) or a finished turn. Such an attempt did not
	// fail to resume, so its stderr is never classified.
	progressed atomic.Bool
}

// noteProgress records that the attempt got going. It is safe on a nil
// capture.
func (c *stderrCapture) noteProgress() {
	if c != nil {
		c.progressed.Store(true)
	}
}

// newStderrCapture returns a capture for an attempt that resumes
// requestedID, along with the pipe's write end for the child's stderr. It
// returns nil, nil, nil when the attempt is not classifiable.
func newStderrCapture(adapter provider.CLIAdapter, dst io.Writer, requestedID string) (*stderrCapture, *os.File, error) {
	classifier, ok := adapter.(provider.SessionLostClassifier)
	if !ok || requestedID == "" {
		return nil, nil, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	return &stderrCapture{
		classifier:  classifier,
		requestedID: requestedID,
		tail:        &tailWriter{w: dst, max: sessionLostTailBytes},
		read:        r,
		copied:      make(chan struct{}),
		classified:  make(chan struct{}),
	}, w, nil
}

// start begins copying the child's stderr to the tail. The write end the
// child inherited must be the only one left open, so the parent's copy is
// closed first.
func (c *stderrCapture) start(writeEnd *os.File) {
	_ = writeEnd.Close()
	go func() {
		defer close(c.copied)
		buf := make([]byte, 32*1024)
		for {
			n, err := c.read.Read(buf)
			if n > 0 {
				// A failing destination must not stop the copy: the child
				// would block on a full pipe. The tail keeps the bytes
				// either way.
				_, _ = c.tail.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
}

// abort releases the pipe of an attempt that never started.
func (c *stderrCapture) abort(writeEnd *os.File) {
	_ = writeEnd.Close()
	_ = c.read.Close()
}

// drain waits for the copier to finish after the child has exited, bounded
// as the stdout drain is. It is safe on a nil capture.
func (c *stderrCapture) drain() {
	if c == nil {
		return
	}
	drainChildOutput(c.read, c.copied)
}

// currentStderrCapture is the capture of the attempt now running, or nil.
func (s *streamingStdioSession) currentStderrCapture() *stderrCapture {
	s.ioLock.Lock()
	defer s.ioLock.Unlock()
	return s.stderrCap
}

// classifyExit decides whether an attempt that has exited, with its stderr
// drained, exited because the provider lost the session it was asked to
// resume. If so the session is marked lost, once, and the loss is returned for
// announceLost. It runs right after the child exits and its stderr is
// drained, before stdout is: a SendInput made from a reader callback, which
// the stdout drain waits for, must find the loss already decided. It closes
// c.classified whatever the outcome.
//
// A clean exit, an attempt that was not a resume, an attempt that got going
// (c.progressed), and an attempt the session or its supervisor ended
// (ended) are never classified. It is safe on a nil capture.
//
// c.progressed is read as the reader left it when the child exited. The reader
// may still be catching up on stdout then, which could only miss an init
// written in the last instants of a child whose stderr also says the session
// is lost. A resume the provider lost writes no init at all.
func (s *streamingStdioSession) classifyExit(c *stderrCapture, waitErr error, ended bool) *SessionLostError {
	if c == nil {
		return nil
	}
	defer close(c.classified)
	if waitErr == nil || ended || c.progressed.Load() || !c.classifier.IsSessionLost(c.tail.Bytes()) {
		return nil
	}
	lost := &SessionLostError{RequestedID: c.requestedID, Err: waitErr}
	if !s.lost.CompareAndSwap(nil, lost) {
		return nil
	}
	return lost
}

// announceLost reports a loss classifyExit decided, after the provider's own
// final output: the typed events.SessionLost and the byte Fanout marker, with
// the reason the per-turn runtime uses for a failed resume. The dead id is
// dropped, so it is not handed out again as the session's provider id. It
// does nothing for a nil loss.
//
// The loss is decided first, so a SendInput can return *SessionLostError a
// moment before this announces it, by as long as the stdout drain takes. The
// announcement is out before Wait returns.
func (s *streamingStdioSession) announceLost(lost *SessionLostError) {
	if lost == nil {
		return
	}
	s.lastSessionID.CompareAndSwap(lost.RequestedID, "")
	emitSessionLost(&s.opts, lost.RequestedID, "", sessionLostFailedReason)
}

// attemptEnded reports whether the session or its supervisor ended the
// attempt rather than the attempt ending on its own: Stop, a canceled ctx, or
// a supervisor kill (idle, watchdog). Its abnormal exit is then not a loss.
func (s *streamingStdioSession) attemptEnded(ctx context.Context, supervisorCause string) bool {
	return ctx.Err() != nil || s.isStopRequested() || supervisorCause != ""
}

// inputFailure turns a failed write to the child into the lost-session error
// when the child died because the provider lost the session. The write fails
// as soon as the child exits, which can be before its exit has been
// classified, so the classification is awaited, briefly. The wait ends with
// the caller's ctx and with Stop, and is skipped when the write did not fail
// because the child is gone. (It does not wait on Wait's done channel, which
// Wait consumes.) The underlying write error stays reachable from the result,
// and so does ErrNoInputChannel, as for a SendInput after the loss. Any other
// failure is returned as it was.
func (s *streamingStdioSession) inputFailure(ctx context.Context, err error) error {
	c := s.currentStderrCapture()
	if c == nil || !isDeadChildWrite(err) {
		return err
	}
	timer := time.NewTimer(sessionLostClassifyWait)
	defer timer.Stop()
	select {
	case <-c.classified:
	case <-ctx.Done():
	case <-s.stopRequested:
	case <-timer.C:
	}
	if lost := s.lost.Load(); lost != nil {
		return &SessionLostError{RequestedID: lost.RequestedID, Err: fmt.Errorf("%w: %w", ErrNoInputChannel, err)}
	}
	return err
}

// isDeadChildWrite reports whether a failed write to the child's stdin means
// the child is gone: a broken or closed pipe, or no input channel left.
func isDeadChildWrite(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, os.ErrClosed) || errors.Is(err, ErrNoInputChannel)
}
