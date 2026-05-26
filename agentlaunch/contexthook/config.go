package contexthook

import (
	"github.com/hollis-labs/agentkit/agentcontext"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// Config tunes the contexthook adapter. The zero value is a no-op hook:
// it returns an empty bootPrompt and plants nothing — matching the
// agentlaunch contract for "no ContextHook registered".
//
// Callers SHOULD supply a SlotExtractor; without one, the adapter has no
// slots to assemble and the hook is a no-op.
type Config struct {
	// SlotExtractor turns a CompiledLaunch into the ordered slot list
	// the agentcontext provider assembles. Nil means "no slots" — the
	// hook becomes a no-op.
	//
	// TODO (CW-20260515-0010 follow-up): Phase 1's BootProfileInline
	// has no Slots field yet. Once BootProfileInline.Slots is added in
	// a follow-up Phase 1 patch, ship a default extractor here that
	// reads c.Plan.BootProfile.Inline.Slots when SlotExtractor is nil.
	// Until then, every caller MUST supply an extractor (or accept the
	// no-op behaviour).
	SlotExtractor func(*agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error)

	// ProvenanceFor returns the request-level ProvenanceInput the
	// adapter forwards into agentcontext.ContextRequest.Provenance.
	// Nil falls back to the built-in default, which copies the
	// compiled plan's Metadata.Labels into ProvenanceInput.Extra.
	ProvenanceFor func(*agentlaunch.CompiledLaunch) agentcontext.ProvenanceInput

	// Limits caps the rendered context. Zero values mean unlimited along
	// that axis — see agentcontext.Limits.
	Limits agentcontext.Limits

	// PlantArtifacts, when true, writes each non-empty SlotResult's
	// content to <bootDir>/context/<slot-name>.txt for debugging and
	// boot-prompt-by-reference workflows. Default false — most callers
	// only consume the rendered bootPrompt.
	//
	// Filenames sanitise slot names to a safe subset: A-Z a-z 0-9 _ .
	// Other characters become underscores. The renderer's truncation
	// applies before planting, so a truncated slot is planted at its
	// truncated length.
	PlantArtifacts bool
}
