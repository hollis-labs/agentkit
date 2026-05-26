package agentlaunch

import (
	"errors"
	"path/filepath"
	"testing"
)

// flakyRegistrar is a test Registrar that wraps a real InMemoryRegistrar
// but can be flipped "down" to simulate an unreachable directory. While
// down every Handle call returns errDown regardless of operation.
type flakyRegistrar struct {
	inner *InMemoryRegistrar
	down  bool
	calls int
}

var errDown = errors.New("test: directory unreachable")

func (f *flakyRegistrar) Handle(env RegistryEnvelope) (RegistryResponse, error) {
	f.calls++
	if f.down {
		return RegistryResponse{}, errDown
	}
	return f.inner.Handle(env)
}

// seededFlaky returns a flakyRegistrar pre-loaded with the given records
// and a degrading wrapper around it. The wrapper starts healthy.
func seededFlaky(t *testing.T, records ...RegistrationRecord) (*flakyRegistrar, *DegradingRegistrar) {
	t.Helper()
	inner := NewInMemoryRegistrar()
	for _, rec := range records {
		if _, err := inner.Handle(registerEnvelope(rec, false)); err != nil {
			t.Fatalf("seed register %s: %v", rec.Meta.Ref.Name, err)
		}
	}
	flaky := &flakyRegistrar{inner: inner}
	return flaky, NewDegradingRegistrar(flaky, NewLastKnownGoodCache())
}

func TestDegradingCachePopulateOnSuccess(t *testing.T) {
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	flaky, d := seededFlaky(t, rec)

	resp, err := d.Handle(queryEnvelope(QueryPayload{}))
	if err != nil {
		t.Fatalf("live query: unexpected error %v", err)
	}
	if len(resp.Records) != 1 || resp.Records[0].Meta.Ref != rec.Meta.Ref {
		t.Fatalf("live query: got %+v, want the one record", resp.Records)
	}
	if d.Cache().Len() != 1 {
		t.Fatalf("cache Len() = %d, want 1 (success should populate)", d.Cache().Len())
	}
	if d.Status().Degraded {
		t.Fatalf("Status().Degraded = true after healthy query, want false")
	}
	if flaky.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", flaky.calls)
	}
}

func TestDegradingFallbackToCacheOnInnerFailure(t *testing.T) {
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	flaky, d := seededFlaky(t, rec)

	// Prime the last-known-good cache with one successful query.
	if _, err := d.Handle(queryEnvelope(QueryPayload{})); err != nil {
		t.Fatalf("prime query: %v", err)
	}

	// Directory goes down. The identical query must still resolve from
	// the last-known-good cache rather than hard-failing.
	flaky.down = true
	resp, err := d.Handle(queryEnvelope(QueryPayload{}))
	if err != nil {
		t.Fatalf("degraded query: got error %v, want cache fallback (no error)", err)
	}
	if len(resp.Records) != 1 || resp.Records[0].Meta.Ref != rec.Meta.Ref {
		t.Fatalf("degraded query: got %+v, want the cached record", resp.Records)
	}
	if !d.Status().Degraded {
		t.Fatalf("Status().Degraded = false after cache fallback, want true")
	}
}

func TestDegradingQueryCacheMissWhenNeverCached(t *testing.T) {
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	flaky, d := seededFlaky(t, rec)

	// Never primed a query and the directory is down. The wrapper has no
	// last-known-good entry, so it surfaces ErrRegistryCacheMiss.
	flaky.down = true
	_, err := d.Handle(queryEnvelope(QueryPayload{Name: "uncached"}))
	if !errors.Is(err, ErrRegistryCacheMiss) {
		t.Fatalf("uncached degraded query: err = %v, want ErrRegistryCacheMiss", err)
	}
	// The underlying inner error is still inspectable.
	if !errors.Is(err, errDown) && err.Error() == "" {
		t.Fatalf("cache miss error should reference the inner failure, got %v", err)
	}
}

