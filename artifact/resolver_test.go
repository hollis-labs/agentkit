package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestResolversProduceEquivalentDeterministicArtifacts(t *testing.T) {
	root := t.TempDir()
	writeFixtureTree(t, root)

	fsReq := SourceRequest{
		Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root}},
	}
	fsTree, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), fsReq)
	if err != nil {
		t.Fatalf("filesystem resolve: %v", err)
	}

	generatedTree := Tree{Entries: cloneEntries(fsTree.Entries)}
	genReq := SourceRequest{
		Source: Source{Kind: SourceGeneratedTree, Generated: &GeneratedSource{ID: "fixture", Entries: generatedTree.Entries}},
	}
	genTree, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), genReq)
	if err != nil {
		t.Fatalf("generated resolve: %v", err)
	}

	ref := ImmutableRef{Store: "memory", Key: "fixture", Digest: digestTree(generatedTree.Entries)}
	storeTree, err := NewResolver(ResolverOptions{
		ImmutableStore: MemoryStore{Trees: map[string]Tree{"fixture": generatedTree}},
	}).ResolveArtifacts(context.Background(), SourceRequest{
		Source: Source{Kind: SourceImmutableTree, Immutable: &ImmutableSource{Ref: ref}},
	})
	if err != nil {
		t.Fatalf("immutable tree resolve: %v", err)
	}

	if !reflect.DeepEqual(fsTree.Entries, genTree.Entries) {
		t.Fatalf("filesystem and generated entries differ:\nfs=%#v\ngen=%#v", fsTree.Entries, genTree.Entries)
	}
	if !reflect.DeepEqual(fsTree.Entries, storeTree.Entries) {
		t.Fatalf("filesystem and immutable entries differ:\nfs=%#v\nstore=%#v", fsTree.Entries, storeTree.Entries)
	}

	again, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), fsReq)
	if err != nil {
		t.Fatalf("filesystem resolve again: %v", err)
	}
	if !reflect.DeepEqual(fsTree.Entries, again.Entries) {
		t.Fatalf("filesystem resolve is not deterministic:\nfirst=%#v\nsecond=%#v", fsTree.Entries, again.Entries)
	}
}

func TestResolverPreservesBinaryModesNestedFilesAndEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	writeFixtureTree(t, root)

	tree, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
		Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	byPath := map[string]Entry{}
	for _, entry := range tree.Entries {
		byPath[entry.Path] = entry
	}
	if got := byPath["bin/run.sh"].Mode; got != 0o755 {
		t.Fatalf("executable mode = %o, want 0755", got)
	}
	if got := byPath["nested/data.bin"].Bytes; !reflect.DeepEqual(got, []byte{0, 1, 2, 255}) {
		t.Fatalf("binary bytes = %#v", got)
	}
	if byPath["empty"].Kind != EntryDirectory {
		t.Fatalf("empty directory not preserved: %#v", byPath["empty"])
	}
}

func TestResolverAppliesDestinationPrefixAndOwnershipGroup(t *testing.T) {
	tree, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
		Source: Source{Kind: SourceGeneratedTree, Generated: &GeneratedSource{
			ID: "fixture",
			Entries: []Entry{{
				Path:  "file.txt",
				Kind:  EntryFile,
				Mode:  0o644,
				Bytes: []byte("x"),
			}},
		}},
		DestinationPrefix: "context/copied",
		OwnershipGroup:    "group-a",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got, want := tree.Entries[0].Path, "context/copied/file.txt"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := tree.Entries[0].Ownership.GroupID, "group-a"; got != want {
		t.Fatalf("group = %q, want %q", got, want)
	}
}

