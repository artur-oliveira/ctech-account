package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// devEncryptionKey is a fixed 32-byte key used ONLY when SECRET_ENC_KEY is
// unset (local dev and unit tests). It is NOT safe for production — real
// deployments MUST set SECRET_ENC_KEY to a 32-byte base64 or hex string.
//
// WARNING: never use this key outside of development/test environments.
var devEncryptionKey = []byte("devkey00devkey01devkey02devkey03") // exactly 32 bytes

const gcmNonceSize = 12

// loadEncryptionKey returns the AES-256 key, preferring SECRET_ENC_KEY (base64
// or hex) and falling back to the dev key when unset.
func loadEncryptionKey() ([]byte, error) {
	if len(devEncryptionKey) != 32 {
		// Guards against an accidental change to the dev constant length.
		panic("crypto.devEncryptionKey must be exactly 32 bytes")
	}
	raw := os.Getenv("SECRET_ENC_KEY")
	if raw == "" {
		return devEncryptionKey, nil
	}
	// try base64
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	// try hex
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("SECRET_ENC_KEY must decode to exactly 32 bytes (base64 or hex), got %q", raw)
}

// Seal encrypts plaintext with AES-256-GCM and returns base64(nonce||ciphertext).
// Empty input round-trips to empty output.
func Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := loadEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating gcm: %w", err)
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	out := append(append([]byte{}, nonce...), ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal, returning the decrypted plaintext.
// Empty input round-trips to empty output.
func Open(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	key, err := loadEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating gcm: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decoding ciphertext: %w", err)
	}
	if len(raw) < gcmNonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:gcmNonceSize], raw[gcmNonceSize:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}
	return string(pt), nil
}
