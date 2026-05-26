package agentlaunch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// S4.3 — Materialization.
//
// This file IMPLEMENTS the locked S1.2 Materializer contract. It does not
// redefine it: the frozen types (Materializer, ContractRenderer,
// ReplantSelector, MaterializeRequest, MaterializeResult, BootFileSpec,
// BootInjectionSpec, ContractObject / ContractObjectKind) are owned by
// bootspec.go and shipped in S1. This file builds the concrete
// DefaultMaterializer on top of them plus the S4.1 assembly types and the
// S4.2 var-resolution surface.
//
// What it provides:
//
//   - DefaultMaterializer: a concrete Materializer that places a BootSpec's
//     declared files and injections into a bootdir.
//   - Library-resolved contract objects (literal / input / var) and a
//     consumer-pluggable seam (slot) for per-harness contract-object
//     CONTENT — the library owns the InjectionSpec/BootInjectionSpec
//     vocabulary and the path-safe write loop, NOT the harness-specific
//     CLAUDE.md / .mcp.json bodies.
//   - Idempotent Populate for crash recovery: a second run, or a run over a
//     partially-populated bootdir, converges without error or duplication.
//   - Slot-granular Replant: re-render one file / injection / slot ref
//     without touching the rest of the bootdir (Nanite's
//     RegenerateSystemPromptSlot pattern).
//
// LOCKED constraints honored here (D5; do not reopen):
//
//   - Partial / slot-granular re-plant is first-class: Replant + the
//     ReplantSelector narrow a reconcile to specific files, injections, or
//     slot refs.
//   - Populate is idempotent against an existing dir: writes are
//     overwrite-if-changed, skip-if-identical; an already-populated bootdir
//     is not an error.
//   - The library owns the vocabulary + the path-safe write loop only.
//     Per-harness contract-object CONTENT stays consumer-pluggable through
//     the ContractRenderer seam. No harness-specific bodies are hardcoded.
//   - D7: this extends the v0.1.0 lib; it adds no new field shapes to the
//     frozen S1/S4.1/S4.2 types.

// Materialization sentinel errors. Kept local to this file so the S4.3
// surface does not collide with the S4.1/S4.2 edits to errors.go. All are
// errors.Is-comparable.
var (
	// ErrMaterializeMissingSpec is returned when a MaterializeRequest
	// carries no BootSpec.
	ErrMaterializeMissingSpec = errors.New("agentlaunch: materialize requires a non-nil BootSpec")

	// ErrMaterializeMissingBootDir is returned when Populate or Replant is
	// handed an empty bootDir.
	ErrMaterializeMissingBootDir = errors.New("agentlaunch: materialize requires a non-empty bootDir")

	// ErrMaterializeUnsafePath is returned when a resolved write target
	// would escape the bootDir. It wraps the path-safety failure so a
	// hostile or buggy spec cannot traverse out of the bootdir.
	ErrMaterializeUnsafePath = errors.New("agentlaunch: materialize write target escapes bootDir")

	// ErrMaterializeNoRenderer is returned when a slot contract object is
	// encountered but no ContractRenderer was supplied.
	ErrMaterializeNoRenderer = errors.New("agentlaunch: materialize slot object requires a ContractRenderer")

	// ErrMaterializeUnknownInput is returned when a contract object of kind
	// input references an input the BootSpec does not declare.
	ErrMaterializeUnknownInput = errors.New("agentlaunch: contract object references an unknown input")

	// ErrMaterializeUnknownVar is returned when a contract object of kind
	// var references a var the BootSpec does not declare.
	ErrMaterializeUnknownVar = errors.New("agentlaunch: contract object references an unknown var")

	// ErrMaterializeSelectorMiss is returned when a ReplantSelector names a
	// file ID, injection ID, or slot ref that the BootSpec does not
	// declare. A replant that silently no-ops on a typo is worse than an
	// error.
	ErrMaterializeSelectorMiss = errors.New("agentlaunch: replant selector references an unknown object")
)

