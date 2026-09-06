package materialize

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/artifact"
)

func TestReconcileRefreshAndOwnedRemovalReportsAndManifests(t *testing.T) {
	target := filepath.Join(t.TempDir(), "boot")
	engine := NewEngine(EngineOptions{Now: fixedNow})
	created, err := engine.Apply(context.Background(), createRequest(target))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	noOpReq := createRequest(target)
	noOpReq.Operation = OperationReconcile
	noOpReq.CurrentManifest = &created.Manifest
	noOpReq.ExpectedGeneration = "gen-1"
	noOp, err := engine.Apply(context.Background(), noOpReq)
	if err != nil {
		t.Fatalf("no-op reconcile: %v", err)
	}
	if !allChanges(noOp.Report.Changes, ChangeUnchanged) || !noOp.Report.Complete {
		t.Fatalf("no-op report = %#v", noOp.Report)
	}

	changedReq := createRequest(target)
	changedReq.Operation = OperationReconcile
	changedReq.CurrentManifest = &created.Manifest
	changedReq.ExpectedGeneration = "gen-1"
	changedReq.Generation = "gen-2"
	changedReq.Artifacts.Entries[0].Bytes = []byte("hello v2\n")
	changedReq.Artifacts.Entries[1].Mode = 0o700
	changed, err := engine.Apply(context.Background(), changedReq)
	if err != nil {
		t.Fatalf("changed reconcile: %v", err)
	}
	if changeKind(changed.Report, "AGENTS.md") != ChangeUpdate || changeKind(changed.Report, "bin/run.sh") != ChangeMode {
		t.Fatalf("changed report = %#v", changed.Report)
	}
	assertFile(t, filepath.Join(target, "AGENTS.md"), []byte("hello v2\n"), 0o644)
	assertFile(t, filepath.Join(target, "bin", "run.sh"), []byte("#!/bin/sh\n"), 0o700)
	loaded, err := LoadManifest(target)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if loaded.Generation != "gen-2" {
		t.Fatalf("manifest generation = %q, want gen-2", loaded.Generation)
	}

	refreshReq := createRequest(target)
	refreshReq.Operation = OperationRefresh
	refreshReq.CurrentManifest = &changed.Manifest
	refreshReq.ExpectedGeneration = "gen-2"
	refreshReq.Generation = "gen-3"
	refreshReq.Selection = Selection{Groups: []string{"binary"}}
	refreshReq.Artifacts.Entries[0].Bytes = []byte("should not write\n")
	refreshReq.Artifacts.Entries[1].Mode = 0o755
	refreshReq.Artifacts.Entries[4].Bytes = []byte{9, 8, 7}
	refreshed, err := engine.Apply(context.Background(), refreshReq)
	if err != nil {
		t.Fatalf("selected refresh: %v", err)
	}
	if changeKind(refreshed.Report, "nested/data.bin") != ChangeUpdate {
		t.Fatalf("refresh report = %#v", refreshed.Report)
	}
	if len(refreshed.Report.Changes) != 1 {
		t.Fatalf("refresh touched %d changes, want 1: %#v", len(refreshed.Report.Changes), refreshed.Report.Changes)
	}
	assertFile(t, filepath.Join(target, "AGENTS.md"), []byte("hello v2\n"), 0o644)
	assertFile(t, filepath.Join(target, "nested", "data.bin"), []byte{9, 8, 7}, 0o600)

	removeReq := createRequest(target)
	removeReq.Operation = OperationReconcile
	removeReq.CurrentManifest = &refreshed.Manifest
	removeReq.ExpectedGeneration = "gen-3"
	removeReq.Generation = "gen-4"
	removeReq.Selection = Selection{Groups: []string{"binary"}}
	removeReq.Reconcile.RemoveOwned = true
	removeReq.Artifacts.Entries = removeReq.Artifacts.Entries[:4]
	removed, err := engine.Apply(context.Background(), removeReq)
	if err != nil {
		t.Fatalf("owned removal: %v", err)
	}
	if changeKind(removed.Report, "nested/data.bin") != ChangeRemove {
		t.Fatalf("remove report = %#v", removed.Report)
	}
	assertAbsent(t, filepath.Join(target, "nested", "data.bin"))
	assertFile(t, filepath.Join(target, "AGENTS.md"), []byte("hello v2\n"), 0o644)
}

