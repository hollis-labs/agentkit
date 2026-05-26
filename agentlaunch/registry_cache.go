package agentlaunch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// registry_cache.go ships S3.4: the consumer-side last-known-good cache
// and the degrading Registrar wrapper that fronts a live registrar.
//
// This file owns deferred Risk 1: the directory registry must not be a
// launch-path single point of failure. S3.1 already made the registrar
// optional in principle (D1); S3.4 builds the concrete operational
// degradation path so that when the live directory/registrar is
// unreachable, a query resolves from a local last-known-good cache
// instead of hard-failing.
//
// Design constraints honored here:
//
//   - D1 (local-first; the registry is NEVER mandatory on the launch hot
//     path). The cache and the fallback path are purely local: an
//     in-memory map plus, optionally, one local JSON file. The degrading
//     wrapper never lets a launch-side read (query) hard-fail solely
//     because the directory is unreachable — it returns the
//     last-known-good snapshot. The only way a query through the wrapper
//     errors is ErrRegistryCacheMiss, i.e. the inner registrar is down
//     AND we genuinely never observed a successful answer for that
//     query; that is an honest cache miss, not a directory dependency.
//
//   - D2 (the directory holds resolver handles, NOT content). The cache
//     snapshots []RegistrationRecord values only — a stable handle
//     (RegistryContractMeta) plus a local file pointer
//     (RegistrationSource). There is deliberately no field anywhere in
//     this file that snapshots a resolved profile / prompt / tool body.
//     The cache mirrors exactly what the registry itself stores.
//
// The policy stance mirrors the VarSpec on_error / best_effort / fallback
// vocabulary in bootspec.go: a degraded read prefers a stale-but-usable
// answer over a hard failure, and the degradation is always observable.

// cacheKey is the deterministic identity of one query result in the
// last-known-good cache. Two QueryPayloads that select the same record
// set must produce the same key so a later identical query is a cache
// hit. The key is derived purely from the query facets.
type cacheKey string

// queryCacheKey derives the deterministic cacheKey for a QueryPayload.
// Every facet that narrows handleQuery's result set contributes; the
// IncludeLocal flag does not (it does not narrow the in-memory result
// set — see queryMatches). Slices and maps are sorted so key derivation
// is order-independent.
func queryCacheKey(q QueryPayload) cacheKey {
	kinds := make([]string, len(q.Kinds))
	for i, k := range q.Kinds {
		kinds[i] = string(k)
	}
	sort.Strings(kinds)

	labels := make([]string, 0, len(q.Labels))
	for k, v := range q.Labels {
		labels = append(labels, k+"="+v)
	}
	sort.Strings(labels)

	var b strings.Builder
	b.WriteString("kinds=")
	b.WriteString(strings.Join(kinds, ","))
	b.WriteString("&owner=")
	b.WriteString(q.Owner)
	b.WriteString("&ns=")
	b.WriteString(q.Namespace)
	b.WriteString("&name=")
	b.WriteString(q.Name)
	b.WriteString("&iface=")
	b.WriteString(q.Interface)
	b.WriteString("&labels=")
	b.WriteString(strings.Join(labels, ","))
	return cacheKey(b.String())
}

// cacheEntry is one last-known-good snapshot: the records a successful
// query returned plus when the snapshot was taken. It stores
// RegistrationRecord values only (D2 — handles, never content).
type cacheEntry struct {
	Key      string               `json:"key"`
	Records  []RegistrationRecord `json:"records"`
	CachedAt string               `json:"cached_at"`
}

// LastKnownGoodCache is a consumer-side cache of successful registry
// query results. Each successful query through the degrading wrapper
// populates or refreshes the entry for that query's key; a later
// identical query can be served from here when the live registrar is
// unreachable.
//
// It is safe for concurrent use. It performs no I/O unless a persistence
// path was configured (see WithCachePersistence), in which case the only
// I/O is reading/writing one local JSON file.
type LastKnownGoodCache struct {
	mu      sync.RWMutex
	entries map[cacheKey]cacheEntry
	path    string // empty => in-memory only, no persistence
}

// CacheOption configures a LastKnownGoodCache at construction time.
type CacheOption func(*LastKnownGoodCache)

// WithCachePersistence makes the cache durable across process restarts
// by mirroring it to a single local JSON file at path. This is the only
// I/O the cache ever performs and it is strictly local. The file is
// loaded (best-effort) at construction and rewritten after every
// successful refresh. A missing or unreadable file is not fatal: the
// cache simply starts empty.
func WithCachePersistence(path string) CacheOption {
	return func(c *LastKnownGoodCache) {
		c.path = path
	}
}

