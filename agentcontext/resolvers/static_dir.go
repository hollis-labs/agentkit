package resolvers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// DefaultStaticDirMaxFiles caps the number of files a single
// static_dir slot will concatenate. The contract's StaticDirSource
// does not yet carry a per-spec cap; this constant is the
// resolver-side safety net. Operators who legitimately need a larger
// cap can construct a custom StaticDirResolver via the
// WithMaxFiles option.
const DefaultStaticDirMaxFiles = 64

// StaticDirResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindStaticDir.
//
// The resolver lists files at SlotSource.StaticDir.Path (expanded for
// ~, resolved against ResolverEnv.Workdir for relative paths), filters
// them by the optional Glob, sorts lexicographically for determinism,
// and concatenates their bodies separated by a "\n\n" boundary. Dot
// files (".something") are skipped by default. Directories inside the
// listed path are NOT recursed — the contract is non-recursive.
//
// # Truncation
//
// If the matched file count exceeds the resolver's max-files cap
// (default DefaultStaticDirMaxFiles), the resolver concatenates the
// first N files in sorted order and marks SlotResult.Truncated. The
// list of included filenames is recorded in
// Provenance.Extra["files"] (comma-joined, sorted) so downstream
// audits can see which files participated.
//
// # Edge cases
//
//   - Empty Path → error.
//   - Path is a file (not a dir) → error.
//   - No glob matches → success with empty Content (the dispatcher's
//     Required check decides whether that's fatal).
type StaticDirResolver struct {
	maxFiles int
}

// StaticDirOption configures a StaticDirResolver. See WithMaxFiles.
type StaticDirOption func(*StaticDirResolver)

// WithMaxFiles overrides the resolver's max-files concatenation cap.
// A non-positive value disables the cap entirely (NOT recommended for
// production — large directories can pin large amounts of memory).
func WithMaxFiles(n int) StaticDirOption {
	return func(r *StaticDirResolver) {
		r.maxFiles = n
	}
}

// NewStaticDirResolver returns a StaticDirResolver configured with
// the supplied options. Defaults: maxFiles = DefaultStaticDirMaxFiles.
func NewStaticDirResolver(opts ...StaticDirOption) agentcontext.Resolver {
	r := &StaticDirResolver{maxFiles: DefaultStaticDirMaxFiles}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve implements agentcontext.Resolver.
func (r *StaticDirResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindStaticDir {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindStaticDir)
	}

	srcPath := spec.Source.StaticDir.Path
	if strings.TrimSpace(srcPath) == "" {
		return agentcontext.SlotResult{}, fmt.Errorf("static_dir: empty path")
	}
	dir, err := resolveFilePath(srcPath, env.Workdir)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("static_dir: resolve path: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("static_dir: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return agentcontext.SlotResult{}, fmt.Errorf("static_dir: %s is not a directory", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("static_dir: read %s: %w", dir, err)
	}

	glob := strings.TrimSpace(spec.Source.StaticDir.Glob)
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if glob != "" {
			matched, mErr := filepath.Match(glob, name)
			if mErr != nil {
				return agentcontext.SlotResult{}, fmt.Errorf("static_dir: bad glob %q: %w", glob, mErr)
			}
			if !matched {
				continue
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)

	truncated := false
	if r.maxFiles > 0 && len(names) > r.maxFiles {
		names = names[:r.maxFiles]
		truncated = true
	}

	var parts []string
	for _, n := range names {
		if err := ctx.Err(); err != nil {
			return agentcontext.SlotResult{}, err
		}
		full := filepath.Join(dir, n)
		b, rErr := os.ReadFile(full) //nolint:gosec // operator-supplied dir; SlotSpec.Validate guards ..
		if rErr != nil {
			return agentcontext.SlotResult{}, fmt.Errorf("static_dir: read %s: %w", full, rErr)
		}
		parts = append(parts, string(b))
	}
	content := strings.Join(parts, "\n\n")

	extra := map[string]string{}
	if len(names) > 0 {
		extra["files"] = strings.Join(names, ",")
	}
	extra["match_count"] = fmt.Sprintf("%d", len(names))

	return agentcontext.SlotResult{
		Content:   content,
		Truncated: truncated,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindStaticDir,
			Source:      dir,
			Bytes:       len(content),
			ContentHash: hashContent(content),
			FetchedAt:   nowUTC(),
			Extra:       extra,
		},
	}, nil
}
