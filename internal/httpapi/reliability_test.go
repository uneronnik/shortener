package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uneronnik/shortener/internal/link"
)

func TestOperationDeadline(t *testing.T) {
	for _, path := range []string{"/links", "/abcdefg", "/links/abcdefg"} {
		t.Run(path, func(t *testing.T) {
			var logs bytes.Buffer
			wait := func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing deadline")
				}
				<-ctx.Done()
				return ctx.Err()
			}
			service := stubService{
				shorten: func(ctx context.Context, _ string) (link.Link, error) { return link.Link{}, wait(ctx) },
				resolve: func(ctx context.Context, _ string) (string, error) { return "", wait(ctx) },
				stats:   func(ctx context.Context, _ string) (link.Link, error) { return link.Link{}, wait(ctx) },
			}
			h := NewHandler(service, "https://sho.rt", Options{OperationTimeout: time.Millisecond, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			method := http.MethodGet
			if path == "/links" {
				method = http.MethodPost
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{"url":"https://go.dev"}`)))
			assertJSONStatus(t, w, http.StatusServiceUnavailable)
			if !strings.Contains(logs.String(), "deadline_exceeded") {
				t.Fatalf("log = %s", &logs)
			}
		})
	}
}

func TestErrorLoggingSafety(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		logged bool
	}{
		{"internal", errors.New("postgres://user:password@db/private https://private.example/token body-secret"), true},
		{"postgres", &pgconn.PgError{Code: "23514", Message: "private", Detail: "password body-secret"}, true},
		{"invalid", link.ErrInvalidURL, false},
		{"missing", link.ErrNotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := NewHandler(stubService{stats: func(context.Context, string) (link.Link, error) { return link.Link{}, tc.err }}, "", Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/links/private-code", nil))
			if (logs.Len() > 0) != tc.logged {
				t.Fatalf("log = %s", &logs)
			}
			for _, secret := range []string{"password", "private", "body-secret"} {
				if strings.Contains(logs.String()+w.Body.String(), secret) {
					t.Fatalf("leaked %q", secret)
				}
			}
			if tc.logged && !strings.Contains(logs.String(), `"operation":"stats"`) {
				t.Fatalf("missing operation: %s", &logs)
			}
			if tc.name == "postgres" && !strings.Contains(logs.String(), "postgres_23514") {
				t.Fatalf("missing SQLSTATE: %s", &logs)
			}
		})
	}
}

func TestDisconnectedClientCancelsOperation(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan error, 1)
	service := stubService{stats: func(ctx context.Context, _ string) (link.Link, error) {
		close(entered)
		<-ctx.Done()
		finished <- ctx.Err()
		return link.Link{}, ctx.Err()
	}}
	server := httptest.NewServer(NewHandler(service, ""))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /links/abcdefg HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("service not called")
	}
	conn.Close()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not cancel operation")
	}
}

func TestClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	service := stubService{stats: func(got context.Context, _ string) (link.Link, error) {
		cancel()
		<-got.Done()
		if !errors.Is(got.Err(), context.Canceled) {
			t.Fatalf("error = %v", got.Err())
		}
		return link.Link{}, got.Err()
	}}
	w := httptest.NewRecorder()
	NewHandler(service, "").ServeHTTP(w, httptest.NewRequest("GET", "/links/abcdefg", nil).WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatalf("response after cancellation: %s", w.Body)
	}
}
