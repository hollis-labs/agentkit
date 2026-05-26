package agentcontext

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ContextProvider is the high-level orchestrator interface that
// turns a ContextRequest into a ContextResult. It composes a map of
// per-kind Resolvers with a Renderer.
//
// Phase 2 ships a single concrete implementation, DefaultProvider.
// The interface exists so downstream consumers (and tests) can wrap
// or stub the entry point without taking a hard dependency on the
// default behaviour.
type ContextProvider interface {
	Assemble(ctx context.Context, req ContextRequest) (*ContextResult, error)
}

// DefaultProvider is the package's concrete ContextProvider. It
// walks ContextRequest.Slots in input order, dispatches each slot
// to the Resolver registered for its SlotSourceKind, collects the
// SlotResults, hands them to the Renderer, and assembles the final
// ContextResult.
//
// A DefaultProvider is constructed via NewProvider. The zero value
// is NOT usable — callers MUST pass a non-nil resolver map and a
// non-nil Renderer (DefaultRenderer{} is the convenience default).
//
// DefaultProvider is goroutine-safe for Assemble calls AS LONG AS
// every registered Resolver is goroutine-safe. The provider itself
// adds no shared mutable state.
type DefaultProvider struct {
	resolvers map[SlotSourceKind]Resolver
	renderer  Renderer
	clock     func() time.Time // injectable for tests; default time.Now
}

// NewProvider constructs a DefaultProvider with the supplied
// resolver map and Renderer. Returns ErrMissingResolver if resolvers
// is nil and ErrMissingRenderer if renderer is nil.
//
// An EMPTY (but non-nil) resolver map is permitted; it merely means
// Assemble will fail any non-zero-kind slot with ErrUnknownSlotKind.
// This is the intended behaviour for test fixtures that wire in
// only the kinds they exercise.
func NewProvider(resolvers map[SlotSourceKind]Resolver, renderer Renderer) (*DefaultProvider, error) {
	if resolvers == nil {
		return nil, ErrMissingResolver
	}
	if renderer == nil {
		return nil, ErrMissingRenderer
	}
	return &DefaultProvider{
		resolvers: resolvers,
		renderer:  renderer,
		clock:     time.Now,
	}, nil
}

// Assemble implements ContextProvider.
//
// # Flow
//
//  1. Validate the request. Returns the validation sentinel on
//     first failure.
//  2. Hash the request (RequestHash on the result provenance is
//     populated up front so cache-key callers can read it even when
//     a downstream resolver fails).
//  3. Walk req.Slots in order. For each slot:
//     a. Look up the Resolver for slot.Source.Kind. Missing
//     resolver → return ErrUnknownSlotKind wrapped with the kind
//     name.
//     b. Invoke Resolver.Resolve. Errors:
//     - non-required slot: record the error in SlotResult.Err
//     and continue with empty content.
//     - required slot: return ErrRequiredSlotFailed wrapped
//     around the resolver error.
//     c. Defensively recompute SlotResult.Bytes /
//     SlotResult.TokenEstimate from the returned Content.
//     d. Defensively set SlotResult.Provenance.Kind to the slot
//     kind (only if the resolver left it empty).
//     e. If the slot is Required and Content is empty (no error),
//     return ErrSlotRequiredAndEmpty wrapped with the slot
//     name.
//  4. Hand the SlotResults to the Renderer.
//  5. Apply renderer-reported truncation/drops back onto the
//     SlotResults (set SlotResult.Truncated on truncated names).
//  6. Stamp result-level provenance (LibraryVersion, RequestHash,
//     AssembledAt) and return.
func (p *DefaultProvider) Assemble(ctx context.Context, req ContextRequest) (*ContextResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	hash, err := HashRequest(req)
	if err != nil {
		return nil, fmt.Errorf("agentcontext: hash request: %w", err)
	}

	env := ResolverEnv{
		Workdir:           req.Workdir,
		RequestProvenance: req.Provenance,
	}

	results := make([]SlotResult, 0, len(req.Slots))
	for _, slot := range req.Slots {
		// Short-circuit on context cancellation between slots so a
		// caller can abort a long assembly without waiting for the
		// next resolver to notice.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		resolver, ok := p.resolvers[slot.Source.Kind]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownSlotKind, slot.Source.Kind)
		}

		res, rerr := resolver.Resolve(ctx, slot, env)
		// Make sure the result echoes the slot identity even if a
		// careless resolver forgot to copy it. This guarantees the
		// per-slot result order matches the request order
		// regardless of resolver hygiene.
		res.Name = slot.Name
		if res.Section == "" {
			res.Section = slot.Section
		}
		if res.Provenance.Kind == "" {
			res.Provenance.Kind = slot.Source.Kind
		}
		// Defensive recomputation of byte/token bookkeeping —
		// covers resolvers that leave the fields zero.
		res.Bytes = len(res.Content)
		res.TokenEstimate = EstimateTokens(res.Bytes)
		if res.Provenance.Bytes == 0 {
			res.Provenance.Bytes = res.Bytes
		}

		if rerr != nil {
			if slot.Required {
				return nil, fmt.Errorf("%w (%s): %w", ErrRequiredSlotFailed, slot.Name, rerr)
			}
			res.Err = rerr
			results = append(results, res)
			continue
		}

		if slot.Required && res.Content == "" {
			return nil, fmt.Errorf("%w: %s", ErrSlotRequiredAndEmpty, slot.Name)
		}

		results = append(results, res)
	}

	rendered, applied := p.renderer.Render(results, req.Limits)

	// Mark truncated slots on the per-slot results.
	if len(applied.TruncatedSlots) > 0 {
		truncated := make(map[string]struct{}, len(applied.TruncatedSlots))
		for _, name := range applied.TruncatedSlots {
			truncated[name] = struct{}{}
		}
		for i := range results {
			if _, ok := truncated[results[i].Name]; ok {
				results[i].Truncated = true
			}
		}
	}

	return &ContextResult{
		Slots:    results,
		Rendered: rendered,
		Provenance: Provenance{
			Input:          req.Provenance,
			LibraryVersion: Version,
			RequestHash:    hash,
			AssembledAt:    p.clock().UTC(),
		},
		Limits: applied,
	}, nil
}

// withClock is a test seam — production callers always use the
// default time.Now. Kept unexported so the public API surface
// remains stable.
func (p *DefaultProvider) withClock(clock func() time.Time) {
	if clock != nil {
		p.clock = clock
	}
}

// Ensure DefaultProvider satisfies ContextProvider at compile time.
var _ ContextProvider = (*DefaultProvider)(nil)

// joinErrors is a small adapter so a future Go version's errors.Join
// can replace it without changing callers. Currently unused but
// kept here to document the intent for the resolver subagent — the
// dispatcher does NOT collect non-required slot errors into a
// composite; it leaves them on SlotResult.Err for inspection. If a
// future revision wants a composite, this is the seam.
//
//nolint:unused
func joinErrors(errs ...error) error {
	out := make([]error, 0, len(errs))
	for _, e := range errs {
		if e != nil {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return errors.Join(out...)
}
