// Package turnstile verifies Cloudflare Turnstile tokens server-side.
package turnstile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	siteverifyURL   = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	maxTokenLength  = 2048
	maxResponseSize = 16 << 10
)

// ErrVerificationFailed means Cloudflare did not accept the submitted token.
// Callers must expose this as a validation error, never as the provider's raw
// response (which can disclose implementation details).
var ErrVerificationFailed = errors.New("turnstile: verification failed")

// Verifier is the narrow dependency consumed by HTTP handlers.
type Verifier interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// Service validates Turnstile tokens through Cloudflare's Siteverify API.
type Service struct {
	secret   string
	endpoint string
	client   *http.Client
}

type verifyRequest struct {
	Secret         string `json:"secret"`
	Response       string `json:"response"`
	RemoteIP       string `json:"remoteip,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

type verifyResponse struct {
	Success bool `json:"success"`
}

// New creates a verifier. An empty secret deliberately disables validation in
// local development; deployed environments receive the secret from SSM.
func New(secret string) *Service {
	return &Service{
		secret:   strings.TrimSpace(secret),
		endpoint: siteverifyURL,
		client:   &http.Client{Timeout: 6 * time.Second},
	}
}

// Enabled reports whether Siteverify calls will be made.
func (s *Service) Enabled() bool {
	return s != nil && s.secret != ""
}

// Verify validates one short-lived, single-use token. Tokens are never cached
// or logged, because Cloudflare rejects a token after its first validation.
func (s *Service) Verify(ctx context.Context, token, remoteIP string) error {
	if !s.Enabled() {
		return nil
	}

	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenLength {
		return ErrVerificationFailed
	}

	body, err := json.Marshal(verifyRequest{
		Secret:         s.secret,
		Response:       token,
		RemoteIP:       strings.TrimSpace(remoteIP),
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		return fmt.Errorf("turnstile: encode siteverify request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("turnstile: build siteverify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: call siteverify: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("turnstile: read siteverify response: %w", err)
	}
	var result verifyResponse
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || json.Unmarshal(raw, &result) != nil || !result.Success {
		return ErrVerificationFailed
	}
	return nil
}

// SetTransportForTest replaces the HTTP transport without opening a listener.
func (s *Service) SetTransportForTest(transport http.RoundTripper) {
	s.client.Transport = transport
}
