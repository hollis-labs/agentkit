package agentlaunch

import "os"

// NativeFileKind tags a NativeFile with the planting convention the
// provider bootdir planter should apply to it. The kind is what lets
// the planter resolve a provider-native path WITHOUT the caller
// hand-coding ".claude/skills/..." per provider.
type NativeFileKind string

const (
	// NativeFileSkill is a provider-native "skill" document. The planter
	// resolves NativeFile.ID to the provider's skill convention:
	//
	//   - claude   → .claude/skills/<ID>.md
	//   - opencode → .opencode/skills/<ID>.md
	//   - codex / others → skills/<ID>.md (no native skill dir; planted
	//     under a neutral skills/ directory for inspection parity)
	//
	// NativeFile.RelPath is ignored for this kind.
	NativeFileSkill NativeFileKind = "skill"

	// NativeFileRaw is a caller-placed file written verbatim at
	// NativeFile.RelPath (bootdir-relative). Use this for AGENTS.md
	// overrides, extra context docs, or any user-supplied file whose
	// path the caller already knows. NativeFile.ID is ignored.
	NativeFileRaw NativeFileKind = "raw"
)

// Valid reports whether the receiver is one of the declared
// NativeFileKind constants. The zero value is not valid.
func (k NativeFileKind) Valid() bool {
	switch k {
	case NativeFileSkill, NativeFileRaw:
		return true
	default:
		return false
	}
}

// NativeFile is a first-class caller-supplied file the provider bootdir
// planter writes into the planted bootdir alongside the provider's own
// BootDirSpec output. It exists so consumers (Tether, Torque, Nanite)
// can plant native skills, context docs, and user files through the
// shared launch API instead of registering app-specific BootDirHooks.
//
// Native files are planted AFTER the provider's BootDirSpec files and
// BEFORE InjectionSpec.BootDirOverlay — see the providerplant package
// docs for the full overwrite ordering.
type NativeFile struct {
	// Kind selects the path-resolution convention. Required.
	Kind NativeFileKind `yaml:"kind" json:"kind"`

	// ID identifies a NativeFileSkill entry; it derives the planted
	// filename. Required for NativeFileSkill, ignored for NativeFileRaw.
	// Must be a single safe path segment ([A-Za-z0-9._-], no separators,
	// not "." or "..").
	ID string `yaml:"id,omitempty" json:"id,omitempty"`

	// RelPath is the bootdir-relative target for a NativeFileRaw entry.
	// Required for NativeFileRaw, ignored for NativeFileSkill. Subject to
	// the same path-safety rules as InjectionSpec.BootDirOverlay keys.
	RelPath string `yaml:"rel_path,omitempty" json:"rel_path,omitempty"`

	// Content is the file body written verbatim. May be empty.
	Content string `yaml:"content,omitempty" json:"content,omitempty"`

	// Mode is the file mode for the planted file. Zero falls back to
	// 0o644.
	Mode os.FileMode `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// Validate runs field-shape correctness checks on a single NativeFile.
// Returns one of the package-level sentinel errors on first failure.
func (f NativeFile) Validate() error {
	if !f.Kind.Valid() {
		return ErrUnknownNativeFileKind
	}
	switch f.Kind {
	case NativeFileSkill:
		if f.ID == "" {
			return ErrNativeFileMissingID
		}
		if !safePathSegment(f.ID) {
			return ErrNativeFileUnsafeID
		}
	case NativeFileRaw:
		if f.RelPath == "" {
			return ErrNativeFileMissingRelPath
		}
		if err := ValidateBootDirRelPath(f.RelPath); err != nil {
			return err
		}
	}
	return nil
}

// safePathSegment reports whether s is usable as a single filename
// segment: non-empty, not a "." / ".." traversal token, and composed
// only of [A-Za-z0-9._-].
func safePathSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
