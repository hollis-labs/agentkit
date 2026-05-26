package agentcontext

// Version is the go-agent-context library version embedded in
// Provenance.LibraryVersion. Bumped manually when the public surface
// changes; consumers should NOT depend on the exact value at runtime.
//
// Held in a typed string constant (rather than the conventional
// pkg-level Version variable) so it cannot be reassigned by a caller.
const Version = "v0.1.0-dev"
