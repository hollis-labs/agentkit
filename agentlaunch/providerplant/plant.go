package providerplant

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/artifact"
	"github.com/hollis-labs/agentkit/materialize"
)

// plantConfig holds the resolved Plant options.
type plantConfig struct {
	adapter  provider.BootDirProvider
	resolver AdapterResolver
}

// Option mutates a plantConfig.
type Option func(*plantConfig)

// WithAdapter pins the provider adapter Plant uses, bypassing resolver
// lookup entirely. Use this to plant a specific CLI variant (bare-mode Claude,
// a pinned codex mode) or a provider the matrix does not model.
func WithAdapter(a provider.BootDirProvider) Option {
	return func(c *plantConfig) { c.adapter = a }
}

// WithResolver overrides the AdapterResolver used when no WithAdapter is
// supplied. Defaults to DefaultResolver.
func WithResolver(r AdapterResolver) Option {
	return func(c *plantConfig) { c.resolver = r }
}

// PrepareExecution projects provider files, caller injection artifacts and
// launch bindings for an already prepared launch. It performs the shared
// materialization step and returns the lossless prepared-execution handoff;
// Plant is the compatibility adapter that copies the same bindings back onto
// PreparedLaunch.
func PrepareExecution(ctx context.Context, prepared *agentlaunch.PreparedLaunch, opts ...Option) (*agentlaunch.PreparedExecution, error) {
	if prepared == nil {
		return nil, ErrNilPrepared
	}
	if err := prepared.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/providerplant: %w", err)
	}
	compiled := prepared.Compiled
	if compiled == nil || compiled.Plan == nil {
		return nil, ErrNilCompiled
	}
	cfg := plantConfig{resolver: DefaultResolver}
	for _, opt := range opts {
		opt(&cfg)
	}
	adapter, err := resolveAdapter(compiled, cfg)
	if err != nil {
		return nil, err
	}
	execution, err := buildPreparedExecution(ctx, prepared, adapter)
	if err != nil {
		return nil, err
	}
	return execution, nil
}

// Plant materializes provider-specific boot files into an already Prepared
// launch's bootdir through the shared artifact/materialize engine, then rewires
// the compatibility PreparedLaunch fields from the same prepared-execution
// bindings. Repeated calls recompute from the compiled launch and do not append
// duplicate argv or reinterpret the planted cwd as the project root.
func Plant(ctx context.Context, prepared *agentlaunch.PreparedLaunch, opts ...Option) error {
	execution, err := PrepareExecution(ctx, prepared, opts...)
	if err != nil {
		return err
	}
	prepared.Argv = append([]string(nil), execution.Bindings.Argv...)
	prepared.Env = envVarMap(execution.Bindings.Env)
	prepared.Workdir = execution.Bindings.CWD
	return nil
}

func resolveAdapter(compiled *agentlaunch.CompiledLaunch, cfg plantConfig) (provider.BootDirProvider, error) {
	adapter := cfg.adapter
	if adapter != nil {
		return adapter, nil
	}
	resolver := cfg.resolver
	if resolver == nil {
		resolver = DefaultResolver
	}
	resolved, err := resolver(compiled)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/providerplant: %w", err)
	}
	if resolved == nil {
		return nil, ErrAdapterResolution
	}
	return resolved, nil
}