// NewLastKnownGoodCache constructs an empty last-known-good cache. With
// WithCachePersistence it eagerly loads any existing snapshot file;
// without it the cache is purely in-memory.
func NewLastKnownGoodCache(opts ...CacheOption) *LastKnownGoodCache {
	c := &LastKnownGoodCache{
		entries: make(map[cacheKey]cacheEntry),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.path != "" {
		// Best-effort load: a missing/corrupt file leaves the cache empty
		// rather than failing construction. Persistence is an optimization,
		// never a dependency (D1).
		_ = c.load()
	}
	return c
}

// Refresh records (or overwrites) the last-known-good snapshot for the
// given query. records is the record set a successful live query
// returned. If persistence is enabled the snapshot file is rewritten;
// a write failure is returned so a caller may surface it, but it does
// not corrupt the in-memory cache.
func (c *LastKnownGoodCache) Refresh(q QueryPayload, records []RegistrationRecord) error {
	key := queryCacheKey(q)
	snapshot := make([]RegistrationRecord, len(records))
	copy(snapshot, records)

	c.mu.Lock()
	c.entries[key] = cacheEntry{
		Key:      string(key),
		Records:  snapshot,
		CachedAt: time.Now().UTC().Format(time.RFC3339),
	}
	c.mu.Unlock()

	if c.path != "" {
		return c.save()
	}
	return nil
}

// Lookup returns the last-known-good record set for the given query and
// whether an entry exists. The returned slice is a defensive copy.
func (c *LastKnownGoodCache) Lookup(q QueryPayload) ([]RegistrationRecord, bool) {
	key := queryCacheKey(q)
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	out := make([]RegistrationRecord, len(entry.Records))
	copy(out, entry.Records)
	return out, true
}

// Len reports how many distinct query snapshots the cache currently
// holds. It is a convenience accessor for callers and tests.
func (c *LastKnownGoodCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// save writes the whole cache to its local JSON file. It writes via a
// temp file + rename so a crash mid-write cannot leave a half-written
// snapshot.
func (c *LastKnownGoodCache) save() error {
	c.mu.RLock()
	list := make([]cacheEntry, 0, len(c.entries))
	for _, e := range c.entries {
		list = append(list, e)
	}
	path := c.path
	c.mu.RUnlock()

	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("agentlaunch: cache marshal: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("agentlaunch: cache mkdir: %w", err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("agentlaunch: cache write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("agentlaunch: cache rename: %w", err)
	}
	return nil
}

// load reads the local JSON snapshot file into the cache. A missing file
// is not an error (the cache simply starts empty).
func (c *LastKnownGoodCache) load() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("agentlaunch: cache read: %w", err)
	}
	var list []cacheEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("agentlaunch: cache unmarshal: %w", err)
	}
	c.mu.Lock()
	for _, e := range list {
		c.entries[cacheKey(e.Key)] = e
	}
	c.mu.Unlock()
	return nil
}

// OnDegrade is the observability hook fired by a DegradingRegistrar when
// it crosses a degradation boundary. reason is a human-readable
// description. It is injectable so consumers wire their own logger /
// metrics emitter; the wrapper never hard-codes a logger.
type OnDegrade func(reason string)

// degradeState is the current health posture of a DegradingRegistrar.
type degradeState int

const (
	// stateHealthy: the inner registrar answered the most recent call.
	stateHealthy degradeState = iota
	// stateDegraded: the inner registrar is unreachable and the wrapper
	// is serving query reads from the last-known-good cache.
	stateDegraded
)

// DegradeStatus is a snapshot of a DegradingRegistrar's current
// degradation posture, returned by DegradingRegistrar.Status. It is
// purely observational.
type DegradeStatus struct {
	// Degraded is true while the wrapper is operating from cache.
	Degraded bool
	// Reason is the human-readable degradation reason, empty when healthy.
	Reason string
	// SinceUTC is when the wrapper last transitioned into the current
	// state, RFC3339 / UTC. Empty before the first observed transition.
	SinceUTC string
	// CachedQueries is the number of distinct query snapshots currently
	// held in the last-known-good cache.
	CachedQueries int
}

// DegradingRegistrar wraps an inner Registrar (the live one) and is
// itself a Registrar. It makes the directory non-load-bearing on the
// launch read path: when the inner registrar is unreachable, query
// resolution falls back to a last-known-good cache instead of failing.
//
// Behavior by operation:
//
//   - query: a successful inner Handle is returned AND its records are
//     written to the last-known-good cache. A failing inner Handle falls
//     back to the cached snapshot; the live error is swallowed (the
//     degradation is reported via the OnDegrade hook and Status, not as
//     a returned error) unless the cache has no entry, in which case
//     ErrRegistryCacheMiss is returned.
//
//   - health: reports HealthStatusOK while the inner registrar is
//     healthy and HealthStatusDegraded — never down, never a hard error
//     — while operating from cache. DirectoryReachable and Detail
//     reflect the posture.
//
//   - register / deregister: these are writes and CANNOT be served from
//     the cache (the cache is a read-side last-known-good mirror, not a
//     write buffer). When the inner registrar is unreachable the wrapper
//     surfaces ErrRegistryWriteWhileDegraded (wrapping the inner error).
//     This is deliberate: a launch READS the directory; it never needs
//     to WRITE it, so failing writes loudly while keeping reads degrading
//     gracefully is the correct asymmetry for Risk 1.
//
// It is safe for concurrent use.
type DegradingRegistrar struct {
	inner Registrar
	cache *LastKnownGoodCache

	mu        sync.Mutex
	state     degradeState
	reason    string
	since     string
	onDegrade OnDegrade
}

