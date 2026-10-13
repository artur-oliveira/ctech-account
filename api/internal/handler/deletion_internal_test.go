package handler_test

import (
	"net/http"
	"testing"

	"gopkg.aoctech.app/account/api/internal/scopes"
)

func TestErasureAck_RequiresServiceTokenOfAParticipant(t *testing.T) {
	ta := newTestApp(t)
	body := map[string]any{"request_id": "nope", "service": "dfe", "result": "done"}

	user := ta.issueToken(t, "u1")
	if resp := ta.doWithToken("POST", "/v1.0/internal/erasure/ack", body, user); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("user token: %d, want 403", resp.StatusCode)
	}
	// issueServiceToken mints azp "dfe"; the test app maps dfe as a participant.
	svc := ta.issueServiceToken(t, []string{scopes.InternalAccountErasureAck})
	if resp := ta.doWithToken("POST", "/v1.0/internal/erasure/ack", body, svc); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ack for unknown request: %d, want 404", resp.StatusCode)
	}
	if resp := ta.doWithToken("POST", "/v1.0/internal/erasure/ack", map[string]any{"request_id": "nope", "service": "wallet", "result": "done"}, svc); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ack on behalf of another participant: %d, want 403", resp.StatusCode)
	}
	if resp := ta.doWithToken("GET", "/v1.0/internal/erasure/nope/subject", nil, ta.issueServiceToken(t, []string{scopes.InternalAccountErasureSubject})); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("subject for unknown request: %d, want 404", resp.StatusCode)
	}
}