func TestDegradingCacheKeyDistinguishesQueries(t *testing.T) {
	a := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	b := testRecord(RegistryKindMCPServer, "torque", "hollis", "prod", MCPServerInterfaceV1, nil)
	flaky, d := seededFlaky(t, a, b)

	// Prime two distinct queries.
	if _, err := d.Handle(queryEnvelope(QueryPayload{Kinds: []RegistryKind{RegistryKindAgentSource}})); err != nil {
		t.Fatalf("prime agent query: %v", err)
	}
	if _, err := d.Handle(queryEnvelope(QueryPayload{Kinds: []RegistryKind{RegistryKindMCPServer}})); err != nil {
		t.Fatalf("prime mcp query: %v", err)
	}
	if d.Cache().Len() != 2 {
		t.Fatalf("cache Len() = %d, want 2 distinct query snapshots", d.Cache().Len())
	}

	flaky.down = true
	resp, err := d.Handle(queryEnvelope(QueryPayload{Kinds: []RegistryKind{RegistryKindMCPServer}}))
	if err != nil {
		t.Fatalf("degraded mcp query: %v", err)
	}
	if len(resp.Records) != 1 || resp.Records[0].Meta.Ref.Kind != RegistryKindMCPServer {
		t.Fatalf("degraded mcp query served wrong snapshot: %+v", resp.Records)
	}
}

func TestDegradingHealthOkVsDegraded(t *testing.T) {
	healthEnv := RegistryEnvelope{
		Version:    "v1alpha1",
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationHealth,
		Registrar:  fileBackedRegistrar(),
		Health:     &HealthPayload{},
	}

	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	flaky, d := seededFlaky(t, rec)

	// Healthy: inner reachable => ok, DirectoryReachable true.
	resp, err := d.Handle(healthEnv)
	if err != nil {
		t.Fatalf("healthy health: %v", err)
	}
	if resp.Health == nil || resp.Health.Status != HealthStatusOK {
		t.Fatalf("healthy health: status = %v, want ok", resp.Health)
	}
	if !resp.Health.DirectoryReachable {
		t.Fatalf("healthy health: DirectoryReachable = false, want true")
	}

	// Degraded: inner down => degraded (NOT down, NOT a hard error).
	flaky.down = true
	resp, err = d.Handle(healthEnv)
	if err != nil {
		t.Fatalf("degraded health: got error %v, want degraded report", err)
	}
	if resp.Health == nil {
		t.Fatalf("degraded health: nil payload")
	}
	if resp.Health.Status != HealthStatusDegraded {
		t.Fatalf("degraded health: status = %q, want degraded", resp.Health.Status)
	}
	if resp.Health.DirectoryReachable {
		t.Fatalf("degraded health: DirectoryReachable = true, want false")
	}
	if resp.Health.Detail == "" {
		t.Fatalf("degraded health: empty Detail, want a degradation reason")
	}
}

func TestDegradingObservabilityHookDegradeAndRecover(t *testing.T) {
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	inner := NewInMemoryRegistrar()
	if _, err := inner.Handle(registerEnvelope(rec, false)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	flaky := &flakyRegistrar{inner: inner}

	var events []string
	d := NewDegradingRegistrar(flaky, NewLastKnownGoodCache(),
		WithDegradeHook(func(reason string) { events = append(events, reason) }))

	// Prime cache while healthy — no event.
	if _, err := d.Handle(queryEnvelope(QueryPayload{})); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("hook fired on healthy query: %v", events)
	}

	// Go down, query twice: the hook fires exactly once on the edge.
	flaky.down = true
	for i := 0; i < 2; i++ {
		if _, err := d.Handle(queryEnvelope(QueryPayload{})); err != nil {
			t.Fatalf("degraded query %d: %v", i, err)
		}
	}
	if len(events) != 1 {
		t.Fatalf("degrade events = %v, want exactly 1 (edge-triggered)", events)
	}
	if got := events[0]; len(got) < 9 || got[:9] != "degraded:" {
		t.Fatalf("degrade event = %q, want a 'degraded:' message", got)
	}

	// Recover: the hook fires once for the degraded->healthy edge.
	flaky.down = false
	if _, err := d.Handle(queryEnvelope(QueryPayload{})); err != nil {
		t.Fatalf("recovery query: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events after recovery = %v, want 2", events)
	}
	if got := events[1]; len(got) < 10 || got[:10] != "recovered:" {
		t.Fatalf("recovery event = %q, want a 'recovered:' message", got)
	}
	if d.Status().Degraded {
		t.Fatalf("Status().Degraded = true after recovery, want false")
	}
}

func TestDegradingWriteWhileDownErrors(t *testing.T) {
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	flaky, d := seededFlaky(t)

	// Directory is down: a write cannot be served from cache. The wrapper
	// surfaces ErrRegistryWriteWhileDegraded wrapping the inner failure.
	flaky.down = true

	_, err := d.Handle(registerEnvelope(rec, false))
	if !errors.Is(err, ErrRegistryWriteWhileDegraded) {
		t.Fatalf("register while down: err = %v, want ErrRegistryWriteWhileDegraded", err)
	}
	if !errors.Is(err, errDown) {
		t.Fatalf("register while down: err = %v, want it to wrap the inner failure", err)
	}

	_, err = d.Handle(deregisterEnvelope(rec.Meta.Ref))
	if !errors.Is(err, ErrRegistryWriteWhileDegraded) {
		t.Fatalf("deregister while down: err = %v, want ErrRegistryWriteWhileDegraded", err)
	}
}

