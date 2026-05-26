package agentcontext

// ContextRequest is the declarative input to the assembly pipeline.
// It carries the ordered list of typed SlotSpecs, the byte/token
// budgets, the workdir against which workdir-relative resolver paths
// are resolved, and the caller-supplied request-level provenance.
//
// A ContextRequest is value-typed and shareable across goroutines.
// Maps and slices inside it are NOT defensively copied — callers
// must not mutate them after the request is handed to Assemble.
//
// Validate enforces field-shape correctness only: slot names are
// non-empty and unique, slot kinds are known, path-bearing slots
// have no ".." segments. Validate deliberately does NOT enforce
// resolver availability — that check lives inside Assemble where
// the resolver map is known.
type ContextRequest struct {
	// Slots is the ordered list of slot specifications. The order
	// is load-bearing: the default Renderer emits sections in this
	// order, byte-budget truncation drops trailing slots first, and
	// HashRequest canonicalizes slots positionally.
	Slots []SlotSpec `yaml:"slots" json:"slots"`

	// Limits caps the rendered output. Zero values mean "unlimited
	// along that axis".
	Limits Limits `yaml:"limits,omitempty" json:"limits,omitempty"`

	// Workdir is the base directory against which workdir-relative
	// resolver paths (CmdSource.CWD, StaticFileSource.Path,
	// StaticDirSource.Path) are resolved. Empty defers to the
	// resolver-side default (typically os.Getwd at resolve time).
	Workdir string `yaml:"workdir,omitempty" json:"workdir,omitempty"`

	// Provenance is the caller-supplied request-level attribution.
	// Threaded through to ContextResult.Provenance.Input unchanged.
	Provenance ProvenanceInput `yaml:"provenance,omitempty" json:"provenance,omitempty"`
}

// Validate runs field-shape correctness checks on the request.
// Returns one of the package-level sentinel errors on first failure
// — use errors.Is to branch on the failure mode.
//
// The order of checks below is stable so consumers writing tests
// against the sentinel-first-returned can rely on it; reorder with
// care.
//
// Validate does NOT verify that a Resolver is registered for every
// slot kind — that is Assemble's responsibility, since the resolver
// map is constructor-supplied and not part of the request shape.
func (r ContextRequest) Validate() error {
	seen := make(map[string]struct{}, len(r.Slots))
	for _, slot := range r.Slots {
		if err := slot.Validate(); err != nil {
			return err
		}
		if _, dup := seen[slot.Name]; dup {
			return ErrDuplicateSlotName
		}
		seen[slot.Name] = struct{}{}
	}
	return nil
}
