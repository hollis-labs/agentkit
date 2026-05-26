package providerplant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/matrix"
)

// plantConfig holds the resolved Plant options.
type plantConfig struct {
	adapter  provider.BootDirProvider
	resolver AdapterResolver
}

// Option mutates a plantConfig.
type Option func(*plantConfig)

// WithAdapter pins the provider adapter Plant uses, bypassing resolver
// lookup entirely. Use this to plant a specific CLI variant (bare-mode
// Claude, a pinned codex mode) or a provider the matrix does not model.
//
// Note: native-file path resolution still consults the matrix for the
// launch's declared provider — pinning an adapter whose provider differs
// from the plan's is supported but means native skills land under the
// PLAN's provider convention, not the pinned adapter's.
func WithAdapter(a provider.BootDirProvider) Option {
	return func(c *plantConfig) { c.adapter = a }
}

// WithResolver overrides the AdapterResolver used when no WithAdapter is
// supplied. Defaults to DefaultResolver.
func WithResolver(r AdapterResolver) Option {
	return func(c *plantConfig) { c.resolver = r }
}

// Plant materializes provider-specific boot files into an already
// Prepared launch's bootdir.
//
// It resolves the go-providers adapter for the launch's provider×runtime
// pair, renders the adapter's BootDirSpec, and writes — in this fixed
// order — the provider files, then InjectionSpec.NativeFiles, then
// InjectionSpec.BootDirOverlay (see the package doc for the rationale).
//
// Plant then rewires the PreparedLaunch in place: BootDirSpec env
// amendments merge into Env, the project-dir arg appends to Argv, and
// Workdir is set to the spec's spawn cwd. The mutated PreparedLaunch is
// still valid per PreparedLaunch.Validate.
//
// Plant is idempotent in effect but not in mutation: calling it twice
// re-renders and re-writes the files and appends the project-dir arg a
// second time. Callers prepare-then-plant exactly once; PrepareAndPlant
// wraps that sequence.
func Plant(ctx context.Context, prepared *agentlaunch.PreparedLaunch, opts ...Option) error {
	_ = ctx // reserved for future cancellation; render funcs are synchronous

	if prepared == nil {
		return ErrNilPrepared
	}
	if err := prepared.Validate(); err != nil {
		return fmt.Errorf("agentlaunch/providerplant: %w", err)
	}
	compiled := prepared.Compiled
	if compiled == nil || compiled.Plan == nil {
		return ErrNilCompiled
	}
	plan := compiled.Plan

	cfg := plantConfig{resolver: DefaultResolver}
	for _, opt := range opts {
		opt(&cfg)
	}

	adapter := cfg.adapter
	if adapter == nil {
		if cfg.resolver == nil {
			cfg.resolver = DefaultResolver
		}
		resolved, err := cfg.resolver(compiled)
		if err != nil {
			return fmt.Errorf("agentlaunch/providerplant: %w", err)
		}
		adapter = resolved
	}
	if adapter == nil {
		return ErrAdapterResolution
	}
	spec := adapter.BootDirSpec()

	bootDir := prepared.PlantedBootDir
	// Capture the project root BEFORE applySpecToPrepared rewires Workdir
	// to the spawn cwd — PlantContext.ProjectDir and the --add-dir token
	// must point at the project, not the (possibly bootdir) spawn cwd.
	projectDir := prepared.Workdir
	plantCtx := PlantContextFor(prepared)

	// 1. Provider BootDirSpec files.
	for _, pf := range spec.PlantedFiles {
		if err := plantSpecFile(bootDir, pf, plantCtx); err != nil {
			return err
		}
	}

	// 2. Native extra files (provider-native skills, raw user files).
	renderer, err := rendererFor(plan)
	if err != nil {
		return fmt.Errorf("agentlaunch/providerplant: %w", err)
	}
	for i := range plan.Injection.NativeFiles {
		if err := plantNativeFile(bootDir, renderer, plan.Injection.NativeFiles[i]); err != nil {
			return err
		}
	}

	// 3. Injection overlay (flat path→content escape hatch; wins last).
	if err := plantOverlay(bootDir, plan.Injection.BootDirOverlay); err != nil {
		return err
	}

	// 4. Rewire env / argv / workdir from the spec.
	applySpecToPrepared(prepared, spec, bootDir, projectDir)
	return nil
}

