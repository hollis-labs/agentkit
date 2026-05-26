package agentlaunch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedClock returns a deterministic clock for freshness tests.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// --- Sink → phase derivation -------------------------------------------

func TestVarSinkPhase(t *testing.T) {
	cases := []struct {
		sink VarSinkKind
		want MaterializationPhase
	}{
		{VarSinkContractFile, VarPhaseBuild},
		{VarSinkMCPConfig, VarPhaseBuild},
		{VarSinkPromptText, VarPhaseSessionStart},
		{VarSinkFirstTurn, VarPhaseSessionStart},
	}
	for _, c := range cases {
		if got := c.sink.Phase(); got != c.want {
			t.Errorf("%s.Phase() = %s, want %s", c.sink, got, c.want)
		}
		if !c.sink.Valid() {
			t.Errorf("%s.Valid() = false, want true", c.sink)
		}
	}
	if VarSinkKind("bogus").Valid() {
		t.Errorf("bogus sink reported Valid")
	}
}

func TestDerivePhaseFromSink(t *testing.T) {
	autoVar := VarSpec{Name: "v", Phase: VarPhaseAuto}

	if got := DerivePhase(autoVar, VarSinkContractFile); got != VarPhaseBuild {
		t.Errorf("contract-file sink: phase = %s, want build", got)
	}
	if got := DerivePhase(autoVar, VarSinkPromptText); got != VarPhaseSessionStart {
		t.Errorf("prompt-text sink: phase = %s, want session-start", got)
	}
	if got := DerivePhase(autoVar, VarSinkFirstTurn); got != VarPhaseSessionStart {
		t.Errorf("first-turn sink: phase = %s, want session-start", got)
	}
}

func TestDerivePhaseMCPConfigIsBuildNonNegotiable(t *testing.T) {
	// Even an explicit session-start declaration is overridden by the
	// MCP-config sink: MCP-config vars are build-time, non-negotiable.
	v := VarSpec{Name: "mcp", Phase: VarPhaseSessionStart}
	if got := DerivePhase(v, VarSinkMCPConfig); got != VarPhaseBuild {
		t.Fatalf("mcp-config sink: phase = %s, want build (non-negotiable)", got)
	}
	if !ReResolvesOnRestart(v, VarSinkMCPConfig) {
		t.Fatalf("mcp-config var should re-resolve on restart")
	}
}

func TestDerivePhaseExplicitOverrideHonoredForOtherSinks(t *testing.T) {
	// A non-auto explicit phase is a deliberate override for non-MCP sinks.
	v := VarSpec{Name: "v", Phase: VarPhaseBuild}
	if got := DerivePhase(v, VarSinkPromptText); got != VarPhaseBuild {
		t.Fatalf("explicit build override: phase = %s, want build", got)
	}
}

func TestReResolvesOnRestart(t *testing.T) {
	build := VarSpec{Name: "b", Phase: VarPhaseAuto}
	if !ReResolvesOnRestart(build, VarSinkContractFile) {
		t.Errorf("build-time var should re-resolve on restart")
	}
	if ReResolvesOnRestart(build, VarSinkPromptText) {
		t.Errorf("session-start var should NOT re-resolve on restart")
	}
}

// --- Source resolution: literal ----------------------------------------

func TestResolveLiteralSource(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "greeting",
		Source:    VarSource{Kind: VarSourceLiteral, Literal: "hello"},
		Freshness: VarFreshnessCacheOK,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rv.Value != "hello" {
		t.Fatalf("Value = %v, want hello", rv.Value)
	}
	if rv.Phase != VarPhaseSessionStart {
		t.Fatalf("Phase = %s, want session-start", rv.Phase)
	}
}

// --- Source resolution: file -------------------------------------------

func TestResolveFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "role.md")
	if err := os.WriteFile(path, []byte("  backend worker  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "role",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: path, TrimSpace: true}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkContractFile, true)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rv.Value != "backend worker" {
		t.Fatalf("Value = %q, want trimmed 'backend worker'", rv.Value)
	}
	if rv.Phase != VarPhaseBuild {
		t.Fatalf("Phase = %s, want build", rv.Phase)
	}
}

func TestResolveFileSourceMissingFileRequiredIsPermanent(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "role",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorWarn, // softer policy overridden by required
		Phase:     VarPhaseAuto,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, true)
	if err == nil {
		t.Fatal("expected error for missing required file")
	}
	if !IsPermanentVarError(err) {
		t.Fatalf("required unresolved var should be permanent: %v", err)
	}
}

// --- Source resolution: cmd --------------------------------------------

