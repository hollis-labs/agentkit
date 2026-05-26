package resolvers

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// DefaultRoleSummaryMaxBytes caps the body size of a role-summary
// slot. Role/persona prompts are typically a few KiB; the cap is a
// safety net for very long role files. Operators override via
// WithRoleSummaryMaxBytes.
const DefaultRoleSummaryMaxBytes = 4096

// RoleSummaryResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindRoleSummary.
//
// The resolver reads SlotSource.RoleSummary.Path (expanding ~), and
// returns either the full body or the markdown section named by
// SlotSource.RoleSummary.Section. Section matching is heading-prefix
// based: any heading line whose trimmed-of-leading-# text starts with
// the requested name (case-insensitive) anchors the section, and the
// section runs until the next heading of equal or shallower depth (or
// EOF).
//
// # Byte cap
//
// The contract's RoleSummarySource does not carry a MaxBytes field,
// so the resolver enforces its own cap
// (DefaultRoleSummaryMaxBytes, configurable via
// WithRoleSummaryMaxBytes). When the resolved body exceeds the cap,
// the resolver truncates and sets SlotResult.Truncated = true.
//
// # Markdown summarization
//
// Unlike Tether's bootgen role_summary (which extracts a "mission"
// paragraph), this resolver returns the requested section verbatim.
// Higher-level prompt composition is the orchestrator's job; the
// resolver only owns I/O and section extraction.
type RoleSummaryResolver struct {
	maxBytes int64
}

// RoleSummaryOption configures a RoleSummaryResolver.
type RoleSummaryOption func(*RoleSummaryResolver)

// WithRoleSummaryMaxBytes overrides the resolver-side byte cap.
// Non-positive disables the cap (NOT recommended).
func WithRoleSummaryMaxBytes(n int64) RoleSummaryOption {
	return func(r *RoleSummaryResolver) {
		r.maxBytes = n
	}
}

// NewRoleSummaryResolver returns a RoleSummaryResolver configured
// with the supplied options. Default: maxBytes =
// DefaultRoleSummaryMaxBytes.
func NewRoleSummaryResolver(opts ...RoleSummaryOption) agentcontext.Resolver {
	r := &RoleSummaryResolver{maxBytes: DefaultRoleSummaryMaxBytes}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve implements agentcontext.Resolver.
func (r *RoleSummaryResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindRoleSummary {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindRoleSummary)
	}

	path, err := resolveFilePath(spec.Source.RoleSummary.Path, env.Workdir)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("role_summary: resolve path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("role_summary: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return agentcontext.SlotResult{}, fmt.Errorf("role_summary: %s is a directory", path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path; SlotSpec.Validate guards ..
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("role_summary: read %s: %w", path, err)
	}

	body := string(data)
	section := strings.TrimSpace(spec.Source.RoleSummary.Section)
	if section != "" {
		body = extractMarkdownSection(body, section)
	}

	truncated := false
	if r.maxBytes > 0 && int64(len(body)) > r.maxBytes {
		body = body[:r.maxBytes]
		truncated = true
	}

	extra := map[string]string{
		"path": path,
	}
	if section != "" {
		extra["section"] = section
	}
	if truncated {
		extra["truncated_to"] = fmt.Sprintf("%d", r.maxBytes)
	}

	return agentcontext.SlotResult{
		Content:   body,
		Truncated: truncated,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindRoleSummary,
			Source:      path,
			Bytes:       len(body),
			ContentHash: hashContent(body),
			FetchedAt:   nowUTC(),
			Extra:       extra,
		},
	}, nil
}

// extractMarkdownSection returns the lines of body starting from the
// first heading whose trimmed text starts with section (case
// insensitive) and ending just before the next heading of equal or
// shallower depth. The matched heading line is included. Returns the
// full body unchanged if no matching heading is found, which mirrors
// the existing Tether behaviour of "best-effort summarize" rather
// than hard-failing on a missing section.
func extractMarkdownSection(body, section string) string {
	lines := strings.Split(body, "\n")
	target := strings.ToLower(section)

	startIdx := -1
	startDepth := 0
	for i, line := range lines {
		depth, text, ok := parseHeading(line)
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.ToLower(text), target) {
			startIdx = i
			startDepth = depth
			break
		}
	}
	if startIdx < 0 {
		return body
	}

	end := len(lines)
	for j := startIdx + 1; j < len(lines); j++ {
		depth, _, ok := parseHeading(lines[j])
		if !ok {
			continue
		}
		if depth <= startDepth {
			end = j
			break
		}
	}
	return strings.Join(lines[startIdx:end], "\n")
}

// parseHeading reports whether line is a markdown ATX heading and
// returns its depth (# count) and trimmed text. Lines that are
// indented code blocks or whose leading # is not followed by a space
// (or end-of-line) are NOT treated as headings.
func parseHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	depth := 0
	for depth < len(trimmed) && trimmed[depth] == '#' {
		depth++
	}
	if depth == 0 || depth > 6 {
		return 0, "", false
	}
	rest := trimmed[depth:]
	if rest == "" {
		return depth, "", true
	}
	if rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	return depth, strings.TrimSpace(rest), true
}
