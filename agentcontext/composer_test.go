package agentcontext

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/artifact"
)

type composeStubProvider struct{}

func (composeStubProvider) Assemble(ctx context.Context, req ContextRequest) (*ContextResult, error) {
	results := make([]SlotResult, 0, len(req.Slots))
	for _, slot := range req.Slots {
		if slot.Required && slot.Source.Inline.Content == "" {
			return nil, ErrSlotRequiredAndEmpty
		}
		results = append(results, SlotResult{
			Name:          slot.Name,
			Section:       slot.Section,
			Content:       slot.Source.Inline.Content,
			Bytes:         len(slot.Source.Inline.Content),
			TokenEstimate: EstimateTokens(len(slot.Source.Inline.Content)),
			Provenance:    SlotProvenance{Kind: slot.Source.Kind, Source: slot.Name, Bytes: len(slot.Source.Inline.Content), ContentHash: digestString(slot.Source.Inline.Content)},
		})
	}
	rendered, limits := DefaultRenderer{}.Render(results, req.Limits)
	return &ContextResult{Slots: results, Rendered: rendered, Provenance: Provenance{Input: req.Provenance, LibraryVersion: Version, RequestHash: HashStringForTest(req), AssembledAt: fixedComposeTime()}, Limits: limits}, nil
}

func TestComposerGraphOrderingDedupAndMergeRules(t *testing.T) {
	defs := []AuthoredRecipe{
		{
			ID:        "base",
			Documents: []Document{{ID: "instructions", Sections: []Section{{ID: "base", Title: "## Base", Content: "base"}}}},
			Extensions: map[string]any{
				"env":    []map[string]any{{"name": "PATH", "value": "/bin"}},
				"opaque": "base",
				"append": []any{"base"},
			},
			Artifacts: artifact.Tree{Entries: []artifact.Entry{{Path: "base.txt", Kind: artifact.EntryFile, Bytes: []byte("base"), Ownership: artifact.Ownership{GroupID: "base"}}}},
		},
		{ID: "shared", Base: "base", Documents: []Document{{ID: "instructions", Sections: []Section{{ID: "shared", Title: "## Shared", Content: "shared"}}}}},
		{ID: "left", Base: "shared", Documents: []Document{{ID: "instructions", Sections: []Section{{ID: "left", Title: "## Left", Content: "left"}}}}, Extensions: map[string]any{"env": []map[string]any{{"name": "PATH", "value": "/usr/bin"}}, "append": []any{"left"}}},
		{ID: "right", Base: "shared", Documents: []Document{{ID: "instructions", Sections: []Section{{ID: "right", Title: "## Right", Content: "right"}}}}},
	}
	req := ComposeRequest{Definitions: defs, Recipe: AuthoredRecipe{
		ID:        "root",
		Parts:     []PartRef{{ID: "left"}, {ID: "right"}},
		Documents: []Document{{ID: "instructions", Sections: []Section{{ID: "root", Title: "## Root", Content: "root"}}}},
		Extensions: map[string]any{
			"env":    []map[string]any{{"name": "MODEL", "value": "gpt"}},
			"append": []any{"root"},
			"opaque": "root",
		},
		MergeRules: []MergeRule{
			{Field: "extensions.env", Kind: MergeKeyed, Key: "name"},
			{Field: "extensions.append", Kind: MergeAppend},
			{Field: "extensions.opaque", Kind: MergeReplace},
		},
	}}
	got, err := NewComposer(ComposerOptions{}).Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	wantContributors := []string{"base", "shared", "left", "right", "root"}
	var contributors []string
	for _, c := range got.Contributors {
		contributors = append(contributors, c.ID)
	}
	if !reflect.DeepEqual(contributors, wantContributors) {
		t.Fatalf("contributors\nwant %#v\ngot  %#v", wantContributors, contributors)
	}
	if strings.Count(got.Documents[0].Content, "shared") != 1 {
		t.Fatalf("shared ancestor was not deduped in content:\n%s", got.Documents[0].Content)
	}
	if !strings.Contains(got.Documents[0].Content, "## Base") || !strings.Contains(got.Documents[0].Content, "## Root") {
		t.Fatalf("document content missing ordered sections:\n%s", got.Documents[0].Content)
	}
	env := got.Extensions["env"].([]map[string]any)
	if len(env) != 2 || env[0]["value"] != "/usr/bin" || env[1]["name"] != "MODEL" {
		t.Fatalf("keyed env merge = %#v", env)
	}
	if got.Extensions["opaque"] != "root" {
		t.Fatalf("replace merge failed: %#v", got.Extensions["opaque"])
	}
	if !reflect.DeepEqual(got.Extensions["append"], []any{"base", "left", "root"}) {
		t.Fatalf("append merge = %#v", got.Extensions["append"])
	}
	if len(got.Artifacts.Entries) != 1 || got.Artifacts.Entries[0].Path != "base.txt" {
		t.Fatalf("artifact merge = %#v", got.Artifacts.Entries)
	}
}

