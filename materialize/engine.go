package materialize

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/agentkit/artifact"
)

type EngineOptions struct {
	BeforeWrite  func(stageRoot string, entry artifact.Entry) error
	BeforeRename func(stageRoot string, targetRoot string) error
	Now          func() time.Time
}

type DefaultEngine struct {
	opts EngineOptions
}

func NewEngine(opts EngineOptions) *DefaultEngine {
	return &DefaultEngine{opts: opts}
}

var _ Engine = (*DefaultEngine)(nil)

func (e *DefaultEngine) Plan(ctx context.Context, req Request) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if req.Operation == OperationReconcile || req.Operation == OperationRefresh {
		plan, err := e.planReconcile(ctx, req)
		if err != nil {
			return Plan{}, err
		}
		return plan.Plan, nil
	}
	entries, err := normalizedEntries(req)
	if err != nil {
		return Plan{}, err
	}
	manifest := buildManifest(req, entries, e.now())
	report := Report{
		Operation: req.Operation,
		Changes:   make([]Change, 0, len(entries)),
		Complete:  true,
	}
	for _, entry := range entries {
		report.Changes = append(report.Changes, Change{
			Path:    entry.Path,
			Kind:    ChangeCreate,
			EntryID: entry.Ownership.EntryID,
			GroupID: entry.Ownership.GroupID,
			After:   manifestDigest(entry),
		})
	}
	return Plan{
		Request:    req,
		Manifest:   manifest,
		Report:     report,
		WillMutate: req.Operation != OperationPlan,
	}, nil
}