func buildPreparedExecution(ctx context.Context, prepared *agentlaunch.PreparedLaunch, adapter provider.BootDirProvider) (*agentlaunch.PreparedExecution, error) {
	compiled := prepared.Compiled
	plan := compiled.Plan
	bootDir := prepared.PlantedBootDir
	projectDir := projectRootForPrepared(prepared)
	plantCtx := plantContextFor(prepared, projectDir)

	artifacts, projection, binding, err := projectArtifactsAndBinding(prepared, adapter, plantCtx, projectDir)
	if err != nil {
		return nil, err
	}
	artifacts, err = appendInjectionArtifacts(artifacts, projection.Provider, plan.Injection)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/providerplant: %w", err)
	}

	roots := agentlaunch.ExecutionRoots{
		ProjectRoot: projectDir,
		BootRoot:    bootDir,
		StateRoot:   prepared.WorkspaceDir,
		ScratchRoot: "",
		CWD:         binding.CWD,
	}
	handle, err := agentlaunch.MaterializeArtifacts(ctx, agentlaunch.ArtifactMaterializationRequest{
		TargetRoot: bootDir,
		Roots:      roots,
		Artifacts:  artifacts,
		Operation:  materialize.OperationReconcile,
		Generation: compiled.Provenance.PlanHash,
		Reconcile:  materialize.ReconcilePolicy{Conflict: materialize.ConflictOverwrite},
	})
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/providerplant: materialize: %w", err)
	}

	env := mergePreparedEnv(prepared.Env, binding.Env)
	execution := &agentlaunch.PreparedExecution{
		InputKind:       agentlaunch.PrepareInputArtifacts,
		Artifacts:       artifacts,
		Materialization: handle,
		Bindings: agentlaunch.ExecutionBindings{
			Argv:       finalArgv(prepared, projection, binding),
			Env:        env,
			CWD:        binding.CWD,
			ConfigRoot: binding.ConfigDir,
		},
		Roots:       roots,
		Access:      defaultAccessRequirements(roots),
		Effects:     append([]agentlaunch.RuntimeEffect(nil), projection.Effects...),
		Diagnostics: append([]agentlaunch.CapabilityDiagnostic(nil), projection.Diagnostics...),
		Legacy: agentlaunch.LegacyCompatibility{
			NativeFile:     len(plan.Injection.NativeFiles) > 0,
			BootDirOverlay: len(plan.Injection.BootDirOverlay) > 0,
		},
	}
	if err := execution.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/providerplant: %w", err)
	}
	return execution, nil
}

func projectArtifactsAndBinding(prepared *agentlaunch.PreparedLaunch, adapter provider.BootDirProvider, plantCtx provider.PlantContext, projectDir string) (artifact.Tree, agentlaunch.ProviderProjection, provider.LaunchBinding, error) {
	plan := prepared.Compiled.Plan
	bootDir := prepared.PlantedBootDir
	if pp, ok := adapter.(provider.ProjectionProvider); ok {
		proj, err := pp.ProviderProjection(plantCtx, provider.ProjectionOptions{Version: plan.Provider.Version})
		if err != nil {
			return artifact.Tree{}, agentlaunch.ProviderProjection{}, provider.LaunchBinding{}, fmt.Errorf("agentlaunch/providerplant: provider projection: %w", err)
		}
		binding, err := proj.ResolveLaunch(provider.ProjectionRoots{ProjectRoot: projectDir, BootRoot: bootDir, ConfigRoot: bootDir, StateRoot: prepared.WorkspaceDir, CWD: projectDir}, bootPromptArg(prepared))
		if err != nil {
			return artifact.Tree{}, agentlaunch.ProviderProjection{}, provider.LaunchBinding{}, fmt.Errorf("agentlaunch/providerplant: provider launch binding: %w", err)
		}
		binding.Argv = appendMissingProjectArg(binding.Argv, adapter.BootDirSpec().ProjectDirArg, bootDir, projectDir)
		translated := agentlaunch.ProviderProjectionFromProvider(proj)
		return translated.Artifacts, translated, binding, nil
	}
	return legacyProjection(prepared, adapter.BootDirSpec(), plantCtx, projectDir)
}

func legacyProjection(prepared *agentlaunch.PreparedLaunch, spec provider.BootDirSpec, plantCtx provider.PlantContext, projectDir string) (artifact.Tree, agentlaunch.ProviderProjection, provider.LaunchBinding, error) {
	entries := []artifact.Entry{}
	for _, pf := range spec.PlantedFiles {
		if pf.Render == nil {
			continue
		}
		content, err := pf.Render(plantCtx)
		if err != nil {
			return artifact.Tree{}, agentlaunch.ProviderProjection{}, provider.LaunchBinding{}, fmt.Errorf("agentlaunch/providerplant: plant %s: render: %w", pf.RelPath, err)
		}
		mode := pf.Mode
		if mode == 0 {
			mode = 0o644
		}
		entries = append(entries, artifact.Entry{Path: pf.RelPath, Kind: artifact.EntryFile, Mode: mode, Bytes: []byte(content), Ownership: artifact.Ownership{EntryID: "provider:legacy:" + pf.RelPath, GroupID: "provider:legacy"}, Provenance: artifact.Provenance{Source: "go-providers.bootdir"}})
	}
	entries, err := artifact.Normalize(entries)
	if err != nil {
		return artifact.Tree{}, agentlaunch.ProviderProjection{}, provider.LaunchBinding{}, err
	}
	binding := provider.LaunchBinding{
		CWD:       spec.SpawnWorkdir(prepared.PlantedBootDir, projectDir),
		ConfigDir: prepared.PlantedBootDir,
		Argv:      substituteArgPattern(spec.ProjectDirArg, prepared.PlantedBootDir, projectDir),
	}
	for _, kv := range spec.EnvAmendments {
		key, val, ok := strings.Cut(substituteTokens(kv, prepared.PlantedBootDir, projectDir), "=")
		if ok && key != "" {
			binding.Env = append(binding.Env, provider.EnvDelta{Name: key, Value: val, Operation: provider.EnvSet, Precedence: provider.EnvProviderWins})
		}
	}
	proj := agentlaunch.ProviderProjection{Provider: prepared.Compiled.Plan.Provider.ID, Runtime: prepared.Compiled.Plan.Runtime, Artifacts: artifact.Tree{Entries: entries}}
	return proj.Artifacts, proj, binding, nil
}