func TestComposerGraphValidation(t *testing.T) {
	_, err := NewComposer(ComposerOptions{}).Compose(ComposeRequest{
		Definitions: []AuthoredRecipe{{ID: "a", Base: "b"}, {ID: "b", Base: "a"}},
		Recipe:      AuthoredRecipe{ID: "root", Parts: []PartRef{{ID: "a"}}},
	})
	if !errors.Is(err, ErrCompositionCycle) {
		t.Fatalf("cycle err = %v, want ErrCompositionCycle", err)
	}
	_, err = NewComposer(ComposerOptions{}).Compose(ComposeRequest{Recipe: AuthoredRecipe{ID: "root", Parts: []PartRef{{ID: "missing"}}}})
	if !errors.Is(err, ErrMissingCompositionDefinition) {
		t.Fatalf("missing err = %v, want ErrMissingCompositionDefinition", err)
	}
	got, err := NewComposer(ComposerOptions{}).Compose(ComposeRequest{Recipe: AuthoredRecipe{ID: "root", Parts: []PartRef{{ID: "optional", Optional: true}}}})
	if err != nil {
		t.Fatalf("optional missing: %v", err)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "optional_part_missing" {
		t.Fatalf("optional diagnostic = %#v", got.Diagnostics)
	}
	_, err = NewComposer(ComposerOptions{}).Compose(ComposeRequest{Definitions: []AuthoredRecipe{{ID: "a", Extensions: map[string]any{"opaque": "a"}}}, Recipe: AuthoredRecipe{ID: "root", Parts: []PartRef{{ID: "a"}}, Extensions: map[string]any{"opaque": "root"}}})
	if !errors.Is(err, ErrCompositionConflict) {
		t.Fatalf("opaque conflict err = %v, want ErrCompositionConflict", err)
	}
}

func TestComposerSlotDocumentAssemblyDeterministicAndDigest(t *testing.T) {
	composer := NewComposer(ComposerOptions{Provider: composeStubProvider{}})
	req := ComposeRequest{Recipe: AuthoredRecipe{
		ID:         "root",
		Provenance: ProvenanceInput{LineageAlias: "fixture", ProfileID: "rev-1"},
		Slots: []SlotSpec{
			{Name: "intro", Section: "## Intro", Required: true, Source: SlotSource{Kind: SlotSourceKindInline, Inline: InlineSource{Content: "hello"}}},
			{Name: "body", Section: "## Body", Source: SlotSource{Kind: SlotSourceKindInline, Inline: InlineSource{Content: "world"}}},
		},
	}}
	first, err := composer.Compose(req)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := composer.Compose(req)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("composition changed across identical inputs")
	}
	if len(first.Documents) != 1 || first.Documents[0].ID != "root:slots" {
		t.Fatalf("slot document = %#v", first.Documents)
	}
	if !strings.Contains(first.Documents[0].Content, "## Intro") || !strings.Contains(first.Documents[0].Content, "world") {
		t.Fatalf("slot document content = %q", first.Documents[0].Content)
	}
	if first.Digests["slots:root"] == "" || first.Digests["document:root:slots"] == "" || first.Digests["recipe:root"] == "" {
		t.Fatalf("digests missing: %#v", first.Digests)
	}
	if first.Contributors[0].SourcePath != "fixture" || first.Contributors[0].Revision != "rev-1" {
		t.Fatalf("contributor provenance = %#v", first.Contributors)
	}
}

func TestComposerAppShapedSectionsWithoutAppImports(t *testing.T) {
	defs := []AuthoredRecipe{
		{ID: "cairn", Documents: []Document{{ID: "install", Path: "AGENTS.md", Sections: []Section{{ID: "install", Title: "## Install", Content: "filesystem tree + managed JSON/TOML"}}}}},
		{ID: "nanite", Documents: []Document{{ID: "install", Sections: []Section{{ID: "refresh", Title: "## Refresh", Content: "immutable blob skill package"}}}}},
		{ID: "torque", Documents: []Document{{ID: "install", Sections: []Section{{ID: "loopback", Title: "## Loopback", Content: "generated task and loopback descriptor"}}}}},
		{ID: "tether", Documents: []Document{{ID: "install", Sections: []Section{{ID: "tree", Title: "## Task Tree", Content: "multi-task copied tree"}}}}},
	}
	got, err := NewComposer(ComposerOptions{}).Compose(ComposeRequest{Definitions: defs, Recipe: AuthoredRecipe{ID: "root", Parts: []PartRef{{ID: "cairn"}, {ID: "nanite"}, {ID: "torque"}, {ID: "tether"}}}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	content := got.Documents[0].Content
	for _, want := range []string{"filesystem tree", "immutable blob", "generated task", "multi-task"} {
		if !strings.Contains(content, want) {
			t.Fatalf("app-shaped content missing %q:\n%s", want, content)
		}
	}
}

func TestNormalizeResolvedCompositionDirectArtifactPath(t *testing.T) {
	resolved, err := NormalizeResolvedComposition(ResolvedComposition{
		ID:        "already-resolved",
		Documents: []Document{{ID: "doc", Content: "direct document\n"}},
		Artifacts: artifact.Tree{Entries: []artifact.Entry{{Path: "bin/run.sh", Kind: artifact.EntryFile, Mode: 0o755, Bytes: []byte("#!/bin/sh\n")}}},
	})
	if err != nil {
		t.Fatalf("NormalizeResolvedComposition: %v", err)
	}
	if resolved.Documents[0].Content != "direct document\n" || resolved.Artifacts.Entries[0].Path != "bin/run.sh" {
		t.Fatalf("resolved composition changed unexpectedly: %#v", resolved)
	}
	if resolved.Digests["document:doc"] == "" || resolved.Digests["artifacts"] == "" {
		t.Fatalf("resolved digests missing: %#v", resolved.Digests)
	}
}

func HashStringForTest(req ContextRequest) string {
	h, err := HashRequest(req)
	if err != nil {
		return digestValue(req)
	}
	return h
}

func fixedComposeTime() time.Time {
	return time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
}
