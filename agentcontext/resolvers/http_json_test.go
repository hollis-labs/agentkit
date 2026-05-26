package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
)

func jsonServer(t *testing.T, payload interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func TestHTTPJSONResolver_WholeDocument(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{"a": 1, "b": "two"})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "j",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got.Content, "\"a\"") || !strings.Contains(got.Content, "\"b\"") {
		t.Fatalf("Content missing keys: %s", got.Content)
	}
}

func TestHTTPJSONResolver_TopLevelKey(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{"foo": "bar"})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "k",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL, JSONPath: "$.foo"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "bar" {
		t.Fatalf("Content: %q", got.Content)
	}
}

func TestHTTPJSONResolver_NestedKey(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{"outer": map[string]interface{}{"inner": "deep"}})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "n",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL, JSONPath: "$.outer.inner"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "deep" {
		t.Fatalf("Content: %q", got.Content)
	}
}

func TestHTTPJSONResolver_ArrayIndex(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"name": "alpha"},
			map[string]interface{}{"name": "beta"},
		},
	})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "a",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL, JSONPath: "$.items[1].name"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != "beta" {
		t.Fatalf("Content: %q", got.Content)
	}
}

func TestHTTPJSONResolver_NonStringLeafReencoded(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{"obj": map[string]interface{}{"k": 1}})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	got, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "re",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL, JSONPath: "$.obj"},
			},
		},
		agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got.Content, "\"k\": 1") {
		t.Fatalf("Content: %q", got.Content)
	}
}

func TestHTTPJSONResolver_PathNotFound(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{"foo": "bar"})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "miss",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL, JSONPath: "$.missing"},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrJSONPathNotFound) {
		t.Fatalf("expected ErrJSONPathNotFound, got %v", err)
	}
}

func TestHTTPJSONResolver_InvalidJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	r := NewHTTPJSONResolver()
	_, err := r.Resolve(context.Background(),
		agentcontext.SlotSpec{
			Name: "bad",
			Source: agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL},
			},
		},
		agentcontext.ResolverEnv{})
	if !errors.Is(err, agentcontext.ErrInvalidJSON) {
		t.Fatalf("expected ErrInvalidJSON, got %v", err)
	}
}

func TestHTTPJSONResolver_UnsupportedSyntax(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{"a": "b"})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	for _, p := range []string{"$.a.*", "$..a", "$.a[?(@.x)]"} {
		_, err := r.Resolve(context.Background(),
			agentcontext.SlotSpec{
				Name: "unsup",
				Source: agentcontext.SlotSource{
					Kind:     agentcontext.SlotSourceKindHTTPJSON,
					HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL, JSONPath: p},
				},
			},
			agentcontext.ResolverEnv{})
		if !errors.Is(err, agentcontext.ErrJSONPathNotFound) {
			t.Fatalf("path %q: expected ErrJSONPathNotFound, got %v", p, err)
		}
	}
}

func TestHTTPJSONResolver_WrongKind(t *testing.T) {
	t.Parallel()
	r := NewHTTPJSONResolver()
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

func TestHTTPJSONResolver_Determinism(t *testing.T) {
	t.Parallel()
	srv := jsonServer(t, map[string]interface{}{"foo": "bar"})
	defer srv.Close()

	r := NewHTTPJSONResolver()
	spec := agentcontext.SlotSpec{
		Name: "d",
		Source: agentcontext.SlotSource{
			Kind:     agentcontext.SlotSourceKindHTTPJSON,
			HTTPJSON: agentcontext.HTTPJSONSource{URL: srv.URL, JSONPath: "$.foo"},
		},
	}
	a, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := r.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a.Content != b.Content || a.Provenance.ContentHash != b.Provenance.ContentHash {
		t.Fatalf("determinism broken: %+v vs %+v", a, b)
	}
}
