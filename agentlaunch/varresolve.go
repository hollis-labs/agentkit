package agentlaunch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// S4.2 — Var resolution.
//
// This file is the resolver layer for the BootSpec.Vars surface. The
// frozen var contract types (VarSpec, VarSource, VarFreshness,
// VarOnError, TrustGate, ...) are owned by bootspec.go and shipped in
// S1; this file builds on them and adds NO new field shapes to those
// types. It is intentionally additive: a separate file so it does not
// collide with the parallel S4.1 Boot Assembly Spec work.
//
// What it provides:
//
//   - VarResolver: resolves each declared VarSpec from its source
//     (literal | file | call(mcp/http) | cmd).
//   - Sink-derived phase: a var that feeds a materialized contract file
//     (a build-time sink the harness reads at startup) resolves at
//     build; a var that feeds prompt / first-turn text resolves at
//     session-start. VarPhaseAuto on a VarSpec is collapsed to a
//     concrete phase by DerivePhase before resolution.
//   - Freshness / fallback / on_error handling per the locked spec.
//
// Locked constraints honored here (do not reopen):
//
//   - D6(a): secret-bearing vars are classifiable (VarSpec.Secret) and
//     their resolved value is flagged Secret in ResolvedVar so callers
//     keep it OUT of any persisted-at-rest blueprint.
//   - D6(b): autonomous dispatch has no TUI. An unresolved REQUIRED var
//     is a PERMANENT error — VarResolutionError.Permanent is true and
//     errors.Is(err, ErrVarPermanent) holds. The consumer marks the
//     task blocked with the carried reason; it MUST NOT retry.
//   - D6(c): call(mcp) / cmd sources pass through a trust/authorization
//     gate (TrustAuthorizer) before execution.

// Var-resolution sentinel errors. Kept local to this file so the S4.2
// surface does not collide with the parallel S4.1 edits to errors.go.
// All are errors.Is-comparable.
var (
	// ErrVarPermanent is the terminal classification for a var failure
	// that must NOT be retried. In autonomous dispatch (no TUI) an
	// unresolved REQUIRED var is permanent: the consumer marks the task
	// blocked with the carried reason and burns no retry budget. Every
	// VarResolutionError with Permanent=true wraps this sentinel.
	ErrVarPermanent = errors.New("agentlaunch: var resolution permanently failed")

	// ErrVarSourceUnsupported is returned when a var source kind has no
	// registered resolver (for example a call transport with no caller).
	ErrVarSourceUnsupported = errors.New("agentlaunch: var source has no resolver")

	// ErrVarTrustGateDenied is returned when the trust/authorization gate
	// (D6c) rejects a call/cmd var source before execution.
	ErrVarTrustGateDenied = errors.New("agentlaunch: var source denied by trust gate")

	// ErrVarSecretSink is returned when a secret-bearing var is routed to
	// a build-time persisted sink — secret values must not be persisted
	// at rest (D6a). Secret vars must resolve at session-start.
	ErrVarSecretSink = errors.New("agentlaunch: secret var may not feed a persisted build-time sink")

	// ErrVarUnresolved is the generic source-failure cause wrapped inside
	// a VarResolutionError when a source resolver returns an error.
	ErrVarUnresolved = errors.New("agentlaunch: var source failed to resolve")

	// ErrVarNoFallback is returned when on_error=fallback is selected but
	// the VarSpec carries no fallback value.
	ErrVarNoFallback = errors.New("agentlaunch: var on_error=fallback but no fallback value declared")

	// ErrVarMissingResolveContext is returned when ResolveAll is handed a
	// nil BootSpec.
	ErrVarMissingResolveContext = errors.New("agentlaunch: var resolution requires a non-nil BootSpec")
)

// VarSinkKind classifies where a resolved var is consumed. The sink — not
// the source — determines the materialization phase: a var that lands in
// a contract file the harness reads at process startup is build-time; a
// var that lands in prompt or first-turn text is session-start.
type VarSinkKind string

const (
	// VarSinkContractFile is a materialized bootdir contract file (or
	// native injection) the harness reads at startup. Build-time sink.
	VarSinkContractFile VarSinkKind = "contract-file"

	// VarSinkPromptText is system-prompt material rendered into the boot
	// prompt. Session-start sink.
	VarSinkPromptText VarSinkKind = "prompt-text"

	// VarSinkFirstTurn is first-turn / opening-message text injected when
	// the session starts. Session-start sink.
	VarSinkFirstTurn VarSinkKind = "first-turn"

	// VarSinkMCPConfig is MCP server configuration material. MCP-config
	// vars are build-time and non-negotiable: they are re-resolved on
	// resume/restart regardless of any phase declared on the VarSpec.
	VarSinkMCPConfig VarSinkKind = "mcp-config"
)

