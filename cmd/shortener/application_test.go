package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/uneronnik/shortener/migrations"
)

func TestRunConfiguredOperationTimeout(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("application_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean up schema: %v", err)
		}
	}()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config.ConnConfig)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// ConnString returns the original input, so add the schema to that input too.
	applicationURL := databaseURL + " search_path=" + schema
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatal("parse test database URL")
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		applicationURL = parsed.String()
	}
	for _, timeout := range []time.Duration{100 * time.Millisecond, 800 * time.Millisecond} {
		t.Run(timeout.String(), func(t *testing.T) {
			t.Setenv("HTTP_READ_TIMEOUT", "1s")
			t.Setenv("OPERATION_TIMEOUT", timeout.String())
			t.Setenv("HTTP_WRITE_TIMEOUT", "5s")
			tx, err := admin.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := tx.Rollback(cleanupCtx); err != nil {
					t.Errorf("release table lock: %v", err)
				}
			}()
			// A table lock blocks the real repository query without delaying health checks.
			if _, err := tx.Exec(ctx, "LOCK TABLE "+quotedSchema+".links IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			listener.Close()
			runCtx, stop := context.WithCancel(ctx)
			result := make(chan error, 1)
			go func() { result <- run(runCtx, addr, applicationURL, slog.New(slog.NewJSONHandler(io.Discard, nil))) }()
			defer func() {
				stop()
				select {
				case err := <-result:
					if err != nil {
						t.Errorf("run: %v", err)
					}
				case <-time.After(6 * time.Second):
					t.Error("application did not stop")
				}
			}()
			client := &http.Client{Timeout: 2 * time.Second}
			defer client.CloseIdleConnections()
			baseURL := "http://" + addr
			readyCtx, stopReady := context.WithTimeout(ctx, 3*time.Second)
			defer stopReady()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				req, err := http.NewRequestWithContext(readyCtx, http.MethodGet, baseURL+"/healthz", nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := client.Do(req)
				if err == nil {
					response.Body.Close()
					if response.StatusCode == http.StatusOK {
						break
					}
				}
				select {
				case <-readyCtx.Done():
					t.Fatal("application did not become ready")
				case <-ticker.C:
				}
			}
			started := time.Now()
			response, err := client.Get(baseURL + "/links/abcdefg")
			elapsed := time.Since(started)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer response.Body.Close()
			var body map[string]string
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusServiceUnavailable || body["error"] != "operation timed out" {
				t.Fatalf("response = %d %v, want operation timeout", response.StatusCode, body)
			}
			// Allow scheduler/network overhead while distinguishing both configured budgets
			// from one another and from the default three-second timeout.
			if elapsed < timeout || elapsed > timeout+500*time.Millisecond {
				t.Fatalf("elapsed = %s, want %s to %s", elapsed, timeout, timeout+500*time.Millisecond)
			}
		})
	}
}