// defaultFileMode is the file mode used when a BootFileSpec or
// BootInjectionSpec leaves Mode zero.
const defaultFileMode os.FileMode = 0o644

// MaterializerOptions configures a DefaultMaterializer.
type MaterializerOptions struct {
	// Vars supplies already-resolved derived-var values keyed by
	// VarSpec.Name. The materializer renders vars; it does not resolve
	// them — var resolution is the sibling S4.2 surface (VarResolver). A
	// var contract object whose value is absent here renders as empty and
	// is reported via the unknown-var error only when the var is not even
	// declared on the spec.
	Vars map[string]any

	// DirMode is the mode used when the materializer creates parent
	// directories. Zero falls back to 0o750.
	DirMode os.FileMode
}

// DefaultMaterializer is the concrete library Materializer. It owns the
// path-safe write loop and the literal/input/var contract-object
// resolution; per-harness slot CONTENT is delegated to the supplied
// ContractRenderer.
//
// A DefaultMaterializer is safe to reuse across multiple Populate/Replant
// calls; it carries no per-call mutable state.
type DefaultMaterializer struct {
	opts MaterializerOptions
}

// NewDefaultMaterializer builds a DefaultMaterializer from opts.
func NewDefaultMaterializer(opts MaterializerOptions) *DefaultMaterializer {
	if opts.DirMode == 0 {
		opts.DirMode = 0o750
	}
	return &DefaultMaterializer{opts: opts}
}

// compile-time assertion: DefaultMaterializer satisfies the frozen
// Materializer contract.
var _ Materializer = (*DefaultMaterializer)(nil)

// Populate places every declared file and injection of the request's
// BootSpec into bootDir. It is idempotent against an existing directory:
// a second run, or a run over a partially-populated bootDir, converges
// without error or duplication. Writes are overwrite-if-changed and
// skip-if-identical so crash recovery is a plain re-run.
//
// Populate is Replant with an empty selector — every declared object is
// reconciled.
func (m *DefaultMaterializer) Populate(
	ctx context.Context,
	bootDir string,
	req MaterializeRequest,
	renderer ContractRenderer,
) (*MaterializeResult, error) {
	return m.reconcile(ctx, bootDir, req, ReplantSelector{}, renderer)
}

// Replant reconciles only the objects named by sel. An empty selector
// reconciles everything (identical to Populate). A non-empty selector
// narrows the operation to the named file IDs, injection IDs, and slot
// refs — the partial / slot-granular re-plant the D5 constraint requires.
// Every other already-materialized file in bootDir is left untouched.
func (m *DefaultMaterializer) Replant(
	ctx context.Context,
	bootDir string,
	req MaterializeRequest,
	sel ReplantSelector,
	renderer ContractRenderer,
) (*MaterializeResult, error) {
	return m.reconcile(ctx, bootDir, req, sel, renderer)
}