// Valid reports whether s is a known var sink kind.
func (s VarSinkKind) Valid() bool {
	switch s {
	case VarSinkContractFile, VarSinkPromptText, VarSinkFirstTurn, VarSinkMCPConfig:
		return true
	default:
		return false
	}
}

// Phase returns the materialization phase implied by the sink.
//
//   - contract-file and mcp-config are build-time: the harness reads them
//     at process startup, so the value must already be on disk.
//   - prompt-text and first-turn are session-start: they are part of the
//     conversation surface assembled when the session begins.
func (s VarSinkKind) Phase() MaterializationPhase {
	switch s {
	case VarSinkContractFile, VarSinkMCPConfig:
		return VarPhaseBuild
	case VarSinkPromptText, VarSinkFirstTurn:
		return VarPhaseSessionStart
	default:
		return VarPhaseAuto
	}
}

// forcesBuild reports whether the sink pins build-time regardless of any
// phase declared on the VarSpec. MCP-config vars are build-time,
// non-negotiable (re-resolved on resume/restart).
func (s VarSinkKind) forcesBuild() bool {
	return s == VarSinkMCPConfig
}

// DerivePhase collapses a VarSpec's declared phase against the sink the
// var feeds. The sink is authoritative for VarPhaseAuto, and the
// MCP-config sink overrides any explicitly declared phase (build-time,
// non-negotiable). For other sinks an explicit non-auto phase on the
// VarSpec is honored as a deliberate override.
func DerivePhase(spec VarSpec, sink VarSinkKind) MaterializationPhase {
	if sink.forcesBuild() {
		return VarPhaseBuild
	}
	if spec.Phase == VarPhaseBuild || spec.Phase == VarPhaseSessionStart {
		return spec.Phase
	}
	// VarPhaseAuto (or unset) — derive from the sink.
	if p := sink.Phase(); p != VarPhaseAuto {
		return p
	}
	// No sink mapping — fall back to session-start, the safe default
	// (resolved latest, never persisted earlier than necessary).
	return VarPhaseSessionStart
}

// ReResolvesOnRestart reports whether a var resolved into the given sink
// must be re-resolved when a session resumes or restarts. Build-time vars
// re-resolve on resume/restart; MCP-config vars always do.
func ReResolvesOnRestart(spec VarSpec, sink VarSinkKind) bool {
	return DerivePhase(spec, sink) == VarPhaseBuild
}

// TrustDecision is the verdict a TrustAuthorizer returns for a gated
// call/cmd var source.
type TrustDecision struct {
	// Allowed reports whether the source may execute.
	Allowed bool

	// Reason is a human-readable explanation, surfaced in the blocked
	// reason when a permanent failure is raised.
	Reason string
}

// TrustAuthorizer is the D6(c) authorization seam. Every call(mcp/http)
// and cmd var source is checked through Authorize before the resolver
// executes it. The gate carried on the VarSource (TrustGate) plus the
// VarSpec identity are handed to the authorizer; the library does not
// itself decide policy.
type TrustAuthorizer interface {
	Authorize(ctx context.Context, varName string, src VarSource) (TrustDecision, error)
}

// TrustAuthorizerFunc adapts a plain function to TrustAuthorizer.
type TrustAuthorizerFunc func(ctx context.Context, varName string, src VarSource) (TrustDecision, error)

// Authorize implements TrustAuthorizer.
func (f TrustAuthorizerFunc) Authorize(ctx context.Context, varName string, src VarSource) (TrustDecision, error) {
	return f(ctx, varName, src)
}

// AllowAllTrustAuthorizer permits every gated source. It exists for
// tests and trusted single-tenant embeddings; production embeddings
// should supply a real policy authorizer.
type AllowAllTrustAuthorizer struct{}

// Authorize implements TrustAuthorizer and always allows.
func (AllowAllTrustAuthorizer) Authorize(context.Context, string, VarSource) (TrustDecision, error) {
	return TrustDecision{Allowed: true}, nil
}