func (e *DefaultEngine) Apply(ctx context.Context, req Request) (Handle, error) {
	if req.Operation == OperationPlan {
		plan, err := e.Plan(ctx, req)
		if err != nil {
			return Handle{}, err
		}
		return Handle{TargetRoot: req.TargetRoot, Manifest: plan.Manifest, Report: plan.Report}, nil
	}
	if req.Operation != OperationCreate {
		if req.Operation == OperationReconcile || req.Operation == OperationRefresh {
			return e.applyReconcile(ctx, req)
		}
		return Handle{}, fmt.Errorf("%w: %s", ErrUnsupportedOperation, req.Operation)
	}
	plan, err := e.Plan(ctx, req)
	if err != nil {
		return Handle{}, err
	}
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	targetAbs, err := filepath.Abs(req.TargetRoot)
	if err != nil {
		return Handle{}, err
	}
	parentAbs := filepath.Dir(targetAbs)
	targetBase := filepath.Base(targetAbs)
	if err := rejectSymlinkParents(parentAbs); err != nil {
		return Handle{}, err
	}
	if _, err := os.Lstat(targetAbs); err == nil {
		return Handle{}, fmt.Errorf("%w: %s", ErrTargetExists, targetAbs)
	} else if !os.IsNotExist(err) {
		return Handle{}, err
	}
	parentRoot, err := os.OpenRoot(parentAbs)
	if err != nil {
		return Handle{}, err
	}
	defer func() { _ = parentRoot.Close() }()

	stageBase := "." + targetBase + ".agentkit-stage-" + randomSuffix()
	if err := parentRoot.Mkdir(stageBase, 0o700); err != nil {
		return Handle{}, err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = parentRoot.RemoveAll(stageBase)
		}
	}()

	stageAbs := filepath.Join(parentAbs, stageBase)
	for _, entry := range plan.Manifest.Entries {
		if err := ctx.Err(); err != nil {
			return Handle{}, err
		}
		source, ok := findEntry(plan.Request.Artifacts.Entries, entry.Path)
		if !ok {
			return Handle{}, fmt.Errorf("materialize: manifest/source mismatch for %s", entry.Path)
		}
		if e.opts.BeforeWrite != nil {
			if err := e.opts.BeforeWrite(stageAbs, source); err != nil {
				return Handle{}, err
			}
		}
		if err := writeEntry(parentRoot, stageBase, source); err != nil {
			return Handle{}, err
		}
	}
	if e.opts.BeforeRename != nil {
		if err := e.opts.BeforeRename(stageAbs, targetAbs); err != nil {
			return Handle{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	if err := parentRoot.Rename(stageBase, targetBase); err != nil {
		return Handle{}, err
	}
	cleanupStage = false
	if err := SaveManifest(targetAbs, plan.Manifest); err != nil {
		return Handle{TargetRoot: targetAbs, Manifest: plan.Manifest, Report: Report{Operation: req.Operation, Changes: plan.Report.Changes, Complete: false}}, err
	}
	return Handle{TargetRoot: targetAbs, Manifest: plan.Manifest, Report: plan.Report}, nil
}

func normalizedEntries(req Request) ([]artifact.Entry, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return artifact.Normalize(req.Artifacts.Entries)
}

func buildManifest(req Request, entries []artifact.Entry, now time.Time) Manifest {
	manifest := Manifest{
		SchemaVersion: "agentkit.materialize.v1",
		Generation:    req.Generation,
		Roots:         req.Roots,
		Entries:       make([]ManifestEntry, 0, len(entries)),
		CreatedAt:     now,
	}
	for _, entry := range entries {
		manifest.Entries = append(manifest.Entries, ManifestEntry{
			Path:       entry.Path,
			Kind:       entry.Kind,
			Mode:       uint32(modeFor(entry)),
			Digest:     manifestDigest(entry),
			Ownership:  entry.Ownership,
			Provenance: entry.Provenance,
		})
	}
	sort.SliceStable(manifest.Entries, func(i, j int) bool {
		return manifest.Entries[i].Path < manifest.Entries[j].Path
	})
	return manifest
}

func writeEntry(root *os.Root, stageBase string, entry artifact.Entry) error {
	rel := filepath.ToSlash(filepath.Join(stageBase, filepath.FromSlash(entry.Path)))
	switch entry.Kind {
	case artifact.EntryDirectory:
		if err := root.MkdirAll(rel, modeFor(entry)); err != nil {
			return fmt.Errorf("materialize: mkdir %s: %w", entry.Path, err)
		}
		return root.Chmod(rel, modeFor(entry))
	case artifact.EntryFile:
		if entry.Bytes == nil {
			return fmt.Errorf("%w: %s", ErrUnresolvedContent, entry.Path)
		}
		if err := root.MkdirAll(filepath.ToSlash(filepath.Dir(rel)), 0o755); err != nil {
			return fmt.Errorf("materialize: mkdir parent %s: %w", entry.Path, err)
		}
		f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, modeFor(entry))
		if err != nil {
			return fmt.Errorf("materialize: create %s: %w", entry.Path, err)
		}
		if _, err := f.Write(entry.Bytes); err != nil {
			_ = f.Close()
			return fmt.Errorf("materialize: write %s: %w", entry.Path, err)
		}
		if err := f.Chmod(modeFor(entry)); err != nil {
			_ = f.Close()
			return fmt.Errorf("materialize: chmod %s: %w", entry.Path, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("materialize: close %s: %w", entry.Path, err)
		}
	}
	return nil
}

func findEntry(entries []artifact.Entry, rel string) (artifact.Entry, bool) {
	for _, entry := range entries {
		if entry.Path == rel {
			return entry, true
		}
	}
	return artifact.Entry{}, false
}

func modeFor(entry artifact.Entry) fs.FileMode {
	if entry.Mode != 0 {
		return entry.Mode.Perm()
	}
	if entry.Kind == artifact.EntryDirectory {
		return 0o755
	}
	return 0o644
}

func manifestDigest(entry artifact.Entry) artifact.Digest {
	if entry.Digest.Algorithm != "" || entry.Digest.Hex != "" {
		return entry.Digest
	}
	if entry.Kind == artifact.EntryFile && entry.Bytes != nil {
		return artifact.DigestBytes(entry.Bytes)
	}
	return artifact.Digest{}
}

func rejectSymlinkParents(parent string) error {
	parent = filepath.Clean(parent)
	for current := parent; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if isAllowedPlatformAlias(current) {
				next := filepath.Dir(current)
				if next == current {
					return nil
				}
				continue
			}
			return fmt.Errorf("%w: target parent contains symlink: %s", ErrUnsafeTarget, current)
		}
		next := filepath.Dir(current)
		if next == current {
			return nil
		}
	}
}

func isAllowedPlatformAlias(path string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	switch path {
	case "/var", "/tmp", "/etc":
	default:
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return resolved == filepath.Join("/private", path)
}

func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return hex.EncodeToString(b[:])
}

func (e *DefaultEngine) now() time.Time {
	if e.opts.Now != nil {
		return e.opts.Now().UTC()
	}
	return time.Now().UTC()
}
