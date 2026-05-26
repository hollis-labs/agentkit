package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// HTTPJSONResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindHTTPJSON.
//
// The resolver fetches like http_text, parses the body as JSON, then
// applies the optional JSONPath expression. When JSONPath is empty,
// the resolver re-encodes the whole document as pretty-printed JSON.
//
// # JSONPath subset
//
// The resolver implements a TINY JSONPath subset, deliberately
// avoiding a third-party dependency for v0.1.0:
//
//   - $          → whole document (also: "" / "." / "$.")
//   - $.foo      → top-level key "foo"
//   - $.foo.bar  → nested keys
//   - $.foo[0]   → array index
//   - $.foo[0].bar → mixed
//
// Identifiers may contain letters, digits, '_', and '-'. The grammar
// rejects '*', '..' (recursive descent), filter expressions, slices,
// and wildcards. Callers that need full JSONPath should wrap a
// dedicated library and register their own resolver.
//
// When the resolved JSON node is a string, it is returned verbatim.
// Other node types are re-encoded as canonical (indent-2) JSON.
type HTTPJSONResolver struct {
	client  *http.Client
	maxBody int64
	timeout time.Duration
}

// HTTPJSONOption configures an HTTPJSONResolver.
type HTTPJSONOption func(*HTTPJSONResolver)

// WithHTTPJSONClient overrides the HTTP client. Mirrors
// WithHTTPClient for symmetry; the two resolvers do not share an
// option type so a custom HTTPTextResolver's options cannot be
// accidentally fed to HTTPJSONResolver.
func WithHTTPJSONClient(c *http.Client) HTTPJSONOption {
	return func(r *HTTPJSONResolver) {
		if c != nil {
			r.client = c
		}
	}
}

// WithHTTPJSONMaxBody overrides the max-body cap.
func WithHTTPJSONMaxBody(n int64) HTTPJSONOption {
	return func(r *HTTPJSONResolver) {
		r.maxBody = n
	}
}

// WithHTTPJSONDefaultTimeout sets the fallback timeout.
func WithHTTPJSONDefaultTimeout(d time.Duration) HTTPJSONOption {
	return func(r *HTTPJSONResolver) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// NewHTTPJSONResolver returns an HTTPJSONResolver. Defaults match
// HTTPTextResolver.
func NewHTTPJSONResolver(opts ...HTTPJSONOption) agentcontext.Resolver {
	r := &HTTPJSONResolver{
		client:  http.DefaultClient,
		maxBody: DefaultHTTPMaxBody,
		timeout: DefaultHTTPTimeout,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve implements agentcontext.Resolver.
func (r *HTTPJSONResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindHTTPJSON {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindHTTPJSON)
	}

	src := spec.Source.HTTPJSON
	body, status, truncated, err := httpFetch(ctx, r.client, src.URL, src.Headers, pickTimeout(src.Timeout, r.timeout), r.maxBody)
	if err != nil {
		return agentcontext.SlotResult{}, err
	}

	var tree interface{}
	if err := json.Unmarshal(body, &tree); err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: %v", agentcontext.ErrInvalidJSON, err)
	}

	node, err := applyJSONPath(tree, src.JSONPath)
	if err != nil {
		return agentcontext.SlotResult{}, err
	}

	content, err := renderJSONNode(node)
	if err != nil {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: render: %v", agentcontext.ErrInvalidJSON, err)
	}

	extra := map[string]string{
		"http_status": fmt.Sprintf("%d", status),
		"url":         src.URL,
		"jsonpath":    src.JSONPath,
	}
	if truncated {
		extra["body_truncated"] = "true"
	}

	return agentcontext.SlotResult{
		Content:   content,
		Truncated: truncated,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindHTTPJSON,
			Source:      src.URL,
			Bytes:       len(content),
			ContentHash: hashContent(content),
			FetchedAt:   nowUTC(),
			Extra:       extra,
		},
	}, nil
}