func TestResolveCmdSourceWithGate(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{
		Authorizer: AllowAllTrustAuthorizer{},
	})
	v := VarSpec{
		Name: "echoed",
		Source: VarSource{
			Kind: VarSourceCmd,
			Cmd: &VarCmdRef{
				Argv: []string{"echo", "hi-there"},
				Gate: TrustGate{Authorization: "shell-read"},
			},
		},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rv.Value != "hi-there" {
		t.Fatalf("Value = %q, want 'hi-there'", rv.Value)
	}
}

func TestResolveCmdSourceDeniedByGateIsPermanent(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{
		Authorizer: TrustAuthorizerFunc(func(context.Context, string, VarSource) (TrustDecision, error) {
			return TrustDecision{Allowed: false, Reason: "untrusted argv"}, nil
		}),
	})
	v := VarSpec{
		Name: "rm",
		Source: VarSource{
			Kind: VarSourceCmd,
			Cmd:  &VarCmdRef{Argv: []string{"echo", "x"}, Gate: TrustGate{Trust: "t"}},
		},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorWarn,
		Phase:     VarPhaseAuto,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err == nil {
		t.Fatal("expected gate-denial error")
	}
	if !errors.Is(err, ErrVarTrustGateDenied) {
		t.Fatalf("error = %v, want ErrVarTrustGateDenied", err)
	}
	if !IsPermanentVarError(err) {
		t.Fatalf("gate denial should be permanent: %v", err)
	}
}

func TestGatedSourceWithNoAuthorizerFailsClosed(t *testing.T) {
	// A missing authorizer must be fail-closed, not fail-open.
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name: "cmd",
		Source: VarSource{
			Kind: VarSourceCmd,
			Cmd:  &VarCmdRef{Argv: []string{"echo", "x"}, Gate: TrustGate{Trust: "t"}},
		},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorWarn,
		Phase:     VarPhaseAuto,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if !errors.Is(err, ErrVarTrustGateDenied) || !IsPermanentVarError(err) {
		t.Fatalf("missing authorizer should fail closed permanently: %v", err)
	}
}

// --- Source resolution: call(mcp/http) ---------------------------------

func TestResolveCallSourceWithGateAndResolver(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{
		Authorizer: AllowAllTrustAuthorizer{},
		CallResolver: CallResolverFunc(func(_ context.Context, ref VarCallRef) (any, error) {
			if ref.Target != "torque.task.get" {
				t.Errorf("unexpected target %q", ref.Target)
			}
			return "Implement var resolution", nil
		}),
	})
	v := VarSpec{
		Name: "task_title",
		Source: VarSource{
			Kind: VarSourceCall,
			Call: &VarCallRef{
				Transport: CallTransportMCP,
				Target:    "torque.task.get",
				Gate:      TrustGate{Authorization: "task-metadata-read"},
				Timeout:   "5s",
			},
		},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, true)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rv.Value != "Implement var resolution" {
		t.Fatalf("Value = %v", rv.Value)
	}
}

func TestResolveCallSourceNoResolverIsPermanent(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{Authorizer: AllowAllTrustAuthorizer{}})
	v := VarSpec{
		Name: "x",
		Source: VarSource{
			Kind: VarSourceCall,
			Call: &VarCallRef{Transport: CallTransportHTTP, Target: "https://x", Gate: TrustGate{Trust: "t"}},
		},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorWarn,
		Phase:     VarPhaseAuto,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if !IsPermanentVarError(err) {
		t.Fatalf("call source with no resolver should be permanent: %v", err)
	}
}

// --- Freshness / fallback / on_error -----------------------------------

