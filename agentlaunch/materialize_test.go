package agentlaunch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// harnessRenderer is a test ContractRenderer standing in for a
// consumer-supplied per-harness renderer. It shapes a contract-object body
// from runtime-injected dynamic content (provider, inputs, vars) keyed by
// the slot Ref — exactly the seam a real harness (AGENTS.md / CLAUDE.md /
// .mcp.json / settings.json / boot.md shaper) plugs into. The library
// owns NO harness-specific body; this lives in the test as the consumer
// would supply it.
type harnessRenderer struct {
	// calls records every slot Ref rendered, in call order, so a test can
	// assert exactly which slots a Replant touched.
	calls []string
}

func (h *harnessRenderer) RenderContractObject(_ context.Context, req ContractRenderRequest) (string, error) {
	h.calls = append(h.calls, req.Object.Ref)
	provider := req.Spec.Runtime.Provider
	switch req.Object.Ref {
	case "claude-md":
		return "# CLAUDE.md\nharness: " + provider +
			"\nrole: " + stringifyValue(req.Inputs["role"]) + "\n", nil
	case "mcp-json":
		return `{"harness":"` + provider + `","tools":"` +
			stringifyValue(req.Vars["tool_list"]) + `"}`, nil
	case "settings-json":
		return `{"permissions":"` + stringifyValue(req.Vars["permissions"]) + `"}`, nil
	case "boot-md":
		return "boot " + stringifyValue(req.Inputs["ticket"]) + "\n", nil
	default:
		return "slot:" + req.Object.Ref + "\n", nil
	}
}

// errRenderer is a ContractRenderer that always fails, used to assert the
// slot render error path.
type errRenderer struct{}

func (errRenderer) RenderContractObject(context.Context, ContractRenderRequest) (string, error) {
	return "", errors.New("renderer boom")
}

// harnessSpec builds a representative BootSpec for the given provider. It
// exercises every contract-object kind: a slot-rendered CLAUDE.md, a
// slot-rendered .mcp.json, a slot settings.json, an opaque verbatim user
// file (literal), an input-projecting file, a var-projecting file, plus a
// skill injection and a raw injection.
func harnessSpec(provider string) *BootSpec {
	return &BootSpec{
		Inputs: []BootInput{
			{Name: "ticket", Type: "string", Required: true},
			{Name: "role", Type: "string", Default: "backend"},
		},
		Vars: []VarSpec{
			{
				Name:      "tool_list",
				Source:    VarSource{Kind: VarSourceLiteral, Literal: "placeholder"},
				Freshness: VarFreshnessCacheOK,
				OnError:   VarOnErrorWarn,
				Phase:     VarPhaseBuild,
			},
			{
				Name:      "permissions",
				Source:    VarSource{Kind: VarSourceLiteral, Literal: "placeholder"},
				Freshness: VarFreshnessCacheOK,
				OnError:   VarOnErrorWarn,
				Phase:     VarPhaseBuild,
			},
		},
		Files: []BootFileSpec{
			{
				ID:      "claude-md",
				RelPath: "CLAUDE.md",
				Object:  ContractObject{Kind: ContractObjectSlot, Ref: "claude-md"},
			},
			{
				ID:      "mcp-json",
				RelPath: ".mcp.json",
				Object:  ContractObject{Kind: ContractObjectSlot, Ref: "mcp-json"},
			},
			{
				ID:      "settings-json",
				RelPath: ".claude/settings.json",
				Object:  ContractObject{Kind: ContractObjectSlot, Ref: "settings-json"},
			},
			{
				ID:      "boot-md",
				RelPath: "boot.md",
				Object:  ContractObject{Kind: ContractObjectSlot, Ref: "boot-md"},
			},
			{
				ID:      "user-notes",
				RelPath: "docs/NOTES.md",
				Object:  ContractObject{Kind: ContractObjectLiteral, Text: "verbatim {{ inputs.ticket }} body"},
			},
			{
				ID:      "agents-md",
				RelPath: "AGENTS.md",
				Object:  ContractObject{Kind: ContractObjectInput, Ref: "role"},
			},
			{
				ID:      "tools-txt",
				RelPath: "tools.txt",
				Object:  ContractObject{Kind: ContractObjectVar, Ref: "tool_list"},
			},
		},
		Injections: []BootInjectionSpec{
			{
				ID:     "skill-review",
				Kind:   NativeFileSkill,
				Name:   "code-review",
				Object: ContractObject{Kind: ContractObjectLiteral, Text: "SKILL BODY"},
			},
			{
				ID:      "raw-overlay",
				Kind:    NativeFileRaw,
				RelPath: "overlay/extra.txt",
				Object:  ContractObject{Kind: ContractObjectLiteral, Text: "RAW OVERLAY"},
			},
		},
		Runtime: RuntimeBinding{Provider: provider, RuntimeKind: RuntimeSubprocess},
	}
}

