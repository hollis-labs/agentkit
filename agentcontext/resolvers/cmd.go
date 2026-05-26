package resolvers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// Cmd resolver defaults. Operators may override
// DefaultCmdTimeout via WithCmdDefaultTimeout. The hard ceiling
// (DefaultCmdMaxTimeout) caps any caller-supplied
// CmdSource.Timeout — a 5-minute slot is already aggressive for
// session-boot orchestration.
const (
	DefaultCmdTimeout    = 30 * time.Second
	DefaultCmdMaxTimeout = 5 * time.Minute
)

// CmdResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindCmd.
//
// The resolver runs SlotSource.Cmd.Run via `sh -c` on POSIX and via
// `cmd /C` on Windows. CWD defaults to ResolverEnv.Workdir when
// CmdSource.CWD is empty. Stdout is captured for SlotResult.Content;
// stderr is captured into SlotProvenance.Extra["stderr_tail"]
// (truncated to 4 KiB so a runaway command does not pin large amounts
// of memory inside provenance).
//
// # Timeout
//
// CmdSource.Timeout overrides the resolver-side default
// (DefaultCmdTimeout). Any caller-supplied timeout is clamped at
// DefaultCmdMaxTimeout so an over-eager operator config cannot stall
// a session. On timeout, the resolver returns ErrCmdTimeout (which
// wraps context.DeadlineExceeded).
//
// # Exit handling
//
// A non-zero exit returns ErrCmdFailed wrapping a description that
// includes the exit code and a stderr tail. The slot's Required-ness
// is the dispatcher's concern.
//
// # Security note
//
// CmdSource.Run is operator-supplied configuration. The resolver
// wraps it with the shell verbatim, matching Tether's and Nanite's
// existing boot-slot semantics (which already pipe arbitrary shell
// pipelines through). This is by design — boot configs are trusted
// inputs. The resolver MUST NOT be exposed to attacker-controlled
// SlotSource values.
type CmdResolver struct {
	defaultTimeout time.Duration
	maxTimeout     time.Duration
	stderrTail     int
}

// CmdOption configures a CmdResolver.
type CmdOption func(*CmdResolver)

// WithCmdDefaultTimeout sets the duration applied when
// CmdSource.Timeout is zero. Non-positive values are ignored.
func WithCmdDefaultTimeout(d time.Duration) CmdOption {
	return func(r *CmdResolver) {
		if d > 0 {
			r.defaultTimeout = d
		}
	}
}

// WithCmdMaxTimeout sets the hard ceiling for caller-supplied
// CmdSource.Timeout values. Non-positive values disable the cap.
func WithCmdMaxTimeout(d time.Duration) CmdOption {
	return func(r *CmdResolver) {
		r.maxTimeout = d
	}
}

// NewCmdResolver returns a CmdResolver configured with the supplied
// options. Defaults: defaultTimeout=DefaultCmdTimeout,
// maxTimeout=DefaultCmdMaxTimeout, stderr tail capped at 4 KiB.
func NewCmdResolver(opts ...CmdOption) agentcontext.Resolver {
	r := &CmdResolver{
		defaultTimeout: DefaultCmdTimeout,
		maxTimeout:     DefaultCmdMaxTimeout,
		stderrTail:     4 * 1024,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve implements agentcontext.Resolver.
func (r *CmdResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindCmd {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindCmd)
	}

	run := strings.TrimSpace(spec.Source.Cmd.Run)
	if run == "" {
		return agentcontext.SlotResult{}, fmt.Errorf("cmd: empty Run")
	}

	timeout := spec.Source.Cmd.Timeout
	if timeout <= 0 {
		timeout = r.defaultTimeout
	}
	if r.maxTimeout > 0 && timeout > r.maxTimeout {
		timeout = r.maxTimeout
	}

	cwd := spec.Source.Cmd.CWD
	if cwd == "" {
		cwd = env.Workdir
	}
	if cwd != "" {
		// Expand ~ for ergonomics; resolveFilePath also cleans the
		// path, but for cwd we don't require existence — exec.Cmd
		// will surface the error if the directory is missing.
		cwd = expandPath(cwd)
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/C", run)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", run)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := nowUTC()
	err := cmd.Run()
	finishedAt := nowUTC()

	stderrTail := tailBytes(stderr.Bytes(), r.stderrTail)
	extra := map[string]string{
		"cmd":         run,
		"timeout":     timeout.String(),
		"started_at":  startedAt.Format(time.RFC3339Nano),
		"finished_at": finishedAt.Format(time.RFC3339Nano),
	}
	if len(stderrTail) > 0 {
		extra["stderr_tail"] = string(stderrTail)
	}

	if cctx.Err() == context.DeadlineExceeded {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: ran for %s: %w", agentcontext.ErrCmdTimeout, timeout, context.DeadlineExceeded)
	}

	if err != nil {
		// Distinguish exit error vs other run errors.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			extra["exit_code"] = fmt.Sprintf("%d", exitErr.ExitCode())
			return agentcontext.SlotResult{}, fmt.Errorf("%w: exit %d: %s", agentcontext.ErrCmdFailed, exitErr.ExitCode(), strings.TrimSpace(string(stderrTail)))
		}
		return agentcontext.SlotResult{}, fmt.Errorf("cmd: run %q: %w", run, err)
	}

	content := stdout.String()
	return agentcontext.SlotResult{
		Content: content,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindCmd,
			Source:      "cmd:" + firstWord(run),
			Bytes:       len(content),
			ContentHash: hashContent(content),
			FetchedAt:   finishedAt,
			Extra:       extra,
		},
	}, nil
}

// tailBytes returns the trailing n bytes of b (or all of b if it is
// shorter). When the slice is trimmed, a leading "...\n" marker is
// prepended so log scrapers can recognise the truncation.
func tailBytes(b []byte, n int) []byte {
	if n <= 0 || len(b) <= n {
		return b
	}
	out := make([]byte, 0, n+4)
	out = append(out, "...\n"...)
	out = append(out, b[len(b)-n:]...)
	return out
}
