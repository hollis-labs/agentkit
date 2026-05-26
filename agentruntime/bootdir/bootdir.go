// Package bootdir defines app-extensible boot directory planting helpers.
package bootdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentlaunch "github.com/hollis-labs/agentkit/agentlaunch"
)

var (
	ErrUnsafePath            = errors.New("bootdir: unsafe bootdir-relative path")
	ErrUnsupportedNativeKind = errors.New("bootdir: unsupported native file kind")
	ErrEmptyBootDir          = errors.New("bootdir: empty boot dir")
	ErrEmptyRelPath          = errors.New("bootdir: empty relative path")
)

const (
	SourceNative  = "native"
	SourceOverlay = "overlay"
	SourceFile    = "file"
)

type File struct {
	RelPath string
	Content string
	Mode    fs.FileMode
}

type DryRunPlan struct {
	Files       []PlannedFile
	NativeFiles []agentlaunch.NativeFile
	Overlays    []File
}

type PlannedFile struct {
	RelPath string
	Mode    fs.FileMode
	Source  string
}

type WrittenFile struct {
	RelPath string
	Mode    fs.FileMode
	Source  string
}

type WriteResult struct {
	Files []WrittenFile
}

type NativeResolver func(agentlaunch.NativeFile) (File, error)

type Writer struct {
	// AtomicWrite overrides the default temp-file + rename writer. It is called
	// after the parent directory has been created.
	AtomicWrite func(path string, data []byte, mode fs.FileMode) error
}

type WriteOptions struct {
	// OverlayWinsLast is the default and only currently supported ordering:
	// native files write first, overlays write last in sorted path order.
	OverlayWinsLast bool

	// NativeResolver resolves non-raw native file kinds. When nil, only
	// agentlaunch.NativeFileRaw is accepted.
	NativeResolver NativeResolver
}

type Request struct {
	Provider    string
	Runtime     agentlaunch.RuntimeKind
	NativeFiles []agentlaunch.NativeFile
	Overlays    map[string]string
}

// BuildInjection validates caller-supplied native files and overlays and
// returns the go-agent-launch InjectionSpec. Apps own the content renderers;
// this package owns the safety boundary and dry-run shape.
func BuildInjection(req Request) (agentlaunch.InjectionSpec, DryRunPlan, error) {
	out := agentlaunch.InjectionSpec{
		NativeFiles:    append([]agentlaunch.NativeFile(nil), req.NativeFiles...),
		BootDirOverlay: map[string]string{},
	}
	plan := DryRunPlan{NativeFiles: append([]agentlaunch.NativeFile(nil), req.NativeFiles...)}
	for i := range out.NativeFiles {
		if err := out.NativeFiles[i].Validate(); err != nil {
			return agentlaunch.InjectionSpec{}, DryRunPlan{}, err
		}
	}
	for rel, content := range req.Overlays {
		if err := ValidateRelPath(rel); err != nil {
			return agentlaunch.InjectionSpec{}, DryRunPlan{}, fmt.Errorf("%w: %s", err, rel)
		}
		out.BootDirOverlay[rel] = content
		plan.Overlays = append(plan.Overlays, File{RelPath: rel, Content: content, Mode: 0o644})
	}
	if len(out.BootDirOverlay) == 0 {
		out.BootDirOverlay = nil
	}
	return out, plan, nil
}

func ValidateRelPath(rel string) error {
	if rel == "" {
		return ErrEmptyRelPath
	}
	if err := agentlaunch.ValidateBootDirRelPath(rel); err != nil {
		return ErrUnsafePath
	}
	return nil
}

func PlanInjectionSpec(spec agentlaunch.InjectionSpec, opts WriteOptions) (DryRunPlan, error) {
	files, err := planInjectionFiles(spec, opts)
	if err != nil {
		return DryRunPlan{}, err
	}
	plan := DryRunPlan{
		Files:       make([]PlannedFile, 0, len(files)),
		NativeFiles: append([]agentlaunch.NativeFile(nil), spec.NativeFiles...),
	}
	for _, f := range files {
		plan.Files = append(plan.Files, PlannedFile{RelPath: f.file.RelPath, Mode: f.file.Mode, Source: f.source})
		if f.source == SourceOverlay {
			plan.Overlays = append(plan.Overlays, f.file)
		}
	}
	return plan, nil
}