func readBootFile(t *testing.T, bootDir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bootDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestPopulateShapesContractObjectsPerHarness checks that each supported
// harness gets correctly shaped contract objects and that user files are
// placed verbatim.
func TestPopulateShapesContractObjectsPerHarness(t *testing.T) {
	cases := []struct {
		provider      string
		wantSkillPath string
	}{
		{"claude", ".claude/skills/code-review.md"},
		{"opencode", ".opencode/skills/code-review.md"},
		{"codex", "skills/code-review.md"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			bootDir := t.TempDir()
			m := NewDefaultMaterializer(MaterializerOptions{
				Vars: map[string]any{
					"tool_list":   "grep,read,bash",
					"permissions": "read-only",
				},
			})
			r := &harnessRenderer{}
			res, err := m.Populate(context.Background(), bootDir,
				MaterializeRequest{
					Spec:   harnessSpec(tc.provider),
					Inputs: map[string]any{"ticket": "CW-1", "role": "ops"},
				}, r)
			if err != nil {
				t.Fatalf("Populate: %v", err)
			}

			// Harness-shaped slot files carry the provider identity.
			claudeMd := readBootFile(t, bootDir, "CLAUDE.md")
			if !strings.Contains(claudeMd, "harness: "+tc.provider) {
				t.Errorf("CLAUDE.md not shaped for harness: %q", claudeMd)
			}
			if !strings.Contains(claudeMd, "role: ops") {
				t.Errorf("CLAUDE.md missing runtime-injected input: %q", claudeMd)
			}
			mcp := readBootFile(t, bootDir, ".mcp.json")
			if !strings.Contains(mcp, `"harness":"`+tc.provider+`"`) ||
				!strings.Contains(mcp, "grep,read,bash") {
				t.Errorf(".mcp.json not shaped with dynamic tools: %q", mcp)
			}
			if got := readBootFile(t, bootDir, ".claude/settings.json"); !strings.Contains(got, "read-only") {
				t.Errorf("settings.json missing injected permissions: %q", got)
			}

			// Opaque user file placed verbatim — NO var substitution.
			if got := readBootFile(t, bootDir, "docs/NOTES.md"); got != "verbatim {{ inputs.ticket }} body" {
				t.Errorf("user file not verbatim: %q", got)
			}
			// Input- and var-projecting files.
			if got := readBootFile(t, bootDir, "AGENTS.md"); got != "ops" {
				t.Errorf("AGENTS.md input projection = %q, want ops", got)
			}
			if got := readBootFile(t, bootDir, "tools.txt"); got != "grep,read,bash" {
				t.Errorf("tools.txt var projection = %q", got)
			}

			// Skill injection routed to the provider-native skill dir.
			if got := readBootFile(t, bootDir, tc.wantSkillPath); got != "SKILL BODY" {
				t.Errorf("skill not planted at %s: %q", tc.wantSkillPath, got)
			}
			// Raw injection placed verbatim at its RelPath.
			if got := readBootFile(t, bootDir, "overlay/extra.txt"); got != "RAW OVERLAY" {
				t.Errorf("raw injection = %q", got)
			}

			// Result report covers every object.
			if len(res.FilesWritten) != 7 {
				t.Errorf("FilesWritten = %v, want 7", res.FilesWritten)
			}
			if len(res.InjectionsWritten) != 2 {
				t.Errorf("InjectionsWritten = %v, want 2", res.InjectionsWritten)
			}
			wantSlots := []string{"boot-md", "claude-md", "mcp-json", "settings-json"}
			if !reflect.DeepEqual(res.SlotsRendered, wantSlots) {
				t.Errorf("SlotsRendered = %v, want %v", res.SlotsRendered, wantSlots)
			}
			if res.Runtime.Provider != tc.provider {
				t.Errorf("result Runtime not echoed: %+v", res.Runtime)
			}
		})
	}
}

