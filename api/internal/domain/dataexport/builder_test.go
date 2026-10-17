package dataexport

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/domain/session"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type srcUsers struct{}

func (srcUsers) GetByID(context.Context, string) (*user.User, error) {
	return &user.User{Email: "a@b.c", PasswordHash: "HASH-SECRET", CPF: "12345678901",
		KYCDocuments: []user.KYCDocument{{ID: "d1", Type: "rg", Key: "kyc/KEY-SECRET"}}}, nil
}

type srcSessions struct{}

func (srcSessions) List(context.Context, string) ([]*session.Session, error) {
	return []*session.Session{{RefreshTokenHash: "TOKEN-SECRET", DeviceName: "Firefox"}}, nil
}

type srcKeys struct{}

func (srcKeys) List(context.Context, string) ([]*apikey.APIKey, error) {
	return []*apikey.APIKey{{Name: "ci", KeyPrefix: "ck_1", KeyHash: "KEYHASH-SECRET"}}, nil
}

type srcConsents struct{}

func (srcConsents) List(context.Context, string) ([]*consent.Grant, error) {
	return []*consent.Grant{{SK: "CONSENT_app", Scopes: []string{"openid"}}}, nil
}

type srcAudit struct{ pages int }

func (a *srcAudit) ListByUser(_ context.Context, _, cursor string, _ int32) ([]*audit.Event, string, error) {
	a.pages++
	if cursor == "" {
		return []*audit.Event{{EventType: "auth.login"}}, "next", nil
	}
	return []*audit.Event{{EventType: "auth.logout"}}, "", nil
}

func TestAccountPartHasEverySectionAndNoSecrets(t *testing.T) {
	a := &srcAudit{}
	data, err := NewZipBuilder(AccountSections(Sources{srcUsers{}, srcSessions{}, srcKeys{}, srcConsents{}, a})...).Build(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		all.Write(b)
	}
	for _, n := range []string{"profile.json", "sessions.json", "api-keys.json", "consents.json", "activity.json", "README.txt"} {
		if !names[n] {
			t.Errorf("missing %s", n)
		}
	}
	for _, secret := range []string{"HASH-SECRET", "TOKEN-SECRET", "KEYHASH-SECRET", "KEY-SECRET"} {
		if strings.Contains(all.String(), secret) {
			t.Errorf("zip leaks %s", secret)
		}
	}
	if !strings.Contains(all.String(), "12345678901") || a.pages != 2 || !strings.Contains(all.String(), "auth.logout") {
		t.Errorf("own data missing or activity not paged (pages=%d)", a.pages)
	}
}
