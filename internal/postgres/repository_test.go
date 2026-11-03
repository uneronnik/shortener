package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/uneronnik/shortener/internal/link"
	"github.com/uneronnik/shortener/migrations"
)

var _ link.Repository = (*Repository)(nil)

func newTestRepository(t *testing.T) (*Repository, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := pgx.Identifier{fmt.Sprintf("repository_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean up test schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config.ConnConfig)
	t.Cleanup(func() { db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewRepository(pool), ctx
}

func TestSaveAndFindByCode(t *testing.T) {
	repository, ctx := newTestRepository(t)
	want := link.Link{
		Code: "aB3xK9q", URL: "https://example.com/path?q=日本語&x='value'",
		Clicks: 7, CreatedAt: time.Date(2026, 11, 3, 12, 0, 0, 123456000, time.UTC),
	}
	if err := repository.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := repository.FindByCode(ctx, want.Code)
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != want.Code || got.URL != want.URL || got.Clicks != want.Clicks || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("FindByCode = %+v, want %+v", got, want)
	}
	duplicate := want
	duplicate.URL = "https://other.example"
	if err := repository.Save(ctx, duplicate); !errors.Is(err, link.ErrCodeTaken) {
		t.Fatalf("duplicate code error = %v, want ErrCodeTaken", err)
	}
	got, err = repository.FindByCode(ctx, want.Code)
	if err != nil || got.URL != want.URL || got.Clicks != want.Clicks {
		t.Fatalf("link after collision = %+v, %v, want original link", got, err)
	}
	want.Code = "bB3xK9q"
	if err := repository.Save(ctx, want); err != nil {
		t.Fatalf("save same URL under another code: %v", err)
	}
}

func TestMissingCode(t *testing.T) {
	repository, ctx := newTestRepository(t)
	got, err := repository.FindByCode(ctx, "missing")
	if !errors.Is(err, link.ErrNotFound) || got != (link.Link{}) {
		t.Fatalf("FindByCode = %+v, %v, want zero link and ErrNotFound", got, err)
	}
	url, err := repository.IncrementClicks(ctx, "missing")
	if !errors.Is(err, link.ErrNotFound) || url != "" {
		t.Fatalf("IncrementClicks = %q, %v, want empty URL and ErrNotFound", url, err)
	}
}

func TestIncrementClicksConcurrent(t *testing.T) {
	repository, ctx := newTestRepository(t)
	original := link.Link{Code: "aB3xK9q", URL: "https://go.dev", CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := repository.Save(ctx, original); err != nil {
		t.Fatal(err)
	}
	const requests = 64
	results := make(chan error, requests)
	start := make(chan struct{})
	for range requests {
		go func() {
			<-start
			url, err := repository.IncrementClicks(ctx, original.Code)
			if err == nil && url != original.URL {
				err = fmt.Errorf("URL = %q, want %q", url, original.URL)
			}
			results <- err
		}()
	}
	close(start)
	for range requests {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	got, err := repository.FindByCode(ctx, original.Code)
	if err != nil {
		t.Fatal(err)
	}
	if got.Clicks != requests || got.URL != original.URL || !got.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("link after increments = %+v, want %d clicks and unchanged URL/time", got, requests)
	}
}

func TestCanceledContext(t *testing.T) {
	repository, setupCtx := newTestRepository(t)
	original := link.Link{Code: "aB3xK9q", URL: "https://go.dev", CreatedAt: time.Now().UTC()}
	if err := repository.Save(setupCtx, original); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(setupCtx)
	cancel()
	tests := []struct {
		name string
		call func() error
	}{
		{"save", func() error {
			return repository.Save(ctx, link.Link{Code: "bB3xK9q", URL: original.URL, CreatedAt: original.CreatedAt})
		}},
		{"find", func() error { _, err := repository.FindByCode(ctx, original.Code); return err }},
		{"increment", func() error { _, err := repository.IncrementClicks(ctx, original.Code); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
	got, err := repository.FindByCode(setupCtx, original.Code)
	if err != nil || got.Clicks != 0 {
		t.Fatalf("link after canceled increment = %+v, %v, want zero clicks", got, err)
	}
	if _, err := repository.FindByCode(setupCtx, "bB3xK9q"); !errors.Is(err, link.ErrNotFound) {
		t.Fatalf("canceled save error = %v, want no saved link", err)
	}
}

func TestSavePreservesDatabaseErrors(t *testing.T) {
	repository, ctx := newTestRepository(t)
	err := repository.Save(ctx, link.Link{Code: "aB3xK9q", URL: "https://go.dev", Clicks: -1, CreatedAt: time.Now().UTC()})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || errors.Is(err, link.ErrCodeTaken) {
		t.Fatalf("negative clicks error = %v, want preserved check violation", err)
	}
}