// TestPopulateIdempotentReRun checks crash-recovery idempotency: a second
// Populate over an already-populated bootDir converges without error and
// without re-writing unchanged files.
func TestPopulateIdempotentReRun(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{
		Vars: map[string]any{"tool_list": "t", "permissions": "p"},
	})
	req := MaterializeRequest{
		Spec:   harnessSpec("claude"),
		Inputs: map[string]any{"ticket": "CW-1"},
	}

	first, err := m.Populate(context.Background(), bootDir, req, &harnessRenderer{})
	if err != nil {
		t.Fatalf("first Populate: %v", err)
	}
	if len(first.FilesWritten) != 7 {
		t.Fatalf("first run FilesWritten = %v, want 7", first.FilesWritten)
	}

	// Capture mtimes so we can assert unchanged files were not rewritten.
	claudePath := filepath.Join(bootDir, "CLAUDE.md")
	before, err := os.Stat(claudePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	second, err := m.Populate(context.Background(), bootDir, req, &harnessRenderer{})
	if err != nil {
		t.Fatalf("second Populate (idempotency): %v", err)
	}
	// Identical content => nothing reported as written.
	if len(second.FilesWritten) != 0 || len(second.InjectionsWritten) != 0 {
		t.Errorf("second run rewrote files: files=%v injections=%v",
			second.FilesWritten, second.InjectionsWritten)
	}
	after, err := os.Stat(claudePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("unchanged file was rewritten: mtime moved %v -> %v",
			before.ModTime(), after.ModTime())
	}
	// Content is still correct after the converging re-run.
	if got := readBootFile(t, bootDir, "CLAUDE.md"); !strings.Contains(got, "harness: claude") {
		t.Errorf("content drifted after re-run: %q", got)
	}
}

// TestPopulateIdempotentOverPartialDir checks that Populate converges when
// run over a bootDir that a prior crashed run left partially populated.
func TestPopulateIdempotentOverPartialDir(t *testing.T) {
	bootDir := t.TempDir()
	// Simulate a crashed prior run: CLAUDE.md exists with stale content,
	// some files are missing entirely.
	if err := os.WriteFile(filepath.Join(bootDir, "CLAUDE.md"),
		[]byte("STALE PARTIAL CONTENT"), 0o644); err != nil {
		t.Fatalf("seed partial: %v", err)
	}

	m := NewDefaultMaterializer(MaterializerOptions{
		Vars: map[string]any{"tool_list": "t", "permissions": "p"},
	})
	res, err := m.Populate(context.Background(), bootDir, MaterializeRequest{
		Spec:   harnessSpec("claude"),
		Inputs: map[string]any{"ticket": "CW-1"},
	}, &harnessRenderer{})
	if err != nil {
		t.Fatalf("Populate over partial dir: %v", err)
	}
	// The stale CLAUDE.md is reconciled to the correct content.
	if got := readBootFile(t, bootDir, "CLAUDE.md"); !strings.Contains(got, "harness: claude") {
		t.Errorf("stale file not reconciled: %q", got)
	}
	// CLAUDE.md was changed, so it is reported written; the rest too.
	if !contains(res.FilesWritten, "CLAUDE.md") {
		t.Errorf("reconciled file not reported: %v", res.FilesWritten)
	}
	if len(res.FilesWritten) != 7 {
		t.Errorf("FilesWritten = %v, want 7 (full convergence)", res.FilesWritten)
	}
}

