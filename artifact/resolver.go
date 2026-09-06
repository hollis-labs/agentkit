package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ImmutableStore interface {
	ResolveTree(ctx context.Context, ref ImmutableRef) (Tree, error)
	ResolveObject(ctx context.Context, ref ImmutableRef) ([]byte, error)
}

type ResolverOptions struct {
	ImmutableStore ImmutableStore
}

type DefaultResolver struct {
	opts ResolverOptions
}

func NewResolver(opts ResolverOptions) *DefaultResolver {
	return &DefaultResolver{opts: opts}
}

var _ Resolver = (*DefaultResolver)(nil)

func (r *DefaultResolver) ResolveArtifacts(ctx context.Context, req SourceRequest) (Tree, error) {
	if err := req.Validate(); err != nil {
		return Tree{}, err
	}
	if err := ctx.Err(); err != nil {
		return Tree{}, err
	}
	var tree Tree
	var err error
	switch req.Source.Kind {
	case SourceFilesystemTree:
		tree, err = readFilesystemTree(ctx, req.Source.Filesystem, req.Limits)
	case SourceGeneratedTree:
		tree = Tree{Entries: cloneEntries(req.Source.Generated.Entries)}
	case SourceImmutableTree:
		if r.opts.ImmutableStore == nil {
			return Tree{}, ErrImmutableMissing
		}
		tree, err = r.opts.ImmutableStore.ResolveTree(ctx, req.Source.Immutable.Ref)
	case SourceImmutableObject:
		if r.opts.ImmutableStore == nil {
			return Tree{}, ErrImmutableMissing
		}
		var data []byte
		data, err = r.opts.ImmutableStore.ResolveObject(ctx, req.Source.Immutable.Ref)
		if err == nil {
			tree = Tree{Entries: []Entry{{
				Path:       filepath.ToSlash(req.Source.Immutable.Ref.Key),
				Kind:       EntryFile,
				Bytes:      data,
				ContentRef: &req.Source.Immutable.Ref,
				Digest:     digestBytes(data),
			}}}
		}
	}
	if err != nil {
		return Tree{}, err
	}
	entries, err := applyRequestMetadata(tree.Entries, req)
	if err != nil {
		return Tree{}, err
	}
	if err := enforceLimits(entries, req.Limits); err != nil {
		return Tree{}, err
	}
	out := Tree{Entries: entries, Provenance: tree.Provenance}
	return out, nil
}

func readFilesystemTree(ctx context.Context, src *FilesystemSource, limits SourceLimits) (Tree, error) {
	root, err := filepath.Abs(src.Root)
	if err != nil {
		return Tree{}, err
	}
	allowed := append([]string{root}, src.AllowedRoots...)
	for i := range allowed {
		allowed[i], err = canonicalPath(allowed[i])
		if err != nil {
			return Tree{}, err
		}
	}
	policy := src.Symlinks
	if policy == "" {
		policy = SymlinkReject
	}
	var entries []Entry
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := ValidateRelPath(rel); err != nil {
			return err
		}
		depth := strings.Count(rel, "/") + 1
		if limits.MaxDepth > 0 && depth > limits.MaxDepth {
			return fmt.Errorf("%w: depth %d exceeds %d", ErrLimitExceeded, depth, limits.MaxDepth)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			entry, err := resolveSymlink(p, rel, policy, allowed)
			if err != nil {
				return err
			}
			entries = append(entries, entry)
			return nil
		}
		if d.IsDir() {
			entries = append(entries, Entry{Path: rel, Kind: EntryDirectory, Mode: info.Mode().Perm()})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact: unsupported source file mode %s: %s", info.Mode(), rel)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, Entry{
			Path:   rel,
			Kind:   EntryFile,
			Mode:   info.Mode().Perm(),
			Bytes:  data,
			Digest: digestBytes(data),
		})
		return nil
	})
	if err != nil {
		return Tree{}, err
	}
	normalized, err := Normalize(entries)
	if err != nil {
		return Tree{}, err
	}
	if err := enforceLimits(normalized, limits); err != nil {
		return Tree{}, err
	}
	return Tree{Entries: normalized}, nil
}

