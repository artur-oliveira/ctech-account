package config

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const TOTPIssuer = "CTech"

type Config struct {
	Environment    string
	TablePrefix    string
	AWSRegion      string
	ValkeyURL      string
	RSAPrivateKey  *rsa.PrivateKey
	PublicKeyKID   string
	BaseURL        string
	TOTPIssuer     string
	AllowedOrigins []string
	Port           string
	InternalToken  string
	CookieSecure   bool
	CookieDomain   string
	// WebAuthn Relying on Party settings.
	// RPID is the registerable domain (e.g. "arturocarvalho.com"). Passkeys registered here
	// can be used on any subdomain of RPID. Defaults to the host portion of BaseURL.
	RPID      string
	RPOrigins []string

	// Email (SES)
	FromEmail string // FROM_EMAIL env var
	AppURL    string // APP_URL env var — base URL for links in emails (defaults to BaseURL)

	// Google OAuth
	GoogleClientID     string
	GoogleClientSecret string

	// Reverse proxy
	// TrustedProxies is a list of IPs/CIDRs whose X-Forwarded-For header is trusted.
	// Set TRUSTED_PROXIES to a comma-separated list (e.g. "10.0.0.0/8,172.16.0.0/12").
	TrustedProxies []string
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

	var trustedProxies []string
	if raw := os.Getenv("TRUSTED_PROXIES"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				trustedProxies = append(trustedProxies, trimmed)
			}
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	tablePrefix := os.Getenv("TABLE_PREFIX")
	if tablePrefix == "" {
		tablePrefix = os.Getenv("ENVIRONMENT") + "_"
	}

	baseURL := getEnv("BASE_URL", "http://localhost:8000")

	rpid := os.Getenv("WEBAUTHN_RPID")
	if rpid == "" {
		if parsed, err := url.Parse(baseURL); err == nil {
			rpid = parsed.Hostname()
		}
	}

	rpOrigins := []string{baseURL}
	for _, o := range origins {
		rpOrigins = append(rpOrigins, o)
	}

	return &Config{
		Environment:   getEnv("ENVIRONMENT", "dev"),
		TablePrefix:   tablePrefix,
		AWSRegion:     getEnv("AWS_REGION", "us-east-1"),
		ValkeyURL:     os.Getenv("VALKEY_URL"),
		RSAPrivateKey: privateKey,

		PublicKeyKID:       kid,
		BaseURL:            baseURL,
		AllowedOrigins:     origins,
		Port:               port,
		InternalToken:      os.Getenv("INTERNAL_TOKEN"),
		CookieSecure:       getEnv("ENVIRONMENT", "dev") != "dev" && getEnv("ENVIRONMENT", "dev") != "development",
		CookieDomain:       os.Getenv("COOKIE_DOMAIN"),
		RPID:               rpid,
		RPOrigins:          rpOrigins,
		FromEmail:          os.Getenv("FROM_EMAIL"),
		AppURL:             getEnv("APP_URL", baseURL),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		TrustedProxies:     trustedProxies,
		TOTPIssuer:         TOTPIssuer,
	}, nil
}

func loadRSAKey() (*rsa.PrivateKey, string, error) {
	pemStr := os.Getenv("RSA_PRIVATE_KEY")
	if pemStr == "" {
		return nil, "", fmt.Errorf("RSA_PRIVATE_KEY is required")
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, "", fmt.Errorf("failed to decode PEM block")
	}
	var err error
	var privateKey *rsa.PrivateKey

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
