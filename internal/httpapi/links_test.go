package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uneronnik/shortener/internal/link"
)

type stubService struct {
	shorten func(context.Context, string) (link.Link, error)
	resolve func(context.Context, string) (string, error)
	stats   func(context.Context, string) (link.Link, error)
}

func (s stubService) Shorten(ctx context.Context, url string) (link.Link, error) {
	return s.shorten(ctx, url)
}

func (s stubService) Resolve(ctx context.Context, code string) (string, error) {
	return s.resolve(ctx, code)
}

func (s stubService) Stats(ctx context.Context, code string) (link.Link, error) {
	return s.stats(ctx, code)
}

func TestCreateLink(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	service := stubService{shorten: func(gotCtx context.Context, url string) (link.Link, error) {
		if gotCtx != ctx || url != "https://go.dev" {
			t.Fatalf("Shorten arguments = %v, %q", gotCtx, url)
		}
		return link.Link{Code: "aB3xK9q"}, nil
	}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/links", strings.NewReader(`{"url":"https://go.dev"}`)).WithContext(ctx)
	NewHandler(service, "https://sho.rt/prefix/").ServeHTTP(w, r)
	assertJSONStatus(t, w, http.StatusCreated)
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "aB3xK9q" || got["short_url"] != "https://sho.rt/prefix/aB3xK9q" || len(got) != 2 {
		t.Fatalf("response = %v", got)
	}
}

func TestCreateLinkInvalidJSON(t *testing.T) {
	for _, body := range []string{"", "{", `null`, `[]`, `{"url":1}`, `{"url":"https://go.dev"} {}`, `{"url":"https://go.dev"} junk`, strings.Repeat(" ", (1<<20)+1)} {
		t.Run(fmt.Sprintf("length_%d_%s", len(body), body[:min(len(body), 30)]), func(t *testing.T) {
			service := stubService{shorten: func(context.Context, string) (link.Link, error) {
				t.Fatal("Shorten called for invalid JSON")
				return link.Link{}, nil
			}}
			w := httptest.NewRecorder()
			NewHandler(service, "https://sho.rt").ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/links", strings.NewReader(body)))
			assertJSONStatus(t, w, http.StatusBadRequest)
		})
	}
}

func TestCreateLinkInvalidURL(t *testing.T) {
	// Validation must fail before the service accesses its repository or generator.
	h := NewHandler(link.NewService(nil, nil), "https://sho.rt")
	for _, body := range []string{`{}`, `{"url":""}`, `{"url":null}`, `{"url":"ftp://example.com"}`, `{"url":"/relative"}`, `{"url":"https://"}`} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/links", strings.NewReader(body)))
			assertJSONStatus(t, w, http.StatusBadRequest)
		})
	}
}

func TestRedirectAndStats(t *testing.T) {
	createdAt := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	check := func(got context.Context, code string) {
		t.Helper()
		if got != ctx || code != "aB3xK9q" {
			t.Fatalf("service arguments = %v, %q", got, code)
		}
	}
	service := stubService{
		resolve: func(got context.Context, code string) (string, error) {
			check(got, code)
			return "https://go.dev", nil
		},
		stats: func(got context.Context, code string) (link.Link, error) {
			check(got, code)
			return link.Link{URL: "https://go.dev", Code: code, Clicks: 2, CreatedAt: createdAt}, nil
		},
	}
	h := NewHandler(service, "https://sho.rt")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/aB3xK9q", nil).WithContext(ctx))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("redirect = %d, %v", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/links/aB3xK9q", nil).WithContext(ctx))
	assertJSONStatus(t, w, http.StatusOK)
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["url"] != "https://go.dev" || got["code"] != "aB3xK9q" || got["clicks"] != float64(2) || got["created_at"] != createdAt.Format(time.RFC3339) {
		t.Fatalf("stats = %v", got)
	}
}

func TestServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"invalid URL", fmt.Errorf("validation: %w", link.ErrInvalidURL), http.StatusBadRequest},
		{"not found", fmt.Errorf("repository: %w", link.ErrNotFound), http.StatusNotFound},
		{"collision exhausted", link.ErrCodeTaken, http.StatusInternalServerError},
		{"unexpected", errors.New("secret database credentials"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := stubService{
				shorten: func(context.Context, string) (link.Link, error) { return link.Link{}, tc.err },
				resolve: func(context.Context, string) (string, error) { return "", tc.err },
				stats:   func(context.Context, string) (link.Link, error) { return link.Link{}, tc.err },
			}
			h := NewHandler(service, "https://sho.rt")
			for _, endpoint := range []struct{ method, path string }{{"POST", "/links"}, {"GET", "/abc1234"}, {"GET", "/links/abc1234"}} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{"url":"https://go.dev"}`)))
				assertJSONStatus(t, w, tc.status)
				if strings.Contains(w.Body.String(), "secret") {
					t.Fatal("response leaks internal error")
				}
			}
		})
	}
}

func TestLinkRouting(t *testing.T) {
	h := NewHandler(stubService{}, "https://sho.rt")
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/healthz", 200},
		{"POST", "/healthz", 405},
		{"DELETE", "/abc1234", 405},
		{"POST", "/links/abc1234", 405},
		{"GET", "/too/many/segments", 404},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
}

func assertJSONStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status || w.Header().Get("Content-Type") != "application/json" || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("response = %d %v %q, want JSON with status %d", w.Code, w.Header(), w.Body.String(), status)
	}
}
