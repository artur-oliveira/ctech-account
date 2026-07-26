package handler_test

import (
	"context"
	"net/http"
	"testing"

	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

func TestUserInfoReportsBasicKYCLevelWhenEnhancedPending(t *testing.T) {
	ta := newTestApp(t)
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_userinfo-pending", Email: "userinfo-pending@example.com", EmailVerified: true,
		KYCLevel: kycDomain.LevelEnhanced, KYCStatus: kycDomain.StatusPending,
	})
	token := ta.issueTokenWithScopes(t, "userinfo-pending", []string{"openid", "kyc"})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/userinfo", nil, token)
	var body map[string]any
	readJSON(t, resp, &body)
	if body["kyc_level"] != "basic" {
		t.Fatalf("kyc_level = %v, want basic (enhanced/pending still keeps basic access, never raw \"enhanced\")", body["kyc_level"])
	}
}

func TestUserInfoReportsVerifiedKYCLevel(t *testing.T) {
	ta := newTestApp(t)
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_userinfo-verified", Email: "userinfo-verified@example.com", EmailVerified: true,
		KYCLevel: kycDomain.LevelEnhanced, KYCStatus: kycDomain.StatusVerified,
	})
	token := ta.issueTokenWithScopes(t, "userinfo-verified", []string{"openid", "kyc"})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/userinfo", nil, token)
	var body map[string]any
	readJSON(t, resp, &body)
	if body["kyc_level"] != "verified" {
		t.Fatalf("kyc_level = %v, want verified", body["kyc_level"])
	}
}
