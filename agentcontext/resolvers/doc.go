// Package resolvers ships the app-neutral concrete Resolver
// implementations registered with agentcontext's DefaultProvider.
//
// Each resolver in this package corresponds to one
// agentcontext.SlotSourceKind and is a pure SlotSpec → SlotResult
// function (modulo external I/O for cmd / http_* / file kinds). They
// are intentionally narrow: no dependency on agentlaunch,
// go-providers, Tether, Torque, or Nanite — only the agentcontext
// contract, its skills subpackage, and the Go standard library.
//
// # Resolvers shipped here
//
//   - static_file: read a single file (StaticFileResolver)
//   - static_dir:  glob a directory, concat by filename (StaticDirResolver)
//   - inline:      copy SlotSource.Inline.Content verbatim (InlineResolver)
//   - cmd:         run `sh -c <Run>` with timeout (CmdResolver)
//   - http_text:   GET (or specified method) and return body (HTTPTextResolver)
//   - http_json:   GET, parse as JSON, apply simple JSONPath subset (HTTPJSONResolver)
//   - role_summary: read a role markdown file with default byte cap
//     (RoleSummaryResolver)
//
// The skill_index resolver (SkillIndexResolver, over the sibling skills
// package's layered discovery) also lives here but is NOT included in
// Default(); WithSkillIndex adds it.
//
// # Registration
//
// Default() returns a populated map ready to hand to
// agentcontext.NewProvider:
//
//	p, err := agentcontext.NewProvider(resolvers.Default(), agentcontext.DefaultRenderer{})
//
// For all eight kinds, wrap it: resolvers.WithSkillIndex(resolvers.Default()).
//
// Callers that need to wire only a subset (e.g. an air-gapped runtime
// that forbids HTTP) construct their own map by calling the
// per-resolver constructors directly.
package resolvers
