package agentcontext

import (
	"strings"
	"time"
)

// SlotSourceKind names a kind of slot source. Each kind has a
// corresponding zero-value-safe parameter sub-struct on SlotSource;
// the kind tag tells the dispatcher which sub-struct to consult and
// which Resolver to invoke.
//
// The set is closed and deliberately small. New kinds are added by
// a library-level change (new SlotSourceKind constant + new
// sub-struct on SlotSource) — they cannot be plugged in by external
// consumers. Consumers extend the system by registering Resolver
// implementations for existing kinds, not by inventing new kinds.
type SlotSourceKind string

// SlotSourceKind constants. The string form is the wire form used in
// YAML / JSON catalog entries; it matches the `type:` field in the
// nanite.backend.main.yaml sample boot profile.
const (
	// SlotSourceKindStaticFile reads the byte content of a single
	// file at SlotSource.StaticFile.Path. Resolved content is the
	// file body verbatim.
	SlotSourceKindStaticFile SlotSourceKind = "static_file"

	// SlotSourceKindStaticDir concatenates the byte content of every
	// file in SlotSource.StaticDir.Path that matches the optional
	// Glob (e.g. "*.md"). Order is filename-sorted for determinism.
	SlotSourceKindStaticDir SlotSourceKind = "static_dir"

	// SlotSourceKindInline returns SlotSource.Inline.Content
	// verbatim. The caller has already resolved the content (e.g. by
	// running a Vanta memory_recall and stuffing the result inline).
	SlotSourceKindInline SlotSourceKind = "inline"

	// SlotSourceKindCmd executes SlotSource.Cmd.Run via the user's
	// shell and captures stdout. Non-zero exit status surfaces as a
	// resolver error; stderr is captured into provenance for
	// debugging.
	SlotSourceKindCmd SlotSourceKind = "cmd"

	// SlotSourceKindHTTPText issues an HTTP GET to
	// SlotSource.HTTPText.URL and returns the response body as text.
	// Non-2xx status codes surface as resolver errors.
	SlotSourceKindHTTPText SlotSourceKind = "http_text"

	// SlotSourceKindHTTPJSON issues an HTTP GET to
	// SlotSource.HTTPJSON.URL, expects an application/json response,
	// and either pretty-prints the body or extracts a JSONPath
	// expression. Concrete formatting policy lives in the resolver.
	SlotSourceKindHTTPJSON SlotSourceKind = "http_json"

	// SlotSourceKindRoleSummary loads a role markdown body from
	// SlotSource.RoleSummary.Path (typically
	// ~/.nanite/roles/<type>/<name>.md) and optionally extracts a
	// summary section. Concrete summarization policy lives in the
	// resolver.
	SlotSourceKindRoleSummary SlotSourceKind = "role_summary"

	// SlotSourceKindSkillIndex enumerates skills discovered under a
	// caller-supplied root and emits a deterministic table of
	// (name, description, trigger). The discovery layer (Subagent C)
	// owns the on-disk skill model; this contract only commits to the
	// kind tag.
	SlotSourceKindSkillIndex SlotSourceKind = "skill_index"
)

// Valid reports whether k is one of the declared SlotSourceKind
// constants. Empty kinds are NOT valid — every SlotSource must
// declare its kind explicitly.
func (k SlotSourceKind) Valid() bool {
	switch k {
	case SlotSourceKindStaticFile,
		SlotSourceKindStaticDir,
		SlotSourceKindInline,
		SlotSourceKindCmd,
		SlotSourceKindHTTPText,
		SlotSourceKindHTTPJSON,
		SlotSourceKindRoleSummary,
		SlotSourceKindSkillIndex:
		return true
	}
	return false
}

