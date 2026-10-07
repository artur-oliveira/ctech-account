package handler_test

import (
	"net/http"
	"strings"
	"testing"
)

const deletionPhrase = "EXCLUIR MINHA CONTA"

func TestDeletion_FullFlow(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "bye@example.com", "Sup3rSecret!", "Ana")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase}, token)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("request: %d %s", resp.StatusCode, bodyString(resp))
	}
	var created struct {
		RequestID string `json:"request_id"`
	}
	readJSON(t, resp, &created)

	resp = ta.do("POST", "/v1.0/auth/deletion/confirm", map[string]string{"request_id": created.RequestID, "token": ta.deletionMail.confirmToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm: %d %s", resp.StatusCode, bodyString(resp))
	}

	// The pre-lock access token is dead on the account API (R3).
	if resp = ta.doWithToken("GET", "/v1.0/account/sessions", nil, token); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after lock: %d, want 401", resp.StatusCode)
	}
	login := map[string]string{"email": "bye@example.com", "password": "Sup3rSecret!"}
	if resp = ta.do("POST", "/v1.0/auth/login", login); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("login while pending: %d, want 403", resp.StatusCode)
	}

	resp = ta.do("POST", "/v1.0/auth/deletion/cancel", map[string]string{"request_id": created.RequestID, "token": ta.deletionMail.cancelToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %s", resp.StatusCode, bodyString(resp))
	}
	if resp = ta.do("POST", "/v1.0/auth/login", login); resp.StatusCode != http.StatusOK {
		t.Fatalf("login after cancel: %d %s", resp.StatusCode, bodyString(resp))
	}
}

func TestDeletion_RequestValidation(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "check@example.com", "Sup3rSecret!", "Ana")

	fresh := ta.issueToken(t, u.ID())
	if resp := ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": "excluir"}, fresh); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong phrase: %d, want 400", resp.StatusCode)
	}

	stale := ta.issueStaleToken(t, u.ID()) // no recent MFA proof
	resp := ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase}, stale)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(bodyString(resp), "step-up-required") {
		t.Fatalf("no MFA, no password: %d %s", resp.StatusCode, bodyString(resp))
	}
	resp = ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase, "password": "wrong"}, stale)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d, want 401", resp.StatusCode)
	}
	resp = ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase, "password": "Sup3rSecret!"}, stale)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("right password: %d %s", resp.StatusCode, bodyString(resp))
	}
	if resp = ta.doWithToken("GET", "/v1.0/account/deletion", nil, fresh); resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDeletion_BadLinks(t *testing.T) {
	ta := newTestApp(t)
	if resp := ta.do("POST", "/v1.0/auth/deletion/confirm", map[string]string{"request_id": "nope", "token": "nope"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown confirm link: %d, want 401", resp.StatusCode)
	}
	if resp := ta.do("POST", "/v1.0/auth/deletion/cancel", map[string]string{"request_id": "nope", "token": "nope"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown cancel link: %d, want 401", resp.StatusCode)
	}
}
