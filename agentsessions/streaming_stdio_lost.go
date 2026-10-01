//go:build !windows

package agentsessions

import (
	"io"
	"os"
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

// sessionLostClassifyWait bounds how long a failed SendInput waits for the
// exit of the attempt it wrote to to be classified, so a write that fails
// because the child just died is reported as a lost session rather than as a
// broken pipe.
const sessionLostClassifyWait = 3 * time.Second

// stderrCapture is one attempt's stderr, routed through a pipe the session
// owns so a bounded tail can be classified when the child exits. Every byte
// still reaches the destination the child's stderr had before: the caller's
// StartOptions.Stderr, or the session log.
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

// classifyExit decides whether an attempt that has exited, with its stdout
// and stderr drained, exited because the provider lost the session it was
// asked to resume. If so the session is marked lost, once, and the loss is
// announced after the provider's own final output: the typed
// events.SessionLost and the byte Fanout marker, with the reason the
// per-turn runtime uses for a failed resume. The dead id is dropped, so it
// is not handed out again as the session's provider id. A clean exit, or an
// attempt that was not a resume, is never classified. It is safe on a nil
// capture.
func (s *streamingStdioSession) classifyExit(c *stderrCapture, waitErr error) {
	if c == nil {
		return
	}
	defer close(c.classified)
	if waitErr == nil || !c.classifier.IsSessionLost(c.tail.Bytes()) {
		return
	}
	if !s.lost.CompareAndSwap(nil, &SessionLostError{RequestedID: c.requestedID, Err: waitErr}) {
		return
	}
	s.lastSessionID.CompareAndSwap(c.requestedID, "")
	emitSessionLost(&s.opts, c.requestedID, "", sessionLostFailedReason)
}

// inputFailure turns a failed write to the child into the lost-session error
// when the child died because the provider lost the session. The write fails
// as soon as the child exits, which can be before its exit has been
// classified, so the classification is awaited, briefly. Any other failure
// is returned as it was.
func (s *streamingStdioSession) inputFailure(err error) error {
	if c := s.currentStderrCapture(); c != nil {
		select {
		case <-c.classified:
		case <-time.After(sessionLostClassifyWait):
		}
		if lost := s.lost.Load(); lost != nil {
			return &SessionLostError{RequestedID: lost.RequestedID, Err: err}
		}
	}
	return err
}
