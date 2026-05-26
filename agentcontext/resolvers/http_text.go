package resolvers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/agentkit/agentcontext"
)

// HTTP resolver defaults.
const (
	DefaultHTTPTimeout = 10 * time.Second
	DefaultHTTPMaxBody = 1 << 20 // 1 MiB
)

// HTTPTextResolver implements agentcontext.Resolver for
// agentcontext.SlotSourceKindHTTPText.
//
// The resolver issues an HTTP GET to SlotSource.HTTPText.URL, applies
// the supplied Headers, and returns the response body verbatim (up to
// the resolver-side maximum body size, default 1 MiB). When the body
// exceeds the cap, the result is marked Truncated.
//
// 2xx → success. Non-2xx → ErrHTTPStatus wrapped with the status
// code. Transport errors → ErrHTTPRequest wrapped.
//
// # Method
//
// The contract's HTTPTextSource does not carry an HTTP method, so the
// resolver always issues GET. Future contract revisions can add a
// Method field; the resolver will respect it.
type HTTPTextResolver struct {
	client  *http.Client
	maxBody int64
	timeout time.Duration
}

// HTTPTextOption configures an HTTPTextResolver.
type HTTPTextOption func(*HTTPTextResolver)

// WithHTTPClient overrides the HTTP client used by the resolver.
// Useful in tests (httptest.NewServer) and for callers that need to
// inject a custom transport (mTLS, proxy, etc.). A nil client is
// ignored.
func WithHTTPClient(c *http.Client) HTTPTextOption {
	return func(r *HTTPTextResolver) {
		if c != nil {
			r.client = c
		}
	}
}

// WithHTTPMaxBody overrides the max-body cap. Non-positive values
// disable the cap (NOT recommended).
func WithHTTPMaxBody(n int64) HTTPTextOption {
	return func(r *HTTPTextResolver) {
		r.maxBody = n
	}
}

// WithHTTPDefaultTimeout sets the resolver-side fallback timeout
// applied when SlotSource.HTTPText.Timeout is zero. Non-positive
// values are ignored.
func WithHTTPDefaultTimeout(d time.Duration) HTTPTextOption {
	return func(r *HTTPTextResolver) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// NewHTTPTextResolver returns an HTTPTextResolver configured with the
// supplied options. Defaults: client = http.DefaultClient (with a
// per-request timeout applied via context), maxBody = 1 MiB,
// timeout = DefaultHTTPTimeout.
func NewHTTPTextResolver(opts ...HTTPTextOption) agentcontext.Resolver {
	r := &HTTPTextResolver{
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
func (r *HTTPTextResolver) Resolve(ctx context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcontext.SlotResult{}, err
	}
	if spec.Source.Kind != agentcontext.SlotSourceKindHTTPText {
		return agentcontext.SlotResult{}, fmt.Errorf("%w: got %s, want %s",
			agentcontext.ErrResolverNotApplicable, spec.Source.Kind, agentcontext.SlotSourceKindHTTPText)
	}

	src := spec.Source.HTTPText
	body, status, truncated, err := httpFetch(ctx, r.client, src.URL, src.Headers, pickTimeout(src.Timeout, r.timeout), r.maxBody)
	if err != nil {
		return agentcontext.SlotResult{}, err
	}
	content := string(body)
	extra := map[string]string{
		"http_status": fmt.Sprintf("%d", status),
		"url":         src.URL,
	}
	if truncated {
		extra["truncated"] = "true"
	}
	return agentcontext.SlotResult{
		Content:   content,
		Truncated: truncated,
		Provenance: agentcontext.SlotProvenance{
			Kind:        agentcontext.SlotSourceKindHTTPText,
			Source:      src.URL,
			Bytes:       len(content),
			ContentHash: hashContent(content),
			FetchedAt:   nowUTC(),
			Extra:       extra,
		},
	}, nil
}

// httpFetch performs the GET, validates the status code, and reads
// the body capped at maxBody. Used by both http_text and http_json.
func httpFetch(ctx context.Context, client *http.Client, target string, headers map[string]string, timeout time.Duration, maxBody int64) ([]byte, int, bool, error) {
	if strings.TrimSpace(target) == "" {
		return nil, 0, false, fmt.Errorf("http resolver: empty URL")
	}
	if _, err := url.ParseRequestURI(target); err != nil {
		return nil, 0, false, fmt.Errorf("http resolver: bad URL %q: %w", target, err)
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, false, fmt.Errorf("%w: build request: %v", agentcontext.ErrHTTPRequest, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, false, fmt.Errorf("%w: %v", agentcontext.ErrHTTPRequest, err)
	}
	defer resp.Body.Close() //nolint:errcheck // body close failure is non-fatal for the resolver

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a short prefix of the body for diagnostics but
		// discard the rest so the connection can be reused.
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		return nil, resp.StatusCode, false, fmt.Errorf("%w: %d %s", agentcontext.ErrHTTPStatus, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	var reader io.Reader = resp.Body
	if maxBody > 0 {
		reader = io.LimitReader(resp.Body, maxBody+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, resp.StatusCode, false, fmt.Errorf("%w: read body: %v", agentcontext.ErrHTTPRequest, err)
	}
	truncated := false
	if maxBody > 0 && int64(len(data)) > maxBody {
		data = data[:maxBody]
		truncated = true
	}
	return data, resp.StatusCode, truncated, nil
}

// pickTimeout returns specTimeout if positive, else the resolver
// default, else DefaultHTTPTimeout as last-resort fallback.
func pickTimeout(specTimeout, resolverDefault time.Duration) time.Duration {
	if specTimeout > 0 {
		return specTimeout
	}
	if resolverDefault > 0 {
		return resolverDefault
	}
	return DefaultHTTPTimeout
}
