package link

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https", url: "https://go.dev"},
		{name: "http", url: "http://example.com"},
		{name: "path query and fragment", url: "https://example.com/docs?q=go#intro"},
		{name: "localhost with port", url: "http://localhost:8080/healthz"},
		{name: "IPv6", url: "http://[::1]:8080/"},
		{name: "empty", url: "", wantErr: true},
		{name: "whitespace", url: "   ", wantErr: true},
		{name: "unsupported scheme", url: "ftp://example.com/file", wantErr: true},
		{name: "relative path", url: "/docs", wantErr: true},
		{name: "missing scheme", url: "example.com/docs", wantErr: true},
		{name: "scheme relative", url: "//example.com/docs", wantErr: true},
		{name: "missing host", url: "https:///docs", wantErr: true},
		{name: "port without hostname", url: "http://:8080/docs", wantErr: true},
		{name: "space in host", url: "https://exa mple.com", wantErr: true},
		{name: "invalid escape", url: "https://example.com/%zz", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.url)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("New(%q) error = %v, want ErrInvalidURL", tt.url, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q): %v", tt.url, err)
			}
			if got.URL != tt.url {
				t.Errorf("URL = %q, want %q", got.URL, tt.url)
			}
		})
	}
}
