package turnstile

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		secret   string
		token    string
		status   int
		body     string
		action   string
		hostname string
		wantErr  error
	}{
		{name: "disabled in local development", token: "token", wantErr: nil},
		{name: "accepts a matching response", secret: "secret", token: "token", action: "support_ticket", hostname: "accounts.aoctech.app", status: http.StatusOK, body: `{"success":true,"action":"support_ticket","hostname":"accounts.aoctech.app"}`, wantErr: nil},
		{name: "rejects an empty token", secret: "secret", wantErr: ErrVerificationFailed},
		{name: "rejects a provider failure", secret: "secret", token: "token", action: "support_ticket", hostname: "accounts.aoctech.app", status: http.StatusOK, body: `{"success":false}`, wantErr: ErrVerificationFailed},
		{name: "rejects an unexpected action", secret: "secret", token: "token", action: "support_ticket", hostname: "accounts.aoctech.app", status: http.StatusOK, body: `{"success":true,"action":"other","hostname":"accounts.aoctech.app"}`, wantErr: ErrVerificationFailed},
		{name: "rejects an unexpected hostname", secret: "secret", token: "token", action: "support_ticket", hostname: "accounts.aoctech.app", status: http.StatusOK, body: `{"success":true,"action":"support_ticket","hostname":"other.aoctech.app"}`, wantErr: ErrVerificationFailed},
		{name: "rejects an invalid response", secret: "secret", token: "token", status: http.StatusOK, body: `not json`, wantErr: ErrVerificationFailed},
		{name: "rejects a non success status", secret: "secret", token: "token", status: http.StatusBadGateway, body: `{"success":true}`, wantErr: ErrVerificationFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := New(tt.secret, "https://accounts.aoctech.app")
			svc.SetTransportForTest(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.String() != siteverifyURL {
					t.Fatalf("unexpected request %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Fatalf("unexpected content type %q", req.Header.Get("Content-Type"))
				}
				if err := req.ParseForm(); err != nil {
					t.Fatalf("parse request form: %v", err)
				}
				if req.Form.Get("secret") != tt.secret || req.Form.Get("response") != tt.token || req.Form.Get("remoteip") != "203.0.113.10" || req.Form.Get("idempotency_key") == "" {
					t.Fatalf("unexpected request form: %v", req.Form)
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			}))

			err := svc.Verify(context.Background(), tt.token, "203.0.113.10", tt.action)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyReturnsProviderTransportErrors(t *testing.T) {
	t.Parallel()
	svc := New("secret", "https://accounts.aoctech.app")
	svc.SetTransportForTest(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unavailable")
	}))

	if err := svc.Verify(context.Background(), "token", "", "support_ticket"); err == nil || errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Verify() error = %v, want transport error", err)
	}
}
