package agentcontext

import "context"

// ResolverEnv carries Assemble-time context that a Resolver needs to
// resolve a slot but that does not live on the SlotSpec itself. The
// caller passes ContextRequest-level values (Workdir, Provenance)
// through ResolverEnv so resolvers can read them without taking a
// dependency on the request type.
type ResolverEnv struct {
	// Workdir mirrors ContextRequest.Workdir. Resolvers consult it
	// when SlotSource carries a relative path or a Cmd with no
	// explicit CWD.
	Workdir string

	// RequestProvenance mirrors ContextRequest.Provenance, exposed
	// so resolvers can use the lineage / profile / project identity
	// for logging or for embedding into resolver-side provenance
	// Extra fields. Resolvers MUST NOT mutate this value.
	RequestProvenance ProvenanceInput
}

// Resolver is the per-kind extension point. Each SlotSourceKind has
// at most one Resolver registered in a Provider's resolver map. The
// Resolver receives the typed SlotSpec, the assemble-time ResolverEnv,
// and the cancelable context.Context, and returns a populated
// SlotResult (with Name/Section copied from spec) or a non-nil error.
//
// # Contract
//
//   - The Resolver MUST honour ctx cancellation promptly.
//   - The Resolver SHOULD populate SlotResult.Provenance with
//     resolver-defined attribution (Source / Bytes / ContentHash /
//     FetchedAt / Extra). SlotResult.Provenance.Kind is set by
//     DefaultProvider.Assemble; the Resolver MAY overwrite it.
//   - The Resolver SHOULD set SlotResult.Bytes and
//     SlotResult.TokenEstimate to match the byte length of the
//     content it returned. DefaultProvider.Assemble recomputes both
//     defensively so a Resolver that forgets does not break
//     downstream consumers — but resolvers SHOULD set them so
//     custom Renderers can rely on the pre-render values.
//   - On success, the Resolver MUST leave SlotResult.Err nil. On
//     failure, the Resolver SHOULD return the error directly (not
//     stuff it into SlotResult.Err) — the dispatcher copies it onto
//     SlotResult.Err for non-required slots.
//
// # Non-goals
//
// The Resolver interface is deliberately narrow. It does not own
// budget enforcement (the Renderer does), does not own rendering
// (the Renderer does), and does not own dispatch (the Provider does).
// A resolver is a pure SlotSpec → SlotResult function.
type Resolver interface {
	Resolve(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error)
}

// ResolverFunc adapts a plain function to the Resolver interface, in
// the http.HandlerFunc style. Useful for in-test stub resolvers and
// for simple resolvers that do not need internal state.
type ResolverFunc func(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error)

// Resolve implements the Resolver interface.
func (f ResolverFunc) Resolve(ctx context.Context, spec SlotSpec, env ResolverEnv) (SlotResult, error) {
	return f(ctx, spec, env)
}
