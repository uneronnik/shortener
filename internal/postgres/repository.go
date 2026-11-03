package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uneronnik/shortener/internal/link"
)

type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository uses the supplied pool; the caller owns its lifecycle.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Save(ctx context.Context, value link.Link) error {
	_, err := r.pool.Exec(ctx,
		"INSERT INTO links (code, url, clicks, created_at) VALUES ($1, $2, $3, $4)",
		value.Code, value.URL, value.Clicks, value.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "links_pkey" {
			err = link.ErrCodeTaken
		}
		return fmt.Errorf("save link %q: %w", value.Code, err)
	}
	return nil
}

func (r *Repository) FindByCode(ctx context.Context, code string) (link.Link, error) {
	var value link.Link
	err := r.pool.QueryRow(ctx,
		"SELECT code, url, clicks, created_at FROM links WHERE code = $1", code).
		Scan(&value.Code, &value.URL, &value.Clicks, &value.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = link.ErrNotFound
		}
		return link.Link{}, fmt.Errorf("find link %q: %w", code, err)
	}
	return value, nil
}

func (r *Repository) IncrementClicks(ctx context.Context, code string) (string, error) {
	var url string
	err := r.pool.QueryRow(ctx,
		"UPDATE links SET clicks = clicks + 1 WHERE code = $1 RETURNING url", code).Scan(&url)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = link.ErrNotFound
		}
		return "", fmt.Errorf("increment clicks for link %q: %w", code, err)
	}
	return url, nil
}
