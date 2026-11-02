package link

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

func GenerateCode() (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var code [7]byte
	limit := big.NewInt(int64(len(alphabet)))
	for i := range code {
		index, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("generate link code: %w", err)
		}
		code[i] = alphabet[index.Int64()]
	}
	return string(code[:]), nil
}
