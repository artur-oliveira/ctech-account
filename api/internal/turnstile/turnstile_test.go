package turnstile

import (
	"context"
	"encoding/json"
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
		name    string
		secret  string
		token   string
		status  int
		body    string
		wantErr error
	}{
		{name: "disabled in local development", token: "token", wantErr: nil},
		{name: "accepts a successful response", secret: "secret", token: "token", status: http.StatusOK, body: `{"success":true}`, wantErr: nil},
		{name: "rejects an empty token", secret: "secret", wantErr: ErrVerificationFailed},
		{name: "rejects a provider failure", secret: "secret", token: "token", status: http.StatusOK, body: `{"success":false}`, wantErr: ErrVerificationFailed},
		{name: "rejects an invalid response", secret: "secret", token: "token", status: http.StatusOK, body: `not json`, wantErr: ErrVerificationFailed},
		{name: "rejects a non success status", secret: "secret", token: "token", status: http.StatusBadGateway, body: `{"success":true}`, wantErr: ErrVerificationFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := New(tt.secret)
			var received verifyRequest
			svc.SetTransportForTest(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.String() != siteverifyURL {
					t.Fatalf("unexpected request %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected content type %q", req.Header.Get("Content-Type"))
				}
				if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			}))

			err := svc.Verify(context.Background(), tt.token, "203.0.113.10")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, tt.wantErr)
			}
			if tt.secret != "" && tt.token != "" && len(tt.token) <= maxTokenLength {
				if received.Secret != tt.secret || received.Response != tt.token || received.RemoteIP != "203.0.113.10" || received.IdempotencyKey == "" {
					t.Fatalf("unexpected request payload: %+v", received)
				}
			}
		})
	}
}

func TestVerifyReturnsProviderTransportErrors(t *testing.T) {
	t.Parallel()
	svc := New("secret")
	svc.SetTransportForTest(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unavailable")
	}))

	if err := svc.Verify(context.Background(), "token", ""); err == nil || errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Verify() error = %v, want transport error", err)
	}
}