// TestReplantPartialFileID checks that a Replant narrowed to one file ID
// re-renders only that file and leaves every other file untouched.
func TestReplantPartialFileID(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{
		Vars: map[string]any{"tool_list": "t1", "permissions": "p"},
	})
	spec := harnessSpec("claude")
	if _, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: spec, Inputs: map[string]any{"ticket": "CW-1", "role": "r1"}},
		&harnessRenderer{}); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	mcpBefore, err := os.Stat(filepath.Join(bootDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Re-plant only AGENTS.md with a new role value.
	r := &harnessRenderer{}
	res, err := m.Replant(context.Background(), bootDir,
		MaterializeRequest{Spec: spec, Inputs: map[string]any{"ticket": "CW-1", "role": "r2"}},
		ReplantSelector{FileIDs: []string{"agents-md"}}, r)
	if err != nil {
		t.Fatalf("Replant: %v", err)
	}
	if !reflect.DeepEqual(res.FilesWritten, []string{"AGENTS.md"}) {
		t.Errorf("Replant FilesWritten = %v, want [AGENTS.md]", res.FilesWritten)
	}
	if len(r.calls) != 0 {
		t.Errorf("Replant of a non-slot file invoked renderer: %v", r.calls)
	}
	if got := readBootFile(t, bootDir, "AGENTS.md"); got != "r2" {
		t.Errorf("AGENTS.md not re-rendered: %q", got)
	}
	// Untouched file keeps its mtime.
	mcpAfter, err := os.Stat(filepath.Join(bootDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !mcpBefore.ModTime().Equal(mcpAfter.ModTime()) {
		t.Errorf("partial replant touched an out-of-scope file")
	}
}

// TestReplantPartialSlotRef checks the slot-granular re-plant: a Replant
// narrowed to one slot ref re-renders only the slot(s) bound to that ref —
// the Nanite RegenerateSystemPromptSlot pattern.
func TestReplantPartialSlotRef(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{
		Vars: map[string]any{"tool_list": "old", "permissions": "p"},
	})
	spec := harnessSpec("claude")
	if _, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: spec, Inputs: map[string]any{"ticket": "CW-1"}},
		&harnessRenderer{}); err != nil {
		t.Fatalf("Populate: %v", err)
	}

	// Re-render ONLY the mcp-json slot with a fresh tool list.
	m2 := NewDefaultMaterializer(MaterializerOptions{
		Vars: map[string]any{"tool_list": "fresh,tools", "permissions": "p"},
	})
	r := &harnessRenderer{}
	res, err := m2.Replant(context.Background(), bootDir,
		MaterializeRequest{Spec: spec, Inputs: map[string]any{"ticket": "CW-1"}},
		ReplantSelector{SlotRefs: []string{"mcp-json"}}, r)
	if err != nil {
		t.Fatalf("Replant by slot ref: %v", err)
	}
	// Only the mcp-json slot was rendered.
	if !reflect.DeepEqual(r.calls, []string{"mcp-json"}) {
		t.Errorf("slot replant rendered %v, want [mcp-json]", r.calls)
	}
	if !reflect.DeepEqual(res.SlotsRendered, []string{"mcp-json"}) {
		t.Errorf("SlotsRendered = %v, want [mcp-json]", res.SlotsRendered)
	}
	if !reflect.DeepEqual(res.FilesWritten, []string{".mcp.json"}) {
		t.Errorf("FilesWritten = %v, want [.mcp.json]", res.FilesWritten)
	}
	// The re-rendered slot has the new content; CLAUDE.md slot untouched.
	if got := readBootFile(t, bootDir, ".mcp.json"); !strings.Contains(got, "fresh,tools") {
		t.Errorf("slot not re-rendered: %q", got)
	}
}

// TestReplantPartialInjectionID checks a Replant narrowed to one injection.
func TestReplantPartialInjectionID(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{
		Vars: map[string]any{"tool_list": "t", "permissions": "p"},
	})
	spec := harnessSpec("claude")
	if _, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: spec, Inputs: map[string]any{"ticket": "CW-1"}},
		&harnessRenderer{}); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	res, err := m.Replant(context.Background(), bootDir,
		MaterializeRequest{Spec: spec, Inputs: map[string]any{"ticket": "CW-1"}},
		ReplantSelector{InjectionIDs: []string{"skill-review"}}, &harnessRenderer{})
	if err != nil {
		t.Fatalf("Replant injection: %v", err)
	}
	if len(res.FilesWritten) != 0 {
		t.Errorf("injection replant wrote files: %v", res.FilesWritten)
	}
	// Injection content identical => idempotent, nothing reported written.
	if len(res.InjectionsWritten) != 0 {
		t.Errorf("idempotent injection replant reported a write: %v", res.InjectionsWritten)
	}
}

