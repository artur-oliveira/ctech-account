package handler_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/crypto"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

const m2mSecret = "m2m-secret"

// seedM2MClient registers a client for client_credentials tests and returns
// its plaintext secret.
func seedM2MClient(t *testing.T, repo *memClientRepo, id, clientType string, firstParty bool, allowedScopes []string) string {
	t.Helper()
	secretHash, err := crypto.HashPassword(m2mSecret)
	if err != nil {
		t.Fatalf("hashing secret: %v", err)
	}
	err = repo.Create(context.Background(), &oauthclient.OAuthClient{
		PK:               oauthclient.BuildPK(id),
		Name:             id,
		ClientType:       clientType,
		ClientSecretHash: secretHash,
		FirstParty:       firstParty,
		AllowedScopes:    allowedScopes,
	})
	if err != nil {
		t.Fatalf("seeding client: %v", err)
	}
	return m2mSecret
}

func clientCredentialsForm(clientID, secret, scope string) url.Values {
	return url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {secret},
		"scope":         {scope},
	}
}

// decodeJWTPayload parses the (unverified) claim set of a JWT for assertions.
func decodeJWTPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshaling claims: %v", err)
	}
	return claims
}

func TestClientCredentialsIssuesToken(t *testing.T) {
	ta := newOAuthTestApp(t)
	secret := seedM2MClient(t, ta.clientRepo, "wallet", "confidential", true, []string{"internal:account:kyc"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("wallet", secret, "internal:account:kyc"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if _, hasRefresh := body["refresh_token"]; hasRefresh {
		t.Fatal("client_credentials must not issue a refresh token")
	}
	if body["scope"] != "internal:account:kyc" {
		t.Fatalf("scope = %v", body["scope"])
	}

	claims := decodeJWTPayload(t, body["access_token"].(string))
	if claims["sub"] != "wallet" || claims["azp"] != "wallet" {
		t.Fatalf("sub/azp = %v/%v", claims["sub"], claims["azp"])
	}
	if sid, ok := claims["sid"].(string); !ok || sid != "" {
		t.Fatalf("machine token must carry an empty sid, got %v", claims["sid"])
	}
	for _, k := range []string{"auth_time", "last_mfa_at", "amr", "kyc_level"} {
		if _, present := claims[k]; present {
			t.Fatalf("machine token must not carry %s", k)
		}
	}
}

func TestClientCredentialsRejectsPublicClient(t *testing.T) {
	ta := newOAuthTestApp(t)
	secret := seedM2MClient(t, ta.clientRepo, "spa", "public", true, []string{"internal:account:kyc"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("spa", secret, "internal:account:kyc"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if !strings.HasSuffix(body["type"].(string), "unauthorized-client") || body["error"] != "unauthorized_client" {
		t.Fatalf("body = %v", body)
	}
}

func TestClientCredentialsRejectsThirdPartyClient(t *testing.T) {
	ta := newOAuthTestApp(t)
	secret := seedM2MClient(t, ta.clientRepo, "third", "confidential", false, []string{"internal:account:kyc"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("third", secret, "internal:account:kyc"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["error"] != "unauthorized_client" {
		t.Fatalf("body = %v", body)
	}
}

func TestClientCredentialsRejectsBadSecret(t *testing.T) {
	ta := newOAuthTestApp(t)
	seedM2MClient(t, ta.clientRepo, "wallet", "confidential", true, []string{"internal:account:kyc"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("wallet", "wrong-secret", "internal:account:kyc"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if !strings.HasSuffix(body["type"].(string), "invalid-client") {
		t.Fatalf("body = %v", body)
	}
}

func TestClientCredentialsClampsScopes(t *testing.T) {
	ta := newOAuthTestApp(t)
	secret := seedM2MClient(t, ta.clientRepo, "wallet", "confidential", true, []string{"internal:account:kyc"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("wallet", secret, "internal:account:kyc dfe:nfes:read"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["scope"] != "internal:account:kyc" {
		t.Fatalf("scope must be clamped to allowed set, got %v", body["scope"])
	}
}

// ── KYC route tests (user-facing + internal) ────────────────────────────────

const validCPF = "52998224725"
const otherValidCPF = "11144477735"

// submitKYCBody builds a valid identity submission. Callers override single
// fields to exercise validation. It assumes every required document is
// already uploaded — see uploadAllRequiredKYCDocuments.
func submitKYCBody(cpf string) map[string]any {
	return map[string]any{
		"cpf":        cpf,
		"legal_name": "Fulano da Silva",
		"birth_date": "1990-01-01",
		"address": map[string]string{
			"zip_code": "01001000",
			"street":   "Praça da Sé",
			"number":   "100",
			"district": "Sé",
			"city":     "São Paulo",
			"state":    "SP",
		},
	}
}

// uploadKYCDocument drives presign → (simulated) S3 upload → confirm for a
// single document type and returns the resulting status.
func uploadKYCDocument(t *testing.T, ta *testApp, userID, token, docType string) map[string]any {
	t.Helper()

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": docType, "content_type": "image/png"}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presign(%s): expected 200, got %d: %s", docType, resp.StatusCode, bodyString(resp))
	}
	var presigned map[string]any
	readJSON(t, resp, &presigned)

	documentID, _ := presigned["document_id"].(string)
	if documentID == "" || presigned["upload_url"] == "" {
		t.Fatalf("presign response = %v", presigned)
	}

	// Stand in for the browser PUT to the presigned URL.
	ta.kycPresigner.putObject(kycDomain.BuildDocumentKey(userID, documentID), 2048)

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents/confirm",
		map[string]string{"document_id": documentID, "type": docType}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm document(%s): expected 200, got %d: %s", docType, resp.StatusCode, bodyString(resp))
	}
	var st map[string]any
	readJSON(t, resp, &st)
	return st
}

// uploadAllRequiredKYCDocuments uploads one document per RequiredDocTypes
// entry (id_front, id_back, and the four selfie poses) and returns the final
// status response.
func uploadAllRequiredKYCDocuments(t *testing.T, ta *testApp, userID, token string) map[string]any {
	t.Helper()
	var st map[string]any
	for _, docType := range kycDomain.RequiredDocTypes {
		st = uploadKYCDocument(t, ta, userID, token, docType)
	}
	return st
}

func TestSubmitKYCRequiresStepUp(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-stepup@example.com", "Password!123", "Fulano")
	stale := ta.issueStaleToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), stale)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if !strings.HasSuffix(body["type"].(string), "step-up-required") {
		t.Fatalf("body = %v", body)
	}
}

func TestSubmitKYCRejectsWithoutDocuments(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-nodocs@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-not-submitted") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestKYCFullFlow(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-flow@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	// 1. upload every required document (id_front, id_back, four selfie poses).
	st := uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	if st["state"] != "awaiting_files" {
		t.Fatalf("state after uploads = %v", st["state"])
	}

	// 2. submit → under review
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	readJSON(t, resp, &st)
	if st["state"] != "under_review" || st["level"] != "" {
		t.Fatalf("status after submit = %v", st)
	}

	// 3. get → masked CPF
	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	readJSON(t, resp, &st)
	if st["cpf_masked"] != "***.***.***-25" || st["state"] != "under_review" {
		t.Fatalf("status = %v", st)
	}

	// 4. a human reviewer approves via cmd/kyc (Service.Review directly — there
	// is no HTTP route for this decision).
	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionApprove, ""); err != nil {
		t.Fatalf("Review: %v", err)
	}

	// 5. get → verified
	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	readJSON(t, resp, &st)
	if st["level"] != "verified" || st["verified_at"] == "" {
		t.Fatalf("status after approval = %v", st)
	}

	// 6. internal get → full CPF, for ctech-wallet withdrawal-key validation
	m2m := ta.issueMachineToken(t, "wallet", []string{"internal:wallet:confirm-deposit"})
	resp = ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+u.ID(), nil, m2m)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("internal get: expected 200, got %d", resp.StatusCode)
	}
	var full map[string]any
	readJSON(t, resp, &full)
	if full["cpf"] != validCPF || full["level"] != "verified" {
		t.Fatalf("internal record = %v", full)
	}

	// 7. submission audit event recorded (verified is emitted by cmd/kyc, not
	// the handler, since the decision itself is a CLI action).
	hasSubmitted := false
	for _, e := range ta.auditRepo.events {
		if e.EventType == "kyc.submitted" {
			hasSubmitted = true
		}
	}
	if !hasSubmitted {
		t.Fatal("kyc.submitted audit event not recorded")
	}
}

func TestSubmitKYCValidation(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-val@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)

	// invalid check digit → 422
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody("52998224724"), token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad cpf: expected 422, got %d", resp.StatusCode)
	}

	// underage → 422 age-requirement-not-met
	body := submitKYCBody(validCPF)
	body["birth_date"] = time.Now().UTC().AddDate(-17, 0, 0).Format("2006-01-02")
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", body, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("underage: expected 422, got %d", resp.StatusCode)
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "age-requirement-not-met") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestSubmitDuplicateCPFConflict(t *testing.T) {
	ta := newTestApp(t)
	u1 := ta.registerUser(t, "kyc-dup1@example.com", "Password!123", "Fulano")
	u2 := ta.registerUser(t, "kyc-dup2@example.com", "Password!123", "Beltrano")
	token1, token2 := ta.issueToken(t, u1.ID()), ta.issueToken(t, u2.ID())
	uploadAllRequiredKYCDocuments(t, ta, u1.ID(), token1)
	uploadAllRequiredKYCDocuments(t, ta, u2.ID(), token2)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first submit: %d", resp.StatusCode)
	}
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token2)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: expected 409, got %d", resp.StatusCode)
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "cpf-already-registered") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestSubmitAfterVerifiedConflict(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-immutable@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)

	ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token)
	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionApprove, ""); err != nil {
		t.Fatalf("Review: %v", err)
	}

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(otherValidCPF), token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-already-verified") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestInternalKYCRejectsUserToken(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-usertoken@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID()) // has sid → not a machine token

	resp := ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+u.ID(), nil, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestInternalKYCRejectsMissingScope(t *testing.T) {
	ta := newTestApp(t)
	m2m := ta.issueMachineToken(t, "wallet", []string{"openid"})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/u1", nil, m2m)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestSubmitKYCRequiresAddress(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-addr@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)

	body := submitKYCBody(validCPF)
	delete(body, "address")

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", body, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing address: expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestSubmitKYCRejectsUnknownState(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-uf@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)

	body := submitKYCBody(validCPF)
	body["address"] = map[string]string{
		"zip_code": "01001000", "street": "Rua X", "number": "1",
		"district": "Centro", "city": "Nowhere", "state": "XX",
	}

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", body, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown UF: expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// A pending submission is frozen: identity data must not be swappable while a
// reviewer has it.
func TestResubmitWhilePendingConflicts(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-locked@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first submit: %d: %s", resp.StatusCode, bodyString(resp))
	}

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(otherValidCPF), token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resubmit: expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-submission-locked") {
		t.Fatalf("problem = %v", problem)
	}
}

