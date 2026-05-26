package resolvers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func TestHTTPTextResolver_Happy(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello-http"))
	}))
	defer srv.Close()

	r := NewHTTPTextResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "http",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{URL: srv.URL},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "hello-http" {
		t.Fatalf("Content: %q", got.Content)
	}
	if got.Provenance.Extra["http_status"] != "200" {
		t.Fatalf("status: %q", got.Provenance.Extra["http_status"])
	}
}

func TestHTTPTextResolver_Headers(t *testing.T) {
	t.Parallel()
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r := NewHTTPTextResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "h",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{
					URL:     srv.URL,
					Headers: map[string]string{"Authorization": "Bearer xyz"},
				},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if seenAuth != "Bearer xyz" {
		t.Fatalf("server saw auth %q", seenAuth)
	}
}

func TestHTTPTextResolver_Non2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := NewHTTPTextResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "bad",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{URL: srv.URL},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrHTTPStatus) {
		t.Fatalf("expected ErrHTTPStatus, got %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 in error, got %v", err)
	}
}

func TestHTTPTextResolver_BodyCapTruncates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	r := NewHTTPTextResolver(WithHTTPMaxBody(4))
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "cap",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{URL: srv.URL},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "0123" {
		t.Fatalf("Content: %q", got.Content)
	}
	if !got.Truncated {
		t.Fatalf("expected Truncated=true")
	}
}

func TestHTTPTextResolver_Timeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	r := NewHTTPTextResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "slow",
			Source: agentcontext.SlotSource{
				Kind: agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{
					URL:     srv.URL,
					Timeout: 20 * time.Millisecond,
				},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrHTTPRequest) {
		t.Fatalf("expected ErrHTTPRequest (timeout), got %v", err)
	}
}

func TestHTTPTextResolver_BadURL(t *testing.T) {
	t.Parallel()
	r := NewHTTPTextResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "bad",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{URL: "://not-a-url"},
			},
		},
		agentcontext.ResolverEnv{})
	if err == nil {
		t.Fatalf("expected URL parse error")
	}
}

func TestHTTPTextResolver_EmptyURL(t *testing.T) {
	t.Parallel()
	r := NewHTTPTextResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name:   "empty",
			Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindHTTPText},
		},
		agentcontext.ResolverEnv{})
	if err == nil || !strings.Contains(err.Error(), "empty URL") {
		t.Fatalf("expected empty-URL error, got %v", err)
	}
}

func TestHTTPTextResolver_WrongKind(t *testing.T) {
	t.Parallel()
	r := NewHTTPTextResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name:   "x",
			Source: agentcontext.SlotSource{Kind: agentcontext.SlotSourceKindInline},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrResolverNotApplicable) {
		t.Fatalf("expected ErrResolverNotApplicable, got %v", err)
	}
}