// TestReplantSelectorMiss checks that a selector naming an undeclared
// object is a hard error, not a silent no-op.
func TestReplantSelectorMiss(t *testing.T) {
	m := NewDefaultMaterializer(MaterializerOptions{})
	spec := harnessSpec("claude")
	cases := []ReplantSelector{
		{FileIDs: []string{"no-such-file"}},
		{InjectionIDs: []string{"no-such-injection"}},
		{SlotRefs: []string{"no-such-slot"}},
	}
	for _, sel := range cases {
		_, err := m.Replant(context.Background(), t.TempDir(),
			MaterializeRequest{Spec: spec, Inputs: map[string]any{"ticket": "x"}},
			sel, &harnessRenderer{})
		if !errors.Is(err, ErrMaterializeSelectorMiss) {
			t.Errorf("selector %+v: err = %v, want ErrMaterializeSelectorMiss", sel, err)
		}
	}
}

// TestMaterializePathSafety checks that a file whose RelPath would escape
// the bootDir is rejected.
func TestMaterializePathSafety(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{})
	spec := &BootSpec{
		Files: []BootFileSpec{{
			ID:      "escape",
			RelPath: "../../etc/passwd",
			Object:  ContractObject{Kind: ContractObjectLiteral, Text: "pwned"},
		}},
		Runtime: RuntimeBinding{Provider: "claude", RuntimeKind: RuntimeSubprocess},
	}
	// The unsafe RelPath is caught at BootSpec.Validate time.
	_, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: spec}, nil)
	if !errors.Is(err, ErrUnsafeInjectionTarget) {
		t.Fatalf("escape path err = %v, want ErrUnsafeInjectionTarget", err)
	}
	// And nothing was written outside the bootDir.
	if _, statErr := os.Stat(filepath.Join(bootDir, "..", "..", "etc", "passwd")); statErr == nil {
		t.Fatalf("traversal write escaped the bootDir")
	}
}

// TestMaterializeRawInjectionPathSafety checks that a raw injection cannot
// traverse out of the bootDir.
func TestMaterializeRawInjectionPathSafety(t *testing.T) {
	m := NewDefaultMaterializer(MaterializerOptions{})
	spec := &BootSpec{
		Injections: []BootInjectionSpec{{
			ID:      "escape",
			Kind:    NativeFileRaw,
			RelPath: "../escape.txt",
			Object:  ContractObject{Kind: ContractObjectLiteral, Text: "x"},
		}},
		Runtime: RuntimeBinding{Provider: "claude", RuntimeKind: RuntimeSubprocess},
	}
	_, err := m.Populate(context.Background(), t.TempDir(),
		MaterializeRequest{Spec: spec}, nil)
	if !errors.Is(err, ErrUnsafeInjectionTarget) {
		t.Fatalf("raw injection escape err = %v, want ErrUnsafeInjectionTarget", err)
	}
}

// TestRenderObjectKinds checks each ContractObjectKind resolves correctly,
// including the unknown-input / unknown-var error paths.
func TestRenderObjectKinds(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{Vars: map[string]any{"v1": "VAL"}})

	// Unknown input ref.
	badInput := &BootSpec{
		Files: []BootFileSpec{{
			ID: "f", RelPath: "f.txt",
			Object: ContractObject{Kind: ContractObjectInput, Ref: "nope"},
		}},
		Runtime: RuntimeBinding{Provider: "claude", RuntimeKind: RuntimeSubprocess},
	}
	if _, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: badInput}, nil); !errors.Is(err, ErrMaterializeUnknownInput) {
		t.Errorf("unknown input err = %v, want ErrMaterializeUnknownInput", err)
	}

	// Unknown var ref.
	badVar := &BootSpec{
		Files: []BootFileSpec{{
			ID: "f", RelPath: "f.txt",
			Object: ContractObject{Kind: ContractObjectVar, Ref: "nope"},
		}},
		Runtime: RuntimeBinding{Provider: "claude", RuntimeKind: RuntimeSubprocess},
	}
	if _, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: badVar}, nil); !errors.Is(err, ErrMaterializeUnknownVar) {
		t.Errorf("unknown var err = %v, want ErrMaterializeUnknownVar", err)
	}
}