func appendInjectionArtifacts(base artifact.Tree, providerID string, inj agentlaunch.InjectionSpec) (artifact.Tree, error) {
	entries := append([]artifact.Entry(nil), base.Entries...)
	for _, nf := range inj.NativeFiles {
		if err := nf.Validate(); err != nil {
			return artifact.Tree{}, fmt.Errorf("native file: %w", err)
		}
		rel, err := nativeFileRelPathByProvider(providerID, nf)
		if err != nil {
			return artifact.Tree{}, fmt.Errorf("native file: %w", err)
		}
		mode := nf.Mode
		if mode == 0 {
			mode = 0o644
		}
		entries = upsertArtifact(entries, artifact.Entry{Path: rel, Kind: artifact.EntryFile, Mode: mode, Bytes: []byte(nf.Content), Ownership: artifact.Ownership{EntryID: "injection:native:" + rel, GroupID: "injection:native"}, Provenance: artifact.Provenance{Source: "agentlaunch.NativeFile"}})
	}
	keys := make([]string, 0, len(inj.BootDirOverlay))
	for k := range inj.BootDirOverlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := agentlaunch.ValidateBootDirRelPath(k); err != nil {
			return artifact.Tree{}, fmt.Errorf("overlay %q: %w", k, err)
		}
		entries = upsertArtifact(entries, artifact.Entry{Path: k, Kind: artifact.EntryFile, Mode: 0o644, Bytes: []byte(inj.BootDirOverlay[k]), Ownership: artifact.Ownership{EntryID: "injection:overlay:" + k, GroupID: "injection:overlay"}, Provenance: artifact.Provenance{Source: "agentlaunch.InjectionSpec", Note: "legacy content-only overlay defaults to 0644"}})
	}
	normalized, err := artifact.Normalize(entries)
	if err != nil {
		return artifact.Tree{}, err
	}
	base.Entries = normalized
	return base, nil
}

func upsertArtifact(entries []artifact.Entry, next artifact.Entry) []artifact.Entry {
	for i := range entries {
		if entries[i].Path == next.Path {
			entries[i] = next
			return entries
		}
	}
	return append(entries, next)
}

func finalArgv(prepared *agentlaunch.PreparedLaunch, projection agentlaunch.ProviderProjection, binding provider.LaunchBinding) []string {
	plan := prepared.Compiled.Plan
	binary := plan.Provider.Binary
	if binary == "" {
		binary = prepared.Compiled.ResolvedProviderBinary
	}
	if binary == "" {
		binary = projection.Provider
	}
	argv := make([]string, 0, 1+len(binding.Argv)+len(plan.Provider.Flags)+len(plan.Injection.Args))
	argv = append(argv, binary)
	argv = append(argv, binding.Argv...)
	argv = append(argv, plan.Provider.Flags...)
	argv = append(argv, plan.Injection.Args...)
	return argv
}

func mergePreparedEnv(base map[string]string, deltas []provider.EnvDelta) map[string]agentlaunch.EnvVar {
	out := make(map[string]agentlaunch.EnvVar, len(base)+len(deltas))
	for k, v := range base {
		out[k] = agentlaunch.EnvVar{Value: v, Source: "caller", Precedence: 10}
	}
	for _, d := range deltas {
		if d.Name == "" {
			continue
		}
		if d.Precedence == provider.EnvCallerWins {
			if _, ok := out[d.Name]; ok {
				continue
			}
		}
		cur := out[d.Name]
		sep := d.Separator
		if sep == "" {
			sep = string(os.PathListSeparator)
		}
		switch d.Operation {
		case provider.EnvUnset:
			delete(out, d.Name)
			continue
		case provider.EnvPrepend:
			if cur.Value != "" {
				cur.Value = d.Value + sep + cur.Value
			} else {
				cur.Value = d.Value
			}
		case provider.EnvAppend:
			if cur.Value != "" {
				cur.Value = cur.Value + sep + d.Value
			} else {
				cur.Value = d.Value
			}
		default:
			cur.Value = d.Value
		}
		cur.Source = "provider"
		cur.Precedence = 20
		out[d.Name] = cur
	}
	return out
}

