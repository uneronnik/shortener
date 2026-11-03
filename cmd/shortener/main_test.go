package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/uneronnik/shortener/internal/httpapi"
	"github.com/uneronnik/shortener/internal/link"
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

func TestSlowRequestBody(t *testing.T) {
	t.Setenv("HTTP_READ_TIMEOUT", "50ms")
	t.Setenv("OPERATION_TIMEOUT", "50ms")
	t.Setenv("HTTP_WRITE_TIMEOUT", "1s")
	server, err := newHTTPServer("", httpapi.NewHandler(link.NewService(nil, nil), ""))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	// Send valid headers but only part of the declared body, then wait for the server.
	fmt.Fprint(conn, "POST /links HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n{")
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusRequestTimeout || !strings.Contains(string(body), "request body timeout") {
		t.Fatalf("response = %v %q, %v", response.StatusCode, body, err)
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

func TestServeDrainsActiveRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	result := make(chan error, 1)
	go func() {
		result <- serve(ctx, addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "completed")
		}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	var conn net.Conn
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err = net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("server did not start")
		case <-ticker.C:
		}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(7 * time.Second))
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	cancel()
	// Observe the listener closing before checking that the active request is retained.
	deadline := time.After(2 * time.Second)
	for {
		probe, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			break
		}
		probe.Close()
		select {
		case <-deadline:
			t.Fatal("shutdown did not close listener")
		case <-ticker.C:
		}
	}
	select {
	case err := <-result:
		t.Fatalf("server returned before request completed: %v", err)
	default:
	}
	close(release)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(body) != "completed" {
		t.Fatalf("response = %d %q, %v", response.StatusCode, body, err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("server did not finish shutdown")
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