// CallResolver executes a call(mcp/http) var source and returns its raw
// resolved value. The library owns no transport: consumers supply the
// MCP/HTTP client through this seam. The authorization gate is applied
// by the VarResolver BEFORE this is invoked.
type CallResolver interface {
	ResolveCall(ctx context.Context, ref VarCallRef) (any, error)
}

// CallResolverFunc adapts a plain function to CallResolver.
type CallResolverFunc func(ctx context.Context, ref VarCallRef) (any, error)

// ResolveCall implements CallResolver.
func (f CallResolverFunc) ResolveCall(ctx context.Context, ref VarCallRef) (any, error) {
	return f(ctx, ref)
}

// VarResolverOptions configures a VarResolver.
type VarResolverOptions struct {
	// Authorizer gates call/cmd sources (D6c). When nil, every call/cmd
	// source is denied — a missing authorizer is fail-closed, not
	// fail-open.
	Authorizer TrustAuthorizer

	// CallResolver executes call(mcp/http) sources. When nil, call
	// sources resolve to ErrVarSourceUnsupported.
	CallResolver CallResolver

	// Clock supplies the current time; defaults to time.Now. Present so
	// freshness/caching is testable.
	Clock func() time.Time

	// CommandRunner executes cmd sources. When nil, a default os/exec
	// runner is used. Present so cmd resolution is testable.
	CommandRunner func(ctx context.Context, ref VarCmdRef) (string, error)
}

// varCacheEntry is one freshness-cache record.
type varCacheEntry struct {
	value any
	at    time.Time
}

// VarResolver resolves the derived-var layer of a BootSpec. It is
// constructed once per boot assembly and is safe to reuse. It carries an
// in-memory freshness cache consulted per the var's VarFreshness policy.
//
// The cache is intentionally process-local and resolver-scoped. A
// durable, cross-process last-known-good cache belongs to the S3.4
// degrading registrar and is out of scope for S4.2; this cache is what
// makes cache_ok / best_effort observably differ from fresh within a
// single boot assembly (and across resume/restart re-resolution when the
// same resolver instance is reused).
type VarResolver struct {
	opts  VarResolverOptions
	cache map[string]varCacheEntry
}

// NewVarResolver builds a VarResolver from the supplied options.
func NewVarResolver(opts VarResolverOptions) *VarResolver {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.CommandRunner == nil {
		opts.CommandRunner = defaultCommandRunner
	}
	return &VarResolver{opts: opts, cache: make(map[string]varCacheEntry)}
}

// ResolvedVar is the outcome of resolving one VarSpec.
type ResolvedVar struct {
	// Name is the var name (mirrors VarSpec.Name).
	Name string

	// Value is the resolved value. For a fallback outcome this is the
	// fallback value. Nil when resolution failed with no fallback.
	Value any

	// Phase is the concrete materialization phase the var resolved for,
	// derived from the sink via DerivePhase.
	Phase MaterializationPhase

	// Sink is the sink kind the var was resolved for.
	Sink VarSinkKind

	// Secret mirrors VarSpec.Secret. When true the value must NOT be
	// written into any persisted-at-rest blueprint (D6a).
	Secret bool

	// FromFallback reports whether Value came from the declared fallback
	// rather than the source.
	FromFallback bool

	// FromCache reports whether the value was served from the freshness
	// cache rather than freshly resolved.
	FromCache bool

	// Warning carries a non-fatal message when on_error=warn or
	// best-effort resolution degraded without a hard failure.
	Warning string

	// ResolvedAt is the clock time resolution completed.
	ResolvedAt time.Time
}

// Redacted returns a copy safe to log or persist: a secret var's Value is
// replaced with a redaction marker. Callers persisting a blueprint must
// route resolved vars through this (D6a).
func (r ResolvedVar) Redacted() ResolvedVar {
	if r.Secret {
		r.Value = "***redacted***"
	}
	return r
}

// VarResolutionError is the typed failure for one var. It carries the
// permanence classification the autonomous-dispatch consumer needs:
// Permanent=true means the task must be marked blocked with Reason and no
// retry must be attempted (D6b).
type VarResolutionError struct {
	// VarName is the var that failed.
	VarName string

	// Required mirrors whether the var was required at resolution time.
	Required bool

	// Permanent classifies the failure. True ⇒ do not retry; mark the
	// task blocked with Reason.
	Permanent bool

	// Reason is the human-readable blocked reason.
	Reason string

	// Cause is the underlying error.
	Cause error
}

