package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/audit"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

func seedPendingKYCReview(t *testing.T, app *testApp) *userDomain.User {
	t.Helper()
	u := app.registerUser(t, "review-subject@example.com", "Password!123", "Review")
	u.LegalName = "Review Subject"
	u.CPF = "11144477735"
	u.BirthDate = "1990-01-01"
	u.PhoneNumber = "+5511987654321"
	u.Address = userDomain.Address{ZipCode: "01310100", Street: "Av. Paulista", Number: "1000", District: "Bela Vista", City: "São Paulo", State: "SP"}
	u.KYCLevel, u.KYCStatus = kycDomain.LevelEnhanced, kycDomain.StatusPending
	u.KYCSubmittedAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	u.KYCDocuments = []userDomain.KYCDocument{
		{ID: "front", Type: kycDomain.DocTypeIDFront, Key: kycDomain.BuildDocumentKey(u.ID(), "front"), UploadedAt: u.KYCSubmittedAt},
	}
	return u
}

func TestAdminKYCRequiresManagerRoleFromDatabase(t *testing.T) {
	app := newTestApp(t)
	seedPendingKYCReview(t, app)

	for _, role := range []string{"", userDomain.SupportRoleAgent} {
		reviewer := app.registerUser(t, "reviewer-"+strings.ReplaceAll(role, "", "x")+"@example.com", "Password!123", "Reviewer")
		reviewer.SupportRole = role
		resp := app.doWithToken(http.MethodGet, "/v1.0/admin/kyc/reviews", nil, app.issueToken(t, reviewer.ID()))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("role %q status = %d, want 403", role, resp.StatusCode)
		}
	}

	manager := app.registerUser(t, "manager@example.com", "Password!123", "Manager")
	manager.SupportRole = userDomain.SupportRoleManager
	resp := app.doWithToken(http.MethodGet, "/v1.0/admin/kyc/reviews", nil, app.issueToken(t, manager.ID()))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manager status = %d, want 200: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestAdminKYCRejectsPrivilegedUserTokenFromAnotherClient(t *testing.T) {
	app := newTestApp(t)
	manager := app.registerUser(t, "manager-client@example.com", "Password!123", "Manager")
	manager.SupportRole = userDomain.SupportRoleManager
	token, err := app.jwtSvc.SignAccessToken(manager.ID(), "session", "delegated-client", testAccountScopes(), "http://localhost", []string{"http://localhost"}, time.Now().Unix(), time.Now().Unix(), []string{"pwd"}, "")
	if err != nil {
		t.Fatal(err)
	}
	resp := app.doWithToken(http.MethodGet, "/v1.0/admin/kyc/reviews", nil, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAdminKYCReviewAccessAndDecisionAreAudited(t *testing.T) {
	app := newTestApp(t)
	subject := seedPendingKYCReview(t, app)
	app.kycPresigner.putObject(kycDomain.BuildDocumentKey(subject.ID(), "front"), 1024)
	reviewer := app.registerUser(t, "manager-audit@example.com", "Password!123", "Maria")
	reviewer.LastName = "Manager"
	reviewer.DisplayName = "Maria Manager"
	reviewer.SupportRole = userDomain.SupportRoleManager
	token := app.issueToken(t, reviewer.ID())

	resp := app.doWithToken(http.MethodPost, "/v1.0/admin/kyc/reviews/"+subject.ID()+"/documents/access", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("documents status = %d: %s", resp.StatusCode, bodyString(resp))
	}

	resp = app.doWithToken(http.MethodPost, "/v1.0/admin/kyc/reviews/"+subject.ID()+"/decision", map[string]any{"decision": "approve"}, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("decision status = %d: %s", resp.StatusCode, bodyString(resp))
	}
	if subject.KYCStatus != kycDomain.StatusVerified || subject.KYCReviewedBy != reviewer.ID() || subject.KYCReviewedByName != "Maria Manager" {
		t.Fatalf("review attribution not persisted: %+v", subject)
	}

	events, _, err := app.auditSvc.ListByUser(context.Background(), subject.ID(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	foundViewed, foundApproved := false, false
	for _, event := range events {
		if event.EventType == audit.EventKYCDocumentsViewed {
			foundViewed = event.Metadata["reviewer_id"] == reviewer.ID() && event.Metadata["reviewer_role"] == userDomain.SupportRoleManager
		}
		if event.EventType == audit.EventKYCVerified {
			foundApproved = event.Metadata["reviewer_id"] == reviewer.ID()
		}
	}
	if !foundViewed || !foundApproved {
		t.Fatalf("missing attributed audit events: viewed=%v approved=%v", foundViewed, foundApproved)
	}

	resp = app.doWithToken(http.MethodPost, "/v1.0/admin/kyc/reviews/"+subject.ID()+"/decision", map[string]any{"decision": "reject", "reason_code": "other", "details": "retry"}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second decision status = %d, want 409", resp.StatusCode)
	}
}

func TestAdminKYCRejectRequiresReason(t *testing.T) {
	app := newTestApp(t)
	subject := seedPendingKYCReview(t, app)
	reviewer := app.registerUser(t, "manager-reject@example.com", "Password!123", "Manager")
	reviewer.SupportRole = userDomain.SupportRoleManager

	resp := app.doWithToken(http.MethodPost, "/v1.0/admin/kyc/reviews/"+subject.ID()+"/decision", map[string]any{"decision": "reject"}, app.issueToken(t, reviewer.ID()))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if subject.KYCStatus != kycDomain.StatusPending {
		t.Fatalf("status changed without rejection reason: %s", subject.KYCStatus)
	}

	resp = app.doWithToken(http.MethodPost, "/v1.0/admin/kyc/reviews/"+subject.ID()+"/decision", map[string]any{"decision": "reject", "reason_code": "other"}, app.issueToken(t, reviewer.ID()))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("other without details status = %d, want 422", resp.StatusCode)
	}

	resp = app.doWithToken(http.MethodPost, "/v1.0/admin/kyc/reviews/"+subject.ID()+"/decision", map[string]any{"decision": "reject", "reason_code": "document_unreadable", "details": strings.Repeat("a", 256)}, app.issueToken(t, reviewer.ID()))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("256-char details status = %d, want 422", resp.StatusCode)
	}

	resp = app.doWithToken(http.MethodPost, "/v1.0/admin/kyc/reviews/"+subject.ID()+"/decision", map[string]any{"decision": "reject", "reason_code": "document_unreadable", "details": "A imagem está desfocada."}, app.issueToken(t, reviewer.ID()))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("valid rejection status = %d: %s", resp.StatusCode, bodyString(resp))
	}
	if subject.KYCRejectionCode != kycDomain.RejectionDocumentUnreadable || subject.KYCRejectionReason != "A imagem está desfocada." {
		t.Fatalf("rejection fields = %q / %q", subject.KYCRejectionCode, subject.KYCRejectionReason)
	}
}
