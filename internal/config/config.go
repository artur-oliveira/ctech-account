package config

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Environment    string
	TablePrefix    string
	AWSRegion      string
	ValkeyURL      string
	RSAPrivateKey  *rsa.PrivateKey
	PublicKeyKID   string
	BaseURL        string
	AllowedOrigins []string
	Port           string
	InternalToken  string
}

func Load() (*Config, error) {
	privateKey, kid, err := loadRSAKey()
	if err != nil {
		return nil, fmt.Errorf("loading RSA key: %w", err)
	}

	var origins []string
	if raw := os.Getenv("ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				origins = append(origins, trimmed)
			}
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	tablePrefix := os.Getenv("TABLE_PREFIX")
	if tablePrefix == "" {
		tablePrefix = os.Getenv("ENVIRONMENT") + "_"
	}

	return &Config{
		Environment:    getEnv("ENVIRONMENT", "dev"),
		TablePrefix:    tablePrefix,
		AWSRegion:      getEnv("AWS_REGION", "us-east-1"),
		ValkeyURL:      os.Getenv("VALKEY_URL"),
		RSAPrivateKey:  privateKey,
		PublicKeyKID:   kid,
		BaseURL:        getEnv("BASE_URL", "http://localhost:8000"),
		AllowedOrigins: origins,
		Port:           port,
		InternalToken:  os.Getenv("INTERNAL_TOKEN"),
	}, nil
}

func loadRSAKey() (*rsa.PrivateKey, string, error) {
	pemStr := os.Getenv("RSA_PRIVATE_KEY_PEM")
	if pemStr == "" {
		return nil, "", fmt.Errorf("RSA_PRIVATE_KEY_PEM is required")
	}

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, "", fmt.Errorf("failed to decode PEM block")
	}

	var privateKey *rsa.PrivateKey
	var err error

	switch block.Type {
	case "RSA PRIVATE KEY":
		privateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr != nil {
			return nil, "", fmt.Errorf("parsing PKCS8 key: %w", parseErr)
		}
		var ok bool
		privateKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, "", fmt.Errorf("key is not RSA")
		}
	default:
		return nil, "", fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}

	if err != nil {
		return nil, "", fmt.Errorf("parsing RSA key: %w", err)
	}

	kid := os.Getenv("PUBLIC_KEY_KID")
	if kid == "" {
		pubDER, derErr := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
		if derErr != nil {
			return nil, "", fmt.Errorf("marshaling public key: %w", derErr)
		}
		sum := sha256.Sum256(pubDER)
		kid = hex.EncodeToString(sum[:])[:16]
	}

	return privateKey, kid, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