// Documents may not be re-uploaded either while a submission is locked.
func TestUploadRejectsWhilePending(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-locked-upload@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-submission-locked") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestKYCStatusExposesAwaitingFilesState(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-state@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	// Only some of the required documents are uploaded so far.
	uploadKYCDocument(t, ta, u.ID(), token, kycDomain.DocTypeIDFront)
	uploadKYCDocument(t, ta, u.ID(), token, kycDomain.DocTypeIDBack)

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	var st map[string]any
	readJSON(t, resp, &st)

	if st["state"] != "awaiting_files" {
		t.Fatalf("status = %v", st)
	}
	docs, ok := st["documents"].([]any)
	if !ok || len(docs) != 2 {
		t.Fatalf("documents = %v", st["documents"])
	}
}

func TestKYCDocumentFlowApproved(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-ok@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	m2m := ta.issueMachineToken(t, "wallet", []string{"internal:wallet:confirm-deposit"})

	st := uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	if st["state"] != "awaiting_files" {
		t.Fatalf("state after uploads = %v", st["state"])
	}

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token)
	readJSON(t, resp, &st)
	if st["state"] != "under_review" {
		t.Fatalf("state after submit = %v", st["state"])
	}

	// Reviewer opens the documents (via kycSvc.DocumentURLs — cmd/kyc show).
	urls, err := ta.kycSvc.DocumentURLs(context.Background(), u.ID())
	if err != nil || len(urls) != len(kycDomain.RequiredDocTypes) {
		t.Fatalf("DocumentURLs: urls=%+v err=%v", urls, err)
	}

	// Reviewer approves.
	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionApprove, ""); err != nil {
		t.Fatalf("Review: %v", err)
	}

	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	readJSON(t, resp, &st)
	if st["state"] != "verified" || st["level"] != "verified" {
		t.Fatalf("status after approval = %v", st)
	}

	// ctech-wallet can still read the raw CPF for withdrawal-key validation.
	resp = ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+u.ID(), nil, m2m)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("internal get: expected 200, got %d", resp.StatusCode)
	}
}

