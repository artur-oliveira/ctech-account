package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func generateRandom(bits int) ([]byte, error) {
	bytes := make([]byte, bits/8)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("generating random bytes: %w", err)
	}
	return bytes, nil
}

func toBase62(b []byte) string {
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	zero := big.NewInt(0)
	mod := new(big.Int)

	var result strings.Builder
	for n.Cmp(zero) > 0 {
		n.DivMod(n, base, mod)
		result.WriteByte(base62Chars[mod.Int64()])
	}

	s := []byte(result.String())
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return string(s)
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// GenerateOpaqueToken generates a 256-bit opaque token in base62 format
// prefixed with "ctk_", along with its SHA-256 hex hash.
func GenerateOpaqueToken() (token, hashHex string, err error) {
	b, err := generateRandom(256)
	if err != nil {
		return "", "", err
	}
	raw := "ctk_" + toBase62(b)
	return raw, hashBytes([]byte(raw)), nil
}

// GenerateRefreshToken generates a 256-bit refresh token.
func GenerateRefreshToken() (token, hashHex string, err error) {
	b, err := generateRandom(256)
	if err != nil {
		return "", "", err
	}
	raw := toBase62(b)
	return raw, hashBytes([]byte(raw)), nil
}

// GenerateCode generates a 128-bit authorization code.
func GenerateCode() (code, hashHex string, err error) {
	b, err := generateRandom(128)
	if err != nil {
		return "", "", err
	}
	raw := toBase62(b)
	return raw, hashBytes([]byte(raw)), nil
}

// HashToken returns the SHA-256 hex hash of a token.
func HashToken(token string) string {
	return hashBytes([]byte(token))
}

// GenerateMFAToken generates a short-lived MFA continuation token.
func GenerateMFAToken() (token, hashHex string, err error) {
	b, err := generateRandom(128)
	if err != nil {
		return "", "", err
	}
	raw := "mfa_" + hex.EncodeToString(b)
	return raw, hashBytes([]byte(raw)), nil
}
