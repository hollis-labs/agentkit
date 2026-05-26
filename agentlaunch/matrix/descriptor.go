package matrix

import "github.com/hollis-labs/agentkit/agentlaunch"

// Descriptor is the result of looking up a legal (provider, runtime)
// pair. It carries everything the Prepare-stage shim needs to translate
// the abstract pair into concrete go-providers / go-agent-sessions
// handles WITHOUT the matrix package itself importing those modules.
//
// The Descriptor is a snapshot — modifying it does not feed back into
// the matrix's internal table. Lookup returns a fresh value per call.
type Descriptor struct {
	// ProviderID is the lowercased provider identifier (e.g. "claude",
	// "codex", "opencode") that was matched. Always equals the canonical
	// form, regardless of the case used in the input ProviderSpec.ID.
	ProviderID string

	// Runtime is the agentlaunch.RuntimeKind that was matched. Echoes the
	// input runtime unchanged.
	Runtime agentlaunch.RuntimeKind

	// Caps is the NATIVE capability declaration for this pair. The
	// Prepare-stage shim (CW-0005) translates this to
	// agentsessions.Capabilities when wiring the concrete Runtime; the
	// field names and semantics mirror the agent-sessions fields used by
	// the lifecycle dispatcher.
	Caps Capabilities

	// BootDirRenderer is the typed-string key identifying which
	// go-providers BootDir planter to invoke for this provider. The
	// Prepare-stage shim resolves the key against provider.PlantClaude /
	// PlantCodex / PlantOpencode (or their equivalents); the matrix does
	// not hold function pointers because that would force a go-providers
	// import here and create a dependency cycle for the catalog port.
	BootDirRenderer BootDirRenderer

	// BinaryName is the default executable the adapter spawns
	// ("claude", "codex", "opencode"). When the caller supplies a non-
	// empty ProviderSpec.Binary, Lookup substitutes that value verbatim;
	// PATH resolution is left to the preparer (consumers should not
	// assume BinaryName is absolute).
	BinaryName string
}

// Capabilities is the matrix's NATIVE capability declaration — a small
// mirror of the agentsessions.Capabilities fields the lifecycle
// dispatcher actually branches on. The Prepare-stage shim translates
// these into agentsessions.Capabilities at runtime; the matrix does not
// import agent-sessions so the catalog port can also depend on the
// matrix without inheriting that module's transitive deps.
//
// Lifecycle flags (PTY, StreamingStdio, JsonRpcStdio) are mutually
// exclusive — at most one is true for any Capabilities value the
// matrix returns. legalPairs is the single source of truth; the matrix
// does not validate the invariant at runtime because every row is
// hand-maintained.
//
// All fields default to false. The zero value is a valid Capabilities
// (the "subprocess-per-turn, no special caps" shape) — although in
// practice every legal pair also has BinaryRequired=true because all
// three known providers are CLI binaries.
type Capabilities struct {
	// PTY indicates the long-lived PTY runtime shape: a single child
	// runs for the whole session behind a pseudo-terminal master, and
	// SendInput writes bytes to the PTY. Implies Resize is meaningful.
	// Mutually exclusive with StreamingStdio and JsonRpcStdio.
	PTY bool

	// StreamingStdio indicates the long-lived non-PTY runtime shape
	// speaking NDJSON over stdin/stdout. Conversation state persists
	// across turns in the long-lived process. Mutually exclusive with
	// PTY and JsonRpcStdio.
	StreamingStdio bool

	// JsonRpcStdio indicates the long-lived non-PTY runtime shape
	// speaking JSON-RPC 2.0 over stdin/stdout. The runtime layers a
	// JSON-RPC client over raw stdio. Mutually exclusive with PTY and
	// StreamingStdio.
	JsonRpcStdio bool

	// ServeHTTP indicates the long-lived non-PTY runtime shape where
	// the child exposes an HTTP API with server-sent events (opencode
	// `serve`): the runtime spawns the child, discovers the bound port
	// from stdout, then attaches via the child's HTTP API for session +
	// message endpoints. Mutually exclusive with PTY, StreamingStdio,
	// and JsonRpcStdio.
	ServeHTTP bool

	// Resize indicates Session.Resize has an observable effect on the
	// spawned child. Only meaningful when PTY=true; non-PTY runtimes
	// no-op Resize regardless of this flag.
	Resize bool

	// BinaryRequired indicates Prepare must verify the underlying
	// executable exists on PATH (or resolves the configured Binary
	// override) before Start. False only for hypothetical in-process
	// providers; true for every legal pair in the current matrix.
	BinaryRequired bool
}

// BootDirRenderer is the typed-string key identifying which go-providers
// bootdir planter to invoke for a given provider. The matrix returns one
// of three constant values; CW-0005's shim resolves the key against the
// concrete planter functions exposed by go-providers (provider.PlantClaude,
// PlantCodex, PlantOpencode) without the matrix needing to import them.
//
// The string-key approach (instead of a function pointer) keeps the
// matrix import graph minimal: catalog port + matrix can share a
// dependency on agentlaunch without either of them dragging in
// go-providers.
type BootDirRenderer string

const (
	// BootDirRendererClaude maps to go-providers' Claude bootdir planter
	// (renders CLAUDE.md + boot.md + .mcp.json + settings.json into the
	// per-session bootdir).
	BootDirRendererClaude BootDirRenderer = "claude"

	// BootDirRendererCodex maps to go-providers' Codex bootdir planter.
	BootDirRendererCodex BootDirRenderer = "codex"

	// BootDirRendererOpencode maps to go-providers' Opencode bootdir
	// planter (renders agents/<name>.md + AGENTS.md).
	BootDirRendererOpencode BootDirRenderer = "opencode"
)

// String returns the underlying token (one of "claude", "codex",
// "opencode"). The zero value returns "".
func (b BootDirRenderer) String() string { return string(b) }

// Valid reports whether the receiver is one of the three declared
// BootDirRenderer constants. The zero value is not valid.
func (b BootDirRenderer) Valid() bool {
	switch b {
	case BootDirRendererClaude, BootDirRendererCodex, BootDirRendererOpencode:
		return true
	default:
		return false
	}
}
