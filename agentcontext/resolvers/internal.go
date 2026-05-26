package resolvers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// expandPath returns p with a leading ~ replaced by the current user's
// home directory. Returns p unchanged if it does not start with ~ or
// if HOME cannot be resolved (preserve original behaviour so a missing
// HOME does not silently rewrite a path).
//
// Tilde forms recognised: "~" alone, "~/...". The "~user/..." form is
// not supported; if it appears, the path is returned unchanged.
func expandPath(p string) string {
	if p == "" {
		return p
	}
	if p[0] != '~' {
		return p
	}
	// "~user/..." — not supported. Pass through unchanged.
	if len(p) > 1 && p[1] != '/' && p[1] != filepath.Separator {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// resolveFilePath returns the absolute filesystem path for a
// SlotSource path-like field. The rules:
//
//  1. Leading ~ is expanded via expandPath.
//  2. Absolute paths are returned as-is.
//  3. Relative paths are joined against the supplied workdir; if
//     workdir is empty, they are joined against the process working
//     directory (filepath.Abs's default).
//
// The function does NOT validate path safety; SlotSpec.Validate
// already rejects ".." segments and we trust the contract layer for
// that defense.
func resolveFilePath(raw, workdir string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty path")
	}
	p := expandPath(raw)
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	if workdir != "" {
		return filepath.Clean(filepath.Join(workdir, p)), nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// hashContent returns the lowercase-hex SHA-256 digest of the supplied
// bytes. Used by every resolver to populate SlotProvenance.ContentHash
// for deterministic provenance.
func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// nowUTC returns time.Now in UTC. Wrapped behind a package-level
// variable so individual resolvers can substitute a deterministic
// clock in tests without exposing a public test seam.
var nowUTC = func() time.Time { return time.Now().UTC() }

// minDuration returns the smaller of two durations. Zero values are
// treated as "no constraint" — that is, minDuration(0, x) returns x
// and minDuration(x, 0) returns x. minDuration(0, 0) returns 0.
func minDuration(a, b time.Duration) time.Duration {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// firstWord returns the first whitespace-separated token of s, or s
// itself if it contains no whitespace. Used to compose a compact
// Provenance.Source label for the cmd resolver
// ("cmd:<first-word-of-run>").
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		return s[:i]
	}
	return s
}