// SlotSource is the typed parameter bundle for a single slot. It uses
// the Go-idiomatic tagged-union pattern: Kind tags which sub-struct
// the dispatcher consults; the remaining sub-structs are present but
// zero-valued.
//
// All sub-structs are zero-value safe — a freshly-constructed
// SlotSource{Kind: SlotSourceKindInline} carries a usable
// (empty-content) Inline sub-struct without explicit initialization.
//
// The struct intentionally embeds every kind's parameter block
// inline rather than via interface{} so the type is fully marshalable
// to JSON / YAML without custom Unmarshaler boilerplate and so
// concrete resolvers can read their parameters with a single field
// access.
type SlotSource struct {
	// Kind tags which sub-struct below the dispatcher consults.
	// Must be one of the SlotSourceKind constants — checked by
	// SlotSpec.Validate.
	Kind SlotSourceKind `yaml:"type" json:"kind"`

	// StaticFile parameters; consulted when Kind ==
	// SlotSourceKindStaticFile.
	StaticFile StaticFileSource `yaml:"static_file,omitempty" json:"static_file,omitempty"`

	// StaticDir parameters; consulted when Kind ==
	// SlotSourceKindStaticDir.
	StaticDir StaticDirSource `yaml:"static_dir,omitempty" json:"static_dir,omitempty"`

	// Inline parameters; consulted when Kind ==
	// SlotSourceKindInline.
	Inline InlineSource `yaml:"inline,omitempty" json:"inline,omitempty"`

	// Cmd parameters; consulted when Kind == SlotSourceKindCmd.
	Cmd CmdSource `yaml:"cmd,omitempty" json:"cmd,omitempty"`

	// HTTPText parameters; consulted when Kind ==
	// SlotSourceKindHTTPText.
	HTTPText HTTPTextSource `yaml:"http_text,omitempty" json:"http_text,omitempty"`

	// HTTPJSON parameters; consulted when Kind ==
	// SlotSourceKindHTTPJSON.
	HTTPJSON HTTPJSONSource `yaml:"http_json,omitempty" json:"http_json,omitempty"`

	// RoleSummary parameters; consulted when Kind ==
	// SlotSourceKindRoleSummary.
	RoleSummary RoleSummarySource `yaml:"role_summary,omitempty" json:"role_summary,omitempty"`

	// SkillIndex parameters; consulted when Kind ==
	// SlotSourceKindSkillIndex.
	SkillIndex SkillIndexSource `yaml:"skill_index,omitempty" json:"skill_index,omitempty"`
}

// StaticFileSource parameters for SlotSourceKindStaticFile.
type StaticFileSource struct {
	// Path is the file path to read. May be absolute, tilde-prefixed
	// (~/...), or workdir-relative. Resolution is the resolver's job;
	// SlotSpec.Validate only rejects ".." segments as a defense.
	Path string `yaml:"path" json:"path"`
}

// StaticDirSource parameters for SlotSourceKindStaticDir.
type StaticDirSource struct {
	// Path is the directory path. Same resolution rules as
	// StaticFileSource.Path.
	Path string `yaml:"path" json:"path"`

	// Glob is an optional filename glob (e.g. "*.md"). Empty matches
	// all files (non-recursive).
	Glob string `yaml:"glob,omitempty" json:"glob,omitempty"`
}

// InlineSource parameters for SlotSourceKindInline.
type InlineSource struct {
	// Content is the pre-resolved slot body. The resolver returns it
	// verbatim.
	Content string `yaml:"content" json:"content"`
}

// CmdSource parameters for SlotSourceKindCmd.
type CmdSource struct {
	// Run is the shell command line to execute. Concrete resolvers
	// typically invoke `sh -c <Run>`; the exact dispatch is a
	// resolver-side concern.
	Run string `yaml:"run" json:"run"`

	// Timeout caps execution. Zero defaults to a resolver-defined
	// safe maximum (concrete resolvers SHOULD document their
	// default). A SlotSpec.Validate-time check does NOT enforce a
	// minimum or maximum.
	Timeout time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// CWD is the working directory for the command. Empty defers to
	// ContextRequest.Workdir.
	CWD string `yaml:"cwd,omitempty" json:"cwd,omitempty"`
}