func resolveSymlink(p, rel string, policy SymlinkPolicy, allowed []string) (Entry, error) {
	if policy == SymlinkReject {
		return Entry{}, fmt.Errorf("%w: %s", ErrSymlinkRejected, rel)
	}
	if policy != SymlinkImportByVal {
		return Entry{}, fmt.Errorf("%w: %s", ErrSymlinkRejected, rel)
	}
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrSymlinkDangling, rel)
	}
	if !withinAnyRoot(target, allowed) {
		return Entry{}, fmt.Errorf("%w: %s", ErrSymlinkOutOfRoot, rel)
	}
	info, err := os.Stat(target)
	if err != nil {
		return Entry{}, err
	}
	if info.IsDir() {
		return Entry{}, fmt.Errorf("%w: %s", ErrSymlinkDirectory, rel)
	}
	if !info.Mode().IsRegular() {
		return Entry{}, fmt.Errorf("artifact: unsupported symlink target mode %s: %s", info.Mode(), rel)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Path: rel, Kind: EntryFile, Mode: info.Mode().Perm(), Bytes: data, Digest: digestBytes(data)}, nil
}

func withinAnyRoot(p string, roots []string) bool {
	p, err := canonicalPath(p)
	if err != nil {
		return false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func canonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	return abs, nil
}

func applyRequestMetadata(entries []Entry, req SourceRequest) ([]Entry, error) {
	out := cloneEntries(entries)
	for i := range out {
		if req.DestinationPrefix != "" {
			out[i].Path = req.DestinationPrefix + "/" + out[i].Path
		}
		if req.OwnershipGroup != "" && out[i].Ownership.GroupID == "" {
			out[i].Ownership.GroupID = req.OwnershipGroup
		}
	}
	return Normalize(out)
}

func enforceLimits(entries []Entry, limits SourceLimits) error {
	if limits.MaxEntries > 0 && len(entries) > limits.MaxEntries {
		return fmt.Errorf("%w: entries %d exceeds %d", ErrLimitExceeded, len(entries), limits.MaxEntries)
	}
	var total int64
	for _, entry := range entries {
		if entry.Kind == EntryFile {
			if entry.Bytes != nil {
				total += int64(len(entry.Bytes))
			} else if entry.ContentRef != nil {
				total += entry.ContentRef.SizeBytes
			}
			if limits.MaxBytes > 0 && total > limits.MaxBytes {
				return fmt.Errorf("%w: bytes %d exceeds %d", ErrLimitExceeded, total, limits.MaxBytes)
			}
		}
	}
	return nil
}

func cloneEntries(in []Entry) []Entry {
	out := make([]Entry, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Bytes != nil {
			out[i].Bytes = append([]byte{}, in[i].Bytes...)
		}
		if in[i].ContentRef != nil {
			ref := *in[i].ContentRef
			out[i].ContentRef = &ref
		}
	}
	return out
}

func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Path == entries[j].Path {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Path < entries[j].Path
	})
}

func digestBytes(data []byte) Digest {
	return DigestBytes(data)
}

// DigestBytes returns a sha256 digest for data.
func DigestBytes(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])}
}

type MemoryStore struct {
	Trees   map[string]Tree
	Objects map[string][]byte
}

func (s MemoryStore) ResolveTree(ctx context.Context, ref ImmutableRef) (Tree, error) {
	if err := ctx.Err(); err != nil {
		return Tree{}, err
	}
	tree, ok := s.Trees[ref.Key]
	if !ok {
		return Tree{}, fmt.Errorf("%w: %s", ErrImmutableMissing, ref.Key)
	}
	normalized, err := Normalize(tree.Entries)
	if err != nil {
		return Tree{}, err
	}
	if ref.Digest.Hex != "" && !digestTree(normalized).Equal(ref.Digest) {
		return Tree{}, fmt.Errorf("%w: %s", ErrImmutableChanged, ref.Key)
	}
	return Tree{Entries: normalized, Provenance: tree.Provenance}, nil
}

func (s MemoryStore) ResolveObject(ctx context.Context, ref ImmutableRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := s.Objects[ref.Key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrImmutableMissing, ref.Key)
	}
	digest := digestBytes(data)
	if ref.Digest.Hex != "" && !digest.Equal(ref.Digest) {
		return nil, fmt.Errorf("%w: %s", ErrImmutableChanged, ref.Key)
	}
	return append([]byte(nil), data...), nil
}

func (d Digest) Equal(other Digest) bool {
	return d.Algorithm == other.Algorithm && d.Hex == other.Hex
}

func digestTree(entries []Entry) Digest {
	var buf bytes.Buffer
	for _, entry := range entries {
		_, _ = io.WriteString(&buf, entry.Path)
		_, _ = io.WriteString(&buf, "\x00")
		_, _ = io.WriteString(&buf, string(entry.Kind))
		_, _ = io.WriteString(&buf, "\x00")
		if entry.Kind == EntryFile && entry.Bytes != nil {
			_, _ = buf.Write(entry.Bytes)
		}
		_, _ = io.WriteString(&buf, "\x00")
	}
	return digestBytes(buf.Bytes())
}
