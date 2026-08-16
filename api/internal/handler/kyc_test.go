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
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/scopes"
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
const validPhone = "+5511987654321"

func validAddress() map[string]any {
	return map[string]any{
		"zip_code": "01310100", "street": "Av. Paulista", "number": "1000",
		"district": "Bela Vista", "city": "São Paulo", "state": "SP",
	}
}

func submitBasicBody(cpf string) map[string]any {
	return map[string]any{
		"cpf": cpf, "legal_name": "Fulano da Silva", "birth_date": "1990-01-01",
		"phone_number": validPhone, "address": validAddress(),
	}
}

// verifyBasicPhone submits Basic KYC and returns its immediate verified status.
func verifyBasicPhone(t *testing.T, ta *testApp, token, cpf string) map[string]any {
	t.Helper()
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(cpf), token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit basic: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var st map[string]any
	readJSON(t, resp, &st)
	return st
}

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

func uploadAllRequiredKYCDocuments(t *testing.T, ta *testApp, userID, token string) map[string]any {
	t.Helper()
	var st map[string]any
	for _, docType := range kycDomain.RequiredDocTypes {
		st = uploadKYCDocument(t, ta, userID, token, docType)
	}
	return st
}

func TestSubmitBasicRequiresStepUp(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-stepup@example.com", "Password!123", "Fulano")
	stale := ta.issueStaleToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), stale)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if !strings.HasSuffix(body["type"].(string), "step-up-required") {
		t.Fatalf("body = %v", body)
	}
}

func TestKYCFullFlow(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-flow@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	// 1. Basic: submit → basic_verified.
	st := verifyBasicPhone(t, ta, token, validCPF)
	if st["state"] != "basic_verified" || st["level"] != "basic" {
		t.Fatalf("status after phone verify = %v", st)
	}

	// 2. Enhanced: upload every required document, then submit → under review.
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit enhanced: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	readJSON(t, resp, &st)
	if st["state"] != "under_review" {
		t.Fatalf("status after enhanced submit = %v", st)
	}

	// 3. get → masked CPF, masked phone.
	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	readJSON(t, resp, &st)
	if st["cpf_masked"] != "***.***.***-25" || st["state"] != "under_review" {
		t.Fatalf("status = %v", st)
	}

	// 4. A human reviewer approves via cmd/kyc (Service.Review directly).
	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionApprove, ""); err != nil {
		t.Fatalf("Review: %v", err)
	}

	// 5. get → verified, kyc_level claim reachable as "verified".
	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	readJSON(t, resp, &st)
	if st["state"] != "verified" || st["verified_at"] == "" {
		t.Fatalf("status after approval = %v", st)
	}

	// 6. internal get → full CPF + phone, for ctech-wallet withdrawal-key validation.
	m2m := ta.issueMachineToken(t, "wallet", []string{"internal:wallet:confirm-deposit"})
	resp = ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+u.ID(), nil, m2m)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("internal get: expected 200, got %d", resp.StatusCode)
	}
	var full map[string]any
	readJSON(t, resp, &full)
	if full["cpf"] != validCPF || full["phone_number"] != validPhone || full["level"] != "enhanced" {
		t.Fatalf("internal record = %v", full)
	}
}

func TestSubmitBasicValidation(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-val@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody("52998224724"), token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad cpf: expected 422, got %d", resp.StatusCode)
	}

	body := submitBasicBody(validCPF)
	body["birth_date"] = time.Now().UTC().AddDate(-17, 0, 0).Format("2006-01-02")
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", body, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("underage: expected 422, got %d", resp.StatusCode)
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "age-requirement-not-met") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestSubmitBasicDuplicateCPFConflict(t *testing.T) {
	ta := newTestApp(t)
	u1 := ta.registerUser(t, "kyc-dup1@example.com", "Password!123", "Fulano")
	u2 := ta.registerUser(t, "kyc-dup2@example.com", "Password!123", "Beltrano")
	token1, token2 := ta.issueToken(t, u1.ID()), ta.issueToken(t, u2.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), token1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first submit: %d", resp.StatusCode)
	}
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), token2)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: expected 409, got %d", resp.StatusCode)
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "cpf-already-registered") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestSubmitEnhancedRequiresBasicVerified(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-nobasic@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-basic-required") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestDocumentUploadRequiresBasicVerified(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-nobasic@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-basic-required") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestSubmitEnhancedRejectsWithoutDocuments(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-nodocs@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-not-submitted") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestResubmitEnhancedWhilePendingConflicts(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-locked@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first submit: %d: %s", resp.StatusCode, bodyString(resp))
	}
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resubmit: expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-submission-locked") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestKYCDocumentFlowRejectedRequiresFreshUploads(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-reject@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)

	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionReject, "document unreadable"); err != nil {
		t.Fatalf("Review: %v", err)
	}

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	var st map[string]any
	readJSON(t, resp, &st)
	if st["state"] != "rejected" || st["rejection_reason"] != "document unreadable" {
		t.Fatalf("status after rejection = %v", st)
	}

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resubmit without fresh docs: expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resubmit after fresh uploads: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestConfirmDocumentWithoutUploadRejected(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-noupload@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, token)
	var presigned map[string]any
	readJSON(t, resp, &presigned)

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents/confirm",
		map[string]string{"document_id": presigned["document_id"].(string), "type": "id_front"}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
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

func TestInternalKYCRejectsUserToken(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-usertoken@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

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

// TestInternalKYCReturnsEmail covers the ctech-wallet Asaas-onboarding
// extension (docs/plans/2026-07-30-asaas-baas-implementation-plan.md §3.1):
// the internal KYC record must also carry the user's email, alongside the
// existing cpf/legal_name/birth_date/phone_number/address.
func TestInternalKYCReturnsEmail(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-email@example.com", "Password!123", "Fulano")
	// This test harness wires the internal-KYC route to InternalWalletConfirmDeposit,
	// not InternalAccountKYC (production uses InternalAccountKYC — see
	// cmd/api/main.go) — a pre-existing test/prod scope divergence, out of
	// scope to fix here.
	m2m := ta.issueMachineToken(t, "wallet", []string{scopes.InternalWalletConfirmDeposit})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+u.ID(), nil, m2m)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["email"] != "kyc-email@example.com" {
		t.Fatalf("expected email in response, got: %+v", body)
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
		CPF: validCPF, KYCLevel: kycDomain.LevelEnhanced, KYCStatus: kycDomain.StatusVerified,
	})
	_, _, err := ta.sessionSvc.Create(context.Background(), "user-kyc", "Chrome", "1.2.3.4", "UA", nil, sessionDomain.GeoData{})
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
}
