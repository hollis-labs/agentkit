package agentcontext

import "errors"

// Sentinel errors returned by Validate methods, the default
// DefaultProvider.Assemble dispatcher, and the default Renderer. All
// sentinels are errors.Is-comparable; callers branch on them with
// errors.Is rather than string match.
//
// The errors document which field or slot failed; they do not carry
// the offending value itself. Validation that needs to report a
// specific value wraps the sentinel with
// fmt.Errorf("%w: %s", sentinel, value).
var (
	// ErrMissingSlotName is returned by SlotSpec.Validate and by
	// ContextRequest.Validate when a slot has an empty Name. Slot names
	// are load-bearing for determinism (they appear in section headers,
	// in per-slot provenance, and in the hash canonicalization) so the
	// empty name is rejected up front.
	ErrMissingSlotName = errors.New("agentcontext: missing slot name")

	// ErrDuplicateSlotName is returned by ContextRequest.Validate when
	// two SlotSpecs share the same Name. Names must be unique within a
	// request so per-slot provenance and rendering output are
	// unambiguous.
	ErrDuplicateSlotName = errors.New("agentcontext: duplicate slot name")

	// ErrUnknownSlotKind is returned by SlotSpec.Validate when
	// SlotSource.Kind is not one of the declared SlotSourceKind values,
	// and by DefaultProvider.Assemble when no Resolver is registered
	// for a slot's declared kind. The wrap value names the offending
	// kind.
	ErrUnknownSlotKind = errors.New("agentcontext: unknown slot kind")

	// ErrUnsafeSlotPath is returned by SlotSpec.Validate when a
	// path-bearing SlotSource (static_file, static_dir) declares a
	// path containing ".." segments. Path-safety is enforced
	// defensively at validation time so consumers cannot trick a
	// resolver into reading outside the intended root. Absolute paths
	// and tilde-prefixed paths are permitted at the contract layer;
	// concrete resolvers MAY tighten this further.
	ErrUnsafeSlotPath = errors.New("agentcontext: unsafe slot path")

	// ErrSlotRequiredAndEmpty is returned by DefaultProvider.Assemble
	// when a SlotSpec marked Required=true resolves to an empty Content
	// AND its resolver returned no error. Resolver-returned errors on
	// required slots are surfaced separately as ErrRequiredSlotFailed.
	ErrSlotRequiredAndEmpty = errors.New("agentcontext: required slot resolved empty")

	// ErrRequiredSlotFailed is returned by DefaultProvider.Assemble
	// when a SlotSpec marked Required=true had its Resolver return a
	// non-nil error. The error is wrapped so callers can use
	// errors.Is(err, ErrRequiredSlotFailed) AND errors.Is(err,
	// resolver-specific-sentinel) to branch on either layer.
	ErrRequiredSlotFailed = errors.New("agentcontext: required slot resolver failed")

	// ErrBudgetExhausted is returned by the default Renderer (and
	// SHOULD be returned by alternative Renderers) when the rendered
	// output would exceed Limits.MaxBytes AND truncation is not
	// permitted. The default policy IS to permit truncation — see
	// LimitsApplied for which slots were dropped or shortened — so
	// this sentinel is reserved for future strict-mode callers.
	ErrBudgetExhausted = errors.New("agentcontext: budget exhausted")

	// ErrMissingResolver is returned by NewProvider when the supplied
	// resolver map is nil. An empty (but non-nil) map is permitted —
	// it merely means Assemble will fail on any non-zero-kind slot
	// with ErrUnknownSlotKind, which is the intended behaviour for
	// test fixtures.
	ErrMissingResolver = errors.New("agentcontext: nil resolver map")

	// ErrMissingRenderer is returned by NewProvider when the supplied
	// Renderer is nil. The package ships DefaultRenderer for the
	// common case; callers must opt out explicitly by passing a
	// non-nil custom Renderer.
	ErrMissingRenderer = errors.New("agentcontext: nil renderer")

	// ErrCmdFailed is returned by the cmd resolver when the underlying
	// shell command exits with a non-zero status. The wrap value
	// includes the exit code and a captured tail of stderr for
	// debugging.
	ErrCmdFailed = errors.New("agentcontext: cmd resolver: non-zero exit")

	// ErrCmdTimeout is returned by the cmd resolver when the
	// underlying shell command did not complete within the
	// resolver-side timeout (CmdSource.Timeout, default 30s, capped at
	// 5m). Wraps context.DeadlineExceeded for callers that prefer to
	// branch on the stdlib sentinel.
	ErrCmdTimeout = errors.New("agentcontext: cmd resolver: timeout")

	// ErrHTTPStatus is returned by the http_text and http_json
	// resolvers when the server replies with a non-2xx status code.
	// The wrap value names the offending status code.
	ErrHTTPStatus = errors.New("agentcontext: http resolver: non-2xx status")

	// ErrHTTPRequest is returned by the http_text and http_json
	// resolvers when the request itself fails (DNS, connection refused,
	// I/O error, response-body read failure). Wraps the underlying
	// transport error.
	ErrHTTPRequest = errors.New("agentcontext: http resolver: request failed")

	// ErrJSONPathNotFound is returned by the http_json resolver when
	// the configured JSONPath expression navigates into a value that
	// does not exist. Empty JSONPath ("$" / "") never triggers this
	// sentinel — it returns the whole document.
	ErrJSONPathNotFound = errors.New("agentcontext: http_json resolver: jsonpath not found")

	// ErrInvalidJSON is returned by the http_json resolver when the
	// HTTP response body is not parseable as JSON.
	ErrInvalidJSON = errors.New("agentcontext: http_json resolver: invalid JSON body")

	// ErrResolverNotApplicable is returned by a resolver when the
	// supplied SlotSpec.Source.Kind does not match the resolver's
	// declared kind. This should never happen in practice — the
	// dispatcher routes by kind — but the sentinel exists so a
	// misregistered resolver fails loudly.
	ErrResolverNotApplicable = errors.New("agentcontext: resolver kind mismatch")

	// ErrSkillMissingName is returned by skills.Skill.Validate (and by
	// extension skills.Parse) when a skill frontmatter omits the
	// required name/slug pair. A skill without a stable name cannot be
	// looked up deterministically and is rejected at parse time.
	ErrSkillMissingName = errors.New("agentcontext: skill missing name")

	// ErrSkillMissingDescription is returned by skills.Skill.Validate
	// when a skill frontmatter omits the required description field.
	// The description anchors the rendered skill_index slot — emitting
	// trigger-only lines would defeat the purpose — so the field is
	// required at the contract layer.
	ErrSkillMissingDescription = errors.New("agentcontext: skill missing description")

	// ErrSkillNoFrontmatter is returned by skills.Parse when the
	// supplied bytes do not begin with a "---" YAML frontmatter
	// delimiter. A skill file without frontmatter is treated as a
	// parse failure — the discovery layer records the failure per
	// file and continues with the rest of the layer.
	ErrSkillNoFrontmatter = errors.New("agentcontext: skill missing frontmatter")

	// ErrSkillInvalidFrontmatter is returned by skills.Parse when the
	// frontmatter delimiters are present but the enclosed body does
	// not parse as YAML. Wraps the underlying yaml decoder error so
	// callers can surface the line/column.
	ErrSkillInvalidFrontmatter = errors.New("agentcontext: skill invalid frontmatter")

	// ErrSkillNotFound is returned by skills.Index.Get when the
	// requested skill name is not present in the index, and by the
	// skill_index slot resolver when Required=true AND zero skills
	// resolved. The latter case wraps ErrSkillNotFound under
	// ErrRequiredSlotFailed via the dispatcher.
	ErrSkillNotFound = errors.New("agentcontext: skill not found")

	// ErrSkillRootMissing is returned by skills.Discover when a
	// configured Layer.Root does not exist AND
	// DiscoveryConfig.StrictMissingRoot is true. The default behaviour
	// is silent skip — missing roots are tolerated so a portable boot
	// profile that references both ~/.tether/skills and ~/.nanite/skills
	// works on a host where only one is present.
	ErrSkillRootMissing = errors.New("agentcontext: skill discovery root missing")
)
