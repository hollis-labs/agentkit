package agentcontext

// SlotResult is the resolved output for a single SlotSpec. It is
// emitted by a Resolver and threaded through the Renderer onto
// ContextResult.Slots.
//
// A non-nil Err indicates that the Resolver failed to produce
// content. For non-required slots, DefaultProvider.Assemble proceeds
// past the failure and records it here for inspection. For required
// slots, Assemble surfaces ErrRequiredSlotFailed wrapping Err.
type SlotResult struct {
	// Name is the slot identifier (copied from SlotSpec.Name).
	Name string `yaml:"name" json:"name"`

	// Section is the heading prefix the Renderer should emit (copied
	// from SlotSpec.Section). Empty means "use Name".
	Section string `yaml:"section,omitempty" json:"section,omitempty"`

	// Content is the resolved slot body — what the Renderer
	// concatenates into the final output. May be empty (for a
	// resolver that legitimately returned no content) or
	// post-truncation shortened (see Truncated).
	Content string `yaml:"content,omitempty" json:"content,omitempty"`

	// Truncated is true when the Renderer shortened Content to fit a
	// byte budget. The original (pre-truncation) length is preserved
	// in Provenance.Bytes.
	Truncated bool `yaml:"truncated,omitempty" json:"truncated,omitempty"`

	// Bytes is the byte length of Content AS RENDERED (i.e.
	// post-truncation). For the pre-render length, see
	// Provenance.Bytes.
	Bytes int `yaml:"bytes" json:"bytes"`

	// TokenEstimate is the char/4 token estimate of Content as
	// rendered. See EstimateTokens for the heuristic.
	TokenEstimate int `yaml:"token_estimate" json:"token_estimate"`

	// Provenance is the per-slot attribution emitted by the
	// Resolver.
	Provenance SlotProvenance `yaml:"provenance,omitempty" json:"provenance,omitempty"`

	// Err is the resolver's error, if any. Nil on success. For
	// non-required slots, a non-nil Err does NOT cause Assemble to
	// fail; it is recorded here for downstream inspection.
	Err error `yaml:"-" json:"-"`
}

// ContextResult is the assembled output of a ContextRequest. It
// carries the per-slot results (in input order), the rendered
// composite body, the request-level provenance, and the
// budget-enforcement record.
type ContextResult struct {
	// Slots are the per-slot results in input order. The order
	// matches ContextRequest.Slots one-for-one — including slots
	// whose resolver returned an error (those carry a non-nil Err
	// and typically empty Content).
	Slots []SlotResult `yaml:"slots" json:"slots"`

	// Rendered is the final composed prompt body, deterministic
	// given the same ContextRequest and the same Resolver outputs.
	Rendered string `yaml:"rendered" json:"rendered"`

	// Provenance is the request-level attribution: caller input,
	// library version, request content hash, assembled timestamp.
	Provenance Provenance `yaml:"provenance" json:"provenance"`

	// Limits records what budget was enforced and which slots got
	// dropped or truncated.
	Limits LimitsApplied `yaml:"limits" json:"limits"`
}