// applyJSONPath walks tree according to the resolver's tiny JSONPath
// subset. Empty / "$" / "$." / "." → return the whole tree. Returns
// ErrJSONPathNotFound when navigation hits a missing key, an
// out-of-range index, or a node of the wrong type.
func applyJSONPath(tree interface{}, path string) (interface{}, error) {
	p := strings.TrimSpace(path)
	if p == "" || p == "$" || p == "$." || p == "." {
		return tree, nil
	}
	// Reject recursive-descent ".." (anywhere in the expression). Done
	// against the RAW path so the check fires on "$..a" before the
	// root-prefix strip below masks the second '.'.
	if strings.Contains(p, "..") {
		return nil, fmt.Errorf("%w: recursive descent '..' not supported in %q", agentcontext.ErrJSONPathNotFound, path)
	}
	// Accept "$.foo..." and ".foo..."; normalise to the body after "$.".
	switch {
	case strings.HasPrefix(p, "$."):
		p = p[2:]
	case strings.HasPrefix(p, "."):
		p = p[1:]
	case strings.HasPrefix(p, "$"):
		p = p[1:]
	}
	if p == "" {
		return tree, nil
	}

	tokens, err := tokenizeJSONPath(p)
	if err != nil {
		return nil, err
	}

	var node = tree
	for _, tok := range tokens {
		if tok.isIndex {
			arr, ok := node.([]interface{})
			if !ok {
				return nil, fmt.Errorf("%w: not an array at [%d]", agentcontext.ErrJSONPathNotFound, tok.index)
			}
			if tok.index < 0 || tok.index >= len(arr) {
				return nil, fmt.Errorf("%w: index [%d] out of range", agentcontext.ErrJSONPathNotFound, tok.index)
			}
			node = arr[tok.index]
			continue
		}
		obj, ok := node.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: not an object at .%s", agentcontext.ErrJSONPathNotFound, tok.name)
		}
		child, has := obj[tok.name]
		if !has {
			return nil, fmt.Errorf("%w: key .%s", agentcontext.ErrJSONPathNotFound, tok.name)
		}
		node = child
	}
	return node, nil
}

type jsonPathToken struct {
	name    string
	index   int
	isIndex bool
}

// tokenizeJSONPath parses the subset described on HTTPJSONResolver's
// godoc. Rejects unsupported syntax up front so a typo'd expression
// fails loudly.
func tokenizeJSONPath(p string) ([]jsonPathToken, error) {
	var (
		tokens []jsonPathToken
		i      = 0
	)
	for i < len(p) {
		switch c := p[i]; {
		case c == '.':
			i++
		case c == '[':
			// numeric index up to matching ']'
			j := strings.IndexByte(p[i+1:], ']')
			if j < 0 {
				return nil, fmt.Errorf("%w: unterminated '[' in %q", agentcontext.ErrJSONPathNotFound, p)
			}
			idxStr := strings.TrimSpace(p[i+1 : i+1+j])
			n, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil, fmt.Errorf("%w: non-numeric index %q in %q", agentcontext.ErrJSONPathNotFound, idxStr, p)
			}
			tokens = append(tokens, jsonPathToken{index: n, isIndex: true})
			i += j + 2
		default:
			// identifier — letters, digits, '_', '-'
			j := i
			for j < len(p) {
				ch := p[j]
				if ch == '.' || ch == '[' {
					break
				}
				if !(ch == '_' || ch == '-' ||
					(ch >= 'a' && ch <= 'z') ||
					(ch >= 'A' && ch <= 'Z') ||
					(ch >= '0' && ch <= '9')) {
					return nil, fmt.Errorf("%w: unsupported char %q in %q", agentcontext.ErrJSONPathNotFound, string(ch), p)
				}
				j++
			}
			if j == i {
				return nil, fmt.Errorf("%w: empty segment in %q", agentcontext.ErrJSONPathNotFound, p)
			}
			tokens = append(tokens, jsonPathToken{name: p[i:j]})
			i = j
		}
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("%w: no tokens in %q", agentcontext.ErrJSONPathNotFound, p)
	}
	return tokens, nil
}

// renderJSONNode emits a string for the resolved node. Strings are
// returned verbatim; everything else is re-encoded as indent-2 JSON.
func renderJSONNode(node interface{}) (string, error) {
	if s, ok := node.(string); ok {
		return s, nil
	}
	out, err := json.MarshalIndent(node, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