func (w Writer) WriteInjectionSpec(bootDir string, spec agentlaunch.InjectionSpec, opts WriteOptions) (WriteResult, error) {
	if strings.TrimSpace(bootDir) == "" {
		return WriteResult{}, ErrEmptyBootDir
	}
	files, err := planInjectionFiles(spec, opts)
	if err != nil {
		return WriteResult{}, err
	}
	return w.writePlannedFiles(bootDir, files)
}

func (w Writer) WriteFiles(bootDir string, files []File) (WriteResult, error) {
	if strings.TrimSpace(bootDir) == "" {
		return WriteResult{}, ErrEmptyBootDir
	}
	planned := make([]plannedFile, 0, len(files))
	for _, file := range files {
		normalized, err := normalizeFile(file)
		if err != nil {
			return WriteResult{}, err
		}
		planned = append(planned, plannedFile{file: normalized, source: SourceFile})
	}
	return w.writePlannedFiles(bootDir, planned)
}

func (w Writer) writePlannedFiles(bootDir string, files []plannedFile) (WriteResult, error) {
	result := WriteResult{Files: make([]WrittenFile, 0, len(files))}
	write := w.AtomicWrite
	if write == nil {
		write = defaultAtomicWrite
	}
	for _, planned := range files {
		path := filepath.Join(bootDir, filepath.FromSlash(planned.file.RelPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return WriteResult{}, fmt.Errorf("bootdir: plant %q: mkdir: %w", planned.file.RelPath, err)
		}
		if err := write(path, []byte(planned.file.Content), planned.file.Mode); err != nil {
			return WriteResult{}, fmt.Errorf("bootdir: plant %q: %w", planned.file.RelPath, err)
		}
		result.Files = append(result.Files, WrittenFile{
			RelPath: planned.file.RelPath,
			Mode:    planned.file.Mode,
			Source:  planned.source,
		})
	}
	return result, nil
}

type plannedFile struct {
	file   File
	source string
}

func planInjectionFiles(spec agentlaunch.InjectionSpec, opts WriteOptions) ([]plannedFile, error) {
	files := make([]plannedFile, 0, len(spec.NativeFiles)+len(spec.BootDirOverlay))
	for _, nf := range spec.NativeFiles {
		file, err := resolveNativeFile(nf, opts.NativeResolver)
		if err != nil {
			return nil, err
		}
		files = append(files, plannedFile{file: file, source: SourceNative})
	}
	keys := make([]string, 0, len(spec.BootDirOverlay))
	for key := range spec.BootDirOverlay {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		file, err := normalizeFile(File{RelPath: key, Content: spec.BootDirOverlay[key]})
		if err != nil {
			return nil, err
		}
		files = append(files, plannedFile{file: file, source: SourceOverlay})
	}
	return files, nil
}

func resolveNativeFile(nf agentlaunch.NativeFile, resolver NativeResolver) (File, error) {
	if nf.Kind != agentlaunch.NativeFileRaw {
		if resolver == nil {
			return File{}, fmt.Errorf("%w: %q", ErrUnsupportedNativeKind, nf.Kind)
		}
		file, err := resolver(nf)
		if err != nil {
			return File{}, err
		}
		return normalizeFile(file)
	}
	return normalizeFile(File{RelPath: nf.RelPath, Content: nf.Content, Mode: nf.Mode})
}

func normalizeFile(file File) (File, error) {
	if file.RelPath == "" {
		return File{}, ErrEmptyRelPath
	}
	if err := ValidateRelPath(file.RelPath); err != nil {
		return File{}, err
	}
	if file.Mode == 0 {
		file.Mode = 0o644
	}
	return file, nil
}

func defaultAtomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// TaskBundle returns raw native files under root. The caller owns body
// rendering; the helper pins Torque/Tether's safe task bundle planting shape.
func TaskBundle(root string, files map[string]string) ([]agentlaunch.NativeFile, error) {
	if root == "" {
		root = "tasks"
	}
	if err := ValidateRelPath(root + "/README.md"); err != nil {
		return nil, err
	}
	out := make([]agentlaunch.NativeFile, 0, len(files))
	for rel, content := range files {
		path := root + "/" + rel
		nf := agentlaunch.NativeFile{Kind: agentlaunch.NativeFileRaw, RelPath: path, Content: content}
		if err := nf.Validate(); err != nil {
			return nil, err
		}
		out = append(out, nf)
	}
	return out, nil
}
