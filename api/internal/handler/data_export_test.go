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
	"gopkg.aoctech.app/account/api/internal/scopes"
	wire "gopkg.aoctech.app/api-commons/dataexport"
)

type memExportStore struct{}

func (memExportStore) PutObject(context.Context, string, string, []byte) error { return nil }
func (memExportStore) PresignGet(_ context.Context, k string, _ time.Duration) (string, error) {
	return "https://exports.test/" + k, nil
}
func (memExportStore) PresignPut(_ context.Context, k string, _ string, _ time.Duration) (string, error) {
	return "https://exports.test/put/" + k, nil
}
func (memExportStore) Size(context.Context, string) (int64, error)        { return 42, nil }
func (memExportStore) ListKeys(context.Context, string) ([]string, error) { return nil, nil }
func (memExportStore) DeleteObject(context.Context, string) error         { return nil }

type recordingDispatcher struct{}

func (recordingDispatcher) Publish(_ context.Context, m wire.Message) error {
	exportMu.Lock()
	defer exportMu.Unlock()
	exportDispatched = append(exportDispatched, m)
	return nil
}

// dispatched waits for the background dispatch of the request and returns it.
func dispatched(t *testing.T) []wire.Message {
	t.Helper()
	for range 100 {
		exportMu.Lock()
		got := append([]wire.Message(nil), exportDispatched...)
		exportMu.Unlock()
		if len(got) > 0 {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("nothing was dispatched")
	return nil
}

type silentExportMail struct{}

func (silentExportMail) SendExportRequested(context.Context, string, string) error { return nil }
func (silentExportMail) SendExportReady(context.Context, string, string) error     { return nil }

// exportDispatched records what the test app asked its (fake) participant, dfe, for.
var (
	exportMu         sync.Mutex
	exportDispatched []wire.Message
)

func newTestExportService(users *user.Service, sessions *sessionDomain.Service, keys *apikey.Service, consents *consentDomain.Service, auditSvc *audit.Service) *dataexport.Service {
	return dataexport.NewService(dataexport.NewMemoryRepository(), memExportStore{},
		dataexport.NewZipBuilder(dataexport.AccountSections(dataexport.Sources{
			Users: users, Sessions: sessions, Keys: keys, Consents: consents, Audit: auditSvc})...),
		dataexport.UserContacts{Users: users}, silentExportMail{}, dataexport.AuditRecorder{Audit: auditSvc}).
		WithParticipants([]string{"dfe"}, recordingDispatcher{})
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

func TestExportAck_ParticipantPartBecomesDownloadable(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "me4@example.com", "Sup3rSecret!", "Ana")
	token := ta.issueToken(t, u.ID())
	exportMu.Lock()
	exportDispatched = nil
	exportMu.Unlock()
	if resp := ta.doWithToken("POST", "/v1.0/account/export", map[string]string{}, token); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("request: %d", resp.StatusCode)
	}
	msgs := dispatched(t)
	if len(msgs) != 1 || msgs[0].Service != "dfe" {
		t.Fatalf("dispatched=%+v", msgs)
	}
	svc := ta.issueServiceToken(t, []string{scopes.InternalAccountExportAck})
	ack := map[string]any{"request_id": msgs[0].RequestID, "sub": u.ID(), "service": "dfe", "result": "done", "size": 42}

	if resp := ta.doWithToken("POST", "/v1.0/internal/export/ack", ack, token); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("user token: %d, want 403", resp.StatusCode)
	}
	if resp := ta.doWithToken("POST", "/v1.0/internal/export/ack", map[string]any{"request_id": "x", "sub": u.ID(), "service": "wallet"}, svc); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ack for another participant: %d, want 403", resp.StatusCode)
	}
	// An ack naming a replaced export is dropped with a 2xx so the participant stops retrying.
	if resp := ta.doWithToken("POST", "/v1.0/internal/export/ack", map[string]any{"request_id": "old", "sub": u.ID(), "result": "done"}, svc); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("stale ack: %d, want 204", resp.StatusCode)
	}
	if resp := ta.doWithToken("POST", "/v1.0/internal/export/ack", ack, svc); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("ack: %d %s", resp.StatusCode, bodyString(resp))
	}
	var body string
	for range 50 {
		body = bodyString(ta.doWithToken("GET", "/v1.0/account/export", nil, token))
		if strings.Contains(body, "/dfe.zip") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "/dfe.zip") || !strings.Contains(body, "/account.zip") {
		t.Fatalf("both parts should be downloadable: %s", body)
	}
}