// reconcile is the shared body of Populate and Replant. It validates
// inputs, resolves the effective input bag, selects the objects in scope,
// and writes each one path-safely.
func (m *DefaultMaterializer) reconcile(
	ctx context.Context,
	bootDir string,
	req MaterializeRequest,
	sel ReplantSelector,
	renderer ContractRenderer,
) (*MaterializeResult, error) {
	if req.Spec == nil {
		return nil, ErrMaterializeMissingSpec
	}
	if bootDir == "" {
		return nil, ErrMaterializeMissingBootDir
	}
	if err := req.Spec.Validate(); err != nil {
		return nil, err
	}

	bootRoot, err := filepath.Abs(bootDir)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch: materialize resolve bootDir %q: %w", bootDir, err)
	}

	scope, err := newReplantScope(req.Spec, sel)
	if err != nil {
		return nil, err
	}

	// Idempotency: creating the bootdir root is a no-op when it already
	// exists, so a re-run over a partially-populated dir is safe.
	if err := os.MkdirAll(bootRoot, m.opts.DirMode); err != nil {
		return nil, fmt.Errorf("agentlaunch: materialize create bootDir %q: %w", bootRoot, err)
	}

	resolvedInputs := m.resolveInputs(req)

	result := &MaterializeResult{Runtime: req.Spec.Runtime}

	// Files first, in declaration order, for deterministic output.
	for i := range req.Spec.Files {
		f := req.Spec.Files[i]
		if !scope.wantsFile(f) {
			continue
		}
		rel, written, rendered, werr := m.materializeFile(ctx, bootRoot, req.Spec, f, resolvedInputs, renderer)
		if werr != nil {
			return result, werr
		}
		if written {
			result.FilesWritten = append(result.FilesWritten, rel)
		}
		if rendered != "" {
			result.SlotsRendered = appendUnique(result.SlotsRendered, rendered)
		}
	}

	// Injections next, in declaration order.
	for i := range req.Spec.Injections {
		inj := req.Spec.Injections[i]
		if !scope.wantsInjection(inj) {
			continue
		}
		rel, written, rendered, werr := m.materializeInjection(ctx, bootRoot, req.Spec, inj, resolvedInputs, renderer)
		if werr != nil {
			return result, werr
		}
		if written {
			result.InjectionsWritten = append(result.InjectionsWritten, rel)
		}
		if rendered != "" {
			result.SlotsRendered = appendUnique(result.SlotsRendered, rendered)
		}
	}

	sort.Strings(result.FilesWritten)
	sort.Strings(result.InjectionsWritten)
	sort.Strings(result.SlotsRendered)
	return result, nil
}

// resolveInputs builds the effective input bag: a value supplied in
// req.Inputs wins, otherwise the BootInput.Default is used. This mirrors
// the locked Hadron-blueprint input-resolution order also used by
// AssemblySpec.Render. Unset required inputs are not an error here — the
// materializer renders absent values as empty; AssemblySpec.Render is the
// surface that enforces required-input collection.
func (m *DefaultMaterializer) resolveInputs(req MaterializeRequest) map[string]any {
	out := make(map[string]any, len(req.Spec.Inputs))
	for i := range req.Spec.Inputs {
		in := req.Spec.Inputs[i]
		if v, ok := req.Inputs[in.Name]; ok {
			out[in.Name] = v
			continue
		}
		if in.Default != nil {
			out[in.Name] = in.Default
		}
	}
	return out
}

// materializeFile resolves and writes one BootFileSpec. It returns the
// bootdir-relative path, whether the write changed the file, and the slot
// ref rendered (empty when the object was not a slot).
func (m *DefaultMaterializer) materializeFile(
	ctx context.Context,
	bootRoot string,
	spec *BootSpec,
	f BootFileSpec,
	inputs map[string]any,
	renderer ContractRenderer,
) (rel string, written bool, slotRendered string, err error) {
	content, slot, rerr := m.renderObject(ctx, spec, f.Object, inputs, renderer)
	if rerr != nil {
		return "", false, "", fmt.Errorf("agentlaunch: materialize file %q: %w", f.ID, rerr)
	}
	written, werr := m.writeSafe(bootRoot, f.RelPath, content, f.Mode)
	if werr != nil {
		return "", false, "", fmt.Errorf("agentlaunch: materialize file %q: %w", f.ID, werr)
	}
	return f.RelPath, written, slot, nil
}