func TestFreshnessCacheOKServesCachedValue(t *testing.T) {
	calls := 0
	vr := NewVarResolver(VarResolverOptions{
		Authorizer: AllowAllTrustAuthorizer{},
		CommandRunner: func(context.Context, VarCmdRef) (string, error) {
			calls++
			return "v", nil
		},
	})
	v := VarSpec{
		Name: "c",
		Source: VarSource{
			Kind: VarSourceCmd,
			Cmd:  &VarCmdRef{Argv: []string{"x"}, Gate: TrustGate{Trust: "t"}},
		},
		Freshness: VarFreshnessCacheOK,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	if _, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false); err != nil {
		t.Fatal(err)
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rv.FromCache {
		t.Fatalf("second cache_ok resolve should be served from cache")
	}
	if calls != 1 {
		t.Fatalf("source ran %d times, cache_ok should run it once", calls)
	}
}

func TestFreshnessFreshAlwaysReResolves(t *testing.T) {
	calls := 0
	vr := NewVarResolver(VarResolverOptions{
		Authorizer: AllowAllTrustAuthorizer{},
		CommandRunner: func(context.Context, VarCmdRef) (string, error) {
			calls++
			return "v", nil
		},
	})
	v := VarSpec{
		Name: "f",
		Source: VarSource{
			Kind: VarSourceCmd,
			Cmd:  &VarCmdRef{Argv: []string{"x"}, Gate: TrustGate{Trust: "t"}},
		},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	for i := 0; i < 3; i++ {
		if _, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("fresh source ran %d times, want 3", calls)
	}
}

func TestFreshnessBestEffortFallsBackToCacheOnFailure(t *testing.T) {
	calls := 0
	vr := NewVarResolver(VarResolverOptions{
		Authorizer: AllowAllTrustAuthorizer{},
		CommandRunner: func(context.Context, VarCmdRef) (string, error) {
			calls++
			if calls == 1 {
				return "first-value", nil
			}
			return "", errors.New("transport down")
		},
	})
	v := VarSpec{
		Name: "be",
		Source: VarSource{
			Kind: VarSourceCmd,
			Cmd:  &VarCmdRef{Argv: []string{"x"}, Gate: TrustGate{Trust: "t"}},
		},
		Freshness: VarFreshnessBestEffort,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	// Prime the cache with a good value.
	if _, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false); err != nil {
		t.Fatal(err)
	}
	// Live failure should degrade to the cached value.
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err != nil {
		t.Fatalf("best_effort should degrade to cache, got error %v", err)
	}
	if rv.Value != "first-value" || !rv.FromCache {
		t.Fatalf("best_effort degrade: Value=%v FromCache=%v", rv.Value, rv.FromCache)
	}
}

func TestOnErrorFallbackUsesFallbackValue(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "role",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorFallback,
		Fallback:  "default-role",
		Phase:     VarPhaseAuto,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rv.Value != "default-role" || !rv.FromFallback {
		t.Fatalf("fallback: Value=%v FromFallback=%v", rv.Value, rv.FromFallback)
	}
}

func TestOnErrorFallbackWithNoFallbackValueIsPermanent(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "role",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorFallback,
		Phase:     VarPhaseAuto,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if !errors.Is(err, ErrVarNoFallback) || !IsPermanentVarError(err) {
		t.Fatalf("fallback with no value should be permanent: %v", err)
	}
}

func TestOnErrorWarnDegradesWithoutAbort(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "opt",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorWarn,
		Phase:     VarPhaseAuto,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err != nil {
		t.Fatalf("on_error=warn should not return an error, got %v", err)
	}
	if rv.Warning == "" {
		t.Fatalf("on_error=warn should carry a warning")
	}
}

func TestOnErrorAbortIsTransientNotPermanent(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "opt",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err == nil {
		t.Fatal("on_error=abort should return an error")
	}
	if IsPermanentVarError(err) {
		t.Fatalf("on_error=abort on an OPTIONAL var should be transient, not permanent: %v", err)
	}
}

func TestOnErrorPermanentFailIsPermanent(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "opt",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorPermanentFail,
		Phase:     VarPhaseAuto,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if !IsPermanentVarError(err) {
		t.Fatalf("on_error=permanent-fail should be permanent: %v", err)
	}
}

// --- D6(b): required unresolved var → permanent, no retry --------------

func TestRequiredVarFailureIsPermanentRegardlessOfPolicy(t *testing.T) {
	// Even with the softest on_error policy, a REQUIRED var that does not
	// resolve is a permanent error so the consumer marks the task
	// blocked and burns no retry budget.
	for _, policy := range []VarOnError{VarOnErrorWarn, VarOnErrorAbort, VarOnErrorFallback} {
		vr := NewVarResolver(VarResolverOptions{})
		v := VarSpec{
			Name:      "required_role",
			Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
			Freshness: VarFreshnessFresh,
			OnError:   policy,
			Fallback:  "x",
			Phase:     VarPhaseAuto,
		}
		_, err := vr.Resolve(context.Background(), v, VarSinkPromptText, true)
		if !IsPermanentVarError(err) {
			t.Fatalf("required var with on_error=%s should be permanent: %v", policy, err)
		}
		var vre *VarResolutionError
		if !errors.As(err, &vre) {
			t.Fatalf("error should be *VarResolutionError: %v", err)
		}
		if !vre.Required || vre.Reason == "" {
			t.Fatalf("permanent error should carry Required + Reason: %+v", vre)
		}
	}
}

// --- D6(a): secret var classification + sink routing -------------------

func TestSecretVarRejectedFromBuildTimeSink(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "api_key",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/tmp/key"}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
		Secret:    true,
	}
	_, err := vr.Resolve(context.Background(), v, VarSinkContractFile, false)
	if !errors.Is(err, ErrVarSecretSink) {
		t.Fatalf("secret var to build-time sink should be rejected: %v", err)
	}
	if !IsPermanentVarError(err) {
		t.Fatalf("secret-sink violation should be permanent: %v", err)
	}
}

func TestSecretVarAllowedAtSessionStartAndRedacted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("sk-supersecret"), 0o600); err != nil {
		t.Fatal(err)
	}
	vr := NewVarResolver(VarResolverOptions{})
	v := VarSpec{
		Name:      "api_key",
		Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: path}},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
		Secret:    true,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, true)
	if err != nil {
		t.Fatalf("secret var at session-start should resolve: %v", err)
	}
	if rv.Value != "sk-supersecret" {
		t.Fatalf("Value = %v", rv.Value)
	}
	if !rv.Secret {
		t.Fatalf("ResolvedVar.Secret should mirror VarSpec.Secret")
	}
	if got := rv.Redacted().Value; got == "sk-supersecret" {
		t.Fatalf("Redacted() must not expose the secret value, got %v", got)
	}
}

