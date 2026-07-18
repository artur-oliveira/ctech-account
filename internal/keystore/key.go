// Package keystore manages the versioned RS256 signing keys: material types,
// SSM-backed storage (jwk/active + jwk/previous) and automatic rotation.
package keystore

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"
)

const rsaKeyBits = 2048

// Key is a signing key with its metadata.
type Key struct {
	KID       string
	Private   *rsa.PrivateKey
	CreatedAt time.Time
}

// KeyJSON is the SSM wire format for a signing key.
type KeyJSON struct {
	KID       string `json:"kid"`
	PEM       string `json:"pem"`
	CreatedAt string `json:"created_at"`
}

// DeriveKID returns the first 16 hex chars of SHA-256 over the PKIX public key
// DER — the same scheme config.loadRSAKey has always used, so wrapping the
// legacy key preserves its KID.
func DeriveKID(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshaling public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])[:16], nil
}

// Generate creates a new RSA-2048 signing key stamped with now.
func Generate(now time.Time) (*Key, error) {
	priv, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, fmt.Errorf("generating RSA key: %w", err)
	}
	kid, err := DeriveKID(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Key{KID: kid, Private: priv, CreatedAt: now.UTC()}, nil
}

// ToJSON serializes the key to its SSM wire format.
func (k *Key) ToJSON() (KeyJSON, error) {
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k.Private)}
	return KeyJSON{
		KID:       k.KID,
		PEM:       string(pem.EncodeToMemory(block)),
		CreatedAt: k.CreatedAt.Format(time.RFC3339),
	}, nil
}

// ParseKey deserializes a key from its SSM wire format.
func ParseKey(j KeyJSON) (*Key, error) {
	block, _ := pem.Decode([]byte(j.PEM))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM in key %s", j.KID)
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing key %s: %w", j.KID, err)
	}
	created, err := time.Parse(time.RFC3339, j.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing created_at of key %s: %w", j.KID, err)
	}
	return &Key{KID: j.KID, Private: priv, CreatedAt: created}, nil
}

// parseLegacyPEM decodes the pre-rotation raw PEM parameter (PKCS#1 or
// PKCS#8) into a Key with its KID derived — identical to config.loadRSAKey.
func parseLegacyPEM(pemStr string, now time.Time) (*Key, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("legacy key: failed to decode PEM block")
	}

	var priv *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("legacy key: parsing PKCS1: %w", err)
		}
		priv = parsed
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("legacy key: parsing PKCS8: %w", err)
		}
		var ok bool
		priv, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("legacy key: not RSA")
		}
	default:
		return nil, fmt.Errorf("legacy key: unsupported PEM block type %q", block.Type)
	}

	kid, err := DeriveKID(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Key{KID: kid, Private: priv, CreatedAt: now.UTC()}, nil
}