// materializeInjection resolves and writes one BootInjectionSpec. Skill
// injections route to the provider-native skill directory derived from the
// BootSpec runtime binding; raw injections write verbatim at RelPath.
func (m *DefaultMaterializer) materializeInjection(
	ctx context.Context,
	bootRoot string,
	spec *BootSpec,
	inj BootInjectionSpec,
	inputs map[string]any,
	renderer ContractRenderer,
) (rel string, written bool, slotRendered string, err error) {
	content, slot, rerr := m.renderObject(ctx, spec, inj.Object, inputs, renderer)
	if rerr != nil {
		return "", false, "", fmt.Errorf("agentlaunch: materialize injection %q: %w", inj.ID, rerr)
	}
	relPath, perr := injectionRelPath(spec.Runtime, inj)
	if perr != nil {
		return "", false, "", fmt.Errorf("agentlaunch: materialize injection %q: %w", inj.ID, perr)
	}
	written, werr := m.writeSafe(bootRoot, relPath, content, inj.Mode)
	if werr != nil {
		return "", false, "", fmt.Errorf("agentlaunch: materialize injection %q: %w", inj.ID, werr)
	}
	return relPath, written, slot, nil
}

// renderObject resolves a ContractObject to its final byte content.
//
//   - literal: the Text field, library-resolved. Use this for opaque user
//     files placed verbatim — no var substitution is applied.
//   - input: projects one declared BootSpec input directly. An unknown
//     input is an error.
//   - var: projects one resolved BootSpec var directly. An unknown var (one
//     the spec does not even declare) is an error; a declared-but-absent
//     var renders as empty.
//   - slot: delegated to the consumer-pluggable ContractRenderer. This is
//     the seam where a harness shapes its CLAUDE.md / .mcp.json /
//     settings.json body with runtime-injected dynamic content. The
//     library does NOT own that content.
//
// The returned slotRef is the slot Ref when the object was a slot, empty
// otherwise — it feeds MaterializeResult.SlotsRendered.
func (m *DefaultMaterializer) renderObject(
	ctx context.Context,
	spec *BootSpec,
	obj ContractObject,
	inputs map[string]any,
	renderer ContractRenderer,
) (content string, slotRef string, err error) {
	switch obj.Kind {
	case ContractObjectLiteral:
		// Opaque content placed verbatim. No var substitution: the library
		// places user files exactly as authored.
		return obj.Text, "", nil

	case ContractObjectInput:
		if !specDeclaresInput(spec, obj.Ref) {
			return "", "", fmt.Errorf("%w: inputs.%s", ErrMaterializeUnknownInput, obj.Ref)
		}
		return stringifyValue(inputs[obj.Ref]), "", nil

	case ContractObjectVar:
		if !specDeclaresVar(spec, obj.Ref) {
			return "", "", fmt.Errorf("%w: vars.%s", ErrMaterializeUnknownVar, obj.Ref)
		}
		return stringifyValue(m.opts.Vars[obj.Ref]), "", nil

	case ContractObjectSlot:
		if renderer == nil {
			return "", "", fmt.Errorf("%w: slot %q", ErrMaterializeNoRenderer, obj.Ref)
		}
		body, rerr := renderer.RenderContractObject(ctx, ContractRenderRequest{
			Spec:   spec,
			Inputs: inputs,
			Vars:   m.opts.Vars,
			Object: obj,
		})
		if rerr != nil {
			return "", "", fmt.Errorf("slot %q render: %w", obj.Ref, rerr)
		}
		return body, obj.Ref, nil

	default:
		return "", "", fmt.Errorf("%w: %q", ErrContractObjectUnknownKind, obj.Kind)
	}
}

