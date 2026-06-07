package crypto

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

type JWTService struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	kid        string
}

func NewJWTService(cfg *config.Config) (*JWTService, error) {
	if cfg.RSAPrivateKey == nil {
		return nil, fmt.Errorf("RSA private key is nil")
	}
	return &JWTService{
		privateKey: cfg.RSAPrivateKey,
		publicKey:  &cfg.RSAPrivateKey.PublicKey,
		kid:        cfg.PublicKeyKID,
	}, nil
}

// SignAccessToken creates a 15-minute RS256 JWT access token.
func (j *JWTService) SignAccessToken(userID, sessionID string, scopes []string, issuer string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":   userID,
		"sid":   sessionID,
		"scope": strings.Join(scopes, " "),
		"iss":   issuer,
		"aud":   []string{"ctech-account"},
		"iat":   now.Unix(),
		"exp":   now.Add(15 * time.Minute).Unix(),
	}
	return j.sign(claims)
}

// SignIDToken creates a 1-hour RS256 JWT id_token per OIDC spec.
func (j *JWTService) SignIDToken(userID, email, name, clientID, nonce, issuer string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":            userID,
		"email":          email,
		"name":           name,
		"iss":            issuer,
		"aud":            []string{clientID},
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"email_verified": true,
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	return j.sign(claims)
}

func (j *JWTService) sign(claims jwt.MapClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = j.kid
	return token.SignedString(j.privateKey)
}

// Verify validates an RS256 JWT and returns its claims.
func (j *JWTService) Verify(tokenStr string) (jwt.MapClaims, error) {
	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.publicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("verifying token: %w", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}

// PublicKeyJWK returns the public key as a JWK map for the JWKS endpoint.
func (j *JWTService) PublicKeyJWK() map[string]any {
	n := base64.RawURLEncoding.EncodeToString(j.publicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(j.publicKey.E)).Bytes())
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": j.kid,
		"n":   n,
		"e":   e,
	}
}

// KID returns the current key ID.
func (j *JWTService) KID() string {
	return j.kid
}
