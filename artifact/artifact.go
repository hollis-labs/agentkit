package artifact

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

var (
	ErrInvalidEntryKind   = errors.New("artifact: invalid entry kind")
	ErrMissingPath        = errors.New("artifact: missing relative path")
	ErrUnsafePath         = errors.New("artifact: unsafe relative path")
	ErrMissingContent     = errors.New("artifact: file requires bytes or immutable content reference")
	ErrDirectoryHasBytes  = errors.New("artifact: directory cannot carry file bytes or content reference")
	ErrMissingSourceKind  = errors.New("artifact: missing source kind")
	ErrInvalidSourceKind  = errors.New("artifact: invalid source kind")
	ErrMissingSourceRoot  = errors.New("artifact: filesystem source requires root")
	ErrMissingGeneratedID = errors.New("artifact: generated source requires id")
	ErrMissingContentRef  = errors.New("artifact: immutable source requires content reference")
	ErrDuplicatePath      = errors.New("artifact: duplicate relative path")
	ErrPrefixCollision    = errors.New("artifact: file/directory prefix collision")
	ErrLimitExceeded      = errors.New("artifact: source limit exceeded")
	ErrSymlinkRejected    = errors.New("artifact: source symlink rejected")
	ErrSymlinkDangling    = errors.New("artifact: source symlink is dangling")
	ErrSymlinkOutOfRoot   = errors.New("artifact: source symlink target is outside allowed roots")
	ErrSymlinkDirectory   = errors.New("artifact: source symlink target is a directory")
	ErrImmutableChanged   = errors.New("artifact: immutable content digest mismatch")
	ErrImmutableMissing   = errors.New("artifact: immutable content missing")
)

// EntryKind identifies the filesystem object represented by an entry.
type EntryKind string

const (
	EntryFile      EntryKind = "file"
	EntryDirectory EntryKind = "directory"
)

func (k EntryKind) Valid() bool {
	switch k {
	case EntryFile, EntryDirectory:
		return true
	default:
		return false
	}
}

// SourceKind identifies the adapter family that produced an artifact tree.
type SourceKind string

const (
	SourceFilesystemTree  SourceKind = "filesystem_tree"
	SourceGeneratedTree   SourceKind = "generated_tree"
	SourceImmutableTree   SourceKind = "immutable_tree"
	SourceImmutableObject SourceKind = "immutable_object"
)

func (k SourceKind) Valid() bool {
	switch k {
	case SourceFilesystemTree, SourceGeneratedTree, SourceImmutableTree, SourceImmutableObject:
		return true
	default:
		return false
	}
}

// Digest names a content digest without constraining callers to sha256 only.
type Digest struct {
	Algorithm string `yaml:"algorithm" json:"algorithm"`
	Hex       string `yaml:"hex" json:"hex"`
}