// writeSafe writes content at the bootdir-relative path rel inside
// bootRoot. It is the path-safe write loop the library owns:
//
//   - rel is re-validated through ValidateBootDirRelPath so a spec
//     assembled outside Validate still cannot escape the bootdir;
//   - the joined absolute path is verified to remain inside bootRoot even
//     after symlink-free cleaning, defending against any residual
//     traversal;
//   - the write is idempotent: an existing file with identical content and
//     mode is left untouched and reported as not-written, so a re-run
//     converges silently.
func (m *DefaultMaterializer) writeSafe(bootRoot, rel, content string, mode os.FileMode) (bool, error) {
	if err := ValidateBootDirRelPath(rel); err != nil {
		return false, fmt.Errorf("%w: %s: %v", ErrMaterializeUnsafePath, rel, err)
	}
	if mode == 0 {
		mode = defaultFileMode
	}

	target := filepath.Join(bootRoot, filepath.FromSlash(rel))
	// Defence in depth: after Join+Clean the target must still be inside
	// bootRoot. ValidateBootDirRelPath already rejects ".." segments, but
	// re-checking the cleaned absolute path closes the loop against any
	// platform-specific cleaning surprise.
	if target != bootRoot &&
		!strings.HasPrefix(target, bootRoot+string(os.PathSeparator)) {
		return false, fmt.Errorf("%w: %s", ErrMaterializeUnsafePath, rel)
	}

	if err := os.MkdirAll(filepath.Dir(target), m.opts.DirMode); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
	}

	// Idempotency: skip a write that would not change the file. This makes
	// Populate a converging re-run and keeps a crash-recovery re-plant from
	// touching mtimes needlessly.
	if existing, err := os.ReadFile(target); err == nil {
		if string(existing) == content {
			if info, serr := os.Stat(target); serr == nil && info.Mode().Perm() == mode.Perm() {
				return false, nil
			}
			// Content matches but mode drifted — reconcile the mode only.
			if cerr := os.Chmod(target, mode); cerr != nil {
				return false, fmt.Errorf("chmod %s: %w", target, cerr)
			}
			return true, nil
		}
	}

	if err := os.WriteFile(target, []byte(content), mode); err != nil {
		return false, fmt.Errorf("write %s: %w", target, err)
	}
	// os.WriteFile honors the mode only when it creates the file; an
	// overwrite leaves the prior mode. Reconcile explicitly so a re-plant
	// converges the mode too.
	if err := os.Chmod(target, mode); err != nil {
		return false, fmt.Errorf("chmod %s: %w", target, err)
	}
	return true, nil
}

// injectionRelPath resolves a BootInjectionSpec to its bootdir-relative
// write target.
//
//   - raw injections write verbatim at the declared RelPath;
//   - skill injections route to the provider-native skill directory keyed
//     off the BootSpec runtime binding's Provider, mirroring the
//     providerplant convention (.claude/skills, .opencode/skills, neutral
//     skills/ otherwise). The harness-specific path convention is a stable
//     library mechanism, not consumer content.
func injectionRelPath(runtime RuntimeBinding, inj BootInjectionSpec) (string, error) {
	switch inj.Kind {
	case NativeFileRaw:
		return inj.RelPath, nil
	case NativeFileSkill:
		return skillRelPath(runtime.Provider, inj.Name), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownNativeFileKind, inj.Kind)
	}
}

// skillRelPath maps a provider identity + skill name to the provider's
// native skill-document path. Providers with no native skill directory get
// a neutral skills/ directory for inspection parity. This mirrors the
// providerplant.nativeFileRelPath convention so a skill planted through
// the materializer lands where the harness expects it.
func skillRelPath(provider, name string) string {
	switch strings.ToLower(provider) {
	case "claude":
		return ".claude/skills/" + name + ".md"
	case "opencode":
		return ".opencode/skills/" + name + ".md"
	default:
		return "skills/" + name + ".md"
	}
}

// replantScope is the in-scope-object filter derived from a
// ReplantSelector. An empty selector reconciles every declared object; a
// non-empty selector narrows to the named files, injections, and slot
// refs.
type replantScope struct {
	all          bool
	fileIDs      map[string]struct{}
	injectionIDs map[string]struct{}
	slotRefs     map[string]struct{}
}

