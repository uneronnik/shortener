package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBaseURL(t *testing.T) {
	for _, value := range []string{"", "https://sho.rt/prefix"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("BASE_URL", value)
			want := value
			if want == "" {
				want = "http://localhost:8080"
			}
			if got := baseURL(); got != want {
				t.Fatalf("baseURL = %q, want %q", got, want)
			}
		})
	}
}

func TestServeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan error, 1)
	go func() {
		result <- serve(ctx, "127.0.0.1:0", http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

func TestServePortInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = serve(ctx, listener.Addr().String(), http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "listen HTTP:") {
		t.Fatalf("error = %v, want listen failure", err)
	}
}

func TestRunDatabaseFailure(t *testing.T) {
	for _, tt := range []struct {
		name string
		url  string
		want string
	}{
		{"missing", "", "DATABASE_URL is required"},
		{"invalid", "postgres://user:secret@localhost:invalid/db", "configure database"},
		{"unavailable", "postgres://user:secret@127.0.0.1:1/db?sslmode=disable", "connect database"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := run(ctx, "127.0.0.1:0", tt.url, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("database error exposes password")
			}
		})
	}
}