// --- ResolveAll batch behavior -----------------------------------------

func TestResolveAllMixedSinks(t *testing.T) {
	dir := t.TempDir()
	rolePath := filepath.Join(dir, "role.md")
	if err := os.WriteFile(rolePath, []byte("backend"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &BootSpec{
		Vars: []VarSpec{
			{
				Name:      "role",
				Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: rolePath}},
				Freshness: VarFreshnessCacheOK,
				OnError:   VarOnErrorAbort,
				Phase:     VarPhaseAuto,
			},
			{
				Name:      "banner",
				Source:    VarSource{Kind: VarSourceLiteral, Literal: "welcome"},
				Freshness: VarFreshnessFresh,
				OnError:   VarOnErrorAbort,
				Phase:     VarPhaseAuto,
			},
		},
	}
	vr := NewVarResolver(VarResolverOptions{})
	sinks := map[string]VarSinkKind{
		"role":   VarSinkContractFile,
		"banner": VarSinkPromptText,
	}
	out, err := vr.ResolveAll(context.Background(), spec, sinks, map[string]bool{"role": true})
	if err != nil {
		t.Fatalf("ResolveAll() error = %v", err)
	}
	if out["role"].Phase != VarPhaseBuild {
		t.Errorf("role phase = %s, want build", out["role"].Phase)
	}
	if out["banner"].Phase != VarPhaseSessionStart {
		t.Errorf("banner phase = %s, want session-start", out["banner"].Phase)
	}
}

func TestResolveAllStopsAtPermanentFailure(t *testing.T) {
	spec := &BootSpec{
		Vars: []VarSpec{
			{
				Name:      "bad",
				Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "/no/such/file"}},
				Freshness: VarFreshnessFresh,
				OnError:   VarOnErrorWarn,
				Phase:     VarPhaseAuto,
			},
		},
	}
	vr := NewVarResolver(VarResolverOptions{})
	_, err := vr.ResolveAll(context.Background(), spec, nil, map[string]bool{"bad": true})
	if !IsPermanentVarError(err) {
		t.Fatalf("ResolveAll should surface the permanent failure: %v", err)
	}
}

func TestResolveAllNilSpec(t *testing.T) {
	vr := NewVarResolver(VarResolverOptions{})
	_, err := vr.ResolveAll(context.Background(), nil, nil, nil)
	if !errors.Is(err, ErrVarMissingResolveContext) {
		t.Fatalf("nil spec should return ErrVarMissingResolveContext: %v", err)
	}
}

// --- error classification ----------------------------------------------

func TestVarResolutionErrorIsComparable(t *testing.T) {
	base := errors.New("inner")
	perm := newPermanent("v", true, "blocked reason", base)
	if !errors.Is(perm, ErrVarPermanent) {
		t.Errorf("permanent error should match ErrVarPermanent")
	}
	if !errors.Is(perm, base) {
		t.Errorf("permanent error should still unwrap to its cause")
	}
	transient := &VarResolutionError{VarName: "v", Cause: base}
	if errors.Is(transient, ErrVarPermanent) {
		t.Errorf("transient error should NOT match ErrVarPermanent")
	}
	if !errors.Is(transient, base) {
		t.Errorf("transient error should unwrap to its cause")
	}
}

func TestClockOptionIsUsed(t *testing.T) {
	frozen := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	vr := NewVarResolver(VarResolverOptions{Clock: fixedClock(frozen)})
	v := VarSpec{
		Name:      "lit",
		Source:    VarSource{Kind: VarSourceLiteral, Literal: 1},
		Freshness: VarFreshnessFresh,
		OnError:   VarOnErrorAbort,
		Phase:     VarPhaseAuto,
	}
	rv, err := vr.Resolve(context.Background(), v, VarSinkPromptText, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rv.ResolvedAt.Equal(frozen) {
		t.Fatalf("ResolvedAt = %v, want frozen clock %v", rv.ResolvedAt, frozen)
	}
}