// newReplantScope builds a replantScope and validates every selector entry
// against the spec. A selector that names an object the spec does not
// declare is ErrMaterializeSelectorMiss — a replant that silently no-ops
// on a typo is worse than an error.
func newReplantScope(spec *BootSpec, sel ReplantSelector) (replantScope, error) {
	empty := len(sel.FileIDs) == 0 &&
		len(sel.InjectionIDs) == 0 &&
		len(sel.SlotRefs) == 0
	if empty {
		return replantScope{all: true}, nil
	}

	declaredFiles := make(map[string]struct{}, len(spec.Files))
	declaredSlots := make(map[string]struct{})
	for i := range spec.Files {
		declaredFiles[spec.Files[i].ID] = struct{}{}
		if spec.Files[i].Object.Kind == ContractObjectSlot {
			declaredSlots[spec.Files[i].Object.Ref] = struct{}{}
		}
	}
	declaredInjections := make(map[string]struct{}, len(spec.Injections))
	for i := range spec.Injections {
		declaredInjections[spec.Injections[i].ID] = struct{}{}
		if spec.Injections[i].Object.Kind == ContractObjectSlot {
			declaredSlots[spec.Injections[i].Object.Ref] = struct{}{}
		}
	}

	scope := replantScope{
		fileIDs:      make(map[string]struct{}, len(sel.FileIDs)),
		injectionIDs: make(map[string]struct{}, len(sel.InjectionIDs)),
		slotRefs:     make(map[string]struct{}, len(sel.SlotRefs)),
	}
	for _, id := range sel.FileIDs {
		if _, ok := declaredFiles[id]; !ok {
			return replantScope{}, fmt.Errorf("%w: file id %q", ErrMaterializeSelectorMiss, id)
		}
		scope.fileIDs[id] = struct{}{}
	}
	for _, id := range sel.InjectionIDs {
		if _, ok := declaredInjections[id]; !ok {
			return replantScope{}, fmt.Errorf("%w: injection id %q", ErrMaterializeSelectorMiss, id)
		}
		scope.injectionIDs[id] = struct{}{}
	}
	for _, ref := range sel.SlotRefs {
		if _, ok := declaredSlots[ref]; !ok {
			return replantScope{}, fmt.Errorf("%w: slot ref %q", ErrMaterializeSelectorMiss, ref)
		}
		scope.slotRefs[ref] = struct{}{}
	}
	return scope, nil
}

// wantsFile reports whether f is in scope for this reconcile. A file is in
// scope when the selector is empty, when its ID was selected, or when it
// is a slot object whose ref was selected.
func (s replantScope) wantsFile(f BootFileSpec) bool {
	if s.all {
		return true
	}
	if _, ok := s.fileIDs[f.ID]; ok {
		return true
	}
	if f.Object.Kind == ContractObjectSlot {
		if _, ok := s.slotRefs[f.Object.Ref]; ok {
			return true
		}
	}
	return false
}

// wantsInjection reports whether inj is in scope for this reconcile. An
// injection is in scope when the selector is empty, when its ID was
// selected, or when it is a slot object whose ref was selected.
func (s replantScope) wantsInjection(inj BootInjectionSpec) bool {
	if s.all {
		return true
	}
	if _, ok := s.injectionIDs[inj.ID]; ok {
		return true
	}
	if inj.Object.Kind == ContractObjectSlot {
		if _, ok := s.slotRefs[inj.Object.Ref]; ok {
			return true
		}
	}
	return false
}

// specDeclaresInput reports whether spec declares an input named name.
func specDeclaresInput(spec *BootSpec, name string) bool {
	for i := range spec.Inputs {
		if spec.Inputs[i].Name == name {
			return true
		}
	}
	return false
}

// specDeclaresVar reports whether spec declares a var named name.
func specDeclaresVar(spec *BootSpec, name string) bool {
	for i := range spec.Vars {
		if spec.Vars[i].Name == name {
			return true
		}
	}
	return false
}

// appendUnique appends s to xs only when xs does not already contain it.
func appendUnique(xs []string, s string) []string {
	if contains(xs, s) {
		return xs
	}
	return append(xs, s)
}
