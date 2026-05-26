package agentlaunch

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Boot Assembly Spec engine (S4.1).
//
// This file BUILDS ON the locked S1.2 BootSpec types (BootSpec, BootInput,
// VarSpec, ...). It does not redefine them. It adds the missing piece: a
// parameterized template BODY and a deterministic renderer that turns one
// AssemblySpec plus a bag of inputs into byte-identical output.
//
// The whole point of S4.1: one Spec, many invocations via inputs. A single
// AssemblySpec replaces the frozen-tuple file explosion (66 launches + 60
// boot-profiles) — each historical file becomes one inputs map handed to
// the same Spec.
//
// D3 is honored: AssemblySpec wraps a BootSpec, which is a parameterized
// BLUEPRINT. It is never conflated with RuntimeBinding (the hot-path
// runtime record). The renderer produces blueprint material only.

// AssemblySpec is a BootSpec extended with a merge-tag template body. The
// embedded BootSpec carries the locked S1.2 contract (typed inputs, files,
// vars, injections, runtime); Template is the additive S4.1 surface that
// makes the spec renderable.
//
// AssemblySpec is the unit that kills the frozen-tuple file explosion:
// rather than one file per (agent × project × ...) tuple, callers persist
// one AssemblySpec and supply the differing values as Inputs at render
// time.
type AssemblySpec struct {
	// BootSpec is the embedded locked S1.2 blueprint contract.
	BootSpec `yaml:",inline" json:",inline"`

	// Template is the merge-tag body rendered by the engine. It uses the
	// Hadron-blueprint merge-tag shape: {{ inputs.<name> }} projects a
	// typed input, {{ vars.<name> }} projects a resolved var. Whitespace
	// inside the braces is tolerated. Literal braces are escaped by
	// doubling: "{{{{" renders as a literal "{{" and "}}}}" renders as a
	// literal "}}".
	Template string `yaml:"template,omitempty" json:"template,omitempty"`
}

// Validate enforces the embedded BootSpec contract plus the additive
// template-body contract: every merge tag in the template must reference a
// declared input or var.
func (a AssemblySpec) Validate() error {
	if err := a.BootSpec.Validate(); err != nil {
		return err
	}
	tags, err := parseMergeTags(a.Template)
	if err != nil {
		return err
	}
	inputNames := make(map[string]struct{}, len(a.Inputs))
	for i := range a.Inputs {
		inputNames[a.Inputs[i].Name] = struct{}{}
	}
	varNames := make(map[string]struct{}, len(a.Vars))
	for i := range a.Vars {
		varNames[a.Vars[i].Name] = struct{}{}
	}
	for _, t := range tags {
		switch t.scope {
		case mergeScopeInput:
			if _, ok := inputNames[t.name]; !ok {
				return fmt.Errorf("%w: inputs.%s", ErrAssemblyUnknownMergeTag, t.name)
			}
		case mergeScopeVar:
			if _, ok := varNames[t.name]; !ok {
				return fmt.Errorf("%w: vars.%s", ErrAssemblyUnknownMergeTag, t.name)
			}
		}
	}
	return nil
}

// MissingPolicy selects how Render handles a missing required input or
// declared-but-unresolved var. Zero value is PolicyError (strict): the
// default for a non-interactive front-end. PolicyCollect is the opt-in
// soft-fail mode for human-driven front-ends that gather missing values
// and re-render.
type MissingPolicy int

const (
	// PolicyError is the strict, fail-loud policy. A required input with
	// no default and no supplied value (or a declared var absent from
	// req.Vars) makes Render return ErrAssemblyMissingRequiredInput.
	// PolicyError is the zero value, so an empty RenderRequest gets
	// strict semantics without explicit opt-in.
	PolicyError MissingPolicy = iota

	// PolicyCollect is the soft-fail policy for interactive front-ends.
	// Missing values do not error; instead Render returns the body with
	// each missing tag collapsed to the empty string and populates
	// RenderResult.Missing so the caller can collect the values and
	// re-render.
	PolicyCollect
)

// RenderRequest is the input bag for one AssemblySpec render.
type RenderRequest struct {
	// Inputs are the caller-supplied values keyed by BootInput.Name. A
	// value supplied here overrides the input's declared Default.
	Inputs map[string]any

	// Vars are the already-resolved derived-var values keyed by
	// VarSpec.Name. S4.1 renders vars; it does not resolve them — var
	// resolution is the sibling S4.2 surface. A var referenced by the
	// template but absent here is reported via RenderResult.Missing.
	Vars map[string]any

	// OnMissing selects missing-required-input handling. Zero value =
	// PolicyError (strict).
	OnMissing MissingPolicy
}

// RenderResult is the deterministic output of an AssemblySpec render.
type RenderResult struct {
	// Body is the rendered template body. It is byte-identical for
	// identical (AssemblySpec, RenderRequest) pairs.
	Body string

	// ResolvedInputs is the effective input value map after applying
	// defaults — the canonical record of what was rendered.
	ResolvedInputs map[string]any

	// Missing lists declared-required inputs (and template-referenced
	// vars) that had no value. For PolicyCollect this is the collect-me
	// list; for PolicyError a non-empty Missing means Render returned an
	// error and Body is empty.
	Missing []string
}