// DegradingOption configures a DegradingRegistrar at construction time.
type DegradingOption func(*DegradingRegistrar)

// WithDegradeHook installs an observability callback fired whenever the
// wrapper transitions between healthy and degraded (in either
// direction). The callback is injectable so consumers emit to their own
// logger / metrics sink; nothing here hard-codes a logger.
func WithDegradeHook(fn OnDegrade) DegradingOption {
	return func(d *DegradingRegistrar) {
		if fn != nil {
			d.onDegrade = fn
		}
	}
}

// NewDegradingRegistrar wraps inner with a last-known-good degradation
// layer backed by cache. inner is the live registrar; cache is the
// consumer-side last-known-good store. Both are required; a nil cache is
// replaced with a fresh in-memory one so the wrapper is always usable.
func NewDegradingRegistrar(inner Registrar, cache *LastKnownGoodCache, opts ...DegradingOption) *DegradingRegistrar {
	if cache == nil {
		cache = NewLastKnownGoodCache()
	}
	d := &DegradingRegistrar{
		inner: inner,
		cache: cache,
		state: stateHealthy,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// compile-time assertion that DegradingRegistrar satisfies Registrar.
var _ Registrar = (*DegradingRegistrar)(nil)

// Cache exposes the underlying last-known-good cache so a consumer can
// inspect or pre-seed it.
func (d *DegradingRegistrar) Cache() *LastKnownGoodCache { return d.cache }

// Status returns a snapshot of the current degradation posture. It is
// the queryable companion to the OnDegrade hook.
func (d *DegradingRegistrar) Status() DegradeStatus {
	d.mu.Lock()
	st := DegradeStatus{
		Degraded: d.state == stateDegraded,
		Reason:   d.reason,
		SinceUTC: d.since,
	}
	d.mu.Unlock()
	st.CachedQueries = d.cache.Len()
	return st
}

// markDegraded transitions the wrapper into the degraded state. It fires
// the OnDegrade hook only on an actual healthy->degraded edge so the
// hook is not spammed on every cache-served query.
func (d *DegradingRegistrar) markDegraded(reason string) {
	d.mu.Lock()
	transitioned := d.state != stateDegraded
	d.state = stateDegraded
	d.reason = reason
	if transitioned {
		d.since = time.Now().UTC().Format(time.RFC3339)
	}
	hook := d.onDegrade
	d.mu.Unlock()
	if transitioned && hook != nil {
		hook("degraded: " + reason)
	}
}

// markHealthy transitions the wrapper into the healthy state. It fires
// the OnDegrade hook only on an actual degraded->healthy edge so a
// consumer observes the recovery exactly once.
func (d *DegradingRegistrar) markHealthy() {
	d.mu.Lock()
	transitioned := d.state != stateHealthy
	d.state = stateHealthy
	d.reason = ""
	if transitioned {
		d.since = time.Now().UTC().Format(time.RFC3339)
	}
	hook := d.onDegrade
	d.mu.Unlock()
	if transitioned && hook != nil {
		hook("recovered: directory reachable again")
	}
}

// Handle dispatches one RegistryEnvelope through the degradation layer.
// It validates the envelope itself first (so a malformed envelope fails
// fast and is never misreported as a directory degradation), then routes
// per operation.
func (d *DegradingRegistrar) Handle(env RegistryEnvelope) (RegistryResponse, error) {
	if err := env.Validate(); err != nil {
		// A bad envelope is a caller error, not a directory outage. Do not
		// touch degradation state or the cache.
		return RegistryResponse{}, err
	}
	switch env.Operation {
	case RegistryOperationQuery:
		return d.handleQuery(env)
	case RegistryOperationHealth:
		return d.handleHealth(env)
	case RegistryOperationRegister, RegistryOperationDeregister:
		return d.handleWrite(env)
	default:
		return RegistryResponse{}, ErrRegistryUnsupportedOperation
	}
}

// handleQuery runs the inner query; on success it refreshes the
// last-known-good cache and reports healthy, on inner failure it serves
// the cached snapshot and reports degraded. The only error it returns is
// ErrRegistryCacheMiss — inner is down and nothing was ever cached for
// this query.
func (d *DegradingRegistrar) handleQuery(env RegistryEnvelope) (RegistryResponse, error) {
	q := *env.Query
	resp, err := d.inner.Handle(env)
	if err == nil {
		// Live answer: refresh the last-known-good snapshot. A cache
		// persistence write failure must not fail the live query — the
		// live result is authoritative — so it is intentionally ignored
		// here; callers that need durable persistence guarantees can
		// inspect Cache() directly.
		_ = d.cache.Refresh(q, resp.Records)
		d.markHealthy()
		return resp, nil
	}

	// Inner registrar is unreachable / errored. Fall back to the
	// last-known-good cache rather than propagating the error: a launch
	// read must not hard-fail because the directory is down (D1).
	reason := "directory unreachable on query: " + err.Error()
	cached, ok := d.cache.Lookup(q)
	if !ok {
		// Honest cache miss: we never observed a successful answer for
		// this exact query. This is the one case the wrapper surfaces an
		// error — and it is a cache miss, not a directory dependency.
		d.markDegraded(reason + " (no cache entry)")
		return RegistryResponse{}, fmt.Errorf("%w: %v", ErrRegistryCacheMiss, err)
	}
	d.markDegraded(reason)
	return RegistryResponse{
		Operation: RegistryOperationQuery,
		Records:   cached,
	}, nil
}

// handleHealth reports the wrapper's own health posture. It prefers the
// inner registrar's live health when reachable; when the inner health
// call fails it reports degraded (never down, never a hard error) with a
// human-readable Detail and DirectoryReachable=false.
func (d *DegradingRegistrar) handleHealth(env RegistryEnvelope) (RegistryResponse, error) {
	resp, err := d.inner.Handle(env)
	if err == nil && resp.Health != nil {
		d.markHealthy()
		detail := resp.Health.Detail
		if detail != "" {
			detail = "degrading wrapper: directory reachable; inner: " + detail
		} else {
			detail = "degrading wrapper: directory reachable"
		}
		return RegistryResponse{
			Operation: RegistryOperationHealth,
			Health: &HealthPayload{
				Status:             HealthStatusOK,
				DirectoryReachable: true,
				LastSyncAt:         resp.Health.LastSyncAt,
				Detail:             detail,
			},
		}, nil
	}

	// Inner health unreachable: report degraded, not down. The wrapper is
	// still serving query reads from the last-known-good cache, so the
	// consumer is operational — just not on live directory data.
	reason := "directory health unreachable"
	if err != nil {
		reason += ": " + err.Error()
	}
	d.markDegraded(reason)
	return RegistryResponse{
		Operation: RegistryOperationHealth,
		Health: &HealthPayload{
			Status:             HealthStatusDegraded,
			DirectoryReachable: false,
			Detail: fmt.Sprintf(
				"degrading wrapper: %s; serving query reads from last-known-good cache (%d snapshot(s))",
				reason, d.cache.Len()),
		},
	}, nil
}

// handleWrite dispatches a register or deregister through the inner
// registrar. Writes are NOT cacheable: the last-known-good cache is a
// read-side mirror, not a write-ahead buffer. When the inner registrar
// is unreachable the write cannot proceed and the wrapper surfaces
// ErrRegistryWriteWhileDegraded wrapping the inner error.
//
// This asymmetry — reads degrade gracefully, writes fail loudly — is
// deliberate and is exactly what closes Risk 1: a LAUNCH only ever reads
// the directory (query), so a degraded directory never blocks a launch;
// a write is an administrative operation whose caller must see the
// failure rather than be told a phantom success.
func (d *DegradingRegistrar) handleWrite(env RegistryEnvelope) (RegistryResponse, error) {
	resp, err := d.inner.Handle(env)
	if err == nil {
		d.markHealthy()
		return resp, nil
	}
	// A duplicate-object / not-found error is a legitimate semantic
	// rejection by a reachable registrar, not an outage. Pass it through
	// unchanged and treat the directory as healthy.
	if isSemanticWriteError(err) {
		d.markHealthy()
		return RegistryResponse{}, err
	}
	d.markDegraded("directory unreachable on write: " + err.Error())
	// Wrap with %w so the sentinel matches AND the inner failure stays
	// inspectable via errors.Is / errors.Unwrap (errors.Join keeps both
	// in the chain).
	return RegistryResponse{}, fmt.Errorf("%w: %w", ErrRegistryWriteWhileDegraded, err)
}

// isSemanticWriteError reports whether err is a normal semantic
// rejection from a reachable registrar (as opposed to an outage). Those
// errors must pass through untouched and must not flip the wrapper into
// the degraded state.
func isSemanticWriteError(err error) bool {
	return errors.Is(err, ErrRegistryDuplicateObject) ||
		errors.Is(err, ErrRegistryObjectNotFound) ||
		errors.Is(err, ErrRegistryUnsupportedOperation)
}
