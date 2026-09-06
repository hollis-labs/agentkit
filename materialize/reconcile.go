package materialize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/artifact"
)

const ManifestRelPath = ".agentkit/materialize-manifest.json"

type plannedAction struct {
	Entry  artifact.Entry
	Before *ManifestEntry
	Change Change
	Remove bool
}

type reconcilePlan struct {
	Plan      Plan
	Actions   []plannedAction
	Conflicts []Change
}

func ManifestPath(targetRoot string) string {
	return filepath.Join(targetRoot, ManifestRelPath)
}

func SaveManifest(targetRoot string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	path := ManifestPath(targetRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func LoadManifest(targetRoot string) (Manifest, error) {
	data, err := os.ReadFile(ManifestPath(targetRoot))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (e *DefaultEngine) planReconcile(ctx context.Context, req Request) (reconcilePlan, error) {
	if req.CurrentManifest == nil {
		loaded, err := LoadManifest(req.TargetRoot)
		if err != nil {
			if os.IsNotExist(err) {
				return reconcilePlan{}, ErrMissingManifest
			}
			return reconcilePlan{}, err
		}
		req.CurrentManifest = &loaded
	}
	if req.ExpectedGeneration != "" && req.ExpectedGeneration != req.CurrentManifest.Generation {
		return reconcilePlan{}, fmt.Errorf("%w: expected %q got %q", ErrStaleGeneration, req.ExpectedGeneration, req.CurrentManifest.Generation)
	}
	entries, err := normalizedEntries(req)
	if err != nil {
		return reconcilePlan{}, err
	}
	desired := make(map[string]artifact.Entry, len(entries))
	for _, entry := range entries {
		desired[entry.Path] = entry
	}
	previous := manifestByPath(req.CurrentManifest.Entries)
	manifestEntries := make([]artifact.Entry, 0, len(entries))
	report := Report{Operation: req.Operation, Complete: true}
	actions := []plannedAction{}
	conflicts := []Change{}
	for _, entry := range entries {
		if !selectionMatches(req.Selection, entry.Ownership) {
			if prior, ok := previous[entry.Path]; ok {
				manifestEntries = append(manifestEntries, entryFromManifest(prior))
			}
			continue
		}
		prior, hadPrior := previous[entry.Path]
		change := Change{
			Path:    entry.Path,
			EntryID: entry.Ownership.EntryID,
			GroupID: entry.Ownership.GroupID,
			After:   manifestDigest(entry),
		}
		if hadPrior {
			change.Before = prior.Digest
		}
		kind, conflict := classifyReconcileChange(req.TargetRoot, entry, prior, hadPrior, req.Reconcile.Conflict)
		change.Kind = kind
		if conflict != "" {
			change.Conflict = conflict
			conflicts = append(conflicts, change)
			report.Complete = false
		} else if kind != ChangeUnchanged {
			actions = append(actions, plannedAction{Entry: entry, Before: priorPtr(prior, hadPrior), Change: change})
		}
		report.Changes = append(report.Changes, change)
		manifestEntries = append(manifestEntries, entry)
	}
	removalSet := map[string]bool{}
	if req.Reconcile.RemoveOwned {
		for _, prior := range req.CurrentManifest.Entries {
			if prior.Path == ManifestRelPath || !selectionMatches(req.Selection, prior.Ownership) {
				continue
			}
			if _, ok := desired[prior.Path]; !ok {
				removalSet[prior.Path] = true
			}
		}
		for _, prior := range req.CurrentManifest.Entries {
			if prior.Path == ManifestRelPath || !selectionMatches(req.Selection, prior.Ownership) {
				manifestEntries = append(manifestEntries, entryFromManifest(prior))
				continue
			}
			if _, ok := desired[prior.Path]; ok {
				continue
			}
			change := Change{Path: prior.Path, Kind: ChangeRemove, EntryID: prior.Ownership.EntryID, GroupID: prior.Ownership.GroupID, Before: prior.Digest}
			conflict := removalConflict(req.TargetRoot, prior, removalSet, previous)
			if conflict != "" {
				change.Kind = ChangeConflict
				change.Conflict = conflict
				conflicts = append(conflicts, change)
				report.Complete = false
				manifestEntries = append(manifestEntries, entryFromManifest(prior))
			} else {
				actions = append(actions, plannedAction{Before: &prior, Change: change, Remove: true})
			}
			report.Changes = append(report.Changes, change)
		}
		sort.SliceStable(actions, func(i, j int) bool {
			if actions[i].Remove != actions[j].Remove {
				return !actions[i].Remove
			}
			if !actions[i].Remove {
				return actions[i].Change.Path < actions[j].Change.Path
			}
			return strings.Count(actions[i].Change.Path, "/") > strings.Count(actions[j].Change.Path, "/")
		})
	} else {
		for _, prior := range req.CurrentManifest.Entries {
			if prior.Path == ManifestRelPath {
				continue
			}
			if _, ok := desired[prior.Path]; !ok {
				manifestEntries = append(manifestEntries, entryFromManifest(prior))
			}
		}
	}
	manifest := buildManifest(req, manifestEntries, e.now())
	plan := Plan{Request: req, Manifest: manifest, Report: report, WillMutate: true}
	return reconcilePlan{Plan: plan, Actions: actions, Conflicts: conflicts}, nil
}

func (e *DefaultEngine) applyReconcile(ctx context.Context, req Request) (Handle, error) {
	rp, err := e.planReconcile(ctx, req)
	if err != nil {
		return Handle{}, err
	}
	if len(rp.Conflicts) > 0 {
		return Handle{TargetRoot: req.TargetRoot, Manifest: rp.Plan.Manifest, Report: rp.Plan.Report}, ErrConflict
	}
	root, targetAbs, err := openExistingTargetRoot(req.TargetRoot)
	if err != nil {
		return Handle{}, err
	}
	defer root.Close()
	report := rp.Plan.Report
	for _, action := range rp.Actions {
		if err := ctx.Err(); err != nil {
			report.Complete = false
			return Handle{TargetRoot: targetAbs, Manifest: rp.Plan.Manifest, Report: report}, err
		}
		if e.opts.BeforeWrite != nil {
			if err := e.opts.BeforeWrite(targetAbs, action.Entry); err != nil {
				report.Complete = false
				return Handle{TargetRoot: targetAbs, Manifest: rp.Plan.Manifest, Report: report}, err
			}
		}
		if action.Remove {
			if err := removeOwned(root, action.Before); err != nil {
				report.Complete = false
				return Handle{TargetRoot: targetAbs, Manifest: rp.Plan.Manifest, Report: report}, err
			}
			continue
		}
		if err := writeReconcileEntry(root, action.Entry); err != nil {
			report.Complete = false
			return Handle{TargetRoot: targetAbs, Manifest: rp.Plan.Manifest, Report: report}, err
		}
	}
	if err := SaveManifest(targetAbs, rp.Plan.Manifest); err != nil {
		report.Complete = false
		return Handle{TargetRoot: targetAbs, Manifest: rp.Plan.Manifest, Report: report}, err
	}
	return Handle{TargetRoot: targetAbs, Manifest: rp.Plan.Manifest, Report: report}, nil
}

func openExistingTargetRoot(target string) (*os.Root, string, error) {
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return nil, "", err
	}
	if err := rejectSymlinkParents(filepath.Dir(targetAbs)); err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(targetAbs)
	if err != nil {
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, "", fmt.Errorf("%w: target root must be a real directory: %s", ErrUnsafeTarget, targetAbs)
	}
	root, err := os.OpenRoot(targetAbs)
	if err != nil {
		return nil, "", err
	}
	return root, targetAbs, nil
}

func classifyReconcileChange(target string, desired artifact.Entry, prior ManifestEntry, hadPrior bool, policy ConflictPolicy) (ChangeKind, string) {
	current, exists, err := diskState(target, desired.Path)
	if err != nil {
		return ChangeConflict, err.Error()
	}
	if !hadPrior {
		if exists {
			return ChangeConflict, "destination path is unowned"
		}
		return ChangeCreate, ""
	}
	if !exists {
		if policy == ConflictOverwrite {
			return ChangeCreate, ""
		}
		return ChangeConflict, "previously owned path is missing"
	}
	if current.Kind != prior.Kind || current.Digest != prior.Digest || current.Mode != prior.Mode {
		if policy != ConflictOverwrite {
			return ChangeConflict, "owned path changed outside materialize"
		}
	}
	wantedDigest := manifestDigest(desired)
	wantedMode := uint32(modeFor(desired))
	if current.Kind == desired.Kind && current.Digest == wantedDigest && current.Mode == wantedMode {
		return ChangeUnchanged, ""
	}
	if current.Kind == desired.Kind && current.Digest == wantedDigest && current.Mode != wantedMode {
		return ChangeMode, ""
	}
	return ChangeUpdate, ""
}

func removalConflict(target string, prior ManifestEntry, removalSet map[string]bool, previous map[string]ManifestEntry) string {
	current, exists, err := diskState(target, prior.Path)
	if err != nil {
		return err.Error()
	}
	if !exists {
		return ""
	}
	if current.Kind != prior.Kind || current.Digest != prior.Digest || current.Mode != prior.Mode {
		return "owned path changed outside materialize"
	}
	if prior.Kind == artifact.EntryDirectory {
		dirPath := filepath.Join(target, filepath.FromSlash(prior.Path))
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return err.Error()
		}
		for _, entry := range entries {
			childRel := path.Join(prior.Path, entry.Name())
			if removalSet[childRel] {
				continue
			}
			if childPrior, ok := previous[childRel]; ok && removalSet[childPrior.Path] {
				continue
			}
			return "owned directory contains unowned children"
		}
	}
	return ""
}

func diskState(root, rel string) (ManifestEntry, bool, error) {
	if err := artifact.ValidateRelPath(rel); err != nil {
		return ManifestEntry{}, false, err
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ManifestEntry{}, false, nil
		}
		return ManifestEntry{}, false, err
	}
	entry := ManifestEntry{Path: rel, Mode: uint32(info.Mode().Perm())}
	if info.IsDir() {
		entry.Kind = artifact.EntryDirectory
		return entry, true, nil
	}
	if !info.Mode().IsRegular() {
		return ManifestEntry{}, false, fmt.Errorf("unsupported existing file mode %s", info.Mode())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ManifestEntry{}, false, err
	}
	entry.Kind = artifact.EntryFile
	entry.Digest = artifact.DigestBytes(data)
	return entry, true, nil
}

