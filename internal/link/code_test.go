package link

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

type failingRandomReader struct{ err error }

func (r failingRandomReader) Read([]byte) (int, error) { return 0, r.err }

func TestGenerateCodeRandomSourceFailure(t *testing.T) {
	original := rand.Reader
	t.Cleanup(func() { rand.Reader = original })
	want := errors.New("random source unavailable")
	rand.Reader = failingRandomReader{want}
	code, err := GenerateCode()
	if code != "" || !errors.Is(err, want) {
		t.Fatalf("GenerateCode = %q, %v, want empty code and source error", code, err)
	}
}

func TestGenerateCode(t *testing.T) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

	for i := 0; i < 100; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode() on call %d: %v", i, err)
		}
		if len(code) != 7 {
			t.Fatalf("code = %q, length = %d, want 7", code, len(code))
		}
		for _, char := range code {
			if !strings.ContainsRune(alphabet, char) {
				t.Errorf("code = %q contains non-base62 character %q", code, char)
			}
		}
	}
}
