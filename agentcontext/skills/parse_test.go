package skills

import (
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func TestParse_Happy_TetherStyle(t *testing.T) {
	t.Parallel()
	src := `---
name: capture-decision
description: "Capture a design decision to Vanta memory"
triggers:
  - /capture-decision
  - capture
---

Body of the skill.

More body.
`
	got, err := Parse([]byte(src), "/abs/path/cap.md")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Name != "capture-decision" {
		t.Fatalf("Name = %q, want capture-decision", got.Name)
	}
	if got.Description != "Capture a design decision to Vanta memory" {
		t.Fatalf("Description = %q", got.Description)
	}
	if len(got.Triggers) != 2 || got.Triggers[0] != "/capture-decision" || got.Triggers[1] != "capture" {
		t.Fatalf("Triggers = %#v", got.Triggers)
	}
	if got.Source != "/abs/path/cap.md" {
		t.Fatalf("Source = %q", got.Source)
	}
	if !strings.HasPrefix(got.Body, "\nBody of the skill.") {
		t.Fatalf("Body = %q", got.Body)
	}
	if _, ok := got.Frontmatter["name"]; !ok {
		t.Fatalf("Frontmatter should preserve original key")
	}
}

func TestParse_Happy_NaniteStyle_SlugWins(t *testing.T) {
	t.Parallel()
	// Nanite emits slug as the canonical kebab-case ID, and name as the
	// human-readable title. Skill.Name should follow slug.
	src := `---
name: Capture Decision
slug: capture-decision
description: "Capture a design decision."
tags: [vanta, decision]
---
body
`
	got, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Name != "capture-decision" {
		t.Fatalf("Name = %q, want capture-decision (slug should win)", got.Name)
	}
	// Original "name" preserved in Frontmatter.
	if v, _ := got.Frontmatter["name"].(string); v != "Capture Decision" {
		t.Fatalf("Frontmatter[name] = %q", v)
	}
	if v, _ := got.Frontmatter["slug"].(string); v != "capture-decision" {
		t.Fatalf("Frontmatter[slug] = %q", v)
	}
	// Unknown fields preserved.
	if got.Frontmatter["tags"] == nil {
		t.Fatalf("Frontmatter[tags] missing")
	}
}

func TestParse_TriggerSingleString(t *testing.T) {
	t.Parallel()
	src := `---
slug: foo
description: thing
triggers: bareword
---
`
	got, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Triggers) != 1 || got.Triggers[0] != "bareword" {
		t.Fatalf("Triggers = %#v", got.Triggers)
	}
}

func TestParse_TriggerEmptyOrMissing(t *testing.T) {
	t.Parallel()
	src := `---
slug: foo
description: thing
---
`
	got, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Triggers) != 0 {
		t.Fatalf("Triggers should be empty: %#v", got.Triggers)
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	t.Parallel()
	src := "# Just a header\n\nNo frontmatter at all.\n"
	_, err := Parse([]byte(src), "")
	if !errors.Is(err, agentcontext.ErrSkillNoFrontmatter) {
		t.Fatalf("err = %v, want ErrSkillNoFrontmatter", err)
	}
}

func TestParse_MissingClosingDelim(t *testing.T) {
	t.Parallel()
	src := "---\nname: x\ndescription: y\nno closer here\n"
	_, err := Parse([]byte(src), "")
	if !errors.Is(err, agentcontext.ErrSkillNoFrontmatter) {
		t.Fatalf("err = %v, want ErrSkillNoFrontmatter", err)
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	t.Parallel()
	src := "---\nname: x\ndescription: y\n  bad: indent: here\n---\nbody"
	_, err := Parse([]byte(src), "")
	if !errors.Is(err, agentcontext.ErrSkillInvalidFrontmatter) {
		t.Fatalf("err = %v, want ErrSkillInvalidFrontmatter", err)
	}
}

func TestParse_MissingName(t *testing.T) {
	t.Parallel()
	src := "---\ndescription: just a desc\n---\nbody"
	_, err := Parse([]byte(src), "")
	if !errors.Is(err, agentcontext.ErrSkillMissingName) {
		t.Fatalf("err = %v, want ErrSkillMissingName", err)
	}
}

func TestParse_MissingDescription(t *testing.T) {
	t.Parallel()
	src := "---\nslug: foo\n---\nbody"
	_, err := Parse([]byte(src), "")
	if !errors.Is(err, agentcontext.ErrSkillMissingDescription) {
		t.Fatalf("err = %v, want ErrSkillMissingDescription", err)
	}
}

func TestParse_EmptyBody_OK(t *testing.T) {
	t.Parallel()
	src := "---\nslug: foo\ndescription: thing\n---\n"
	got, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Body != "" {
		t.Fatalf("Body should be empty: %q", got.Body)
	}
}

func TestParse_ExtraFieldsPreserved(t *testing.T) {
	t.Parallel()
	src := `---
slug: foo
description: thing
allowed-tools: [dev_read, dev_write]
effort: low
broker-hints: [hint1, hint2]
custom_field: some_value
modes: [chat, cli]
---
body
`
	got, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, k := range []string{"allowed-tools", "effort", "broker-hints", "custom_field", "modes"} {
		if _, ok := got.Frontmatter[k]; !ok {
			t.Errorf("Frontmatter missing %q", k)
		}
	}
}

func TestSkill_Validate(t *testing.T) {
	t.Parallel()
	s := Skill{Name: "x", Description: "y"}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	s = Skill{Description: "y"}
	if !errors.Is(s.Validate(), agentcontext.ErrSkillMissingName) {
		t.Fatalf("want ErrSkillMissingName")
	}
	s = Skill{Name: "x"}
	if !errors.Is(s.Validate(), agentcontext.ErrSkillMissingDescription) {
		t.Fatalf("want ErrSkillMissingDescription")
	}
}
