package agentlaunch

import (
	"path/filepath"
	"strings"
)

// InjectionSpec carries caller-supplied overrides spliced into the
// launch at prepare time: extra environment variables, extra argv
// segments, and a map of additional files to plant inside the bootdir.
// Used by tests and fixtures; production launches typically leave it
// zero. The overlay map is path-safety-validated by LaunchPlan.Validate
// so a hostile or buggy caller cannot escape the bootdir.
type InjectionSpec struct {
	// Env is merged into the spawned process's environment after
	// ProviderSpec.Env and the runtime base env; InjectionSpec.Env
	// wins on conflict. Empty leaves the merged env untouched.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Args is appended to the spawned process's argv after the
	// adapter's BuildArgs output and ProviderSpec.Flags. The preparer
	// does NOT template-substitute these; pre-resolve any tokens before
	// passing them in.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

	// BootDirOverlay maps bootdir-relative file paths to file contents
	// the preparer writes into the planted bootdir before spawning.
	// Path-safety is enforced at LaunchPlan.Validate time via
	// ErrUnsafeInjectionTarget: keys must be relative, contain no ".."
	// segments, and must not target reserved names (".git/", ".ssh/",
	// etc.). Useful for test fixtures that need to inject CLAUDE.md
	// stubs, MCP descriptors, or scratch files without going through
	// the catalog.
	BootDirOverlay map[string]string `yaml:"boot_dir_overlay,omitempty" json:"boot_dir_overlay,omitempty"`

	// NativeFiles lists provider-native extra files (skills, context
	// docs, user-supplied files) the provider bootdir planter writes
	// into the planted bootdir. Unlike BootDirOverlay — a flat
	// path→content map — each NativeFile carries a Kind so the planter
	// can resolve provider-native paths (.claude/skills/<id>.md etc.)
	// without an app-specific callback. Validated per-entry by
	// LaunchPlan.Validate. See the NativeFile type and the providerplant
	// package for planting order and path conventions.
	NativeFiles []NativeFile `yaml:"native_files,omitempty" json:"native_files,omitempty"`
}

// ValidateBootDirRelPath reports an error when rel is unsafe to use as a
// bootdir-relative target. It is the exported form of the path-safety
// rule LaunchPlan.Validate applies to BootDirOverlay keys: rel must be
// non-empty, relative, free of ".." segments, and must not target a
// reserved name (".git/", ".ssh/", …). Returns ErrUnsafeInjectionTarget
// on any violation.
//
// The provider bootdir planter (providerplant package) calls this a
// second time at planting phase so a PreparedLaunch assembled outside
// the Compile/Validate path still cannot escape the bootdir.
func ValidateBootDirRelPath(rel string) error {
	return validateOverlayKey(rel)
}

// reservedOverlayPrefixes lists path prefixes that the overlay map is
// not permitted to target. The list is deliberately conservative; we
// can grow it as the bootdir layout solidifies under CW-0005. Each
// entry is a slash-separated prefix (matched case-sensitively) tested
// after path normalization.
var reservedOverlayPrefixes = []string{
	".git/",
	".ssh/",
	".gnupg/",
	".aws/",
}

// reservedOverlayExact lists exact path values the overlay map is not
// permitted to target. Used for top-level config files that would
// otherwise be writable via the prefix rules above.
var reservedOverlayExact = []string{
	".git",
	".ssh",
	".gnupg",
	".aws",
}

// validateOverlayKey reports an error when key is unsafe to use as a
// bootdir-relative target. Safe keys are:
//
//   - non-empty
//   - relative (no leading "/" on Unix; no drive letter / leading "\"
//     on Windows — filepath.IsAbs covers both)
//   - free of ".." segments
//   - not targeting one of the reserved-prefix names
//
// Forward slashes and backslashes are both treated as separators so a
// caller-supplied key like ".git\config" on Windows is still rejected.
// The check normalizes to forward slashes before prefix matching.
func validateOverlayKey(key string) error {
	if key == "" {
		return ErrUnsafeInjectionTarget
	}
	if filepath.IsAbs(key) {
		return ErrUnsafeInjectionTarget
	}
	// Normalize separators to "/" so the same rule covers both POSIX
	// callers and Windows callers feeding "\" paths.
	normalized := strings.ReplaceAll(key, "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		// Defensive: filepath.IsAbs on Unix-only test hosts wouldn't catch
		// a Windows-style leading slash, so we reject explicitly.
		return ErrUnsafeInjectionTarget
	}
	// Walk the segments and reject any ".." or empty segment (which
	// represents a "//" duplicate-slash that could foul cleaning logic).
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return ErrUnsafeInjectionTarget
		}
	}
	// Reject reserved exact matches and reserved prefixes.
	for _, exact := range reservedOverlayExact {
		if normalized == exact {
			return ErrUnsafeInjectionTarget
		}
	}
	for _, prefix := range reservedOverlayPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return ErrUnsafeInjectionTarget
		}
	}
	return nil
}