// HTTPTextSource parameters for SlotSourceKindHTTPText.
type HTTPTextSource struct {
	// URL to GET. Must be a full URL including scheme; the resolver
	// rejects relative URLs.
	URL string `yaml:"url" json:"url"`

	// Timeout caps the request. Zero defaults to a resolver-defined
	// safe maximum.
	Timeout time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Headers are extra request headers to attach (e.g. an auth
	// token). Map order does not affect determinism — the hash
	// canonicalizer sorts keys.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

// HTTPJSONSource parameters for SlotSourceKindHTTPJSON.
type HTTPJSONSource struct {
	// URL to GET (same rules as HTTPTextSource.URL).
	URL string `yaml:"url" json:"url"`

	// Timeout caps the request. Zero defaults to a resolver-defined
	// safe maximum.
	Timeout time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Headers same as HTTPTextSource.Headers.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	// JSONPath is an optional extraction expression. Empty means
	// "pretty-print the full body". Expression syntax is a
	// resolver-side concern; this contract treats the field as opaque.
	JSONPath string `yaml:"jsonpath,omitempty" json:"jsonpath,omitempty"`
}

// RoleSummarySource parameters for SlotSourceKindRoleSummary.
type RoleSummarySource struct {
	// Path is the role markdown file (typically
	// ~/.nanite/roles/<type>/<name>.md).
	Path string `yaml:"path" json:"path"`

	// Section optionally names a heading within the role file to
	// extract. Empty means "load the full body".
	Section string `yaml:"section,omitempty" json:"section,omitempty"`
}

// SkillIndexSource parameters for SlotSourceKindSkillIndex.
type SkillIndexSource struct {
	// Roots are the directories under which the resolver discovers
	// skills. The discovery model is Subagent C's territory; this
	// contract only carries the field shape.
	Roots []string `yaml:"roots,omitempty" json:"roots,omitempty"`

	// Limit caps the number of skills emitted. Zero means
	// "resolver-defined default".
	Limit int `yaml:"limit,omitempty" json:"limit,omitempty"`

	// PreferTriggers ranks skills whose trigger keywords match any
	// substring in this list higher in the emitted table. Order
	// within the list is meaningful (earlier = higher boost).
	PreferTriggers []string `yaml:"prefer_triggers,omitempty" json:"prefer_triggers,omitempty"`
}

// SlotSpec is one entry in a ContextRequest.Slots list. It carries
// the slot's user-facing Name (used in section headers and per-slot
// provenance), the Section heading prefix the default Renderer
// emits, a Required flag (errors out if the slot resolves empty),
// and the typed Source.
type SlotSpec struct {
	// Name is the slot identifier — appears in per-slot provenance,
	// hash canonicalization, and (as a fallback) in the rendered
	// section header. Must be unique within a request; checked by
	// ContextRequest.Validate.
	Name string `yaml:"name" json:"name"`

	// Section is the heading prefix the default Renderer emits ahead
	// of the slot body (e.g. "## §1 — Role"). Empty means "use Name
	// as the section heading". Multi-line section values are
	// preserved verbatim.
	Section string `yaml:"section,omitempty" json:"section,omitempty"`

	// Required marks the slot as load-bearing. If a required slot
	// resolves to empty Content (or its Resolver returns an error),
	// DefaultProvider.Assemble surfaces ErrSlotRequiredAndEmpty or
	// ErrRequiredSlotFailed respectively. Non-required slots are
	// silently skipped on resolver failure (the failure is recorded
	// in SlotResult.Err for inspection).
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`

	// Source is the typed parameter bundle.
	Source SlotSource `yaml:"source" json:"source"`
}

// Validate runs field-shape correctness checks on the slot. Returns
// one of the package-level sentinel errors on first failure — use
// errors.Is to branch on the failure mode.
//
// The order of checks below is stable so consumers writing tests
// against the sentinel-first-returned can rely on it.
func (s SlotSpec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return ErrMissingSlotName
	}
	if !s.Source.Kind.Valid() {
		return ErrUnknownSlotKind
	}
	// Path-safety for path-bearing kinds. We reject ".." segments
	// defensively; absolute and tilde-prefixed paths are permitted
	// at the contract layer.
	switch s.Source.Kind {
	case SlotSourceKindStaticFile:
		if hasParentSegment(s.Source.StaticFile.Path) {
			return ErrUnsafeSlotPath
		}
	case SlotSourceKindStaticDir:
		if hasParentSegment(s.Source.StaticDir.Path) {
			return ErrUnsafeSlotPath
		}
	case SlotSourceKindRoleSummary:
		if hasParentSegment(s.Source.RoleSummary.Path) {
			return ErrUnsafeSlotPath
		}
	}
	return nil
}

// hasParentSegment reports whether p contains a literal ".." path
// segment. We deliberately use a segment match (not a substring
// match) so paths like "foo..bar" / "..baz" embedded inside a
// filename are not falsely rejected.
func hasParentSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