// plantSpecFile renders and writes one provider BootDirSpec file. A nil
// Render is honored as "create the parent dir, write nothing" so a
// caller can supply the content via overlay/native file.
func plantSpecFile(bootDir string, pf provider.PlantedFile, pc provider.PlantContext) error {
	path := filepath.Join(bootDir, filepath.FromSlash(pf.RelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("agentlaunch/providerplant: plant %s: mkdir: %w", pf.RelPath, err)
	}
	if pf.Render == nil {
		return nil
	}
	content, err := pf.Render(pc)
	if err != nil {
		return fmt.Errorf("agentlaunch/providerplant: plant %s: render: %w", pf.RelPath, err)
	}
	if err := writeFile(path, content, pf.Mode); err != nil {
		return fmt.Errorf("agentlaunch/providerplant: plant %s: %w", pf.RelPath, err)
	}
	return nil
}

// plantNativeFile resolves a NativeFile to its provider-native bootdir
// path and writes it. The entry is re-validated here (defence in depth:
// a PreparedLaunch may have been assembled outside Compile/Validate).
func plantNativeFile(bootDir string, renderer matrix.BootDirRenderer, nf agentlaunch.NativeFile) error {
	if err := nf.Validate(); err != nil {
		return fmt.Errorf("agentlaunch/providerplant: native file: %w", err)
	}
	rel, err := nativeFileRelPath(renderer, nf)
	if err != nil {
		return fmt.Errorf("agentlaunch/providerplant: native file: %w", err)
	}
	path := filepath.Join(bootDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("agentlaunch/providerplant: native file %s: mkdir: %w", rel, err)
	}
	if err := writeFile(path, nf.Content, nf.Mode); err != nil {
		return fmt.Errorf("agentlaunch/providerplant: native file %s: %w", rel, err)
	}
	return nil
}

// nativeFileRelPath resolves a NativeFile to a bootdir-relative path
// (forward-slash form). Raw files use their RelPath verbatim; skill
// files map to the provider's native skill directory, falling back to a
// neutral skills/ dir for providers with no native convention.
func nativeFileRelPath(renderer matrix.BootDirRenderer, nf agentlaunch.NativeFile) (string, error) {
	switch nf.Kind {
	case agentlaunch.NativeFileRaw:
		return nf.RelPath, nil
	case agentlaunch.NativeFileSkill:
		switch renderer {
		case matrix.BootDirRendererClaude:
			return ".claude/skills/" + nf.ID + ".md", nil
		case matrix.BootDirRendererOpencode:
			return ".opencode/skills/" + nf.ID + ".md", nil
		default:
			return "skills/" + nf.ID + ".md", nil
		}
	default:
		return "", fmt.Errorf("%w: %q", agentlaunch.ErrUnknownNativeFileKind, nf.Kind)
	}
}

// plantOverlay writes the InjectionSpec.BootDirOverlay entries. Keys are
// re-validated and planted in sorted order for deterministic output.
func plantOverlay(bootDir string, overlay map[string]string) error {
	if len(overlay) == 0 {
		return nil
	}
	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := agentlaunch.ValidateBootDirRelPath(k); err != nil {
			return fmt.Errorf("agentlaunch/providerplant: overlay %q: %w", k, err)
		}
		path := filepath.Join(bootDir, filepath.FromSlash(k))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("agentlaunch/providerplant: overlay %q: mkdir: %w", k, err)
		}
		if err := writeFile(path, overlay[k], 0); err != nil {
			return fmt.Errorf("agentlaunch/providerplant: overlay %q: %w", k, err)
		}
	}
	return nil
}

// applySpecToPrepared folds the BootDirSpec's runtime-location outputs
// into the PreparedLaunch: env amendments merge into Env, the project-dir
// arg appends to Argv, and Workdir becomes the spec's spawn cwd.
func applySpecToPrepared(prepared *agentlaunch.PreparedLaunch, spec provider.BootDirSpec, bootDir, projectDir string) {
	if len(spec.EnvAmendments) > 0 && prepared.Env == nil {
		prepared.Env = make(map[string]string, len(spec.EnvAmendments))
	}
	for _, kv := range spec.EnvAmendments {
		kv = substituteTokens(kv, bootDir, projectDir)
		key, val, found := strings.Cut(kv, "=")
		if !found || key == "" {
			continue
		}
		prepared.Env[key] = val
	}
	// ProjectDirArg only fires when there is a project to point at —
	// matches the BootDirSpec convention go-agent-sessions follows.
	if projectDir != "" && strings.TrimSpace(spec.ProjectDirArg) != "" {
		arg := substituteTokens(spec.ProjectDirArg, bootDir, projectDir)
		prepared.Argv = append(prepared.Argv, strings.Fields(arg)...)
	}
	prepared.Workdir = spec.SpawnWorkdir(bootDir, projectDir)
}

// rendererFor returns the matrix BootDirRenderer for the plan's
// provider×runtime pair — used to resolve native-file paths.
func rendererFor(plan *agentlaunch.LaunchPlan) (matrix.BootDirRenderer, error) {
	desc, err := matrix.Lookup(plan.Provider, plan.Runtime)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAdapterResolution, err)
	}
	return desc.BootDirRenderer, nil
}

// substituteTokens replaces the {{.BootDir}} / {{.ProjectDir}} template
// tokens BootDirSpec env amendments and arg patterns may carry.
func substituteTokens(s, bootDir, projectDir string) string {
	s = strings.ReplaceAll(s, "{{.BootDir}}", bootDir)
	s = strings.ReplaceAll(s, "{{.ProjectDir}}", projectDir)
	return s
}

// writeFile writes content at path with mode (0 → 0o644). os.WriteFile
// only applies the mode on create, so an explicit Chmod follows to keep
// the final mode correct when an earlier write (e.g. a provider file)
// already created the path and a later write (overlay) reuses it.
func writeFile(path, content string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