// Provenance records where an artifact came from. Revision is a commit, blob
// generation, database revision or other source-version token supplied by the
// caller.
type Provenance struct {
	Source     string `yaml:"source,omitempty" json:"source,omitempty"`
	SourcePath string `yaml:"source_path,omitempty" json:"source_path,omitempty"`
	Revision   string `yaml:"revision,omitempty" json:"revision,omitempty"`
	Dirty      bool   `yaml:"dirty,omitempty" json:"dirty,omitempty"`
	Note       string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Ownership identifies the stable managed entry/group that later reconcile and
// refresh operations use. Generation is caller-defined and increases when a
// managed group is rewritten.
type Ownership struct {
	EntryID    string `yaml:"entry_id,omitempty" json:"entry_id,omitempty"`
	GroupID    string `yaml:"group_id,omitempty" json:"group_id,omitempty"`
	Generation string `yaml:"generation,omitempty" json:"generation,omitempty"`
}

// ImmutableRef points at content that must be resolved and verified before a
// pure provider projection or materialization plan can be considered frozen.
type ImmutableRef struct {
	Store     string `yaml:"store" json:"store"`
	Key       string `yaml:"key" json:"key"`
	Digest    Digest `yaml:"digest" json:"digest"`
	SizeBytes int64  `yaml:"size_bytes,omitempty" json:"size_bytes,omitempty"`
}

// Entry is one normalized destination-relative file or directory.
type Entry struct {
	Path       string        `yaml:"path" json:"path"`
	Kind       EntryKind     `yaml:"kind" json:"kind"`
	Mode       fs.FileMode   `yaml:"mode,omitempty" json:"mode,omitempty"`
	Bytes      []byte        `yaml:"bytes,omitempty" json:"bytes,omitempty"`
	ContentRef *ImmutableRef `yaml:"content_ref,omitempty" json:"content_ref,omitempty"`
	Ownership  Ownership     `yaml:"ownership,omitempty" json:"ownership,omitempty"`
	Provenance Provenance    `yaml:"provenance,omitempty" json:"provenance,omitempty"`
	Digest     Digest        `yaml:"digest,omitempty" json:"digest,omitempty"`
}

func (e Entry) Validate() error {
	if !e.Kind.Valid() {
		return ErrInvalidEntryKind
	}
	if err := ValidateRelPath(e.Path); err != nil {
		return err
	}
	switch e.Kind {
	case EntryFile:
		if e.Bytes == nil && e.ContentRef == nil {
			return ErrMissingContent
		}
	case EntryDirectory:
		if e.Bytes != nil || e.ContentRef != nil {
			return ErrDirectoryHasBytes
		}
	}
	return nil
}

// CloneEntries returns a deep copy of entries.
func CloneEntries(in []Entry) []Entry {
	return cloneEntries(in)
}

// Tree is a deterministic set of entries. Implementations must sort entries
// before producing a plan; M01 keeps the value contract independent of a writer.
type Tree struct {
	Entries    []Entry    `yaml:"entries" json:"entries"`
	Provenance Provenance `yaml:"provenance,omitempty" json:"provenance,omitempty"`
}

func (t Tree) Validate() error {
	for i := range t.Entries {
		if err := t.Entries[i].Validate(); err != nil {
			return fmt.Errorf("artifact: entry %d: %w", i, err)
		}
	}
	return nil
}

// SourceLimits bounds adapters that expand external trees.
type SourceLimits struct {
	MaxEntries int   `yaml:"max_entries,omitempty" json:"max_entries,omitempty"`
	MaxBytes   int64 `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`
	MaxDepth   int   `yaml:"max_depth,omitempty" json:"max_depth,omitempty"`
}

// SymlinkPolicy names how filesystem adapters treat source symlinks.
type SymlinkPolicy string

const (
	SymlinkReject       SymlinkPolicy = "reject"
	SymlinkImportByVal  SymlinkPolicy = "import_by_value"
	SymlinkPreserveLink SymlinkPolicy = "preserve_link"
)

// FilesystemSource describes a bounded filesystem tree import. AllowedRoots
// are source-read bounds, not child-process access grants.
type FilesystemSource struct {
	Root         string        `yaml:"root" json:"root"`
	AllowedRoots []string      `yaml:"allowed_roots,omitempty" json:"allowed_roots,omitempty"`
	Symlinks     SymlinkPolicy `yaml:"symlinks,omitempty" json:"symlinks,omitempty"`
}

// GeneratedSource describes a caller-generated tree.
type GeneratedSource struct {
	ID      string  `yaml:"id" json:"id"`
	Entries []Entry `yaml:"entries,omitempty" json:"entries,omitempty"`
}

// ImmutableSource describes a content-addressed tree or object.
type ImmutableSource struct {
	Ref ImmutableRef `yaml:"ref" json:"ref"`
}

// Source is the narrow union accepted by artifact source resolvers.
type Source struct {
	Kind       SourceKind        `yaml:"kind" json:"kind"`
	Filesystem *FilesystemSource `yaml:"filesystem,omitempty" json:"filesystem,omitempty"`
	Generated  *GeneratedSource  `yaml:"generated,omitempty" json:"generated,omitempty"`
	Immutable  *ImmutableSource  `yaml:"immutable,omitempty" json:"immutable,omitempty"`
}

func (s Source) Validate() error {
	if s.Kind == "" {
		return ErrMissingSourceKind
	}
	if !s.Kind.Valid() {
		return ErrInvalidSourceKind
	}
	switch s.Kind {
	case SourceFilesystemTree:
		if s.Filesystem == nil || strings.TrimSpace(s.Filesystem.Root) == "" {
			return ErrMissingSourceRoot
		}
	case SourceGeneratedTree:
		if s.Generated == nil || strings.TrimSpace(s.Generated.ID) == "" {
			return ErrMissingGeneratedID
		}
	case SourceImmutableTree, SourceImmutableObject:
		if s.Immutable == nil || s.Immutable.Ref.Store == "" || s.Immutable.Ref.Key == "" {
			return ErrMissingContentRef
		}
	}
	return nil
}

// SourceRequest asks an adapter to resolve a source into destination-relative
// entries rooted under DestinationPrefix.
type SourceRequest struct {
	Source            Source       `yaml:"source" json:"source"`
	DestinationPrefix string       `yaml:"destination_prefix,omitempty" json:"destination_prefix,omitempty"`
	Limits            SourceLimits `yaml:"limits,omitempty" json:"limits,omitempty"`
	OwnershipGroup    string       `yaml:"ownership_group,omitempty" json:"ownership_group,omitempty"`
}

func (r SourceRequest) Validate() error {
	if err := r.Source.Validate(); err != nil {
		return err
	}
	if r.DestinationPrefix != "" {
		return ValidateRelPath(r.DestinationPrefix)
	}
	return nil
}

// Resolver freezes a source into an artifact tree. Implementations may read
// source content, but they do not grant those source paths to a spawned agent.
type Resolver interface {
	ResolveArtifacts(ctx context.Context, req SourceRequest) (Tree, error)
}

// Normalize validates, sorts and collision-checks entries.
func Normalize(entries []Entry) ([]Entry, error) {
	out := cloneEntries(entries)
	for i := range out {
		if err := out[i].Validate(); err != nil {
			return nil, fmt.Errorf("artifact: entry %d: %w", i, err)
		}
	}
	sortEntries(out)
	seen := make(map[string]Entry, len(out))
	for _, entry := range out {
		if prior, ok := seen[entry.Path]; ok {
			return nil, fmt.Errorf("%w: %s conflicts %s", ErrDuplicatePath, prior.Path, entry.Path)
		}
		seen[entry.Path] = entry
	}
	for _, parent := range out {
		if parent.Kind == EntryDirectory {
			continue
		}
		prefix := parent.Path + "/"
		for _, child := range out {
			if strings.HasPrefix(child.Path, prefix) {
				return nil, fmt.Errorf("%w: file %s contains %s", ErrPrefixCollision, parent.Path, child.Path)
			}
		}
	}
	return out, nil
}

// ValidateRelPath checks the cross-package relative-path contract. Paths use
// slash separators, must be relative, and cannot contain traversal segments.
func ValidateRelPath(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return ErrMissingPath
	}
	if path.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}
	if strings.HasPrefix(rel, "./") || strings.Contains(rel, "//") || clean != rel {
		return fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}
	return nil
}
