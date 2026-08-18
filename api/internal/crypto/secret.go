package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"

	"gopkg.aoctech.app/account/api/internal/config"
)

// devEncryptionKey is a fixed 32-byte key used ONLY when SECRET_ENC_KEY is
// unset (local dev and unit tests). It is NOT safe for production — real
// deployments MUST set SECRET_ENC_KEY to a 32-byte base64 or hex string.
//
// WARNING: never use this key outside of development/test environments.
var devEncryptionKey = []byte("devkey00devkey01devkey02devkey03") // exactly 32 bytes

const gcmNonceSize = 12

type Sealer struct {
	key []byte
}

func NewSealer(cfg *config.Config) (*Sealer, error) {
	// SECRET_ENC_KEY must be set in non-dev environments to avoid using hardcoded development key.
	// The crypto/secret package falls back to a dev key when SECRET_ENC_KEY is unset, which is
	// unsafe for production. Refuse to boot without it in non-dev environments.
	raw := os.Getenv("SECRET_ENC_KEY")
	if raw == "" && cfg.Environment != "dev" && cfg.Environment != "development" {
		log.Fatalf("SECRET_ENC_KEY must be set in environment %q to avoid using hardcoded development encryption key", cfg.Environment)
	}
	key, err := loadEncryptionKey(raw)
	if err != nil {
		return nil, err
	}
	return &Sealer{
		key: key,
	}, nil
}

// loadEncryptionKey returns the AES-256 key, preferring SECRET_ENC_KEY (base64
// or hex) and falling back to the dev key when unset.
func loadEncryptionKey(encryptionKey string) ([]byte, error) {
	if encryptionKey == "" {
		if len(devEncryptionKey) != 32 {
			// Guards against an accidental change to the dev constant length.
			panic("crypto.devEncryptionKey must be exactly 32 bytes")
		}
		return devEncryptionKey, nil
	}
	// try base64
	if b, err := base64.StdEncoding.DecodeString(encryptionKey); err == nil && len(b) == 32 {
		return b, nil
	}
	// try hex
	if b, err := hex.DecodeString(encryptionKey); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("SECRET_ENC_KEY must decode to exactly 32 bytes (base64 or hex), got %q", encryptionKey)
}

func (s Sealer) Seal(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	block, err := aes.NewCipher(s.key)
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
	ct := gcm.Seal(nil, nonce, []byte(text), nil)
	out := append(append([]byte{}, nonce...), ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (s Sealer) Unseal(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(s.key)
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
