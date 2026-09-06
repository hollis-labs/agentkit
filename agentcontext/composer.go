package agentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/artifact"
)

type ComposerOptions struct {
	Provider ContextProvider
}

type DefaultComposer struct {
	provider ContextProvider
}

func NewComposer(opts ComposerOptions) *DefaultComposer {
	return &DefaultComposer{provider: opts.Provider}
}

var _ Composer = (*DefaultComposer)(nil)

func (c *DefaultComposer) Compose(req ComposeRequest) (ResolvedComposition, error) {
	return c.ComposeContext(context.Background(), req)
}

func (c *DefaultComposer) ComposeContext(ctx context.Context, req ComposeRequest) (ResolvedComposition, error) {
	ordered, diagnostics, err := expandRecipes(req)
	if err != nil {
		return ResolvedComposition{}, err
	}
	allRules := make([]MergeRule, 0)
	for _, recipe := range ordered {
		allRules = append(allRules, recipe.MergeRules...)
	}
	merged := ResolvedComposition{
		ID:           firstNonEmpty(req.Recipe.ID, "composition"),
		RecipeID:     req.Recipe.ID,
		Extensions:   map[string]any{},
		Contributors: []Contributor{},
		Digests:      map[string]string{},
		Diagnostics:  diagnostics,
	}
	seenContributor := map[string]bool{}
	for _, recipe := range ordered {
		if err := ctx.Err(); err != nil {
			return ResolvedComposition{}, err
		}
		if recipe.ID != "" && !seenContributor[recipe.ID] {
			merged.Contributors = append(merged.Contributors, Contributor{
				ID:         recipe.ID,
				SourcePath: recipe.Provenance.LineageAlias,
				Revision:   recipe.Provenance.ProfileID,
			})
			seenContributor[recipe.ID] = true
		}
		merged.Digests["recipe:"+recipe.ID] = digestValue(recipe)
		if err := mergeDocuments(&merged, recipe.Documents, recipe.ID); err != nil {
			return ResolvedComposition{}, err
		}
		if err := mergeArtifacts(&merged, recipe.Artifacts); err != nil {
			return ResolvedComposition{}, err
		}
		if err := mergeExtensions(&merged, recipe.Extensions, allRules); err != nil {
			return ResolvedComposition{}, err
		}
		if len(recipe.Slots) > 0 {
			if c.provider == nil {
				return ResolvedComposition{}, fmt.Errorf("%w: slot provider required", ErrMissingResolver)
			}
			result, err := c.provider.Assemble(ctx, ContextRequest{Slots: recipe.Slots, Limits: Limits{}, Provenance: recipe.Provenance})
			if err != nil {
				return ResolvedComposition{}, err
			}
			doc := Document{ID: recipe.ID + ":slots", Path: "AGENTS.md"}
			for _, slot := range result.Slots {
				doc.Sections = append(doc.Sections, Section{ID: slot.Name, Title: slot.Section, Content: slot.Content, Contributor: recipe.ID})
			}
			doc.Content = result.Rendered
			if err := mergeDocuments(&merged, []Document{doc}, recipe.ID); err != nil {
				return ResolvedComposition{}, err
			}
			merged.Digests["slots:"+recipe.ID] = result.Provenance.RequestHash
		}
	}
	return NormalizeResolvedComposition(merged)
}

func NormalizeResolvedComposition(in ResolvedComposition) (ResolvedComposition, error) {
	out := in
	if out.ID == "" {
		out.ID = firstNonEmpty(out.RecipeID, "composition")
	}
	for i := range out.Documents {
		out.Documents[i].Content = renderDocument(out.Documents[i])
	}
	sort.SliceStable(out.Documents, func(i, j int) bool {
		if out.Documents[i].ID == out.Documents[j].ID {
			return out.Documents[i].Path < out.Documents[j].Path
		}
		return out.Documents[i].ID < out.Documents[j].ID
	})
	entries, err := artifact.Normalize(out.Artifacts.Entries)
	if err != nil && len(out.Artifacts.Entries) > 0 {
		return ResolvedComposition{}, err
	}
	out.Artifacts.Entries = entries
	if out.Extensions != nil && len(out.Extensions) == 0 {
		out.Extensions = nil
	}
	if out.Digests == nil {
		out.Digests = map[string]string{}
	}
	for _, doc := range out.Documents {
		out.Digests["document:"+doc.ID] = digestString(doc.Content)
	}
	if len(out.Artifacts.Entries) > 0 {
		out.Digests["artifacts"] = digestValue(out.Artifacts.Entries)
	}
	return out, nil
}

