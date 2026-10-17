package handler_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/dataexport"
	consentDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type memExportRepo struct {
	mu sync.Mutex
	m  map[string]dataexport.Export
}

func (r *memExportRepo) Get(_ context.Context, id string) (*dataexport.Export, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[id]
	if !ok {
		return nil, dataexport.ErrNotFound
	}
	return &e, nil
}

func (r *memExportRepo) Save(_ context.Context, e *dataexport.Export, prev string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.m[e.UserID]; (ok && cur.ID != prev) || (!ok && prev != "") {
		return dataexport.ErrBuilding
	}
	r.m[e.UserID] = *e
	return nil
}

func (r *memExportRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
	return nil
}

type memExportStore struct{}

func (memExportStore) PutObject(context.Context, string, string, []byte) error { return nil }
func (memExportStore) PresignGet(_ context.Context, k string, _ time.Duration) (string, error) {
	return "https://exports.test/" + k, nil
}
func (memExportStore) DeleteObject(context.Context, string) error { return nil }

type silentExportMail struct{}

func (silentExportMail) SendExportRequested(context.Context, string, string) error { return nil }
func (silentExportMail) SendExportReady(context.Context, string, string) error     { return nil }

func newTestExportService(users *user.Service, sessions *sessionDomain.Service, keys *apikey.Service, consents *consentDomain.Service, auditSvc *audit.Service) *dataexport.Service {
	return dataexport.NewService(&memExportRepo{m: map[string]dataexport.Export{}}, memExportStore{},
		dataexport.NewZipBuilder(dataexport.AccountSections(dataexport.Sources{
			Users: users, Sessions: sessions, Keys: keys, Consents: consents, Audit: auditSvc})...),
		dataexport.UserContacts{Users: users}, silentExportMail{}, dataexport.AuditRecorder{Audit: auditSvc})
}

func TestExport_RequestThenDownloadLink(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "me@example.com", "Sup3rSecret!", "Ana")
	token := ta.issueToken(t, u.ID())

	if resp := ta.doWithToken("POST", "/v1.0/account/export", map[string]string{}, token); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("request: %d %s", resp.StatusCode, bodyString(resp))
	}
	var body string
	for range 50 {
		resp := ta.doWithToken("GET", "/v1.0/account/export", nil, token)
		body = bodyString(resp)
		if strings.Contains(body, `"state":"ready"`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, `"state":"ready"`) || !strings.Contains(body, "https://exports.test/") {
		t.Fatalf("status: %s", body)
	}
	if resp := ta.doWithToken("POST", "/v1.0/account/export", map[string]string{}, token); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request: %d, want 429", resp.StatusCode)
	}
	if resp := ta.doWithToken("DELETE", "/v1.0/account/export", nil, token); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("discard: %d", resp.StatusCode)
	}
	if body := bodyString(ta.doWithToken("GET", "/v1.0/account/export", nil, token)); !strings.Contains(body, `"state":"expired"`) || strings.Contains(body, "https://") {
		t.Fatalf("after discard: %s", body)
	}
	if resp := ta.doWithToken("POST", "/v1.0/account/export", map[string]string{}, token); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request right after discard: %d, want 429", resp.StatusCode)
	}
}

func TestExport_NeedsFreshIdentity(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "me2@example.com", "Sup3rSecret!", "Ana")
	stale := ta.issueStaleToken(t, u.ID())
	resp := ta.doWithToken("POST", "/v1.0/account/export", map[string]string{}, stale)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(bodyString(resp), "step-up-required") {
		t.Fatalf("no proof: %d", resp.StatusCode)
	}
	if resp = ta.doWithToken("POST", "/v1.0/account/export", map[string]string{"password": "Sup3rSecret!"}, stale); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("with password: %d %s", resp.StatusCode, bodyString(resp))
	}
}

// A third-party client granted account:export:write must not read the
// person's whole data: self client only.
func TestExport_RequiresSelfClient(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "me3@example.com", "Sup3rSecret!", "Ana")
	now := time.Now().Unix()
	token, err := ta.jwtSvc.SignAccessToken(u.ID(), "sess-test", "third-party-app", testAccountScopes(), "http://localhost", []string{"http://localhost"}, now, now, []string{sessionDomain.AMRPassword, sessionDomain.AMRTOTP}, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp := ta.doWithToken("GET", "/v1.0/account/export", nil, token); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("third-party: %d, want 403", resp.StatusCode)
	}
}
