package link

import "context"

type Repository interface {
	// Save returns ErrCodeTaken if the code already exists.
	Save(ctx context.Context, link Link) error
	// FindByCode returns ErrNotFound if the code does not exist.
	FindByCode(ctx context.Context, code string) (Link, error)
	// IncrementClicks atomically increments the counter and returns the URL.
	// It returns ErrNotFound if the code does not exist.
	IncrementClicks(ctx context.Context, code string) (string, error)
}
