package link

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Service struct {
	repository   Repository
	generateCode func() (string, error)
}

func NewService(repository Repository, generateCode func() (string, error)) *Service {
	return &Service{repository: repository, generateCode: generateCode}
}

func (s *Service) Shorten(ctx context.Context, rawURL string) (Link, error) {
	link, err := New(rawURL)
	if err != nil {
		return Link{}, fmt.Errorf("shorten link: %w", err)
	}
	link.CreatedAt = time.Now().UTC()
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Link{}, fmt.Errorf("shorten link: %w", err)
		}
		code, err := s.generateCode()
		if err != nil {
			return Link{}, fmt.Errorf("generate short link code: %w", err)
		}
		link.Code = code
		if err := s.repository.Save(ctx, link); err != nil {
			if errors.Is(err, ErrCodeTaken) {
				continue
			}
			return Link{}, fmt.Errorf("save link %q: %w", code, err)
		}
		return link, nil
	}
	return Link{}, fmt.Errorf("shorten link after %d attempts: %w", maxAttempts, ErrCodeTaken)
}

func (s *Service) Resolve(ctx context.Context, code string) (string, error) {
	url, err := s.repository.IncrementClicks(ctx, code)
	if err != nil {
		return "", fmt.Errorf("resolve link %q: %w", code, err)
	}
	return url, nil
}

func (s *Service) Stats(ctx context.Context, code string) (Link, error) {
	link, err := s.repository.FindByCode(ctx, code)
	if err != nil {
		return Link{}, fmt.Errorf("find link %q: %w", code, err)
	}
	return link, nil
}
