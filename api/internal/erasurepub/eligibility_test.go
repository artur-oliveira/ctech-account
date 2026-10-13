package erasurepub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/config"
)

func TestEligibility_MergesBlockersAndFailsClosed(t *testing.T) {
	var gotAuth, gotURI string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotURI = r.Header.Get("Authorization"), r.URL.RequestURI()
		_, _ = w.Write([]byte(`{"eligible":false,"blockers":[{"code":"wallet.balance_nonzero","detail":{"amount_cents":1250}}]}`))
	}))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()

	mint := func(aud, scope string) (string, error) { return "tok:" + aud + ":" + scope, nil }
	e := NewEligibility(ok.Client(), []config.ErasureParticipant{{Service: "wallet", URL: ok.URL, Audience: "https://wallet", ClientID: "w"}}, mint)
	blockers, err := e.Check(context.Background(), "u1", []string{"org_a", "org_b"})
	if err != nil || len(blockers) != 1 || blockers[0].Code != "wallet.balance_nonzero" {
		t.Fatalf("blockers %+v err %v", blockers, err)
	}
	if gotURI != "/internal/erasure/eligibility/u1?workspaces=org_a%2Corg_b" || gotAuth != "Bearer tok:https://wallet:internal:wallet:erasure-eligibility" {
		t.Fatalf("uri %q auth %q", gotURI, gotAuth)
	}
	if _, err := e.Check(context.Background(), "u1", nil); err != nil || gotURI != "/internal/erasure/eligibility/u1" {
		t.Fatalf("no workspaces must omit the query: %q %v", gotURI, err)
	}

	e = NewEligibility(down.Client(), []config.ErasureParticipant{{Service: "dfe", URL: down.URL, Audience: "https://dfe", ClientID: "d"}}, mint)
	if _, err := e.Check(context.Background(), "u1", nil); err == nil || !strings.Contains(err.Error(), "dfe") {
		t.Fatalf("an unreachable participant must be an error naming it, got %v", err)
	}
}
