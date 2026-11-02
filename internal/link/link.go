package link

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

var (
	ErrInvalidURL = errors.New("invalid URL")
	ErrNotFound   = errors.New("link not found")
	ErrCodeTaken  = errors.New("link code already taken")
)

type Link struct {
	URL       string
	Code      string
	Clicks    int64
	CreatedAt time.Time
}

func New(rawURL string) (Link, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Link{}, fmt.Errorf("parse link URL: %w: %v", ErrInvalidURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return Link{}, fmt.Errorf("validate link URL: %w: expected an absolute HTTP or HTTPS URL with a hostname", ErrInvalidURL)
	}
	return Link{URL: rawURL}, nil
}