func writeReconcileEntry(root *os.Root, entry artifact.Entry) error {
	switch entry.Kind {
	case artifact.EntryDirectory:
		if err := root.MkdirAll(entry.Path, modeFor(entry)); err != nil {
			return err
		}
		return root.Chmod(entry.Path, modeFor(entry))
	case artifact.EntryFile:
		if entry.Bytes == nil {
			return fmt.Errorf("%w: %s", ErrUnresolvedContent, entry.Path)
		}
		dir := filepath.ToSlash(filepath.Dir(entry.Path))
		if dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		tmp := filepath.ToSlash(filepath.Join(dir, "."+filepath.Base(entry.Path)+".agentkit-tmp-"+randomSuffix()))
		f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, modeFor(entry))
		if err != nil {
			return err
		}
		if _, err := f.Write(entry.Bytes); err != nil {
			_ = f.Close()
			_ = root.Remove(tmp)
			return err
		}
		if err := f.Chmod(modeFor(entry)); err != nil {
			_ = f.Close()
			_ = root.Remove(tmp)
			return err
		}
		if err := f.Close(); err != nil {
			_ = root.Remove(tmp)
			return err
		}
		if err := root.Rename(tmp, entry.Path); err != nil {
			_ = root.Remove(tmp)
			return err
		}
	}
	return nil
}

