package link

import (
	"strings"
	"testing"
)

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
