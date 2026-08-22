// Package turnstile verifies Cloudflare Turnstile tokens server-side.
package turnstile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.aoctech.app/account/api/internal/observability"
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
	Verify(ctx context.Context, token, remoteIP, expectedAction string) error
}

// Service validates Turnstile tokens through Cloudflare's Siteverify API.
type Service struct {
	secret   string
	hostname string
	endpoint string
	client   *http.Client
}

type verifyResponse struct {
	Success  bool   `json:"success"`
	Action   string `json:"action"`
	Hostname string `json:"hostname"`
}

// New creates a verifier. An empty secret deliberately disables validation in
// local development; deployed environments receive the secret from SSM.
// appURL supplies the one frontend hostname allowed by this API deployment.
func New(secret, appURL string) *Service {
	hostname := ""
	if parsed, err := url.Parse(appURL); err == nil {
		hostname = parsed.Hostname()
	} else {
		slog.Warn("turnstile: invalid application URL", "error", err)
	}
	return &Service{
		secret:   strings.TrimSpace(secret),
		hostname: hostname,
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
func (s *Service) Verify(ctx context.Context, token, remoteIP, expectedAction string) error {
	if !s.Enabled() {
		return nil
	}

	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenLength {
		return ErrVerificationFailed
	}

	body := url.Values{
		"secret":          {s.secret},
		"response":        {token},
		"idempotency_key": {uuid.NewString()},
	}
	if remoteIP = strings.TrimSpace(remoteIP); remoteIP != "" {
		body.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewBufferString(body.Encode()))
	if err != nil {
		return fmt.Errorf("turnstile: build siteverify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: call siteverify: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			observability.Warn(ctx, "turnstile: failed to close siteverify response", closeErr)
		}
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("turnstile: read siteverify response: %w", err)
	}
	var result verifyResponse
	decodeErr := json.Unmarshal(raw, &result)
	if decodeErr != nil {
		observability.Warn(ctx, "turnstile: failed to decode siteverify response", decodeErr, "status", resp.StatusCode)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || decodeErr != nil || !result.Success ||
		(expectedAction != "" && result.Action != expectedAction) ||
		(s.hostname != "" && result.Hostname != s.hostname) {
		return ErrVerificationFailed
	}
	return nil
}

// SetTransportForTest replaces the HTTP transport without opening a listener.
func (s *Service) SetTransportForTest(transport http.RoundTripper) {
	s.client.Transport = transport
}
