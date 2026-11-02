package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestRunRequiresDatabaseURL(t *testing.T) {
	err := run(context.Background(), "", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("run without database URL = %v, want configuration error", err)
	}
}

func TestRunMigrations(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { db.Close() })
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean up test schema: %v", err)
		}
	})
	config.RuntimeParams["search_path"] = schema
	isolatedDB := stdlib.OpenDB(*config)
	defer isolatedDB.Close()
	// Pass the schema as a URL parameter so the command uses the same isolated schema.
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	isolatedURL := parsedURL.String()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if err := run(ctx, isolatedURL, logger); err != nil {
		t.Fatal(err)
	}
	var clicks int64
	var createdAt time.Time
	err = isolatedDB.QueryRowContext(ctx, "INSERT INTO links (code, url) VALUES ($1, $2) RETURNING clicks, created_at", "aB3xK9q", "https://go.dev").Scan(&clicks, &createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if clicks != 0 || createdAt.IsZero() {
		t.Fatalf("defaults: clicks=%d, created_at=%v", clicks, createdAt)
	}
	_, err = isolatedDB.ExecContext(ctx, "INSERT INTO links (code, url) VALUES ($1, $2)", "aB3xK9q", "https://example.com")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate code error = %v, want unique violation", err)
	}
	if err := run(ctx, isolatedURL, logger); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var count int
	if err := isolatedDB.QueryRowContext(ctx, "SELECT count(*) FROM links").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("links after repeated migration = %d, want 1", count)
	}
}