func TestKYCDocumentFlowRejectedRequiresFreshUploads(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-reject@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(validCPF), token)

	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionReject, "document unreadable"); err != nil {
		t.Fatalf("Review: %v", err)
	}

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	var st map[string]any
	readJSON(t, resp, &st)
	if st["state"] != "rejected" || st["rejection_reason"] != "document unreadable" {
		t.Fatalf("status after rejection = %v", st)
	}

	// A rejection clears the old documents — resubmitting without fresh
	// uploads must fail.
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(otherValidCPF), token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resubmit without fresh docs: expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	// Fresh uploads unlock resubmission again.
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc", submitKYCBody(otherValidCPF), token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resubmit after fresh uploads: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// Confirming a document the client never actually uploaded must fail — the
// service proves the object exists rather than trusting the request.
func TestConfirmDocumentWithoutUploadRejected(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-noupload@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, token)
	var presigned map[string]any
	readJSON(t, resp, &presigned)

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents/confirm",
		map[string]string{"document_id": presigned["document_id"].(string), "type": "id_front"}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-document-not-uploaded") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestPresignDocumentRequiresStepUp(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-stepup@example.com", "Password!123", "Fulano")
	stale := ta.issueStaleToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, stale)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAccessTokenCarriesKYCLevelAfterRefresh(t *testing.T) {
	ta := newOAuthTestApp(t)
	secretHash, _ := crypto.HashPassword("web-secret")
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("web"), ClientType: "confidential",
		ClientSecretHash: secretHash,
		AllowedScopes:    []string{"openid", "profile", "email", "kyc"},
		FirstParty:       true,
	})
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-kyc", Email: "kyc@example.com", EmailVerified: true,
		CPF: validCPF, KYCLevel: "verified",
	})
	_, _, err := ta.sessionSvc.Create(context.Background(), "user-kyc", "Chrome", "1.2.3.4", "UA", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessions, _ := ta.sessionSvc.List(context.Background(), "user-kyc")
	refreshToken, err := ta.sessionSvc.IssueClientToken(context.Background(), "user-kyc", sessions[0].ID(), "web", []string{"openid", "profile", "email", "kyc"})
	if err != nil {
		t.Fatalf("issue client token: %v", err)
	}

	resp := ta.postForm("/v1.0/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {"web"},
		"client_secret": {"web-secret"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	claims := decodeJWTPayload(t, body["access_token"].(string))
	if claims["kyc_level"] != "verified" {
		t.Fatalf("kyc_level = %v", claims["kyc_level"])
	}
	if !strings.Contains(body["scope"].(string), "kyc") {
		t.Fatalf("scope = %v", body["scope"])
	}
}