// Render deterministically renders the AssemblySpec template body against
// req. Same (spec, req) always produces a byte-identical RenderResult.Body.
//
// Input resolution order, per the locked Hadron-blueprint input contract:
//  1. a value supplied in req.Inputs wins;
//  2. otherwise the BootInput.Default is used;
//  3. otherwise the input is unset. If it is Required, it is collected
//     (interactive) or errors (autonomous).
//
// Render does not resolve derived vars. req.Vars supplies already-resolved
// var values; an unresolved var referenced by the template is reported in
// Missing exactly like a missing required input.
//
// req.OnMissing controls missing-value handling:
//   - PolicyError (default, zero value): a missing required input or var
//     makes Render return ErrAssemblyMissingRequiredInput.
//   - PolicyCollect: missing values populate RenderResult.Missing and
//     Render returns the body with each missing tag collapsed to the
//     empty string. Used by interactive front-ends that collect values.
func (a AssemblySpec) Render(req RenderRequest) (RenderResult, error) {
	if err := a.BootSpec.Validate(); err != nil {
		return RenderResult{}, err
	}

	resolved := make(map[string]any, len(a.Inputs))
	var missing []string

	for i := range a.Inputs {
		in := a.Inputs[i]
		if v, ok := req.Inputs[in.Name]; ok {
			resolved[in.Name] = v
			continue
		}
		if in.Default != nil {
			resolved[in.Name] = in.Default
			continue
		}
		if in.Required {
			missing = append(missing, "inputs."+in.Name)
		}
	}

	tags, err := parseMergeTags(a.Template)
	if err != nil {
		return RenderResult{}, err
	}

	for _, t := range tags {
		switch t.scope {
		case mergeScopeInput:
			declared := false
			for i := range a.Inputs {
				if a.Inputs[i].Name == t.name {
					declared = true
					break
				}
			}
			if !declared {
				return RenderResult{}, fmt.Errorf("%w: inputs.%s", ErrAssemblyUnknownMergeTag, t.name)
			}
		case mergeScopeVar:
			declared := false
			for i := range a.Vars {
				if a.Vars[i].Name == t.name {
					declared = true
					break
				}
			}
			if !declared {
				return RenderResult{}, fmt.Errorf("%w: vars.%s", ErrAssemblyUnknownMergeTag, t.name)
			}
			if _, ok := req.Vars[t.name]; !ok {
				key := "vars." + t.name
				if !contains(missing, key) {
					missing = append(missing, key)
				}
			}
		}
	}

	// Deterministic ordering: declared-input order first, then any
	// remaining (var) entries sorted lexically. This keeps Missing stable
	// across runs regardless of Go map iteration order.
	sortMissing(a.Inputs, missing)

	if len(missing) > 0 && req.OnMissing == PolicyError {
		return RenderResult{ResolvedInputs: resolved, Missing: missing},
			fmt.Errorf("%w: %s", ErrAssemblyMissingRequiredInput, strings.Join(missing, ", "))
	}

	body, err := renderTemplate(a.Template, resolved, req.Vars)
	if err != nil {
		return RenderResult{ResolvedInputs: resolved, Missing: missing}, err
	}

	return RenderResult{Body: body, ResolvedInputs: resolved, Missing: missing}, nil
}

// CollectableInputs returns the declared-required inputs that have neither
// a supplied value nor a default — the set an interactive front-end must
// collect before an autonomous render would succeed. The result is in
// declared-input order.
func (a AssemblySpec) CollectableInputs(supplied map[string]any) []BootInput {
	var out []BootInput
	for i := range a.Inputs {
		in := a.Inputs[i]
		if !in.Required {
			continue
		}
		if _, ok := supplied[in.Name]; ok {
			continue
		}
		if in.Default != nil {
			continue
		}
		out = append(out, in)
	}
	return out
}

// mergeScope identifies which value namespace a merge tag projects.
type mergeScope int

const (
	mergeScopeInput mergeScope = iota
	mergeScopeVar
)

// mergeTag is one parsed {{ scope.name }} occurrence.
type mergeTag struct {
	scope mergeScope
	name  string
	// start/end are byte offsets of the full tag (including braces) in
	// the source template.
	start int
	end   int
}

