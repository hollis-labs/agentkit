package resolvers

import (
	"github.com/hollis-labs/agentkit/agentcontext"
)

// Default returns a map populated with the seven app-neutral
// resolvers shipped in this package:
//
//   - static_file  (StaticFileResolver)
//   - static_dir   (StaticDirResolver)
//   - inline       (InlineResolver)
//   - cmd          (CmdResolver)
//   - http_text    (HTTPTextResolver)
//   - http_json    (HTTPJSONResolver)
//   - role_summary (RoleSummaryResolver)
//
// The skill_index resolver is intentionally NOT included — that kind
// is owned by the skill-discovery layer (Subagent C / sibling
// subpackage), and the on-disk skill model is layered on top of the
// agentcontext contract. Callers that need skill_index should compose
// their own resolver map, e.g.:
//
//	res := resolvers.Default()
//	res[agentcontext.SlotSourceKindSkillIndex] = skill.NewIndexResolver(...)
//
// Each entry is constructed with its zero-config defaults. Callers
// who need to tune individual resolvers should build their own map
// using the per-resolver constructors (NewStaticDirResolver, etc.)
// and the functional options each exposes.
func Default() map[agentcontext.SlotSourceKind]agentcontext.Resolver {
	return map[agentcontext.SlotSourceKind]agentcontext.Resolver{
		agentcontext.SlotSourceKindStaticFile:  NewStaticFileResolver(),
		agentcontext.SlotSourceKindStaticDir:   NewStaticDirResolver(),
		agentcontext.SlotSourceKindInline:      NewInlineResolver(),
		agentcontext.SlotSourceKindCmd:         NewCmdResolver(),
		agentcontext.SlotSourceKindHTTPText:    NewHTTPTextResolver(),
		agentcontext.SlotSourceKindHTTPJSON:    NewHTTPJSONResolver(),
		agentcontext.SlotSourceKindRoleSummary: NewRoleSummaryResolver(),
	}
}

// WithSkillIndex augments an existing resolver map with the
// skill_index resolver and returns the (same) map for chaining. This
// is the opt-in pairing for Default() — callers that want the full
// eight-kind set do:
//
//	res := resolvers.WithSkillIndex(resolvers.Default())
//	p, _ := agentcontext.NewProvider(res, agentcontext.DefaultRenderer{})
//
// The skill_index resolver is intentionally kept OUT of Default()
// because the on-disk skill model (skills.DiscoveryConfig) is a
// non-trivial extension over the agentcontext core contract and
// some consumers will legitimately want to wire a custom resolver
// that talks to a network skill registry instead. The opt-in
// pairing keeps Default() pure-stdlib while making the common
// "give me everything" case a one-liner.
//
// The supplied options are forwarded to NewSkillIndexResolver — see
// WithSkillIndexDefaultLimit, WithSkillIndexStrictMissingRoot,
// WithSkillIndexRecursive, and WithSkillIndexFilePattern.
//
// If m is nil, a new empty map is allocated and returned.
func WithSkillIndex(m map[agentcontext.SlotSourceKind]agentcontext.Resolver, opts ...SkillIndexOption) map[agentcontext.SlotSourceKind]agentcontext.Resolver {
	if m == nil {
		m = map[agentcontext.SlotSourceKind]agentcontext.Resolver{}
	}
	m[agentcontext.SlotSourceKindSkillIndex] = NewSkillIndexResolver(opts...)
	return m
}