func removeOwned(root *os.Root, prior *ManifestEntry) error {
	if prior == nil {
		return nil
	}
	if prior.Kind == artifact.EntryDirectory {
		return root.Remove(prior.Path)
	}
	return root.Remove(prior.Path)
}

func manifestByPath(entries []ManifestEntry) map[string]ManifestEntry {
	out := make(map[string]ManifestEntry, len(entries))
	for _, entry := range entries {
		out[entry.Path] = entry
	}
	return out
}

func entryFromManifest(entry ManifestEntry) artifact.Entry {
	return artifact.Entry{Path: entry.Path, Kind: entry.Kind, Mode: os.FileMode(entry.Mode), Ownership: entry.Ownership, Provenance: entry.Provenance, Digest: entry.Digest}
}

func priorPtr(entry ManifestEntry, ok bool) *ManifestEntry {
	if !ok {
		return nil
	}
	return &entry
}

func selectionMatches(sel Selection, own artifact.Ownership) bool {
	if len(sel.Groups) == 0 && len(sel.EntryIDs) == 0 {
		return true
	}
	for _, group := range sel.Groups {
		if group == own.GroupID {
			return true
		}
	}
	for _, id := range sel.EntryIDs {
		if id == own.EntryID {
			return true
		}
	}
	return false
}