func TestReconcileConflictsAndPartialRecovery(t *testing.T) {
	t.Run("omitted owned entry stays in manifest unless removal requested", func(t *testing.T) {
		target, manifest := createBoot(t)
		req := createRequest(target)
		req.Operation = OperationReconcile
		req.CurrentManifest = &manifest
		req.Generation = "gen-2"
		req.Artifacts.Entries = req.Artifacts.Entries[:1]
		handle, err := NewEngine(EngineOptions{}).Apply(context.Background(), req)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if _, ok := manifestByPath(handle.Manifest.Entries)["bin/run.sh"]; !ok {
			t.Fatalf("omitted owned file was dropped from manifest: %#v", handle.Manifest.Entries)
		}
		assertFile(t, filepath.Join(target, "bin", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	})
	t.Run("stale generation", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "boot")
		created, err := NewEngine(EngineOptions{}).Apply(context.Background(), createRequest(target))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		req := createRequest(target)
		req.Operation = OperationReconcile
		req.CurrentManifest = &created.Manifest
		req.ExpectedGeneration = "stale"
		_, err = NewEngine(EngineOptions{}).Apply(context.Background(), req)
		if !errors.Is(err, ErrStaleGeneration) {
			t.Fatalf("err = %v, want ErrStaleGeneration", err)
		}
	})
	t.Run("user edited owned file", func(t *testing.T) {
		target, manifest := createBoot(t)
		if err := os.WriteFile(filepath.Join(target, "AGENTS.md"), []byte("user edit\n"), 0o644); err != nil {
			t.Fatalf("user edit: %v", err)
		}
		req := createRequest(target)
		req.Operation = OperationReconcile
		req.CurrentManifest = &manifest
		req.Artifacts.Entries[0].Bytes = []byte("library update\n")
		handle, err := NewEngine(EngineOptions{}).Apply(context.Background(), req)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if changeKind(handle.Report, "AGENTS.md") != ChangeConflict || handle.Report.Complete {
			t.Fatalf("conflict report = %#v", handle.Report)
		}
		assertFile(t, filepath.Join(target, "AGENTS.md"), []byte("user edit\n"), 0o644)
	})
	t.Run("missing owned file", func(t *testing.T) {
		target, manifest := createBoot(t)
		if err := os.Remove(filepath.Join(target, "AGENTS.md")); err != nil {
			t.Fatalf("remove: %v", err)
		}
		req := createRequest(target)
		req.Operation = OperationReconcile
		req.CurrentManifest = &manifest
		req.Artifacts.Entries[0].Bytes = []byte("library update\n")
		_, err := NewEngine(EngineOptions{}).Apply(context.Background(), req)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
	t.Run("interrupted multi file write reports incomplete", func(t *testing.T) {
		target, manifest := createBoot(t)
		fail := errors.New("synthetic second write failure")
		seen := 0
		req := createRequest(target)
		req.Operation = OperationReconcile
		req.CurrentManifest = &manifest
		req.Generation = "gen-2"
		req.Artifacts.Entries[0].Bytes = []byte("first update\n")
		req.Artifacts.Entries[4].Bytes = []byte{3, 2, 1}
		handle, err := NewEngine(EngineOptions{BeforeWrite: func(_ string, entry artifact.Entry) error {
			if entry.Kind != artifact.EntryFile || (entry.Path != "AGENTS.md" && entry.Path != "nested/data.bin") {
				return nil
			}
			seen++
			if seen == 2 {
				return fail
			}
			return nil
		}}).Apply(context.Background(), req)
		if !errors.Is(err, fail) {
			t.Fatalf("err = %v, want synthetic failure", err)
		}
		if handle.Report.Complete {
			t.Fatalf("partial failure reported complete: %#v", handle.Report)
		}
		assertFile(t, filepath.Join(target, "AGENTS.md"), []byte("first update\n"), 0o644)
		assertFile(t, filepath.Join(target, "nested", "data.bin"), []byte{0, 1, 2, 255}, 0o600)
		loaded, err := LoadManifest(target)
		if err != nil {
			t.Fatalf("load manifest: %v", err)
		}
		if loaded.Generation != manifest.Generation {
			t.Fatalf("manifest advanced after partial failure: %q", loaded.Generation)
		}
	})
}

func TestReconcilePreservesUnownedContentAndDocumentKeys(t *testing.T) {
	target, manifest := createBoot(t)
	if err := os.WriteFile(filepath.Join(target, "unowned.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write unowned: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(target, "nested", "unowned-child"), 0o755); err != nil {
		t.Fatalf("mkdir unowned nested child: %v", err)
	}
	removeReq := createRequest(target)
	removeReq.Operation = OperationReconcile
	removeReq.CurrentManifest = &manifest
	removeReq.Selection = Selection{Groups: []string{"binary"}}
	removeReq.Reconcile.RemoveOwned = true
	for i := range manifest.Entries {
		if manifest.Entries[i].Path == "nested" {
			manifest.Entries[i].Ownership.GroupID = "binary"
		}
	}
	removeReq.CurrentManifest = &manifest
	removeReq.Artifacts.Entries = removeReq.Artifacts.Entries[:3]
	_, err := NewEngine(EngineOptions{}).Apply(context.Background(), removeReq)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict for non-empty owned directory", err)
	}
	assertFile(t, filepath.Join(target, "unowned.txt"), []byte("keep\n"), 0o644)
	if _, err := os.Stat(filepath.Join(target, "nested", "unowned-child")); err != nil {
		t.Fatalf("nested unowned child missing: %v", err)
	}

	jsonMerge, err := MergeDocument(DocumentPatch{
		Kind:     DocumentJSON,
		Existing: []byte(`{"unowned":true,"managed":"old"}`),
		Previous: []ManagedKey{{Key: "managed", Digest: digestForJSON(t, `"old"`), EntryID: "json.managed", GroupID: "provider.json"}},
		Desired:  []ManagedKey{{Key: "managed", Value: json.RawMessage(`"new"`), EntryID: "json.managed", GroupID: "provider.json"}},
	})
	if err != nil {
		t.Fatalf("json merge: %v", err)
	}
	if !strings.Contains(string(jsonMerge.Bytes), `"unowned": true`) || !strings.Contains(string(jsonMerge.Bytes), `"managed": "new"`) {
		t.Fatalf("json merge did not preserve/update keys: %s", jsonMerge.Bytes)
	}
	if _, err := MergeDocument(DocumentPatch{Kind: DocumentJSON, Existing: []byte(`{"managed":"user"}`), Previous: []ManagedKey{{Key: "managed", Digest: digestForJSON(t, `"old"`)}}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("json managed-key edit err = %v, want ErrConflict", err)
	}
	if _, err := MergeDocument(DocumentPatch{Kind: DocumentJSON, Existing: []byte(`{"broken"`)}); !errors.Is(err, ErrMalformedDocument) {
		t.Fatalf("malformed json err = %v, want ErrMalformedDocument", err)
	}

	tomlMerge, err := MergeDocument(DocumentPatch{
		Kind:     DocumentTOML,
		Existing: []byte("unowned = true\nmanaged = \"old\"\n"),
		Previous: []ManagedKey{{Key: "managed", Digest: artifact.DigestBytes([]byte(`"old"`)), EntryID: "toml.managed", GroupID: "provider.toml"}},
		Desired:  []ManagedKey{{Key: "managed", Raw: `"new"`, EntryID: "toml.managed", GroupID: "provider.toml"}},
	})
	if err != nil {
		t.Fatalf("toml merge: %v", err)
	}
	if !strings.Contains(string(tomlMerge.Bytes), "unowned = true") || !strings.Contains(string(tomlMerge.Bytes), `managed = "new"`) {
		t.Fatalf("toml merge did not preserve/update keys: %s", tomlMerge.Bytes)
	}
	if _, err := MergeDocument(DocumentPatch{Kind: DocumentTOML, Existing: []byte("not valid\n")}); !errors.Is(err, ErrMalformedDocument) {
		t.Fatalf("malformed toml err = %v, want ErrMalformedDocument", err)
	}
}

func createBoot(t *testing.T) (string, Manifest) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "boot")
	handle, err := NewEngine(EngineOptions{Now: fixedNow}).Apply(context.Background(), createRequest(target))
	if err != nil {
		t.Fatalf("create boot: %v", err)
	}
	return target, handle.Manifest
}

func allChanges(changes []Change, kind ChangeKind) bool {
	if len(changes) == 0 {
		return false
	}
	for _, change := range changes {
		if change.Kind != kind {
			return false
		}
	}
	return true
}

func changeKind(report Report, path string) ChangeKind {
	for _, change := range report.Changes {
		if change.Path == path {
			return change.Kind
		}
	}
	return ""
}

func digestForJSON(t *testing.T, raw string) artifact.Digest {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return artifact.DigestBytes(canonical)
}

func TestDocumentMergeDeterministic(t *testing.T) {
	patch := DocumentPatch{Kind: DocumentJSON, Existing: []byte(`{"b":2}`), Desired: []ManagedKey{{Key: "a", Value: json.RawMessage(`1`)}}}
	first, err := MergeDocument(patch)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := MergeDocument(patch)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("merge changed across identical inputs")
	}
}