// Error implements error.
func (e *VarResolutionError) Error() string {
	kind := "transient"
	if e.Permanent {
		kind = "permanent"
	}
	return fmt.Sprintf("agentlaunch: var %q resolution failed (%s): %s", e.VarName, kind, e.Reason)
}

// Unwrap exposes the cause and, for permanent failures, the
// ErrVarPermanent sentinel so errors.Is(err, ErrVarPermanent) holds.
func (e *VarResolutionError) Unwrap() error {
	if e.Permanent {
		return permanentChain{cause: e.Cause}
	}
	return e.Cause
}

// permanentChain lets a VarResolutionError satisfy errors.Is for BOTH
// ErrVarPermanent and the underlying cause.
type permanentChain struct{ cause error }

func (c permanentChain) Error() string {
	if c.cause != nil {
		return c.cause.Error()
	}
	return ErrVarPermanent.Error()
}

func (c permanentChain) Is(target error) bool { return target == ErrVarPermanent }

func (c permanentChain) Unwrap() error { return c.cause }

// IsPermanentVarError reports whether err is (or wraps) a permanent var
// resolution failure. Autonomous-dispatch consumers branch on this to
// decide block-vs-retry.
func IsPermanentVarError(err error) bool {
	return errors.Is(err, ErrVarPermanent)
}

// newPermanent builds a permanent VarResolutionError.
func newPermanent(name string, required bool, reason string, cause error) *VarResolutionError {
	return &VarResolutionError{
		VarName:   name,
		Required:  required,
		Permanent: true,
		Reason:    reason,
		Cause:     cause,
	}
}

// ResolveAll resolves every var in spec.Vars against the supplied sink
// map. sinks maps a var name to the VarSinkKind it feeds; a var absent
// from the map defaults to VarSinkPromptText (session-start, never
// persisted earlier than necessary). required marks var names that are
// required — an unresolved required var is a PERMANENT failure (D6b).
//
// Resolution stops at the first permanent failure and returns it; the
// partial result map is still returned so the caller can see what did
// resolve. Non-permanent (warn / best-effort) outcomes never abort the
// batch.
func (vr *VarResolver) ResolveAll(
	ctx context.Context,
	spec *BootSpec,
	sinks map[string]VarSinkKind,
	required map[string]bool,
) (map[string]ResolvedVar, error) {
	if spec == nil {
		return nil, ErrVarMissingResolveContext
	}
	out := make(map[string]ResolvedVar, len(spec.Vars))
	for i := range spec.Vars {
		v := spec.Vars[i]
		sink, ok := sinks[v.Name]
		if !ok || !sink.Valid() {
			sink = VarSinkPromptText
		}
		rv, err := vr.Resolve(ctx, v, sink, required[v.Name])
		if err != nil {
			return out, err
		}
		out[v.Name] = rv
	}
	return out, nil
}

