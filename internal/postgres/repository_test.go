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

func singleConnectionRepository(t *testing.T, source *Repository, ctx context.Context) *Repository {
	t.Helper()
	config := source.pool.Config()
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewRepository(pool)
}

func TestPoolWaitDeadline(t *testing.T) {
	original, setupCtx := newTestRepository(t)
	repository := singleConnectionRepository(t, original, setupCtx)
	held, err := repository.pool.Acquire(setupCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	for _, operation := range []struct {
		name string
		call func(context.Context) error
	}{
		{"save", func(ctx context.Context) error {
			return repository.Save(ctx, link.Link{Code: "abcdefg", URL: "https://go.dev", CreatedAt: time.Now()})
		}},
		{"find", func(ctx context.Context) error { _, err := repository.FindByCode(ctx, "abcdefg"); return err }},
		{"increment", func(ctx context.Context) error { _, err := repository.IncrementClicks(ctx, "abcdefg"); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(setupCtx, 20*time.Millisecond)
			defer cancel()
			if err := operation.call(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	held.Release()
	if err := repository.pool.Ping(setupCtx); err != nil {
		t.Fatalf("pool unusable after timeouts: %v", err)
	}
}

func TestBlockedUpdateCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			original, setupCtx := newTestRepository(t)
			repository := singleConnectionRepository(t, original, setupCtx)
			value := link.Link{Code: "abcdefg", URL: "https://go.dev", CreatedAt: time.Now()}
			if err := repository.Save(setupCtx, value); err != nil {
				t.Fatal(err)
			}
			connection, err := repository.pool.Acquire(setupCtx)
			if err != nil {
				t.Fatal(err)
			}
			pid := connection.Conn().PgConn().PID()
			connection.Release()
			tx, err := original.pool.Begin(setupCtx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(setupCtx)
			if _, err := tx.Exec(setupCtx, "SELECT 1 FROM links WHERE code = $1 FOR UPDATE", value.Code); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(setupCtx, time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := repository.IncrementClicks(ctx, value.Code); result <- err }()
			// Observe the actual database lock wait before exercising cancellation.
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if err := original.pool.QueryRow(setupCtx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock')", pid).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("SQL did not enter lock wait")
				case <-ticker.C:
				}
			}
			want := context.DeadlineExceeded
			if mode == "cancel" {
				cancel()
				want = context.Canceled
			}
			select {
			case err := <-result:
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			case <-setupCtx.Done():
				t.Fatal("blocked SQL did not return")
			}
			// Driver cancellation may return before PostgreSQL receives the cancel packet.
			// Keep the lock until the backend has stopped, so the update cannot race cleanup.
			stopCtx, stopBackend := context.WithTimeout(setupCtx, 2*time.Second)
			defer stopBackend()
			for {
				var active bool
				if err := original.pool.QueryRow(stopCtx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND state = 'active')", pid).Scan(&active); err != nil {
					t.Fatal(err)
				}
				if !active {
					break
				}
				select {
				case <-stopCtx.Done():
					t.Fatal("database backend did not stop")
				case <-ticker.C:
				}
			}
			if err := tx.Rollback(setupCtx); err != nil {
				t.Fatal(err)
			}
			checkCtx, stop := context.WithTimeout(setupCtx, 2*time.Second)
			defer stop()
			got, err := repository.FindByCode(checkCtx, value.Code)
			if err != nil || got.Clicks != 0 {
				t.Fatalf("after canceled SQL: %+v, %v", got, err)
			}
		})
	}
}