func expandRecipes(req ComposeRequest) ([]AuthoredRecipe, []CompositionDiagnostic, error) {
	defs := map[string]AuthoredRecipe{}
	for _, def := range req.Definitions {
		if def.ID == "" {
			return nil, nil, fmt.Errorf("%w: definition without id", ErrMissingCompositionDefinition)
		}
		defs[def.ID] = def
	}
	var ordered []AuthoredRecipe
	var diagnostics []CompositionDiagnostic
	visiting := map[string]bool{}
	emitted := map[string]bool{}
	var visit func(AuthoredRecipe) error
	visit = func(recipe AuthoredRecipe) error {
		if recipe.ID == "" {
			return fmt.Errorf("%w: recipe without id", ErrMissingCompositionDefinition)
		}
		if emitted[recipe.ID] {
			return nil
		}
		if visiting[recipe.ID] {
			return fmt.Errorf("%w: %s", ErrCompositionCycle, recipe.ID)
		}
		visiting[recipe.ID] = true
		if recipe.Base != "" {
			base, ok := defs[recipe.Base]
			if !ok {
				return fmt.Errorf("%w: base %s", ErrMissingCompositionDefinition, recipe.Base)
			}
			if err := visit(base); err != nil {
				return err
			}
		}
		for _, ref := range recipe.Parts {
			part, ok := defs[ref.ID]
			if !ok {
				if ref.Optional {
					diagnostics = append(diagnostics, CompositionDiagnostic{Code: "optional_part_missing", Severity: "warning", Message: "optional part missing: " + ref.ID, Source: recipe.ID})
					continue
				}
				return fmt.Errorf("%w: part %s", ErrMissingCompositionDefinition, ref.ID)
			}
			if err := visit(part); err != nil {
				return err
			}
		}
		visiting[recipe.ID] = false
		emitted[recipe.ID] = true
		ordered = append(ordered, recipe)
		return nil
	}
	if err := visit(req.Recipe); err != nil {
		return nil, nil, err
	}
	return ordered, diagnostics, nil
}

func mergeDocuments(dst *ResolvedComposition, docs []Document, contributor string) error {
	byID := map[string]int{}
	for i, doc := range dst.Documents {
		byID[doc.ID] = i
	}
	for _, doc := range docs {
		if doc.ID == "" {
			return fmt.Errorf("%w: document without id", ErrCompositionConflict)
		}
		for i := range doc.Sections {
			if doc.Sections[i].Contributor == "" {
				doc.Sections[i].Contributor = contributor
			}
		}
		if idx, ok := byID[doc.ID]; ok {
			if doc.Path != "" {
				dst.Documents[idx].Path = doc.Path
			}
			dst.Documents[idx].Sections = append(dst.Documents[idx].Sections, doc.Sections...)
			if doc.Content != "" && len(doc.Sections) == 0 {
				dst.Documents[idx].Sections = append(dst.Documents[idx].Sections, Section{ID: doc.ID + ":content", Content: doc.Content, Contributor: contributor})
			}
			continue
		}
		if doc.Content != "" && len(doc.Sections) == 0 {
			doc.Sections = append(doc.Sections, Section{ID: doc.ID + ":content", Content: doc.Content, Contributor: contributor})
		}
		byID[doc.ID] = len(dst.Documents)
		dst.Documents = append(dst.Documents, doc)
	}
	return nil
}

func mergeArtifacts(dst *ResolvedComposition, tree artifact.Tree) error {
	if len(tree.Entries) == 0 {
		return nil
	}
	entries := append(artifact.CloneEntries(dst.Artifacts.Entries), tree.Entries...)
	normalized, err := artifact.Normalize(entries)
	if err != nil {
		return err
	}
	dst.Artifacts.Entries = normalized
	if dst.Artifacts.Provenance.Source == "" {
		dst.Artifacts.Provenance = tree.Provenance
	}
	return nil
}