// parseMergeTags scans a template body for {{ ... }} merge tags. Doubled
// braces escape literals: "{{{{" is a literal "{{" and "}}}}" is a literal
// "}}" — neither opens nor closes a tag. Every tag must be of the form
// inputs.<name> or vars.<name>.
func parseMergeTags(tmpl string) ([]mergeTag, error) {
	var tags []mergeTag
	for i := 0; i+1 < len(tmpl); {
		if tmpl[i] != '{' || tmpl[i+1] != '{' {
			i++
			continue
		}
		// Escaped literal "{{{{" -> skip both pairs.
		if i+3 < len(tmpl) && tmpl[i+2] == '{' && tmpl[i+3] == '{' {
			i += 4
			continue
		}
		// Find the closing "}}", skipping any escaped "}}}}" pair.
		closeAt := findTagClose(tmpl, i+2)
		if closeAt < 0 {
			return nil, fmt.Errorf("%w: unterminated tag at offset %d", ErrAssemblyMalformedTemplate, i)
		}
		inner := tmpl[i+2 : closeAt]
		fullEnd := closeAt + 2
		scope, name, perr := parseTagBody(inner)
		if perr != nil {
			return nil, fmt.Errorf("%w: %v at offset %d", ErrAssemblyMalformedTemplate, perr, i)
		}
		tags = append(tags, mergeTag{scope: scope, name: name, start: i, end: fullEnd})
		i = fullEnd
	}
	return tags, nil
}

// findTagClose returns the byte offset of the closing "}}" that ends a tag
// opened before from, skipping escaped "}}}}" pairs. It returns -1 when no
// unescaped close exists.
func findTagClose(tmpl string, from int) int {
	for j := from; j+1 < len(tmpl); j++ {
		if tmpl[j] != '}' || tmpl[j+1] != '}' {
			continue
		}
		// Escaped "}}}}" -> literal, not a tag close. Skip both pairs.
		if j+3 < len(tmpl) && tmpl[j+2] == '}' && tmpl[j+3] == '}' {
			j += 3
			continue
		}
		return j
	}
	return -1
}

// parseTagBody parses the text between the braces of a merge tag.
func parseTagBody(inner string) (mergeScope, string, error) {
	body := strings.TrimSpace(inner)
	if body == "" {
		return 0, "", errors.New("empty tag")
	}
	dot := strings.IndexByte(body, '.')
	if dot < 0 {
		return 0, "", fmt.Errorf("tag %q missing scope.name form", body)
	}
	scopeTok := body[:dot]
	name := body[dot+1:]
	if name == "" {
		return 0, "", fmt.Errorf("tag %q missing name", body)
	}
	if !isMergeName(name) {
		return 0, "", fmt.Errorf("tag name %q is not a safe identifier", name)
	}
	switch scopeTok {
	case "inputs":
		return mergeScopeInput, name, nil
	case "vars":
		return mergeScopeVar, name, nil
	default:
		return 0, "", fmt.Errorf("unknown tag scope %q (want inputs or vars)", scopeTok)
	}
}

// isMergeName reports whether s is a safe merge-tag identifier:
// [A-Za-z0-9_-]+. It deliberately rejects dots so nested paths cannot
// smuggle traversal into a lookup key.
func isMergeName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// renderTemplate substitutes every merge tag in tmpl with its value.
// "{{{{" collapses to a literal "{{". A tag whose value is absent renders
// as the empty string — the Missing list is the caller's signal, not a
// render-time panic.
func renderTemplate(tmpl string, inputs, vars map[string]any) (string, error) {
	tags, err := parseMergeTags(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(len(tmpl))
	cursor := 0
	for _, t := range tags {
		// Emit literal text before the tag, collapsing "{{{{" escapes.
		b.WriteString(unescapeBraces(tmpl[cursor:t.start]))
		var val any
		switch t.scope {
		case mergeScopeInput:
			val = inputs[t.name]
		case mergeScopeVar:
			val = vars[t.name]
		}
		b.WriteString(stringifyValue(val))
		cursor = t.end
	}
	b.WriteString(unescapeBraces(tmpl[cursor:]))
	return b.String(), nil
}

// unescapeBraces collapses the doubled-brace literal escapes: "{{{{" -> "{{"
// and "}}}}" -> "}}".
func unescapeBraces(s string) string {
	if strings.Contains(s, "{{{{") {
		s = strings.ReplaceAll(s, "{{{{", "{{")
	}
	if strings.Contains(s, "}}}}") {
		s = strings.ReplaceAll(s, "}}}}", "}}")
	}
	return s
}

// stringifyValue renders a resolved value deterministically. nil renders
// as the empty string; everything else uses a stable fmt verb so the same
// value always produces the same bytes.
func stringifyValue(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}

// contains reports whether s is in xs.
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// sortMissing orders the Missing slice deterministically: declared-input
// entries first in declaration order, then remaining (var) entries sorted
// lexically.
func sortMissing(inputs []BootInput, missing []string) {
	rank := make(map[string]int, len(inputs))
	for i := range inputs {
		rank["inputs."+inputs[i].Name] = i
	}
	sort.SliceStable(missing, func(i, j int) bool {
		ri, iIn := rank[missing[i]]
		rj, jIn := rank[missing[j]]
		switch {
		case iIn && jIn:
			return ri < rj
		case iIn:
			return true
		case jIn:
			return false
		default:
			return missing[i] < missing[j]
		}
	})
}
