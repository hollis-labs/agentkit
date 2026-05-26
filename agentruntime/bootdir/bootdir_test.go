package bootdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	agentlaunch "github.com/hollis-labs/agentkit/agentlaunch"
)

func TestBuildInjectionRejectsUnsafePaths(t *testing.T) {
	_, _, err := BuildInjection(Request{Overlays: map[string]string{"../escape": "x"}})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
}

func TestTaskBundleRejectsUnsafePaths(t *testing.T) {
	_, err := TaskBundle("tasks", map[string]string{"../bad.md": "x"})
	if err == nil {
		t.Fatal("expected unsafe path error")
	}
}

func TestBuildInjectionNativeFiles(t *testing.T) {
	_, _, err := BuildInjection(Request{NativeFiles: []agentlaunch.NativeFile{{
		Kind: agentlaunch.NativeFileRaw, RelPath: "tasks/README.md", Content: "read me",
	}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriteInjectionSpecOrderAndOverlayWins(t *testing.T) {
	dir := t.TempDir()
	var calls []string
	w := Writer{AtomicWrite: func(path string, data []byte, mode fs.FileMode) error {
		calls = append(calls, filepath.Base(path)+":"+string(data))
		return os.WriteFile(path, data, mode)
	}}
	spec := agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			raw("same.md", "native", 0),
			raw("native-only.md", "native-only", 0),
		},
		BootDirOverlay: map[string]string{
			"z.md":    "z",
			"a.md":    "a",
			"same.md": "overlay",
		},
	}
	result, err := w.WriteInjectionSpec(dir, spec, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := rels(result.Files)
	wantOrder := []string{"same.md", "native-only.md", "a.md", "same.md", "z.md"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("write order = %v, want %v", gotOrder, wantOrder)
	}
	if string(mustRead(t, filepath.Join(dir, "same.md"))) != "overlay" {
		t.Fatal("overlay did not win over native file at same path")
	}
	if !reflect.DeepEqual(calls, []string{
		"same.md:native",
		"native-only.md:native-only",
		"a.md:a",
		"same.md:overlay",
		"z.md:z",
	}) {
		t.Fatalf("atomic calls = %v", calls)
	}
}

func TestPlanInjectionSpecMatchesActualWriteOrder(t *testing.T) {
	spec := agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{raw("n.md", "n", 0)},
		BootDirOverlay: map[string]string{
			"b.md": "b",
			"a.md": "a",
		},
	}
	plan, err := PlanInjectionSpec(spec, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Writer{}).WriteInjectionSpec(t.TempDir(), spec, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plannedRels(plan.Files), rels(result.Files)) {
		t.Fatalf("plan order = %v, write order = %v", plannedRels(plan.Files), rels(result.Files))
	}
	if got, want := sources(result.Files), []string{SourceNative, SourceOverlay, SourceOverlay}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}

func TestWriteRejectsUnsafeAndEmptyPaths(t *testing.T) {
	cases := []struct {
		name string
		spec agentlaunch.InjectionSpec
		err  error
	}{
		{
			name: "native unsafe",
			spec: agentlaunch.InjectionSpec{NativeFiles: []agentlaunch.NativeFile{raw("../bad.md", "x", 0)}},
			err:  ErrUnsafePath,
		},
		{
			name: "overlay unsafe",
			spec: agentlaunch.InjectionSpec{BootDirOverlay: map[string]string{"/abs.md": "x"}},
			err:  ErrUnsafePath,
		},
		{
			name: "native empty rel",
			spec: agentlaunch.InjectionSpec{NativeFiles: []agentlaunch.NativeFile{raw("", "x", 0)}},
			err:  ErrEmptyRelPath,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (Writer{}).WriteInjectionSpec(t.TempDir(), tc.spec, WriteOptions{})
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
		})
	}

	_, err := (Writer{}).WriteFiles(t.TempDir(), []File{{RelPath: ".git/config", Content: "x"}})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("partial unsafe err = %v, want ErrUnsafePath", err)
	}
}

func TestUnsupportedNativeKindAndResolverHook(t *testing.T) {
	spec := agentlaunch.InjectionSpec{NativeFiles: []agentlaunch.NativeFile{{
		Kind: agentlaunch.NativeFileSkill,
		ID:   "review",
	}}}
	_, err := (Writer{}).WriteInjectionSpec(t.TempDir(), spec, WriteOptions{})
	if !errors.Is(err, ErrUnsupportedNativeKind) {
		t.Fatalf("err = %v, want ErrUnsupportedNativeKind", err)
	}

	dir := t.TempDir()
	_, err = (Writer{}).WriteInjectionSpec(dir, spec, WriteOptions{
		NativeResolver: func(nf agentlaunch.NativeFile) (File, error) {
			return File{RelPath: "skills/" + nf.ID + ".md", Content: "skill"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(dir, "skills/review.md"))) != "skill" {
		t.Fatal("resolver output was not written")
	}
}

func TestModesDefaultAndExplicitPreserved(t *testing.T) {
	dir := t.TempDir()
	result, err := (Writer{}).WriteInjectionSpec(dir, agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			raw("default.md", "x", 0),
			raw("private.md", "x", 0o600),
		},
	}, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Files[0].Mode; got != 0o644 {
		t.Fatalf("default mode = %#o, want 0644", got)
	}
	if got := result.Files[1].Mode; got != 0o600 {
		t.Fatalf("explicit mode = %#o, want 0600", got)
	}
	if mode := mustMode(t, filepath.Join(dir, "private.md")); mode != 0o600 {
		t.Fatalf("filesystem mode = %#o, want 0600", mode)
	}
}

func TestWriteFilesRewritesOnlySpecifiedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sibling.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Writer{}).WriteFiles(dir, []File{{RelPath: "slot.md", Content: "v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := (Writer{}).WriteFiles(dir, []File{{RelPath: "slot.md", Content: "v2"}}); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(dir, "slot.md"))) != "v2" {
		t.Fatal("slot was not rewritten")
	}
	if string(mustRead(t, filepath.Join(dir, "sibling.md"))) != "keep" {
		t.Fatal("sibling was touched")
	}
}

func TestEmptyBootDirRejected(t *testing.T) {
	_, err := (Writer{}).WriteFiles("", []File{{RelPath: "x.md", Content: "x"}})
	if !errors.Is(err, ErrEmptyBootDir) {
		t.Fatalf("err = %v, want ErrEmptyBootDir", err)
	}
}

func raw(rel, content string, mode fs.FileMode) agentlaunch.NativeFile {
	return agentlaunch.NativeFile{Kind: agentlaunch.NativeFileRaw, RelPath: rel, Content: content, Mode: mode}
}

func rels(files []WrittenFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.RelPath)
	}
	return out
}

func plannedRels(files []PlannedFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.RelPath)
	}
	return out
}

func sources(files []WrittenFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Source)
	}
	return out
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
