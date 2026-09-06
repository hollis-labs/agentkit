package materialize

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/artifact"
)

func TestPlanIsDeterministicAndNonMutating(t *testing.T) {
	target := filepath.Join(t.TempDir(), "boot")
	engine := NewEngine(EngineOptions{Now: fixedNow})
	req := createRequest(target)

	first, err := engine.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan first: %v", err)
	}
	second, err := engine.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("plan changed across identical inputs:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("plan mutated target, stat err=%v", err)
	}
}

func TestCreatePublishesCompleteTreeWithManifestAndReport(t *testing.T) {
	target := filepath.Join(t.TempDir(), "boot")
	handle, err := NewEngine(EngineOptions{Now: fixedNow}).Apply(context.Background(), createRequest(target))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got, want := handle.TargetRoot, target; got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
	assertFile(t, filepath.Join(target, "AGENTS.md"), []byte("hello\n"), 0o644)
	assertFile(t, filepath.Join(target, "bin", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	assertFile(t, filepath.Join(target, "nested", "data.bin"), []byte{0, 1, 2, 255}, 0o600)
	if info, err := os.Stat(filepath.Join(target, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: info=%v err=%v", info, err)
	}
	if len(handle.Manifest.Entries) != 5 {
		t.Fatalf("manifest entries = %d, want 5", len(handle.Manifest.Entries))
	}
	if len(handle.Report.Changes) != 5 || !handle.Report.Complete {
		t.Fatalf("report = %#v", handle.Report)
	}
	for _, change := range handle.Report.Changes {
		if change.Redacted != nil {
			t.Fatalf("change leaked redaction map in non-secret fixture: %#v", change)
		}
		if change.Kind != ChangeCreate {
			t.Fatalf("change kind = %q, want create", change.Kind)
		}
	}
}

func TestCreateFailureCasesLeaveDestinationAbsent(t *testing.T) {
	t.Run("existing target", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "boot")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
		_, err := NewEngine(EngineOptions{}).Apply(context.Background(), createRequest(target))
		if !errors.Is(err, ErrTargetExists) {
			t.Fatalf("err = %v, want ErrTargetExists", err)
		}
	})
	t.Run("write failure", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "boot")
		fail := errors.New("synthetic write failure")
		_, err := NewEngine(EngineOptions{
			BeforeWrite: func(string, artifact.Entry) error { return fail },
		}).Apply(context.Background(), createRequest(target))
		if !errors.Is(err, fail) {
			t.Fatalf("err = %v, want synthetic write failure", err)
		}
		assertAbsent(t, target)
		assertNoStages(t, filepath.Dir(target))
	})
	t.Run("rename failure", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "boot")
		fail := errors.New("synthetic rename failure")
		_, err := NewEngine(EngineOptions{
			BeforeRename: func(string, string) error { return fail },
		}).Apply(context.Background(), createRequest(target))
		if !errors.Is(err, fail) {
			t.Fatalf("err = %v, want synthetic rename failure", err)
		}
		assertAbsent(t, target)
		assertNoStages(t, filepath.Dir(target))
	})
	t.Run("cancellation", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "boot")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NewEngine(EngineOptions{}).Apply(ctx, createRequest(target))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		assertAbsent(t, target)
	})
	t.Run("unresolved content", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "boot")
		req := createRequest(target)
		req.Artifacts.Entries[1].Bytes = nil
		req.Artifacts.Entries[1].ContentRef = &artifact.ImmutableRef{Store: "memory", Key: "bin/run.sh"}
		_, err := NewEngine(EngineOptions{}).Apply(context.Background(), req)
		if !errors.Is(err, ErrUnresolvedContent) {
			t.Fatalf("err = %v, want ErrUnresolvedContent", err)
		}
		assertAbsent(t, target)
		assertNoStages(t, filepath.Dir(target))
	})
}

func TestCreateRejectsSymlinkParentAndStagedPathSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires Unix symlink behavior")
	}
	t.Run("symlink parent", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		link := filepath.Join(root, "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := NewEngine(EngineOptions{}).Apply(context.Background(), createRequest(filepath.Join(link, "boot")))
		if !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("err = %v, want ErrUnsafeTarget", err)
		}
		assertAbsent(t, filepath.Join(outside, "boot"))
	})
	t.Run("symlink ancestor parent", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.Mkdir(filepath.Join(outside, "sub"), 0o755); err != nil {
			t.Fatalf("mkdir outside sub: %v", err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := NewEngine(EngineOptions{}).Apply(context.Background(), createRequest(filepath.Join(link, "sub", "boot")))
		if !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("err = %v, want ErrUnsafeTarget", err)
		}
		assertAbsent(t, filepath.Join(outside, "sub", "boot"))
	})
	t.Run("staged path swap", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "boot")
		outside := t.TempDir()
		swapped := false
		_, err := NewEngine(EngineOptions{
			BeforeWrite: func(stageRoot string, entry artifact.Entry) error {
				if entry.Path != "nested/data.bin" || swapped {
					return nil
				}
				swapped = true
				if err := os.RemoveAll(filepath.Join(stageRoot, "nested")); err != nil {
					return err
				}
				return os.Symlink(outside, filepath.Join(stageRoot, "nested"))
			},
		}).Apply(context.Background(), createRequest(target))
		if err == nil {
			t.Fatal("apply succeeded after staged path swap, want containment error")
		}
		assertAbsent(t, filepath.Join(outside, "data.bin"))
		assertAbsent(t, target)
	})
}

func createRequest(target string) Request {
	return Request{
		Operation:      OperationCreate,
		TargetRoot:     target,
		ExistingTarget: ExistingTargetRefuse,
		Generation:     "gen-1",
		Roots: TargetRoots{
			ProjectRoot: filepath.Join(filepath.Dir(target), "project"),
			BootRoot:    target,
			StateRoot:   filepath.Join(filepath.Dir(target), "state"),
			ScratchRoot: filepath.Join(filepath.Dir(target), "scratch"),
		},
		Artifacts: artifact.Tree{Entries: []artifact.Entry{
			{
				Path:      "AGENTS.md",
				Kind:      artifact.EntryFile,
				Mode:      0o644,
				Bytes:     []byte("hello\n"),
				Ownership: artifact.Ownership{EntryID: "agents", GroupID: "boot", Generation: "gen-1"},
			},
			{
				Path:      "bin/run.sh",
				Kind:      artifact.EntryFile,
				Mode:      0o755,
				Bytes:     []byte("#!/bin/sh\n"),
				Ownership: artifact.Ownership{EntryID: "run", GroupID: "scripts", Generation: "gen-1"},
			},
			{Path: "empty", Kind: artifact.EntryDirectory, Mode: 0o755},
			{Path: "nested", Kind: artifact.EntryDirectory, Mode: 0o755},
			{
				Path:      "nested/data.bin",
				Kind:      artifact.EntryFile,
				Mode:      0o600,
				Bytes:     []byte{0, 1, 2, 255},
				Ownership: artifact.Ownership{EntryID: "data", GroupID: "binary", Generation: "gen-1"},
			},
		}},
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
}

func assertFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !reflect.DeepEqual(got, data) {
		t.Fatalf("%s bytes = %#v, want %#v", path, got, data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("%s mode = %o, want %o", path, got, mode)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or stat failed differently: %v", path, err)
	}
}

func assertNoStages(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	for _, entry := range entries {
		if matched, _ := filepath.Match(".*.agentkit-stage-*", entry.Name()); matched {
			t.Fatalf("staging directory was not cleaned: %s", entry.Name())
		}
	}
}
