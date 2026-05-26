package agentcontext

import "time"

// ProvenanceInput is the caller-supplied request-level attribution
// that flows through the assembly pipeline unchanged. The library
// does NOT interpret these fields — it copies them onto
// Provenance.Input and rehashes them as part of HashRequest.
//
// The field names mirror the identity block in go-agent-launch
// catalog entries (lineage_alias / lineage_id / profile_id /
// profile_version / role / project) so a consumer can pass the
// identity block in unchanged.
type ProvenanceInput struct {
	// LineageAlias is the human-readable lineage tag (e.g.
	// "nanite.backend.main").
	LineageAlias string `yaml:"lineage_alias,omitempty" json:"lineage_alias,omitempty"`

	// LineageID is the durable lineage identifier (typically
	// "agtln_<ulid>" once Agent Mux issues it).
	LineageID string `yaml:"lineage_id,omitempty" json:"lineage_id,omitempty"`

	// ProfileID is the launch profile identifier (e.g.
	// "nanite-backend").
	ProfileID string `yaml:"profile_id,omitempty" json:"profile_id,omitempty"`

	// ProfileVersion is the launch profile schema version. Opaque
	// integer; the library treats it as a passthrough field.
	ProfileVersion int `yaml:"profile_version,omitempty" json:"profile_version,omitempty"`

	// Role is the role identifier (e.g. "backend", "uat").
	Role string `yaml:"role,omitempty" json:"role,omitempty"`

	// Project is the project identifier (e.g. "nanite").
	Project string `yaml:"project,omitempty" json:"project,omitempty"`

	// Extra is an opaque map for orchestrator-defined extension
	// fields. Hash canonicalization sorts the keys, so map iteration
	// order does NOT affect HashRequest output.
	Extra map[string]string `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// Provenance is the request-level attribution attached to a
// ContextResult. It carries the caller's ProvenanceInput unchanged
// plus library-supplied fields (LibraryVersion, RequestHash,
// AssembledAt) so downstream consumers can attribute a rendered
// context back to the exact request that produced it.
type Provenance struct {
	// Input is the caller-supplied attribution, copied verbatim.
	Input ProvenanceInput `yaml:"input,omitempty" json:"input,omitempty"`

	// LibraryVersion is the go-agent-context library version (the
	// package-level Version constant) that ran Assemble.
	LibraryVersion string `yaml:"library_version" json:"library_version"`

	// RequestHash is the deterministic SHA-256 hex digest of the
	// source ContextRequest, as returned by HashRequest. Useful as a
	// cache key.
	RequestHash string `yaml:"request_hash" json:"request_hash"`

	// AssembledAt is the wall-clock time at which Assemble returned.
	// UTC is conventional but not enforced. Zero value indicates
	// "assembler did not stamp" (e.g. hand-constructed
	// ContextResult in tests).
	AssembledAt time.Time `yaml:"assembled_at,omitempty" json:"assembled_at,omitempty"`
}

// SlotProvenance is the per-slot attribution attached to a
// SlotResult. Populated by the Resolver that produced the slot — the
// library threads it through but does not interpret it.
type SlotProvenance struct {
	// Kind is the SlotSourceKind that resolved the slot. Copied from
	// SlotSpec.Source.Kind for convenience.
	Kind SlotSourceKind `yaml:"kind" json:"kind"`

	// Source is a resolver-defined human-readable description of
	// where the content came from (file path, URL, command line).
	// Treated as opaque by the library; surfaced to downstream
	// consumers via SlotResult.Provenance.
	Source string `yaml:"source,omitempty" json:"source,omitempty"`

	// Bytes is the byte length of the resolved content (pre-render,
	// pre-truncation). Equal to len(SlotResult.Content) when no
	// truncation occurred.
	Bytes int `yaml:"bytes" json:"bytes"`

	// ContentHash is an optional resolver-emitted SHA-256 hex digest
	// of the resolved content. Resolvers MAY leave this empty;
	// downstream consumers MUST tolerate empty values.
	ContentHash string `yaml:"content_hash,omitempty" json:"content_hash,omitempty"`

	// FetchedAt is the wall-clock time at which the resolver fetched
	// the content. UTC is conventional but not enforced.
	FetchedAt time.Time `yaml:"fetched_at,omitempty" json:"fetched_at,omitempty"`

	// Extra is an opaque map for resolver-defined extension fields
	// (e.g. HTTP response status code, command exit code, stderr
	// tail). Hash canonicalization sorts keys.
	Extra map[string]string `yaml:"extra,omitempty" json:"extra,omitempty"`
}