func TestDegradingWriteSemanticErrorPassesThrough(t *testing.T) {
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	flaky, d := seededFlaky(t, rec)

	// Inner is UP. A duplicate register is a semantic rejection from a
	// reachable registrar — it must pass through unchanged and must not
	// flip the wrapper into degraded.
	_, err := d.Handle(registerEnvelope(rec, false))
	if !errors.Is(err, ErrRegistryDuplicateObject) {
		t.Fatalf("duplicate register: err = %v, want ErrRegistryDuplicateObject passthrough", err)
	}
	if errors.Is(err, ErrRegistryWriteWhileDegraded) {
		t.Fatalf("duplicate register misreported as degradation: %v", err)
	}
	if d.Status().Degraded {
		t.Fatalf("Status().Degraded = true after a semantic write rejection, want false")
	}
	_ = flaky
}

func TestDegradingWriteSucceedsWhenHealthy(t *testing.T) {
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	_, d := seededFlaky(t)

	resp, err := d.Handle(registerEnvelope(rec, false))
	if err != nil {
		t.Fatalf("healthy register: %v", err)
	}
	if resp.Operation != RegistryOperationRegister || resp.Ref == nil {
		t.Fatalf("healthy register: got %+v", resp)
	}
}

func TestDegradingInvalidEnvelopeIsCallerErrorNotDegradation(t *testing.T) {
	_, d := seededFlaky(t)
	// Missing version: a malformed envelope must fail fast as a caller
	// error and must not flip the wrapper into degraded.
	bad := RegistryEnvelope{
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationQuery,
		Registrar:  fileBackedRegistrar(),
		Query:      &QueryPayload{},
	}
	_, err := d.Handle(bad)
	if !errors.Is(err, ErrRegistryMissingSchemaVersion) {
		t.Fatalf("bad envelope: err = %v, want ErrRegistryMissingSchemaVersion", err)
	}
	if d.Status().Degraded {
		t.Fatalf("Status().Degraded = true after a malformed envelope, want false")
	}
}

func TestLastKnownGoodCachePersistenceSaveReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lkg-cache.json")
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)

	// First process: prime a query through a persistent cache.
	inner := NewInMemoryRegistrar()
	if _, err := inner.Handle(registerEnvelope(rec, false)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	flaky := &flakyRegistrar{inner: inner}
	d1 := NewDegradingRegistrar(flaky, NewLastKnownGoodCache(WithCachePersistence(path)))
	if _, err := d1.Handle(queryEnvelope(QueryPayload{})); err != nil {
		t.Fatalf("prime persistent query: %v", err)
	}

	// Second process: a brand-new cache loading the same file restores the
	// last-known-good snapshot, and a degraded query resolves from it.
	reloaded := NewLastKnownGoodCache(WithCachePersistence(path))
	if reloaded.Len() != 1 {
		t.Fatalf("reloaded cache Len() = %d, want 1 (snapshot should survive restart)", reloaded.Len())
	}
	got, ok := reloaded.Lookup(QueryPayload{})
	if !ok {
		t.Fatalf("reloaded cache: empty-query snapshot missing")
	}
	if len(got) != 1 || got[0].Meta.Ref != rec.Meta.Ref {
		t.Fatalf("reloaded cache: got %+v, want the persisted record", got)
	}

	downInner := &flakyRegistrar{inner: NewInMemoryRegistrar(), down: true}
	d2 := NewDegradingRegistrar(downInner, reloaded)
	resp, err := d2.Handle(queryEnvelope(QueryPayload{}))
	if err != nil {
		t.Fatalf("degraded query against reloaded cache: %v", err)
	}
	if len(resp.Records) != 1 || resp.Records[0].Meta.Ref != rec.Meta.Ref {
		t.Fatalf("degraded query against reloaded cache: got %+v", resp.Records)
	}
}

func TestLastKnownGoodCacheMissingFileIsNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	c := NewLastKnownGoodCache(WithCachePersistence(path))
	if c.Len() != 0 {
		t.Fatalf("fresh cache with missing file Len() = %d, want 0", c.Len())
	}
}
