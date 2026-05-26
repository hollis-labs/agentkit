package agentlaunch

import (
	"sort"
	"strconv"
	"sync"
)

// registry_core.go builds the LIVE directory-registry service core on top
// of the S1.3 contract types in directory_registry.go. S1.3 shipped the
// schemas; this file ships the running service that processes
// RegistryEnvelope operations.
//
// Design constraints honored here:
//
//   - D1 (local-first; never mandatory on the launch hot path). The
//     in-memory registrar has zero network dependency and is fully usable
//     standalone/offline. It is a permanent first-class mode, not
//     migration scaffolding. No code path here makes a launch require a
//     remote directory: the registrar is a side service the launch
//     pipeline may consult, never one it must reach.
//
//   - D2 (the directory holds resolver handles, NOT content). The store
//     keys and returns RegistrationRecord values only — a stable handle
//     (RegistryContractMeta) plus a local file pointer (RegistrationSource).
//     Nothing here ingests or snapshots resolved profile/prompt/tool
//     bodies; there is deliberately no field or path that would.
//
// Per-kind contract-body decode and validation (S3.2), file-backed
// catalog ingest (S3.3), the last-known-good degradation cache (S3.4),
// and the bus-topic kind (S3.5) are out of scope. A clean seam for S3.2
// is left via the recordValidator hook (see WithRecordValidator).

// RegistryResponse is the result of dispatching one RegistryEnvelope
// through a Registrar. Exactly the fields relevant to the request's
// operation are populated:
//
//   - register / deregister populate Ref with the affected object and,
//     for register, set Replaced when an existing record was upserted.
//   - query populates Records with every matching RegistrationRecord.
//   - health populates Health with the service-health report.
//
// Operation echoes the envelope verb so callers can branch without
// re-inspecting the request.
type RegistryResponse struct {
	Operation RegistryOperation    `yaml:"operation" json:"operation"`
	Ref       *RegistryObjectRef   `yaml:"ref,omitempty" json:"ref,omitempty"`
	Replaced  bool                 `yaml:"replaced,omitempty" json:"replaced,omitempty"`
	Records   []RegistrationRecord `yaml:"records,omitempty" json:"records,omitempty"`
	Health    *HealthPayload       `yaml:"health,omitempty" json:"health,omitempty"`
}

// Registrar is the live directory-registry service surface. Handle is the
// single envelope-dispatch entry point: it validates the envelope, routes
// on RegistryEnvelope.Operation, and returns a RegistryResponse.
//
// Implementations must be safe for concurrent use — the registrar is a
// "live service" and callers may register, query, and health-check from
// multiple goroutines.
type Registrar interface {
	// Handle validates and dispatches one RegistryEnvelope.
	Handle(env RegistryEnvelope) (RegistryResponse, error)
}

// recordValidator is the S3.2 extension seam. The core registrar always
// runs RegistrationRecord.Validate (the structural pointer-model check);
// a recordValidator, when supplied, runs an ADDITIONAL caller-provided
// check before a record is stored. S3.2 will plug per-kind contract-body
// decode+validation in here without touching the core dispatch.
type recordValidator func(RegistrationRecord) error

// InMemoryRegistrar is the in-memory, offline, concurrency-safe registrar
// implementation. It backs both the file-backed and directory registrar
// envelope modes; the mode in the envelope is descriptive metadata, the
// store itself is always local. This satisfies D1: the registrar runs
// with no network and is never required by the launch hot path.
type InMemoryRegistrar struct {
	mu        sync.RWMutex
	store     map[RegistryObjectRef]RegistrationRecord
	validator recordValidator
	lastSync  string
}

// RegistrarOption configures an InMemoryRegistrar at construction time.
type RegistrarOption func(*InMemoryRegistrar)

// WithRecordValidator installs an additional per-record validation hook
// run before a record is stored on register. This is the S3.2 seam for
// per-kind contract-body validation; the core registrar does not decode
// contract bodies itself.
func WithRecordValidator(fn func(RegistrationRecord) error) RegistrarOption {
	return func(r *InMemoryRegistrar) {
		if fn != nil {
			r.validator = recordValidator(fn)
		}
	}
}

