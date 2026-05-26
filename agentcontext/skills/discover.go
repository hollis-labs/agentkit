package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// Layer is one tier of skill discovery. Layers are processed in the
// order supplied on DiscoveryConfig.Layers; later layers override
// earlier ones by Skill.Name. The Name field is purely operator-
// facing (e.g. "tether-catalog", "nanite-user", "project-local") and
// surfaces in provenance and the override chain.
type Layer struct {
	// Name is the human-readable label for this layer. Used in
	// Index.Layers and propagated into skill_index resolver
	// provenance under Extra["layers"]. Empty strings are tolerated
	// but discouraged.
	Name string

	// Root is the directory to walk. A leading "~" is expanded to the
	// current user's HOME by Discover. Empty roots are silently
	// skipped — DiscoveryConfig.StrictMissingRoot controls whether a
	// root that is configured but missing on disk is an error or a
	// no-op.
	Root string
}

// DiscoveryConfig is the input shape for Discover. Zero values are
// intentionally usable: a freshly-constructed DiscoveryConfig with
// no Layers returns an empty Index with no error. Missing roots are
// silently skipped by default; flip StrictMissingRoot for fail-loud
// behaviour.
type DiscoveryConfig struct {
	// Layers is the ordered list of discovery layers, lowest-priority
	// first. Skills with the same Name in a later layer override the
	// earlier definition; the earlier source path is recorded in
	// Index.Overridden[name].
	Layers []Layer

	// FilePattern is the filename glob to match (passed to
	// filepath.Match). Empty defaults to "*.md".
	FilePattern string

	// Recursive controls whether subdirectories are walked. Default
	// (zero-value false) means non-recursive — only files directly
	// under Layer.Root participate. Set to true to walk the whole
	// tree.
	Recursive bool

	// StrictMissingRoot, when true, makes Discover return
	// ErrSkillRootMissing if any configured Layer.Root does not exist
	// on disk. Default false: missing roots are silently skipped, so a
	// portable boot profile that references both ~/.tether/skills and
	// ~/.nanite/skills works on a host where only one is present.
	StrictMissingRoot bool
}

// defaultFilePattern is the file glob used when DiscoveryConfig
// leaves FilePattern empty.
const defaultFilePattern = "*.md"

// ParseError captures a per-file parse failure during Discover. The
// failure does NOT abort discovery — the rest of the layer is
// processed and the error is recorded here for operator visibility.
type ParseError struct {
	// Layer is the operator-facing label of the layer the failed
	// file belongs to (matches Layer.Name).
	Layer string

	// Path is the absolute filesystem path of the failed file.
	Path string

	// Err is the underlying parse failure. Wraps one of the skills
	// package's sentinels (ErrSkillNoFrontmatter,
	// ErrSkillInvalidFrontmatter, ErrSkillMissingName,
	// ErrSkillMissingDescription) or an OS read error.
	Err error
}

// Error implements the error interface — handy for callers that
// want to surface a ParseError directly.
func (p ParseError) Error() string {
	return fmt.Sprintf("skill parse: layer=%q path=%s: %v", p.Layer, p.Path, p.Err)
}

// Unwrap exposes the wrapped error for errors.Is / errors.As.
func (p ParseError) Unwrap() error { return p.Err }

