package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
	"gopkg.aoctech.app/account/api/internal/keystore"
)

const TOTPIssuer = "CTech"

// DefaultAppVersion is used when APP_VERSION is absent (local dev, tests).
const DefaultAppVersion = "0.0.1"

type Config struct {
	// AppVersion is the release identifier shipped inside the deployment artifact
	// (release.env → APP_VERSION). Format: YYMMDDHHMM:<7-char commit>.
	AppVersion     string
	Environment    string
	TablePrefix    string
	AWSRegion      string
	ValkeyURL      string
	RSAPrivateKey  *rsa.PrivateKey
	PublicKeyKID   string
	BaseURL        string
	Audience       string
	TOTPIssuer     string
	AllowedOrigins []string
	Port           string
	CookieSecure   bool
	CookieDomain   string
	// WebAuthn Relying on Party settings.
	// RPID is the registerable domain (e.g. "aoctech.app"). Passkeys registered here
	// can be used on any subdomain of RPID. Defaults to the host portion of AppURL
	// (the browser-facing SPA origin) — never BaseURL, which is this API's own origin.
	RPID      string
	RPOrigins []string

	// Email (SES)
	FromEmail string // FROM_EMAIL env var
	AppURL    string // APP_URL env var — base URL for links in emails (defaults to BaseURL)

	// Google OAuth
	GoogleClientID     string
	GoogleClientSecret string

	// KYCDocumentsBucket is the S3 bucket holding identity documents uploaded for
	// manual review (KYC_DOCUMENTS_BUCKET env var). When empty, the document
	// verification path is disabled and only PIX-match verification is offered.
	KYCDocumentsBucket string

	// PhoneVerificationEnabled gates AWS SNS phone verification (PHONE_VERIFICATION_ENABLED
	// env var, default false). While false, every Basic/OTP route hard-blocks
	// with 503 — mirrors how an absent KYCDocumentsBucket disables document
	// verification. Flip to true once production SNS SMS access is granted.
	PhoneVerificationEnabled bool

	// Reverse proxy
	// TrustedProxies is a list of IPs/CIDRs whose X-Forwarded-For header is trusted.
	// Set TRUSTED_PROXIES to a comma-separated list (e.g. "10.0.0.0/8,172.16.0.0/12").
	TrustedProxies []string

	// SelfClientID is the OAuth client_id of this service's own first-party
	// frontend (ui/). /v1.0/account/* and /v1.0/step-up/* are self-service
	// account-management endpoints — never meant to be reachable by any other
	// OAuth client (dfe included) — and are gated on the token's azp claim
	// matching this value, since no scope currently governs them.
	SelfClientID string

	// AccessTokenTTL is the lifetime of signed access tokens. It threads into
	// crypto.JWTService so the signed `exp` and the advertised `expires_in`
	// always agree (BUG-027). Defaults to 15 minutes.
	AccessTokenTTL time.Duration
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
		port = "8001"
	}

	accessTokenTTL := 15 * time.Minute
	if raw := os.Getenv("ACCESS_TOKEN_TTL"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			accessTokenTTL = time.Duration(secs) * time.Second
		}
	}

	tablePrefix := os.Getenv("TABLE_PREFIX")
	if tablePrefix == "" {
		tablePrefix = os.Getenv("ENVIRONMENT")
	}
	tablePrefix = strings.TrimSuffix(tablePrefix, "_")

	baseURL := getEnv("BASE_URL", "http://localhost:8001")
	// appURL is the browser-facing SPA origin — distinct from baseURL (this
	// API's own origin). WebAuthn RPID/RPOrigins must reflect the origin the
	// ceremony actually runs on (the SPA), never the API's own hostname, or
	// every passkey ceremony fails with a SecurityError in any deployment
	// where the API and SPA sit on different subdomains (prod: accountsapi.*
	// vs accounts.*). Locally both default to the same value, masking the bug.
	appURL := getEnv("APP_URL", baseURL)

	rpid := os.Getenv("WEBAUTHN_RPID")
	if rpid == "" {
		if parsed, err := url.Parse(appURL); err == nil {
			rpid = parsed.Hostname()
		}
	}

	rpOrigins := []string{appURL}
	for _, o := range origins {
		rpOrigins = append(rpOrigins, o)
	}

	if rpid != "" && !isRegistrableMatch(rpid, rpOrigins) {
		log.Printf("config: WEBAUTHN_RPID %q does not match any RPOrigins entry %v — WebAuthn ceremonies will fail with a SecurityError", rpid, rpOrigins)
	}

	phoneVerificationEnabled, _ := strconv.ParseBool(getEnv("PHONE_VERIFICATION_ENABLED", "false"))

	return &Config{
		AppVersion:    getEnv("APP_VERSION", DefaultAppVersion),
		Environment:   getEnv("ENVIRONMENT", "dev"),
		TablePrefix:   tablePrefix,
		AWSRegion:     getEnv("AWS_REGION", "us-east-1"),
		ValkeyURL:     os.Getenv("VALKEY_URL"),
		RSAPrivateKey: privateKey,

		PublicKeyKID:             kid,
		BaseURL:                  baseURL,
		Audience:                 getEnv("AUDIENCE", appURL),
		AllowedOrigins:           origins,
		Port:                     port,
		CookieSecure:             getEnv("ENVIRONMENT", "dev") != "dev" && getEnv("ENVIRONMENT", "dev") != "development",
		CookieDomain:             os.Getenv("COOKIE_DOMAIN"),
		RPID:                     rpid,
		RPOrigins:                rpOrigins,
		FromEmail:                getEnv("FROM_EMAIL", "no-reply@aoctech.app"),
		AppURL:                   appURL,
		GoogleClientID:           os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:       os.Getenv("GOOGLE_CLIENT_SECRET"),
		KYCDocumentsBucket:       os.Getenv("KYC_DOCUMENTS_BUCKET"),
		PhoneVerificationEnabled: phoneVerificationEnabled,
		TrustedProxies:           trustedProxies,
		TOTPIssuer:               TOTPIssuer,
		SelfClientID:             getEnv("SELF_CLIENT_ID", "accounts"),
		AccessTokenTTL:           accessTokenTTL,
	}, nil
}

// loadRSAKey parses the RSA_PRIVATE_KEY env var. When the variable is absent
// it returns nil material without error — production loads versioned keys
// from SSM instead (see internal/keystore); the env path is dev-only.
func loadRSAKey() (*rsa.PrivateKey, string, error) {
	pemStr := os.Getenv("RSA_PRIVATE_KEY")
	if pemStr == "" {
		return nil, "", nil
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
		derived, derErr := keystore.DeriveKID(&privateKey.PublicKey)
		if derErr != nil {
			return nil, "", derErr
		}
		kid = derived
	}

	return privateKey, kid, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// isRegistrableMatch reports whether rpid is the registrable domain (or an
// exact match) of at least one origin's hostname in origins — i.e. whether
// WebAuthn's RPID-vs-origin check can ever succeed for this configuration.
func isRegistrableMatch(rpid string, origins []string) bool {
	rpid = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rpid), "."))
	if rpid == "" {
		return false
	}
	if rpid != "localhost" && net.ParseIP(rpid) == nil {
		public, _ := publicsuffix.PublicSuffix(rpid)
		if public == rpid {
			return false
		}
	}
	for _, o := range origins {
		parsed, err := url.Parse(o)
		if err != nil {
			continue
		}
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if host == rpid {
			return true
		}
		if !strings.HasSuffix(host, "."+rpid) {
			continue
		}
		return true
	}
	return false
}
