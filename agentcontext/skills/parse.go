package skills

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// frontmatterDelim is the literal "---" delimiter that opens and
// closes a YAML frontmatter block.
var frontmatterDelim = []byte("---")

// Parse decodes a markdown-with-YAML-frontmatter byte buffer into a
// Skill. The source argument is recorded into Skill.Source verbatim
// (Discover uses the absolute on-disk path; callers using Parse
// directly may pass any opaque identifier or empty string).
//
// Errors:
//
//   - ErrSkillNoFrontmatter — data does not start with "---".
//   - ErrSkillInvalidFrontmatter — frontmatter block is present but
//     the enclosed body is not valid YAML. Wraps the yaml decoder
//     error.
//   - ErrSkillMissingName — neither "slug" nor "name" was set in the
//     frontmatter.
//   - ErrSkillMissingDescription — "description" is empty or absent.
//
// The empty body case is NOT an error — a skill whose entire content
// lives in the frontmatter is legal (rare but exists in some tester
// fixtures).
func Parse(data []byte, source string) (Skill, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}

	raw := map[string]any{}
	if len(fm) > 0 {
		if err := yaml.Unmarshal(fm, &raw); err != nil {
			return Skill{}, fmt.Errorf("%w: %v", agentcontext.ErrSkillInvalidFrontmatter, err)
		}
	}

	skill := Skill{
		Frontmatter: raw,
		Source:      source,
		Body:        string(body),
	}

	// slug wins over name when both are present (see doc.go).
	if v, ok := stringField(raw, "slug"); ok {
		skill.Name = v
	} else if v, ok := stringField(raw, "name"); ok {
		skill.Name = v
	}
	skill.Name = strings.TrimSpace(skill.Name)

	if v, ok := stringField(raw, "description"); ok {
		skill.Description = strings.TrimSpace(v)
	}

	skill.Triggers = readTriggers(raw)

	if err := skill.Validate(); err != nil {
		return Skill{}, err
	}

	return skill, nil
}

// splitFrontmatter splits the byte buffer into a frontmatter slice
// (YAML body inside the delimiter pair, without the delimiters
// themselves) and the markdown body that follows.
//
// Leading blank lines / whitespace lines BEFORE the opening "---"
// are tolerated; the opening delimiter must be the first
// non-whitespace line.
func splitFrontmatter(data []byte) (frontmatter, body []byte, err error) {
	trimmed := bytes.TrimLeft(data, "\n\r\t ")
	if !bytes.HasPrefix(trimmed, frontmatterDelim) {
		return nil, nil, agentcontext.ErrSkillNoFrontmatter
	}

	// Step past the opening "---" line.
	rest := trimmed[len(frontmatterDelim):]
	// Require a newline after the opening delim.
	if _, after, ok := bytes.Cut(rest, []byte("\n")); ok {
		rest = after
	} else {
		return nil, nil, fmt.Errorf("%w: no newline after opening ---", agentcontext.ErrSkillNoFrontmatter)
	}

	// Find the closing "---" on its own line. We match "\n---" so we
	// do not confuse a frontmatter-internal "---" appearing in a
	// string value with the closing delimiter.
	before, after, found := bytes.Cut(rest, append([]byte("\n"), frontmatterDelim...))
	if !found {
		return nil, nil, fmt.Errorf("%w: no closing --- delimiter", agentcontext.ErrSkillNoFrontmatter)
	}
	frontmatter = before

	// Step past the closing-delim line.
	if _, bodyPart, ok := bytes.Cut(after, []byte("\n")); ok {
		body = bodyPart
	}

	return frontmatter, body, nil
}

// stringField pulls a stringly-typed value out of a parsed
// frontmatter map. Returns ("", false) when the key is missing OR
// when the value is not a string-shaped node. This deliberately
// rejects numeric or boolean coercion — skill identifiers should
// not pun on YAML type inference.
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// readTriggers extracts the "triggers" field from a frontmatter map
// in a tolerant way. The canonical shape is a YAML list of strings;
// a single bare string is also accepted (some authors write
// `triggers: capture` instead of a one-element list).
//
// Non-string entries inside the list are silently dropped — they
// almost certainly indicate a frontmatter typo, but the rest of the
// skill is still usable, so we degrade gracefully rather than
// failing the whole parse.
//
// Empty strings (post-trim) are filtered out.
func readTriggers(m map[string]any) []string {
	v, ok := m["triggers"]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case string:
		if t := strings.TrimSpace(x); t != "" {
			return []string{t}
		}
		return nil
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}
