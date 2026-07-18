package handler_test

import (
	"net/http"
	"testing"

	"gopkg.aoctech.app/account/internal/domain/audit"
)

func TestActivityListReturnsOwnEventsOnly(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "activity@example.com", "Str0ngP4ss!word", "Act")
	token := ta.issueToken(t, u.ID())

	ta.auditSvc.Record(t.Context(), audit.Entry{UserID: u.ID(), Type: audit.EventLoginSuccess, IP: "1.1.1.1", Metadata: map[string]string{"client_id": "web", "internal_note": "hide-me"}})
	ta.auditSvc.Record(t.Context(), audit.Entry{UserID: "other-user", Type: audit.EventLoginSuccess})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/activity", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Events []struct {
			EventType string            `json:"event_type"`
			IP        string            `json:"ip"`
			Metadata  map[string]string `json:"metadata"`
			CreatedAt string            `json:"created_at"`
		} `json:"events"`
		NextCursor string `json:"next_cursor"`
	}
	readJSON(t, resp, &body)

	if len(body.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(body.Events))
	}
	e := body.Events[0]
	if e.EventType != audit.EventLoginSuccess || e.IP != "1.1.1.1" || e.CreatedAt == "" {
		t.Errorf("unexpected event: %+v", e)
	}
	if e.Metadata["client_id"] != "web" {
		t.Errorf("allowlisted metadata missing: %v", e.Metadata)
	}
	if _, ok := e.Metadata["internal_note"]; ok {
		t.Errorf("non-allowlisted metadata leaked: %v", e.Metadata)
	}
}

func TestActivityPaginatesWithCursor(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "activity2@example.com", "Str0ngP4ss!word", "Act")
	token := ta.issueToken(t, u.ID())

	for range 3 {
		ta.auditSvc.Record(t.Context(), audit.Entry{UserID: u.ID(), Type: audit.EventLoginSuccess})
	}

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/activity?limit=2", nil, token)
	var page1 struct {
		Events     []map[string]any `json:"events"`
		NextCursor string           `json:"next_cursor"`
	}
	readJSON(t, resp, &page1)
	if len(page1.Events) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1: %d events, cursor %q", len(page1.Events), page1.NextCursor)
	}

	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/activity?limit=2&cursor="+page1.NextCursor, nil, token)
	var page2 struct {
		Events     []map[string]any `json:"events"`
		NextCursor string           `json:"next_cursor"`
	}
	readJSON(t, resp, &page2)
	if len(page2.Events) != 1 || page2.NextCursor != "" {
		t.Fatalf("page2: %d events, cursor %q", len(page2.Events), page2.NextCursor)
	}
}

func TestActivityRejectsInvalidLimit(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "activity3@example.com", "Str0ngP4ss!word", "Act")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/activity?limit=999", nil, token)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestActivityRequiresAuth(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodGet, "/v1.0/account/activity", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLoginRecordsAuditEvents(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "audit-login@example.com", "Str0ngP4ss!word", "Aud")

	resp := ta.do(http.MethodPost, "/v1.0/auth/login", map[string]string{
		"email": "audit-login@example.com", "password": "Str0ngP4ss!word",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", resp.StatusCode)
	}

	events, _, err := ta.auditSvc.ListByUser(t.Context(), u.ID(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.EventType == audit.EventLoginSuccess {
			found = true
		}
	}
	if !found {
		t.Error("login did not record login.success audit event")
	}
}

func TestFailedLoginRecordsAuditEvent(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "audit-fail@example.com", "Str0ngP4ss!word", "Aud")

	resp := ta.do(http.MethodPost, "/v1.0/auth/login", map[string]string{
		"email": "audit-fail@example.com", "password": "wrong-password",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	events, _, err := ta.auditSvc.ListByUser(t.Context(), u.ID(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].EventType != audit.EventLoginFailed {
		t.Errorf("expected login.failed event, got %+v", events)
	}
}