// NewInMemoryRegistrar constructs an empty, ready-to-use in-memory
// registrar. It performs no I/O and has no network dependency.
func NewInMemoryRegistrar(opts ...RegistrarOption) *InMemoryRegistrar {
	r := &InMemoryRegistrar{
		store: make(map[RegistryObjectRef]RegistrationRecord),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// compile-time assertion that InMemoryRegistrar satisfies Registrar.
var _ Registrar = (*InMemoryRegistrar)(nil)

// Handle validates the envelope and dispatches on its operation. It is
// the single entry point for the live service; register, deregister,
// query, and health are not exported individually so callers always go
// through the validated envelope contract.
func (r *InMemoryRegistrar) Handle(env RegistryEnvelope) (RegistryResponse, error) {
	if err := env.Validate(); err != nil {
		return RegistryResponse{}, err
	}
	switch env.Operation {
	case RegistryOperationRegister:
		return r.handleRegister(*env.Register)
	case RegistryOperationDeregister:
		return r.handleDeregister(*env.Deregister)
	case RegistryOperationQuery:
		return r.handleQuery(*env.Query)
	case RegistryOperationHealth:
		return r.handleHealth()
	default:
		// env.Validate already rejects unknown operations; this guards
		// against a future verb being added to the vocabulary without a
		// matching dispatch arm.
		return RegistryResponse{}, ErrRegistryUnsupportedOperation
	}
}

// handleRegister stores a RegistrationRecord keyed by its
// RegistryObjectRef. RegisterPayload.Upsert governs collision behavior:
// false rejects a duplicate ref with ErrRegistryDuplicateObject; true
// replaces the existing record and reports Replaced.
func (r *InMemoryRegistrar) handleRegister(p RegisterPayload) (RegistryResponse, error) {
	rec := p.Record
	// RegistryEnvelope.Validate already ran rec.Validate via the register
	// arm; re-run defensively so handleRegister is correct if ever called
	// outside Handle.
	if err := rec.Validate(); err != nil {
		return RegistryResponse{}, err
	}
	if r.validator != nil {
		if err := r.validator(rec); err != nil {
			return RegistryResponse{}, err
		}
	}
	ref := rec.Meta.Ref

	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.store[ref]
	if exists && !p.Upsert {
		return RegistryResponse{}, ErrRegistryDuplicateObject
	}
	r.store[ref] = rec
	return RegistryResponse{
		Operation: RegistryOperationRegister,
		Ref:       &ref,
		Replaced:  exists,
	}, nil
}

// handleDeregister removes a record by RegistryObjectRef. Deregistering
// an absent ref returns ErrRegistryObjectNotFound.
func (r *InMemoryRegistrar) handleDeregister(p DeregisterPayload) (RegistryResponse, error) {
	ref := p.Ref
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.store[ref]; !exists {
		return RegistryResponse{}, ErrRegistryObjectNotFound
	}
	delete(r.store, ref)
	return RegistryResponse{
		Operation: RegistryOperationDeregister,
		Ref:       &ref,
	}, nil
}

// handleQuery returns every stored record matching the QueryPayload
// facets. All provided filters AND together; an empty filter matches
// everything. Results are sorted by (kind, namespace, owner, name) so the
// response order is deterministic.
func (r *InMemoryRegistrar) handleQuery(q QueryPayload) (RegistryResponse, error) {
	r.mu.RLock()
	matched := make([]RegistrationRecord, 0, len(r.store))
	for _, rec := range r.store {
		if queryMatches(q, rec) {
			matched = append(matched, rec)
		}
	}
	r.mu.RUnlock()

	sort.Slice(matched, func(i, j int) bool {
		return refLess(matched[i].Meta.Ref, matched[j].Meta.Ref)
	})
	return RegistryResponse{
		Operation: RegistryOperationQuery,
		Records:   matched,
	}, nil
}

// handleHealth reports a HealthPayload for the live service. The
// in-memory registrar is always self-contained, so it reports ok with
// directory_reachable true (the local store is its own directory) and
// echoes the last register sync timestamp it observed.
func (r *InMemoryRegistrar) handleHealth() (RegistryResponse, error) {
	r.mu.RLock()
	count := len(r.store)
	lastSync := r.lastSync
	r.mu.RUnlock()

	detail := "in-memory registrar: local-first, no network dependency"
	return RegistryResponse{
		Operation: RegistryOperationHealth,
		Health: &HealthPayload{
			Status:             HealthStatusOK,
			DirectoryReachable: true,
			LastSyncAt:         lastSync,
			Detail:             detail + "; " + countDetail(count),
		},
	}, nil
}

// Len reports the number of records currently stored. It is a
// convenience accessor for callers and tests; it does not touch the
// envelope contract.
func (r *InMemoryRegistrar) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.store)
}

// queryMatches reports whether one record satisfies every facet set on
// the QueryPayload. Unset facets are skipped (match-all). The
// QueryPayload.IncludeLocal flag is informational for resolution policy
// and does not narrow the in-memory result set.
func queryMatches(q QueryPayload, rec RegistrationRecord) bool {
	ref := rec.Meta.Ref
	if len(q.Kinds) > 0 && !containsKind(q.Kinds, ref.Kind) {
		return false
	}
	if q.Owner != "" && q.Owner != ref.Owner {
		return false
	}
	if q.Namespace != "" && q.Namespace != ref.Namespace {
		return false
	}
	if q.Name != "" && q.Name != ref.Name {
		return false
	}
	if q.Interface != "" && q.Interface != rec.Meta.Interface {
		return false
	}
	for k, v := range q.Labels {
		got, ok := rec.Meta.Labels[k]
		if !ok || got != v {
			return false
		}
	}
	return true
}

// containsKind reports whether kinds includes want.
func containsKind(kinds []RegistryKind, want RegistryKind) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// refLess orders two refs by (kind, namespace, owner, name) for stable
// query output.
func refLess(a, b RegistryObjectRef) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	if a.Owner != b.Owner {
		return a.Owner < b.Owner
	}
	return a.Name < b.Name
}

// countDetail renders the stored-record count for the health detail
// string.
func countDetail(n int) string {
	if n == 1 {
		return "1 record"
	}
	return strconv.Itoa(n) + " records"
}