// TestSlotWithoutRendererErrors checks that a slot object with no renderer
// is a clear error rather than a nil-panic.
func TestSlotWithoutRendererErrors(t *testing.T) {
	m := NewDefaultMaterializer(MaterializerOptions{})
	spec := &BootSpec{
		Files: []BootFileSpec{{
			ID: "s", RelPath: "s.md",
			Object: ContractObject{Kind: ContractObjectSlot, Ref: "slot-x"},
		}},
		Runtime: RuntimeBinding{Provider: "claude", RuntimeKind: RuntimeSubprocess},
	}
	_, err := m.Populate(context.Background(), t.TempDir(),
		MaterializeRequest{Spec: spec}, nil)
	if !errors.Is(err, ErrMaterializeNoRenderer) {
		t.Fatalf("slot w/o renderer err = %v, want ErrMaterializeNoRenderer", err)
	}
}

// TestSlotRendererErrorPropagates checks a failing renderer surfaces.
func TestSlotRendererErrorPropagates(t *testing.T) {
	m := NewDefaultMaterializer(MaterializerOptions{})
	spec := &BootSpec{
		Files: []BootFileSpec{{
			ID: "s", RelPath: "s.md",
			Object: ContractObject{Kind: ContractObjectSlot, Ref: "slot-x"},
		}},
		Runtime: RuntimeBinding{Provider: "claude", RuntimeKind: RuntimeSubprocess},
	}
	_, err := m.Populate(context.Background(), t.TempDir(),
		MaterializeRequest{Spec: spec}, errRenderer{})
	if err == nil || !strings.Contains(err.Error(), "renderer boom") {
		t.Fatalf("renderer error not propagated: %v", err)
	}
}

// TestMaterializeMissingInputs checks the nil-spec and empty-bootDir guards.
func TestMaterializeMissingInputs(t *testing.T) {
	m := NewDefaultMaterializer(MaterializerOptions{})
	if _, err := m.Populate(context.Background(), "x",
		MaterializeRequest{Spec: nil}, nil); !errors.Is(err, ErrMaterializeMissingSpec) {
		t.Errorf("nil spec err = %v, want ErrMaterializeMissingSpec", err)
	}
	if _, err := m.Populate(context.Background(), "",
		MaterializeRequest{Spec: harnessSpec("claude")}, nil); !errors.Is(err, ErrMaterializeMissingBootDir) {
		t.Errorf("empty bootDir err = %v, want ErrMaterializeMissingBootDir", err)
	}
}

// TestFileModeReconciled checks that a declared mode is applied on create
// and reconciled when it drifts on disk.
func TestFileModeReconciled(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{})
	spec := &BootSpec{
		Files: []BootFileSpec{{
			ID: "exec", RelPath: "run.sh", Mode: 0o755,
			Object: ContractObject{Kind: ContractObjectLiteral, Text: "#!/bin/sh\n"},
		}},
		Runtime: RuntimeBinding{Provider: "claude", RuntimeKind: RuntimeSubprocess},
	}
	if _, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: spec}, nil); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	target := filepath.Join(bootDir, "run.sh")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 0755", info.Mode().Perm())
	}
	// Drift the mode, then re-plant: identical content but drifted mode
	// should be reconciled and reported as written.
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	res, err := m.Populate(context.Background(), bootDir,
		MaterializeRequest{Spec: spec}, nil)
	if err != nil {
		t.Fatalf("re-Populate: %v", err)
	}
	if !contains(res.FilesWritten, "run.sh") {
		t.Errorf("mode drift not reconciled-as-written: %v", res.FilesWritten)
	}
	info, _ = os.Stat(target)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode not reconciled: %o", info.Mode().Perm())
	}
}

// TestReplantEmptySelectorIsFullReconcile checks that Replant with an empty
// selector behaves identically to Populate.
func TestReplantEmptySelectorIsFullReconcile(t *testing.T) {
	bootDir := t.TempDir()
	m := NewDefaultMaterializer(MaterializerOptions{
		Vars: map[string]any{"tool_list": "t", "permissions": "p"},
	})
	res, err := m.Replant(context.Background(), bootDir,
		MaterializeRequest{Spec: harnessSpec("claude"), Inputs: map[string]any{"ticket": "CW-1"}},
		ReplantSelector{}, &harnessRenderer{})
	if err != nil {
		t.Fatalf("Replant empty selector: %v", err)
	}
	got := append([]string{}, res.FilesWritten...)
	sort.Strings(got)
	if len(got) != 7 {
		t.Errorf("empty-selector Replant FilesWritten = %v, want 7", got)
	}
}
