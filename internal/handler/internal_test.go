package handler_test

import (
	"net/http"
	"testing"
)

const testInternalToken = "test-internal-token"

// ── POST /internal/v1.0/users/migrate ──────────────────────────────────────────

func TestMigrateUser_NoToken_401(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodPost, "/internal/v1.0/users/migrate", map[string]any{
		"email":         "migrate@test.com",
		"password_hash": "$argon2id$...",
		"first_name":    "Alice",
		"last_name":     "Smith",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestMigrateUser_WrongToken_401(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodPost, "/internal/v1.0/users/migrate", map[string]any{
		"email":         "migrate@test.com",
		"password_hash": "$argon2id$...",
		"first_name":    "Alice",
		"last_name":     "Smith",
	}, map[string]string{"X-Internal-Token": "wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestMigrateUser_MissingFields_422(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodPost, "/internal/v1.0/users/migrate",
		map[string]any{"email": "migrate@test.com"},
		map[string]string{"X-Internal-Token": testInternalToken},
	)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestMigrateUser_New_200(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodPost, "/internal/v1.0/users/migrate", map[string]any{
		"email":         "new-migrated@test.com",
		"password_hash": "$argon2id$v=19$m=65536,t=1,p=2$abc$def",
		"first_name":    "Bob",
		"last_name":     "Jones",
	}, map[string]string{"X-Internal-Token": testInternalToken})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
		return
	}
	var body struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	}
	readJSON(t, resp, &body)
	if body.UserID == "" {
		t.Error("user_id should not be empty")
	}
	if body.Email != "new-migrated@test.com" {
		t.Errorf("unexpected email: %s", body.Email)
	}
}

func TestMigrateUser_Idempotent_200(t *testing.T) {
	ta := newTestApp(t)

	payload := map[string]any{
		"email":         "idempotent@test.com",
		"password_hash": "$argon2id$v=19$m=65536,t=1,p=2$abc$def",
		"first_name":    "Carol",
		"last_name":     "White",
	}
	headers := map[string]string{"X-Internal-Token": testInternalToken}

	resp1 := ta.do(http.MethodPost, "/internal/v1.0/users/migrate", payload, headers)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first call: expected 200, got %d: %s", resp1.StatusCode, bodyString(resp1))
	}
	var body1 struct{ UserID string `json:"user_id"` }
	readJSON(t, resp1, &body1)

	resp2 := ta.do(http.MethodPost, "/internal/v1.0/users/migrate", payload, headers)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second call: expected 200, got %d: %s", resp2.StatusCode, bodyString(resp2))
	}
	var body2 struct{ UserID string `json:"user_id"` }
	readJSON(t, resp2, &body2)

	if body1.UserID != body2.UserID {
		t.Errorf("idempotent calls returned different user_ids: %s vs %s", body1.UserID, body2.UserID)
	}
}