// Resolve resolves a single VarSpec for the given sink. required marks
// whether the var is required: an unresolved required var becomes a
// permanent error regardless of the var's on_error policy (D6b).
func (vr *VarResolver) Resolve(
	ctx context.Context,
	spec VarSpec,
	sink VarSinkKind,
	required bool,
) (ResolvedVar, error) {
	phase := DerivePhase(spec, sink)

	rv := ResolvedVar{
		Name:       spec.Name,
		Phase:      phase,
		Sink:       sink,
		Secret:     spec.Secret,
		ResolvedAt: vr.opts.Clock(),
	}

	// D6(a): a secret-bearing var must never land in a persisted
	// build-time sink. Secret values are only safe in session-start
	// sinks, which are assembled in memory at session start.
	if spec.Secret && phase == VarPhaseBuild {
		return rv, newPermanent(spec.Name, required,
			"secret var routed to a persisted build-time sink; secret values must resolve at session-start",
			ErrVarSecretSink)
	}

	value, fromCache, srcErr := vr.resolveSource(ctx, spec, required)
	if srcErr == nil {
		rv.Value = value
		rv.FromCache = fromCache
		return rv, nil
	}

	// A permanent error from the source layer (trust-gate denial,
	// unsupported source) propagates as-is regardless of on_error.
	var permErr *VarResolutionError
	if errors.As(srcErr, &permErr) && permErr.Permanent {
		return rv, permErr
	}

	// D6(b): an unresolved REQUIRED var is permanent — no retry burn.
	// This overrides a softer on_error policy on the VarSpec.
	if required {
		return rv, newPermanent(spec.Name, true,
			fmt.Sprintf("required var %q could not be resolved: %v", spec.Name, srcErr),
			srcErr)
	}

	// Optional var: apply the declared on_error policy.
	switch spec.OnError {
	case VarOnErrorPermanentFail:
		return rv, newPermanent(spec.Name, required,
			fmt.Sprintf("var %q failed and on_error=permanent-fail: %v", spec.Name, srcErr),
			srcErr)

	case VarOnErrorAbort:
		// Abort: transient (retryable) failure for the batch.
		return rv, &VarResolutionError{
			VarName:   spec.Name,
			Required:  required,
			Permanent: false,
			Reason:    fmt.Sprintf("var %q failed and on_error=abort: %v", spec.Name, srcErr),
			Cause:     srcErr,
		}

	case VarOnErrorFallback:
		if spec.Fallback == nil {
			// Misconfiguration: fallback selected with no fallback
			// value. Treat as permanent — re-running won't help.
			return rv, newPermanent(spec.Name, required,
				fmt.Sprintf("var %q on_error=fallback but no fallback declared", spec.Name),
				ErrVarNoFallback)
		}
		rv.Value = spec.Fallback
		rv.FromFallback = true
		rv.Warning = fmt.Sprintf("var %q fell back after source failure: %v", spec.Name, srcErr)
		return rv, nil

	case VarOnErrorWarn:
		// Warn: degrade to a nil value, carry a warning, never abort.
		if spec.Fallback != nil {
			rv.Value = spec.Fallback
			rv.FromFallback = true
		}
		rv.Warning = fmt.Sprintf("var %q failed (on_error=warn): %v", spec.Name, srcErr)
		return rv, nil

	default:
		// Unknown policy — VarSpec.Validate should have caught it;
		// fail closed permanently rather than guess.
		return rv, newPermanent(spec.Name, required,
			fmt.Sprintf("var %q has unknown on_error policy %q", spec.Name, spec.OnError),
			srcErr)
	}
}

// resolveSource resolves a var honoring its VarFreshness policy. It
// returns the raw value, whether the value came from the freshness
// cache, and any error.
//
// Freshness semantics:
//
//   - fresh: always resolve live. The cache is never read; a fresh
//     success refreshes the cache. A live failure is a failure.
//   - cache_ok: a cached value is acceptable — if the cache holds an
//     entry it is returned without touching the source. Otherwise resolve
//     live and populate the cache.
//   - best_effort: resolve live; on a live FAILURE fall back to a cached
//     value if one exists (degrade rather than fail). A live success
//     refreshes the cache.
//
// A permanent source error (trust-gate denial, unsupported source) is
// never masked by the cache: it propagates regardless of freshness.
func (vr *VarResolver) resolveSource(ctx context.Context, spec VarSpec, required bool) (any, bool, error) {
	// Literal is always live and never cached — it carries no I/O.
	if spec.Source.Kind == VarSourceLiteral {
		return spec.Source.Literal, false, nil
	}

	cached, hasCached := vr.cache[spec.Name]

	if spec.Freshness == VarFreshnessCacheOK && hasCached {
		return cached.value, true, nil
	}

	value, _, err := vr.resolveLive(ctx, spec, required)
	if err == nil {
		vr.cache[spec.Name] = varCacheEntry{value: value, at: vr.opts.Clock()}
		return value, false, nil
	}

	// Permanent errors are never masked by cache fallback.
	var permErr *VarResolutionError
	if errors.As(err, &permErr) && permErr.Permanent {
		return nil, false, err
	}

	// best_effort: degrade to a cached value on a transient live failure.
	if spec.Freshness == VarFreshnessBestEffort && hasCached {
		return cached.value, true, nil
	}

	return nil, false, err
}

// resolveLive dispatches to the source-kind resolver without consulting
// the freshness cache.
func (vr *VarResolver) resolveLive(ctx context.Context, spec VarSpec, required bool) (any, bool, error) {
	switch spec.Source.Kind {
	case VarSourceLiteral:
		return spec.Source.Literal, false, nil

	case VarSourceFile:
		return vr.resolveFile(spec.Source.File)

	case VarSourceCall:
		return vr.resolveCall(ctx, spec, required)

	case VarSourceCmd:
		return vr.resolveCmd(ctx, spec, required)

	default:
		return nil, false, fmt.Errorf("%w: %q", ErrVarSourceUnsupported, spec.Source.Kind)
	}
}