func envVarMap(in map[string]agentlaunch.EnvVar) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v.Value
	}
	return out
}

func defaultAccessRequirements(roots agentlaunch.ExecutionRoots) agentlaunch.AccessRequirements {
	fs := []agentlaunch.AccessPath{}
	if roots.ProjectRoot != "" {
		fs = append(fs, agentlaunch.AccessPath{Mode: agentlaunch.AccessRead, Root: agentlaunch.RootProject, Path: "."})
	}
	if roots.BootRoot != "" {
		fs = append(fs, agentlaunch.AccessPath{Mode: agentlaunch.AccessRead, Root: agentlaunch.RootBoot, Path: "."})
	}
	if roots.StateRoot != "" {
		fs = append(fs, agentlaunch.AccessPath{Mode: agentlaunch.AccessWrite, Root: agentlaunch.RootState, Path: "."})
	}
	return agentlaunch.AccessRequirements{Mode: agentlaunch.AccessRequired, Host: agentlaunch.ExecutionHostLocal, Roots: roots, Filesystem: fs, Network: agentlaunch.NetworkAccess{Loopback: true}, Subprocess: agentlaunch.SubprocessAccess{Allowed: true}}
}

func plantContextFor(prepared *agentlaunch.PreparedLaunch, projectDir string) provider.PlantContext {
	pc := PlantContextFor(prepared)
	pc.ProjectDir = projectDir
	pc.BootDir = prepared.PlantedBootDir
	pc.LegacyAllowHostEffects = false
	return pc
}

func projectRootForPrepared(prepared *agentlaunch.PreparedLaunch) string {
	plan := prepared.Compiled.Plan
	if plan.Workspace.Workdir != "" {
		return plan.Workspace.Workdir
	}
	if plan.Project.Root != "" {
		return plan.Project.Root
	}
	return prepared.WorkspaceDir
}

func bootPromptArg(prepared *agentlaunch.PreparedLaunch) string {
	if prepared.BootContent != "" {
		return prepared.BootContent
	}
	return prepared.BootPrompt
}

func nativeFileRelPathByProvider(providerID string, nf agentlaunch.NativeFile) (string, error) {
	switch nf.Kind {
	case agentlaunch.NativeFileRaw:
		return nf.RelPath, nil
	case agentlaunch.NativeFileSkill:
		return skillRelPath(providerID, nf.ID), nil
	default:
		return "", fmt.Errorf("%w: %q", agentlaunch.ErrUnknownNativeFileKind, nf.Kind)
	}
}

func skillRelPath(providerID, name string) string {
	switch strings.ToLower(providerID) {
	case "claude":
		return ".claude/skills/" + name + ".md"
	case "opencode":
		return ".opencode/skills/" + name + ".md"
	default:
		return "skills/" + name + ".md"
	}
}

func substituteTokens(s, bootDir, projectDir string) string {
	s = strings.ReplaceAll(s, "{{.BootDir}}", bootDir)
	s = strings.ReplaceAll(s, "{{.ProjectDir}}", projectDir)
	return s
}

func appendMissingProjectArg(argv []string, pattern, bootDir, projectDir string) []string {
	parts := substituteArgPattern(pattern, bootDir, projectDir)
	if len(parts) == 0 {
		return argv
	}
	if len(parts) >= 1 {
		for _, arg := range argv {
			if arg == parts[0] {
				return argv
			}
		}
	}
	out := append([]string(nil), argv...)
	return append(out, parts...)
}

func substituteArgPattern(pattern, bootDir, projectDir string) []string {
	if pattern == "" || projectDir == "" {
		return nil
	}
	parts := splitArgPattern(pattern)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ReplaceAll(part, "{{.BootDir}}", bootDir)
		part = strings.ReplaceAll(part, "{{.ProjectDir}}", projectDir)
		out = append(out, part)
	}
	return out
}

func splitArgPattern(pattern string) []string {
	var (
		out      []string
		current  strings.Builder
		quote    rune
		escaping bool
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		out = append(out, current.String())
		current.Reset()
	}
	for _, r := range pattern {
		if escaping {
			current.WriteRune(r)
			escaping = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaping = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaping {
		current.WriteRune('\\')
	}
	flush()
	return out
}