func mergeExtensions(dst *ResolvedComposition, next map[string]any, rules []MergeRule) error {
	if len(next) == 0 {
		return nil
	}
	if dst.Extensions == nil {
		dst.Extensions = map[string]any{}
	}
	ruleByField := map[string]MergeRule{}
	for _, rule := range rules {
		ruleByField[rule.Field] = rule
	}
	for key, value := range next {
		rule := ruleByField["extensions."+key]
		if rule.Kind == "" {
			rule = ruleByField[key]
		}
		if _, exists := dst.Extensions[key]; !exists {
			dst.Extensions[key] = cloneJSONish(value)
			continue
		}
		switch rule.Kind {
		case MergeReplace:
			dst.Extensions[key] = cloneJSONish(value)
		case MergeAppend:
			merged, err := appendValues(dst.Extensions[key], value)
			if err != nil {
				return err
			}
			dst.Extensions[key] = merged
		case MergeKeyed:
			merged, err := mergeKeyedValues(dst.Extensions[key], value, rule.Key)
			if err != nil {
				return err
			}
			dst.Extensions[key] = merged
		case MergeOpaque, "":
			return fmt.Errorf("%w: opaque extension %s", ErrCompositionConflict, key)
		default:
			return fmt.Errorf("%w: unknown merge rule %s", ErrCompositionConflict, rule.Kind)
		}
	}
	return nil
}

func appendValues(a, b any) ([]any, error) {
	left, ok := toAnySlice(a)
	if !ok {
		return nil, fmt.Errorf("%w: append requires list", ErrCompositionConflict)
	}
	right, ok := toAnySlice(b)
	if !ok {
		return nil, fmt.Errorf("%w: append requires list", ErrCompositionConflict)
	}
	return append(left, right...), nil
}

func mergeKeyedValues(a, b any, key string) ([]map[string]any, error) {
	if key == "" {
		return nil, fmt.Errorf("%w: keyed merge requires key", ErrCompositionConflict)
	}
	left, ok := toMapSlice(a)
	if !ok {
		return nil, fmt.Errorf("%w: keyed merge requires object list", ErrCompositionConflict)
	}
	right, ok := toMapSlice(b)
	if !ok {
		return nil, fmt.Errorf("%w: keyed merge requires object list", ErrCompositionConflict)
	}
	index := map[string]int{}
	for i, item := range left {
		id, ok := item[key].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("%w: keyed item missing %s", ErrCompositionConflict, key)
		}
		index[id] = i
	}
	for _, item := range right {
		id, ok := item[key].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("%w: keyed item missing %s", ErrCompositionConflict, key)
		}
		if i, exists := index[id]; exists {
			left[i] = item
		} else {
			index[id] = len(left)
			left = append(left, item)
		}
	}
	return left, nil
}

func toAnySlice(v any) ([]any, bool) {
	if in, ok := v.([]any); ok {
		return append([]any(nil), in...), true
	}
	return nil, false
}

func toMapSlice(v any) ([]map[string]any, bool) {
	slice, ok := v.([]map[string]any)
	if ok {
		out := make([]map[string]any, len(slice))
		for i := range slice {
			out[i] = cloneStringAnyMap(slice[i])
		}
		return out, true
	}
	anys, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(anys))
	for _, item := range anys {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, cloneStringAnyMap(m))
	}
	return out, true
}

func cloneJSONish(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneStringAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneJSONish(typed[i])
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i := range typed {
			out[i] = cloneStringAnyMap(typed[i])
		}
		return out
	default:
		return typed
	}
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneJSONish(value)
	}
	return out
}

func renderDocument(doc Document) string {
	if len(doc.Sections) == 0 {
		return doc.Content
	}
	var b strings.Builder
	for _, section := range doc.Sections {
		if section.Title != "" {
			b.WriteString(section.Title)
			b.WriteString("\n\n")
		}
		b.WriteString(section.Content)
		if !strings.HasSuffix(section.Content, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func digestValue(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", v))
	}
	return digestBytes(data)
}

func digestString(s string) string { return digestBytes([]byte(s)) }

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