func TestResolverRejectsImmutableMissingAndChanged(t *testing.T) {
	resolver := NewResolver(ResolverOptions{ImmutableStore: MemoryStore{Trees: map[string]Tree{
		"fixture": {Entries: []Entry{{Path: "x", Kind: EntryFile, Bytes: []byte("x")}}},
	}}})
	_, err := resolver.ResolveArtifacts(context.Background(), SourceRequest{
		Source: Source{Kind: SourceImmutableTree, Immutable: &ImmutableSource{Ref: ImmutableRef{Store: "memory", Key: "missing"}}},
	})
	if !errors.Is(err, ErrImmutableMissing) {
		t.Fatalf("missing immutable err = %v, want ErrImmutableMissing", err)
	}

	_, err = resolver.ResolveArtifacts(context.Background(), SourceRequest{
		Source: Source{Kind: SourceImmutableTree, Immutable: &ImmutableSource{Ref: ImmutableRef{
			Store: "memory",
			Key:   "fixture",
			Digest: Digest{
				Algorithm: "sha256",
				Hex:       "wrong",
			},
		}}},
	})
	if !errors.Is(err, ErrImmutableChanged) {
		t.Fatalf("changed immutable err = %v, want ErrImmutableChanged", err)
	}
}

func TestResolverResolvesImmutableObject(t *testing.T) {
	data := []byte("object bytes")
	ref := ImmutableRef{Store: "memory", Key: "objects/blob.txt", Digest: digestBytes(data)}
	tree, err := NewResolver(ResolverOptions{
		ImmutableStore: MemoryStore{Objects: map[string][]byte{ref.Key: data}},
	}).ResolveArtifacts(context.Background(), SourceRequest{
		Source: Source{Kind: SourceImmutableObject, Immutable: &ImmutableSource{Ref: ref}},
	})
	if err != nil {
		t.Fatalf("resolve object: %v", err)
	}
	if len(tree.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(tree.Entries))
	}
	if got := tree.Entries[0]; got.Path != "objects/blob.txt" || !reflect.DeepEqual(got.Bytes, data) || got.ContentRef == nil {
		t.Fatalf("entry = %#v", got)
	}
}

func TestResolverRejectsLimitsCancellationCollisionsAndTraversal(t *testing.T) {
	t.Run("max entries", func(t *testing.T) {
		_, err := resolveGenerated(t, SourceLimits{MaxEntries: 1}, []Entry{
			{Path: "a", Kind: EntryFile, Bytes: []byte("a")},
			{Path: "b", Kind: EntryFile, Bytes: []byte("b")},
		})
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("err = %v, want ErrLimitExceeded", err)
		}
	})
	t.Run("max bytes", func(t *testing.T) {
		_, err := resolveGenerated(t, SourceLimits{MaxBytes: 1}, []Entry{{Path: "a", Kind: EntryFile, Bytes: []byte("ab")}})
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("err = %v, want ErrLimitExceeded", err)
		}
	})
	t.Run("max depth", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a", "b", "c.txt"), []byte("x"), 0o644)
		_, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root}},
			Limits: SourceLimits{MaxDepth: 2},
		})
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("err = %v, want ErrLimitExceeded", err)
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := resolveGeneratedWithContext(ctx, SourceLimits{}, []Entry{{Path: "a", Kind: EntryFile, Bytes: []byte("a")}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		_, err := resolveGenerated(t, SourceLimits{}, []Entry{
			{Path: "a", Kind: EntryFile, Bytes: []byte("a")},
			{Path: "a", Kind: EntryFile, Bytes: []byte("b")},
		})
		if !errors.Is(err, ErrDuplicatePath) {
			t.Fatalf("err = %v, want ErrDuplicatePath", err)
		}
	})
	t.Run("prefix collision", func(t *testing.T) {
		_, err := resolveGenerated(t, SourceLimits{}, []Entry{
			{Path: "a", Kind: EntryFile, Bytes: []byte("a")},
			{Path: "a/b", Kind: EntryFile, Bytes: []byte("b")},
		})
		if !errors.Is(err, ErrPrefixCollision) {
			t.Fatalf("err = %v, want ErrPrefixCollision", err)
		}
	})
	t.Run("traversal", func(t *testing.T) {
		_, err := resolveGenerated(t, SourceLimits{}, []Entry{{Path: "../x", Kind: EntryFile, Bytes: []byte("x")}})
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("err = %v, want ErrUnsafePath", err)
		}
	})
	t.Run("case-sensitive paths are distinct", func(t *testing.T) {
		tree, err := resolveGenerated(t, SourceLimits{}, []Entry{
			{Path: "A.txt", Kind: EntryFile, Bytes: []byte("upper")},
			{Path: "a.txt", Kind: EntryFile, Bytes: []byte("lower")},
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if len(tree.Entries) != 2 {
			t.Fatalf("entries = %d, want 2", len(tree.Entries))
		}
	})
	t.Run("unsafe prefix", func(t *testing.T) {
		_, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceGeneratedTree, Generated: &GeneratedSource{
				ID:      "fixture",
				Entries: []Entry{{Path: "a", Kind: EntryFile, Bytes: []byte("a")}},
			}},
			DestinationPrefix: "../x",
		})
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("err = %v, want ErrUnsafePath", err)
		}
	})
}

func TestFilesystemSymlinkPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires Unix symlink behavior")
	}
	t.Run("rejects by default", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "target.txt"), []byte("x"), 0o644)
		if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root}},
		})
		if !errors.Is(err, ErrSymlinkRejected) {
			t.Fatalf("err = %v, want ErrSymlinkRejected", err)
		}
	})
	t.Run("imports in-root file by value", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "target.txt"), []byte("x"), 0o755)
		if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		tree, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root, Symlinks: SymlinkImportByVal}},
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		byPath := map[string]Entry{}
		for _, entry := range tree.Entries {
			byPath[entry.Path] = entry
		}
		if got := byPath["link.txt"]; !reflect.DeepEqual(got.Bytes, []byte("x")) || got.Mode != 0o755 {
			t.Fatalf("imported link = %#v", got)
		}
	})
	t.Run("dangling", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink("missing.txt", filepath.Join(root, "link.txt")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root, Symlinks: SymlinkImportByVal}},
		})
		if !errors.Is(err, ErrSymlinkDangling) {
			t.Fatalf("err = %v, want ErrSymlinkDangling", err)
		}
	})
	t.Run("cycle rejected", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink("link.txt", filepath.Join(root, "link.txt")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root}},
		})
		if !errors.Is(err, ErrSymlinkRejected) {
			t.Fatalf("err = %v, want ErrSymlinkRejected", err)
		}
	})
	t.Run("out of root", func(t *testing.T) {
		root := t.TempDir()
		other := t.TempDir()
		writeFile(t, filepath.Join(other, "secret.txt"), []byte("x"), 0o644)
		if err := os.Symlink(filepath.Join(other, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root, Symlinks: SymlinkImportByVal}},
		})
		if !errors.Is(err, ErrSymlinkOutOfRoot) {
			t.Fatalf("err = %v, want ErrSymlinkOutOfRoot", err)
		}
	})
	t.Run("directory target", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.Symlink("dir", filepath.Join(root, "link")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := NewResolver(ResolverOptions{}).ResolveArtifacts(context.Background(), SourceRequest{
			Source: Source{Kind: SourceFilesystemTree, Filesystem: &FilesystemSource{Root: root, Symlinks: SymlinkImportByVal}},
		})
		if !errors.Is(err, ErrSymlinkDirectory) {
			t.Fatalf("err = %v, want ErrSymlinkDirectory", err)
		}
	})
}

func resolveGenerated(t *testing.T, limits SourceLimits, entries []Entry) (Tree, error) {
	t.Helper()
	return resolveGeneratedWithContext(context.Background(), limits, entries)
}

func resolveGeneratedWithContext(ctx context.Context, limits SourceLimits, entries []Entry) (Tree, error) {
	return NewResolver(ResolverOptions{}).ResolveArtifacts(ctx, SourceRequest{
		Source: Source{Kind: SourceGeneratedTree, Generated: &GeneratedSource{
			ID:      "fixture",
			Entries: entries,
		}},
		Limits: limits,
	})
}

func writeFixtureTree(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "AGENTS.md"), []byte("hello\n"), 0o644)
	writeFile(t, filepath.Join(root, "bin", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	writeFile(t, filepath.Join(root, "nested", "data.bin"), []byte{0, 1, 2, 255}, 0o600)
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
}

func writeFile(t *testing.T, p string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, data, mode); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}
