// Package keystore manages the versioned RS256/ES256 signing keys: material
// types, SSM-backed storage (jwk/active + jwk/previous) and automatic
// rotation.
package keystore

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"
)

const (
	rsaKeyBits = 3072

	// AlgRS256 and AlgES256 are the only signing algorithms this service
	// supports. Every Key carries one of these two values.
	AlgRS256 = "RS256"
	AlgES256 = "ES256"
)

// Key is a signing key with its metadata. Private is *rsa.PrivateKey when
// Alg == AlgRS256, or *ecdsa.PrivateKey when Alg == AlgES256 — Generate and
// ParseKey are the only two constructors, and both enforce this invariant.
type Key struct {
	KID       string
	Alg       string
	Private   crypto.Signer
	CreatedAt time.Time
}

// KeyJSON is the SSM wire format for a signing key. Alg is absent ("") on
// keys stored before this migration — the pre-ES256 wire format was always
// PKCS1 RSA with no algorithm field.
type KeyJSON struct {
	KID       string `json:"kid"`
	Alg       string `json:"alg,omitempty"`
	PEM       string `json:"pem"`
	CreatedAt string `json:"created_at"`
}

// DeriveKID returns the full SHA-256 hex (256 bits) over the PKIX public key
// DER — the same scheme config.loadSigningKey has always used, so wrapping
// the legacy key preserves its KID. SEC-044: previously truncated to 64
// bits; now the full digest is used for collision resistance. This only
// affects newly derived KIDs; existing keys keep their explicitly stored KID.
func DeriveKID(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshaling public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// Generate creates a new signing key of the given algorithm, stamped with
// now. alg must be AlgRS256 or AlgES256.
func Generate(now time.Time, alg string) (*Key, error) {
	var signer crypto.Signer
	switch alg {
	case AlgRS256:
		priv, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
		if err != nil {
			return nil, fmt.Errorf("generating RSA key: %w", err)
		}
		signer = priv
	case AlgES256:
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generating EC key: %w", err)
		}
		signer = priv
	default:
		return nil, fmt.Errorf("unsupported signing algorithm %q", alg)
	}
	kid, err := DeriveKID(signer.Public())
	if err != nil {
		return nil, err
	}
	return &Key{KID: kid, Alg: alg, Private: signer, CreatedAt: now.UTC()}, nil
}

// ToJSON serializes the key to its SSM wire format. Always PKCS8 going
// forward (RSA and EC both marshal through the same call) — this is a
// forward-only format change; ParseKey still reads the pre-migration PKCS1
// format for keys already stored in SSM.
func (k *Key) ToJSON() (KeyJSON, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.Private)
	if err != nil {
		return KeyJSON{}, fmt.Errorf("marshaling key %s: %w", k.KID, err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return KeyJSON{
		KID:       k.KID,
		Alg:       k.Alg,
		PEM:       string(pem.EncodeToMemory(block)),
		CreatedAt: k.CreatedAt.Format(time.RFC3339),
	}, nil
}

// ParseKey deserializes a key from its SSM wire format. Handles both the
// pre-migration wire format (PEM type "RSA PRIVATE KEY", PKCS1, always RSA,
// no Alg field) and the current format (PEM type "PRIVATE KEY", PKCS8, RSA
// or EC, Alg field set).
func ParseKey(j KeyJSON) (*Key, error) {
	block, _ := pem.Decode([]byte(j.PEM))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM in key %s", j.KID)
	}

	var signer crypto.Signer
	var alg string
	switch block.Type {
	case "RSA PRIVATE KEY": // pre-ES256 wire format: PKCS1, always RSA
		priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing key %s: %w", j.KID, err)
		}
		signer, alg = priv, AlgRS256
	case "PRIVATE KEY": // PKCS8: RSA or EC, alg carried in KeyJSON.Alg
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing key %s: %w", j.KID, err)
		}
		switch key := parsed.(type) {
		case *rsa.PrivateKey:
			signer, alg = key, AlgRS256
		case *ecdsa.PrivateKey:
			signer, alg = key, AlgES256
		default:
			return nil, fmt.Errorf("key %s: unsupported private key type %T", j.KID, parsed)
		}
	default:
		return nil, fmt.Errorf("key %s: unsupported PEM block type %q", j.KID, block.Type)
	}
	if j.Alg != "" && j.Alg != alg {
		return nil, fmt.Errorf("key %s: stored alg %q does not match key material %q", j.KID, j.Alg, alg)
	}

	created, err := time.Parse(time.RFC3339, j.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing created_at of key %s: %w", j.KID, err)
	}
	return &Key{KID: j.KID, Alg: alg, Private: signer, CreatedAt: created}, nil
}

// parseLegacyPEM decodes the pre-rotation raw PEM parameter (PKCS#1 or
// PKCS#8) into a Key with its KID derived — identical to
// config.loadSigningKey. Always RSA: this wraps the single key that existed
// before key rotation (and before ES256 support) was introduced.
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
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("legacy key: not RSA")
		}
		priv = rsaKey
	default:
		return nil, fmt.Errorf("legacy key: unsupported PEM block type %q", block.Type)
	}

	kid, err := DeriveKID(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Key{KID: kid, Alg: AlgRS256, Private: priv, CreatedAt: now.UTC()}, nil
}
