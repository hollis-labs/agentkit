package agentsessions

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync/atomic"
)

// maxLineBytes is the longest child output line a session routes. A child
// writes one protocol frame per line and a frame can be large (Codex's
// item/completed carries a command's whole output, Claude's tool_result a
// whole file), so the limit is generous. A longer line is read through and
// reported, never left in the pipe. A reader that stopped at a long line
// stopped draining stdout: the child blocked on the full pipe while the
// session still looked alive, and every later call waited out its deadline
// (CW-20261001-0086). It is a variable so tests can exercise the oversize
// path without allocating 64 MiB.
var maxLineBytes = 64 << 20

// readLines calls fn with each line of r, without its line ending (as
// bufio.ScanLines strips "\n" and "\r\n"). The slice is valid only during the
// call. A line longer than maxLineBytes is read through and reported to
// oversize with its length instead; reading carries on. A last line without
// a newline is delivered at EOF. readLines returns nil at EOF and the read
// error otherwise.
func readLines(r io.Reader, fn func(line []byte), oversize func(n int)) error {
	limit := maxLineBytes
	br := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	n, over := 0, false
	flush := func() {
		if over {
			if oversize != nil {
				oversize(n)
			}
		} else {
			l := bytes.TrimSuffix(line, []byte{'\n'})
			fn(bytes.TrimSuffix(l, []byte{'\r'}))
		}
		// Do not hold on to the buffer of an unusually long line.
		if cap(line) > 1<<20 {
			line = nil
		} else {
			line = line[:0]
		}
		n, over = 0, false
	}
	for {
		chunk, err := br.ReadSlice('\n')
		n += len(chunk)
		if !over {
			if len(line)+len(chunk) > limit+1 {
				over = true
				line = line[:0]
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case err == nil:
			flush()
		case errors.Is(err, bufio.ErrBufferFull):
			// The line continues past the read buffer; keep reading.
		case errors.Is(err, io.EOF):
			if n > 0 {
				flush()
			}
			return nil
		default:
			return err
		}
	}
}

// readerFailed reports whether err, returned by readLines, is a real read
// failure rather than the end of the stream. EOF and a closed read end (the
// session closing its own end after the child exits, see drainChildOutput)
// are how a reader normally stops.
func readerFailed(err error) bool {
	return err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.ErrClosedPipe)
}

// noteOversizeLine records a line readLines skipped, in the process log and
// in the session's own log.
func noteOversizeLine(sessionLog io.Writer, runtime, id string, n int) {
	msg := fmt.Sprintf("agentsessions: %s session %s: skipped a %d-byte output line over the %d-byte limit; it was not parsed or routed", runtime, id, n, maxLineBytes)
	log.Print(msg)
	if sessionLog != nil {
		_, _ = fmt.Fprintln(sessionLog, msg)
	}
}

// readerFault holds why a session's output reader stopped before the child
// closed its output. Once set, the session is no longer usable: Health
// reports it not alive and input fails at once with the error instead of
// waiting on a reader that will never answer.
type readerFault struct{ v atomic.Value }

type readerFaultBox struct{ err error }

func (f *readerFault) set(err error) { f.v.Store(readerFaultBox{err}) }

func (f *readerFault) get() error {
	if b, ok := f.v.Load().(readerFaultBox); ok {
		return b.err
	}
	return nil
}

// failReader records a reader failure, logs it, and keeps draining r so the
// child can never block on a full pipe.
func failReader(f *readerFault, runtime, id string, err error, r io.Reader) {
	f.set(fmt.Errorf("agentsessions: %s output reader failed: %w", runtime, err))
	log.Printf("agentsessions: %s session %s: output reader failed, the session is no longer usable: %v", runtime, id, err)
	_, _ = io.Copy(io.Discard, r)
}