// resolveFile reads a var value from a file.
func (vr *VarResolver) resolveFile(ref *VarFileRef) (any, bool, error) {
	if ref == nil || ref.Path == "" {
		return nil, false, fmt.Errorf("%w: file source has no path", ErrVarUnresolved)
	}
	raw, err := os.ReadFile(ref.Path)
	if err != nil {
		return nil, false, fmt.Errorf("%w: reading %s: %v", ErrVarUnresolved, ref.Path, err)
	}
	s := string(raw)
	if ref.TrimSpace {
		s = strings.TrimSpace(s)
	}
	return s, false, nil
}

// resolveCall resolves a call(mcp/http) var source. The trust gate (D6c)
// is enforced HERE, before the call executes.
func (vr *VarResolver) resolveCall(ctx context.Context, spec VarSpec, required bool) (any, bool, error) {
	ref := spec.Source.Call
	if ref == nil {
		return nil, false, fmt.Errorf("%w: call source is nil", ErrVarUnresolved)
	}
	if err := vr.gate(ctx, spec, required); err != nil {
		return nil, false, err
	}
	if vr.opts.CallResolver == nil {
		// No transport wired — permanent: re-running won't help.
		return nil, false, newPermanent(spec.Name, required,
			fmt.Sprintf("var %q is a call source but no CallResolver is configured", spec.Name),
			ErrVarSourceUnsupported)
	}
	cctx := ctx
	if ref.Timeout != "" {
		if d, err := time.ParseDuration(ref.Timeout); err == nil {
			var cancel context.CancelFunc
			cctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}
	val, err := vr.opts.CallResolver.ResolveCall(cctx, *ref)
	if err != nil {
		return nil, false, fmt.Errorf("%w: call %s: %v", ErrVarUnresolved, ref.Target, err)
	}
	return val, false, nil
}

// resolveCmd resolves a cmd var source. The trust gate (D6c) is enforced
// HERE, before the command executes.
func (vr *VarResolver) resolveCmd(ctx context.Context, spec VarSpec, required bool) (any, bool, error) {
	ref := spec.Source.Cmd
	if ref == nil || len(ref.Argv) == 0 {
		return nil, false, fmt.Errorf("%w: cmd source has no argv", ErrVarUnresolved)
	}
	if err := vr.gate(ctx, spec, required); err != nil {
		return nil, false, err
	}
	out, err := vr.opts.CommandRunner(ctx, *ref)
	if err != nil {
		return nil, false, fmt.Errorf("%w: cmd %s: %v", ErrVarUnresolved, strings.Join(ref.Argv, " "), err)
	}
	return out, false, nil
}

// gate runs the trust authorizer for a call/cmd source (D6c). A missing
// authorizer is fail-closed. A denial is a PERMANENT failure: an
// unauthorized source will not become authorized on retry.
func (vr *VarResolver) gate(ctx context.Context, spec VarSpec, required bool) error {
	if vr.opts.Authorizer == nil {
		return newPermanent(spec.Name, required,
			fmt.Sprintf("var %q has a gated source but no TrustAuthorizer is configured (fail-closed)", spec.Name),
			ErrVarTrustGateDenied)
	}
	decision, err := vr.opts.Authorizer.Authorize(ctx, spec.Name, spec.Source)
	if err != nil {
		return newPermanent(spec.Name, required,
			fmt.Sprintf("var %q trust authorization errored: %v", spec.Name, err),
			ErrVarTrustGateDenied)
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = "denied by trust gate"
		}
		return newPermanent(spec.Name, required,
			fmt.Sprintf("var %q denied by trust gate: %s", spec.Name, reason),
			ErrVarTrustGateDenied)
	}
	return nil
}

// defaultCommandRunner is the os/exec-backed cmd resolver. The command is
// run as argv (never shell-interpreted) so the trust gate authorizes the
// exact executable + args.
func defaultCommandRunner(ctx context.Context, ref VarCmdRef) (string, error) {
	cctx := ctx
	if ref.Timeout != "" {
		if d, err := time.ParseDuration(ref.Timeout); err == nil {
			var cancel context.CancelFunc
			cctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}
	//nolint:gosec // argv is operator-declared and trust-gated upstream.
	cmd := exec.CommandContext(cctx, ref.Argv[0], ref.Argv[1:]...)
	if ref.Workdir != "" {
		cmd.Dir = ref.Workdir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}
