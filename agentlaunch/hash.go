package agentlaunch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// HashLaunchPlan returns a stable SHA-256 hex digest of the supplied
// LaunchPlan. The digest is deterministic: callers that pass identical
// plans (including identical map contents — Go map iteration order does
// NOT influence the result) receive identical hashes.
//
// # Canonicalization contract
//
// The plan is hashed by canonical JSON encoding. Two stability guarantees
// matter here:
//
//   - All maps inside the plan are encoded with keys in lexicographic
//     order. Go's encoding/json already sorts map keys, but the helper
//     below walks the marshalled tree and re-sorts defensively so future
//     stdlib changes cannot silently alter the digest.
//   - The encoder emits compact JSON (no whitespace, no indentation) so
//     the hash is invariant under encoder formatting tweaks.
//
// # Determinism caveat
//
// The hash covers EVERY field of the LaunchPlan as serialized, including
// Metadata.Labels and Metadata.Annotations. Callers that want to vary
// orchestrator-side metadata without varying the hash must either avoid
// putting volatile values in Metadata or strip the volatile keys before
// calling HashLaunchPlan. The library does not interpret metadata and
// therefore cannot decide on the caller's behalf which keys are
// non-load-bearing.
//
// # No external deps
//
// Implemented against crypto/sha256 + encoding/json only. The function
// returns an error only when JSON marshalling fails, which for the
// well-formed LaunchPlan fields ([]string, map[string]string, fixed
// structs) is not reachable in practice — the error path is preserved
// for future fields that might encode non-JSON-able values.
func HashLaunchPlan(plan LaunchPlan) (string, error) {
	canonical, err := canonicalJSON(plan)
	if err != nil {
		return "", fmt.Errorf("agentlaunch/hash: canonicalize plan: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON marshals v to JSON and then re-encodes it through the
// generic interface{} tree so any nested map is serialized with sorted
// keys regardless of stdlib internal ordering behaviour. The output is
// compact (no indentation, no extra whitespace).
//
// encoding/json already sorts map[string]X keys at marshal time, so the
// second pass is defense-in-depth — a stdlib change that altered map
// ordering would break the digest contract silently otherwise.
func canonicalJSON(v interface{}) ([]byte, error) {
	// First pass: marshal to a generic tree.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var tree interface{}
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	return marshalCanonical(tree)
}

// marshalCanonical walks an interface{} tree (the output of
// json.Unmarshal into an empty interface) and emits compact JSON with
// every map's keys sorted lexicographically.
func marshalCanonical(v interface{}) ([]byte, error) {
	switch typed := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			keyBytes, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf = append(buf, keyBytes...)
			buf = append(buf, ':')
			valBytes, err := marshalCanonical(typed[k])
			if err != nil {
				return nil, err
			}
			buf = append(buf, valBytes...)
		}
		buf = append(buf, '}')
		return buf, nil
	case []interface{}:
		buf := []byte{'['}
		for i, item := range typed {
			if i > 0 {
				buf = append(buf, ',')
			}
			itemBytes, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}
			buf = append(buf, itemBytes...)
		}
		buf = append(buf, ']')
		return buf, nil
	default:
		// Scalars (string, bool, float64, nil) — defer to encoding/json
		// for proper escaping and number formatting.
		return json.Marshal(typed)
	}
}