// Discover walks each configured layer in order, parses every file
// that matches FilePattern, and returns an Index that respects the
// layered override rule (later layers win).
//
// Cancellation: Discover honors ctx.Done. A cancelled context
// surfaces ctx.Err() immediately; partial results are NOT returned.
//
// Errors:
//
//   - ErrSkillRootMissing — at least one Layer.Root does not exist
//     AND DiscoveryConfig.StrictMissingRoot is true.
//   - ctx.Err() — context was cancelled mid-walk.
//
// Per-file parse failures are NOT promoted to top-level errors;
// they are collected onto Index.ParseErrors and discovery continues.
func Discover(ctx context.Context, cfg DiscoveryConfig) (*Index, error) {
	pattern := cfg.FilePattern
	if pattern == "" {
		pattern = defaultFilePattern
	}

	idx := &Index{
		ByName:     map[string]Skill{},
		Overridden: map[string][]string{},
		Layers:     append([]Layer(nil), cfg.Layers...),
	}

	for _, layer := range cfg.Layers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		root, err := expandRoot(layer.Root)
		if err != nil {
			return nil, fmt.Errorf("skills: expand layer root %q: %w", layer.Root, err)
		}
		if root == "" {
			continue
		}

		info, statErr := os.Stat(root)
		switch {
		case statErr != nil && os.IsNotExist(statErr):
			if cfg.StrictMissingRoot {
				return nil, fmt.Errorf("%w: %s", agentcontext.ErrSkillRootMissing, root)
			}
			continue
		case statErr != nil:
			return nil, fmt.Errorf("skills: stat layer root %s: %w", root, statErr)
		case !info.IsDir():
			return nil, fmt.Errorf("skills: layer root %s is not a directory", root)
		}

		files, walkErr := collectFiles(root, pattern, cfg.Recursive)
		if walkErr != nil {
			return nil, fmt.Errorf("skills: walk %s: %w", root, walkErr)
		}

		for _, path := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			data, readErr := os.ReadFile(path) //nolint:gosec // operator-supplied discovery root
			if readErr != nil {
				idx.ParseErrors = append(idx.ParseErrors, ParseError{
					Layer: layer.Name,
					Path:  path,
					Err:   readErr,
				})
				continue
			}
			skill, parseErr := Parse(data, path)
			if parseErr != nil {
				idx.ParseErrors = append(idx.ParseErrors, ParseError{
					Layer: layer.Name,
					Path:  path,
					Err:   parseErr,
				})
				continue
			}
			if prev, ok := idx.ByName[skill.Name]; ok {
				idx.Overridden[skill.Name] = append(idx.Overridden[skill.Name], prev.Source)
			}
			idx.ByName[skill.Name] = skill
		}
	}

	// Materialise the sorted Skills slice from ByName for
	// deterministic iteration.
	idx.Skills = make([]Skill, 0, len(idx.ByName))
	for _, s := range idx.ByName {
		idx.Skills = append(idx.Skills, s)
	}
	sort.Slice(idx.Skills, func(i, j int) bool {
		return idx.Skills[i].Name < idx.Skills[j].Name
	})

	// Sort each Overridden entry for determinism.
	for k, v := range idx.Overridden {
		sorted := append([]string(nil), v...)
		sort.Strings(sorted)
		idx.Overridden[k] = sorted
	}

	// Sort ParseErrors by (Layer, Path) for determinism.
	sort.Slice(idx.ParseErrors, func(i, j int) bool {
		a, b := idx.ParseErrors[i], idx.ParseErrors[j]
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		return a.Path < b.Path
	})

	return idx, nil
}

// collectFiles returns the sorted list of files matching pattern
// directly under root (or recursively, if recursive is true). Dot
// files are skipped. Directories are not included in the result.
func collectFiles(root, pattern string, recursive bool) ([]string, error) {
	var out []string
	if recursive {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			matched, mErr := filepath.Match(pattern, d.Name())
			if mErr != nil {
				return mErr
			}
			if !matched {
				return nil
			}
			abs, absErr := filepath.Abs(path)
			if absErr != nil {
				return absErr
			}
			out = append(out, abs)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			matched, mErr := filepath.Match(pattern, name)
			if mErr != nil {
				return nil, mErr
			}
			if !matched {
				continue
			}
			abs, absErr := filepath.Abs(filepath.Join(root, name))
			if absErr != nil {
				return nil, absErr
			}
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	return out, nil
}

// expandRoot expands a leading "~" to HOME and returns the cleaned
// absolute path. Empty input passes through unchanged so callers can
// pre-filter no-op layers.
func expandRoot(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if p[0] == '~' {
		// Support "~" and "~/...".
		if len(p) == 1 {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			return home, nil
		}
		if p[1] == '/' || p[1] == filepath.Separator {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(home, p[2:]), nil
		}
		// "~user/..." — not supported. Pass through unchanged.
	}
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	return filepath.Clean(p), nil
}
