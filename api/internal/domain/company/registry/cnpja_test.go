package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serving(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNamesReadsTheCompanyAndItsAlias(t *testing.T) {
	srv := serving(t, 200, `{"company":{"name":"ACME COMERCIO LTDA"},"alias":"Acme"}`)
	l := &cnpja{client: srv.Client(), endpoint: srv.URL + "/"}
	names, ok := l.Names(context.Background(), "11222333000181")
	if !ok || names.LegalName != "ACME COMERCIO LTDA" || names.TradeName != "Acme" {
		t.Fatalf("got %+v %v", names, ok)
	}
}

// Every failure is the same failure to the caller: the person types the names.
// Distinguishing them would only tempt a caller into treating one as fatal.
func TestNamesIsQuietOnEveryFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not found", 404, `{"message":"not found"}`},
		{"rate limited", 429, ``},
		{"upstream error", 500, ``},
		{"malformed body", 200, `<html>`},
		{"empty name", 200, `{"company":{"name":""}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serving(t, tc.status, tc.body)
			l := &cnpja{client: srv.Client(), endpoint: srv.URL + "/"}
			if names, ok := l.Names(context.Background(), "11222333000181"); ok {
				t.Errorf("got %+v true, want quiet", names)
			}
		})
	}
}

// An unreachable register is the outage case, and it must be as quiet as a 404.
func TestNamesIsQuietWhenTheRegisterIsUnreachable(t *testing.T) {
	srv := serving(t, 200, `{}`)
	client := srv.Client()
	endpoint := srv.URL + "/"
	srv.Close()
	l := &cnpja{client: client, endpoint: endpoint}
	if _, ok := l.Names(context.Background(), "11222333000181"); ok {
		t.Error("an unreachable register answered")
	}
}
