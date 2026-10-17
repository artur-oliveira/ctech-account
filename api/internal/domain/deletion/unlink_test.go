package deletion

import (
	"context"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type recordingConsents struct {
	revoked []string
	grants  []*consent.Grant
}

func (r *recordingConsents) List(context.Context, string) ([]*consent.Grant, error) {
	return r.grants, nil
}

func (r *recordingConsents) Revoke(_ context.Context, _, client string) error {
	r.revoked = append(r.revoked, client)
	return nil
}

type recordingUnlinks struct{ added []string }

func (r *recordingUnlinks) AddUnlinkedService(_ context.Context, _, service string) error {
	r.added = append(r.added, service)
	return nil
}

type recordingAudit struct{ entries []audit.Entry }

func (r *recordingAudit) Record(_ context.Context, e audit.Entry) { r.entries = append(r.entries, e) }

func TestFinishRevokesConsentsRecordsTheServiceAuditsAndWrites(t *testing.T) {
	grant := func(client string, scopes ...string) *consent.Grant {
		return &consent.Grant{PK: consent.BuildPK("u1"), SK: consent.BuildSK(client), Scopes: scopes}
	}
	consents := &recordingConsents{grants: []*consent.Grant{
		grant("poker-web", "openid"), grant("poker-app", "openid"),
		grant("third-party", "poker:tables:read"), // another client holding a poker scope
		grant("wallet-web", "wallet:balance:read"),
	}}
	unlinks, au, mail := &recordingUnlinks{}, &recordingAudit{}, &fakeMailer{}
	tokens := &fakeSessions{}
	u := NewServiceUnlinker(consents, tokens, unlinks, fakeUsers{u: &user.User{Email: "ana@example.com", FirstName: "Ana"}}, au, mail,
		func(string) []string { return []string{"poker-web", "poker-app"} })
	r := &Request{ID: "r1", UserID: "u1", Scope: ScopeService, Service: "poker"}

	if err := u.Finish(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if len(consents.revoked) != 3 || consents.revoked[0] != "poker-web" || consents.revoked[1] != "poker-app" || consents.revoked[2] != "third-party" {
		t.Fatalf("consents revoked = %v", consents.revoked)
	}
	if len(tokens.clients) != 1 || len(tokens.clients[0]) != 2 {
		t.Fatalf("refresh tokens swept = %v, want one sweep for the login clients", tokens.clients)
	}
	if len(unlinks.added) != 1 || unlinks.added[0] != "poker" {
		t.Fatalf("unlinked = %v", unlinks.added)
	}
	if len(au.entries) != 1 || au.entries[0].Type != audit.EventDeletionServiceUnlinked ||
		au.entries[0].Metadata["service"] != "poker" || au.entries[0].Metadata["request_id"] != "r1" {
		t.Fatalf("audit = %+v", au.entries)
	}
	if mail.unlinkedTo != "poker" {
		t.Fatalf("e-mail product = %q, want poker", mail.unlinkedTo)
	}
}

func TestFinishDoesNotAuditWhenAnEarlierStepFails(t *testing.T) {
	au := &recordingAudit{}
	u := NewServiceUnlinker(failingConsents{}, &fakeSessions{}, &recordingUnlinks{}, fakeUsers{u: &user.User{}}, au, &fakeMailer{},
		func(string) []string { return []string{"poker-web"} })
	if err := u.Finish(context.Background(), &Request{ID: "r1", UserID: "u1", Service: "poker"}); err == nil {
		t.Fatal("a failed consent revoke must fail Finish so the worker retries")
	}
	if len(au.entries) != 0 {
		t.Fatal("the audit row must come after every step that can fail, so a retry does not write it twice")
	}
}

type failingConsents struct{}

func (failingConsents) List(context.Context, string) ([]*consent.Grant, error) {
	return []*consent.Grant{{PK: consent.BuildPK("u1"), SK: consent.BuildSK("poker-web")}}, nil
}

func (failingConsents) Revoke(context.Context, string, string) error { return context.DeadlineExceeded }
