package materialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/agentkit/artifact"
)

type DocumentKind string

const (
	DocumentJSON DocumentKind = "json"
	DocumentTOML DocumentKind = "toml"
)

type ManagedKey struct {
	Key     string          `yaml:"key" json:"key"`
	Value   json.RawMessage `yaml:"value,omitempty" json:"value,omitempty"`
	Raw     string          `yaml:"raw,omitempty" json:"raw,omitempty"`
	Digest  artifact.Digest `yaml:"digest,omitempty" json:"digest,omitempty"`
	Remove  bool            `yaml:"remove,omitempty" json:"remove,omitempty"`
	EntryID string          `yaml:"entry_id,omitempty" json:"entry_id,omitempty"`
	GroupID string          `yaml:"group_id,omitempty" json:"group_id,omitempty"`
}

type DocumentPatch struct {
	Kind     DocumentKind `yaml:"kind" json:"kind"`
	Existing []byte       `yaml:"existing,omitempty" json:"existing,omitempty"`
	Previous []ManagedKey `yaml:"previous,omitempty" json:"previous,omitempty"`
	Desired  []ManagedKey `yaml:"desired,omitempty" json:"desired,omitempty"`
}

type DocumentMerge struct {
	Bytes   []byte   `yaml:"bytes" json:"bytes"`
	Changed []Change `yaml:"changed,omitempty" json:"changed,omitempty"`
}

func MergeDocument(patch DocumentPatch) (DocumentMerge, error) {
	switch patch.Kind {
	case DocumentJSON:
		return mergeJSONDocument(patch)
	case DocumentTOML:
		return mergeTOMLDocument(patch)
	default:
		return DocumentMerge{}, fmt.Errorf("%w: unsupported document kind %q", ErrUnsupportedOperation, patch.Kind)
	}
}

func mergeJSONDocument(patch DocumentPatch) (DocumentMerge, error) {
	object := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(patch.Existing)) > 0 {
		if err := json.Unmarshal(patch.Existing, &object); err != nil {
			return DocumentMerge{}, fmt.Errorf("%w: json: %v", ErrMalformedDocument, err)
		}
	}
	for _, prev := range patch.Previous {
		current, ok := object[prev.Key]
		if !ok {
			return DocumentMerge{}, fmt.Errorf("%w: managed json key %q missing", ErrConflict, prev.Key)
		}
		if digestRawJSON(current) != prev.Digest {
			return DocumentMerge{}, fmt.Errorf("%w: managed json key %q changed", ErrConflict, prev.Key)
		}
	}
	var changes []Change
	for _, desired := range patch.Desired {
		before, existed := object[desired.Key]
		if desired.Remove {
			delete(object, desired.Key)
			changes = append(changes, documentChange(desired, ChangeRemove, digestRawJSON(before), artifact.Digest{}))
			continue
		}
		value := append(json.RawMessage(nil), desired.Value...)
		if !json.Valid(value) {
			return DocumentMerge{}, fmt.Errorf("%w: json key %q", ErrMalformedDocument, desired.Key)
		}
		object[desired.Key] = value
		kind := ChangeCreate
		beforeDigest := artifact.Digest{}
		if existed {
			beforeDigest = digestRawJSON(before)
			kind = ChangeUpdate
			if beforeDigest == digestRawJSON(value) {
				kind = ChangeUnchanged
			}
		}
		changes = append(changes, documentChange(desired, kind, beforeDigest, digestRawJSON(value)))
	}
	out, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return DocumentMerge{}, err
	}
	return DocumentMerge{Bytes: append(out, '\n'), Changed: changes}, nil
}

func mergeTOMLDocument(patch DocumentPatch) (DocumentMerge, error) {
	pairs, order, err := parseFlatTOML(patch.Existing)
	if err != nil {
		return DocumentMerge{}, err
	}
	for _, prev := range patch.Previous {
		current, ok := pairs[prev.Key]
		if !ok {
			return DocumentMerge{}, fmt.Errorf("%w: managed toml key %q missing", ErrConflict, prev.Key)
		}
		if artifact.DigestBytes([]byte(current)) != prev.Digest {
			return DocumentMerge{}, fmt.Errorf("%w: managed toml key %q changed", ErrConflict, prev.Key)
		}
	}
	seen := map[string]bool{}
	for _, key := range order {
		seen[key] = true
	}
	var changes []Change
	for _, desired := range patch.Desired {
		before, existed := pairs[desired.Key]
		if desired.Remove {
			delete(pairs, desired.Key)
			changes = append(changes, documentChange(desired, ChangeRemove, artifact.DigestBytes([]byte(before)), artifact.Digest{}))
			continue
		}
		if strings.TrimSpace(desired.Raw) == "" {
			return DocumentMerge{}, fmt.Errorf("%w: toml key %q has empty raw value", ErrMalformedDocument, desired.Key)
		}
		pairs[desired.Key] = desired.Raw
		if !seen[desired.Key] {
			order = append(order, desired.Key)
			seen[desired.Key] = true
		}
		kind := ChangeCreate
		beforeDigest := artifact.Digest{}
		if existed {
			beforeDigest = artifact.DigestBytes([]byte(before))
			kind = ChangeUpdate
			if before == desired.Raw {
				kind = ChangeUnchanged
			}
		}
		changes = append(changes, documentChange(desired, kind, beforeDigest, artifact.DigestBytes([]byte(desired.Raw))))
	}
	var b strings.Builder
	for _, key := range order {
		value, ok := pairs[key]
		if !ok {
			continue
		}
		b.WriteString(key)
		b.WriteString(" = ")
		b.WriteString(value)
		b.WriteByte('\n')
	}
	return DocumentMerge{Bytes: []byte(b.String()), Changed: changes}, nil
}

func parseFlatTOML(data []byte) (map[string]string, []string, error) {
	pairs := map[string]string{}
	var order []string
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, nil, fmt.Errorf("%w: toml line %d", ErrMalformedDocument, i+1)
		}
		key := strings.TrimSpace(parts[0])
		if _, exists := pairs[key]; !exists {
			order = append(order, key)
		}
		pairs[key] = strings.TrimSpace(parts[1])
	}
	return pairs, order, nil
}

func documentChange(key ManagedKey, kind ChangeKind, before, after artifact.Digest) Change {
	return Change{Path: key.Key, Kind: kind, EntryID: key.EntryID, GroupID: key.GroupID, Before: before, After: after}
}

func digestRawJSON(raw json.RawMessage) artifact.Digest {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return artifact.DigestBytes(raw)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return artifact.DigestBytes(raw)
	}
	return artifact.DigestBytes(canonical)
}
