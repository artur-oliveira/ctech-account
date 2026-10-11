package client

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type ownerStatusStub struct {
	active map[string]bool
	err    error
	calls  int
}

func (o *ownerStatusStub) IsActive(_ context.Context, userID string) (bool, error) {
	o.calls++
	if o.err != nil {
		return false, o.err
	}
	return o.active[userID], nil
}

// getCountingRepo proves an impossible id never reaches DynamoDB.
type getCountingRepo struct {
	*operatorRepoStub
	gets int
}

func (r *getCountingRepo) GetByID(ctx context.Context, id string) (*OAuthClient, error) {
	r.gets++
	return r.operatorRepoStub.GetByID(ctx, id)
}

func newInternalService(clients ...*OAuthClient) (*InternalService, *getCountingRepo, *ownerStatusStub) {
	repo := &getCountingRepo{operatorRepoStub: &operatorRepoStub{clients: map[string]*OAuthClient{}}}
	for _, c := range clients {
		repo.clients[c.ID()] = c
	}
	owners := &ownerStatusStub{active: map[string]bool{"usr_live": true}}
	return NewInternalService(repo, owners), repo, owners
}

func merchantClient(id, owner string) *OAuthClient {
	return &OAuthClient{
		PK:               BuildPK(id),
		Name:             "Loja",
		ClientType:       TypeConfidential,
		ClientSecretHash: "hash",
		AllowedScopes:    []string{"billing:invoices:write"},
		OwnerUserID:      owner,
	}
}

func TestDescribeReturnsOwnerScopesAndActive(t *testing.T) {
	svc, _, _ := newInternalService(merchantClient("cli_1", "usr_live"))
	view, err := svc.Describe(context.Background(), "cli_1")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if view.ClientID != "cli_1" || view.OwnerUserID != "usr_live" || !view.Active {
		t.Fatalf("got %+v, want cli_1 owned by usr_live, active", view)
	}
	if len(view.AllowedScopes) != 1 || view.AllowedScopes[0] != "billing:invoices:write" {
		t.Fatalf("allowed scopes = %v", view.AllowedScopes)
	}
}

func TestDescribeUnknownClientIsNotFound(t *testing.T) {
	svc, _, _ := newInternalService()
	if _, err := svc.Describe(context.Background(), "cli_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// No creation path makes an id this long, and a DynamoDB key this size comes
// back as an error (a 500), not a miss. The answer is a 404, without a read.
func TestDescribeRefusesAnImpossibleIDWithoutReading(t *testing.T) {
	svc, repo, _ := newInternalService()
	for _, id := range []string{"", strings.Repeat("a", maxOperatorClientIDLength+1)} {
		if _, err := svc.Describe(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id of length %d: err = %v, want ErrNotFound", len(id), err)
		}
	}
	if repo.gets != 0 {
		t.Fatalf("GetByID called %d times for impossible ids, want 0", repo.gets)
	}
}

func TestDescribeIsInactiveWhenTheOwnerCannotBeActedFor(t *testing.T) {
	public := merchantClient("cli_public", "usr_live")
	public.ClientType, public.ClientSecretHash = TypePublic, ""
	noSecret := merchantClient("cli_nosecret", "usr_live")
	noSecret.ClientSecretHash = ""

	svc, _, _ := newInternalService(
		merchantClient("cli_locked", "usr_locked"), // owner stub answers false
		merchantClient("cli_orphan", ""),
		public,
		noSecret,
	)
	for _, id := range []string{"cli_locked", "cli_orphan", "cli_public", "cli_nosecret"} {
		view, err := svc.Describe(context.Background(), id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if view.Active {
			t.Fatalf("%s is active, want inactive", id)
		}
	}
}

// Platform clients (createclient) are owned by "system", which is not a user
// row: they are active without asking the user domain.
func TestDescribeTreatsSystemClientsAsActiveWithoutAnOwnerLookup(t *testing.T) {
	svc, _, owners := newInternalService(merchantClient("billing-account", SystemOwnerUserID))
	view, err := svc.Describe(context.Background(), "billing-account")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !view.Active || view.OwnerUserID != SystemOwnerUserID {
		t.Fatalf("got %+v, want active, owner system", view)
	}
	if owners.calls != 0 {
		t.Fatalf("owner lookups = %d, want 0", owners.calls)
	}
}

// An owner lookup that fails is an outage: an error the handler turns into a
// 500, never "inactive" (which billing would read as "not yours").
func TestDescribeSurfacesAnOwnerLookupFailure(t *testing.T) {
	svc, _, owners := newInternalService(merchantClient("cli_1", "usr_live"))
	owners.err = errors.New("dynamo down")
	_, err := svc.Describe(context.Background(), "cli_1")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want a non-NotFound error", err)
	}
}

func TestDescribeNeverReturnsNilScopes(t *testing.T) {
	c := merchantClient("cli_empty", "usr_live")
	c.AllowedScopes = nil
	svc, _, _ := newInternalService(c)
	view, err := svc.Describe(context.Background(), "cli_empty")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if view.AllowedScopes == nil {
		t.Fatal("AllowedScopes is nil; the route would answer null instead of []")
	}
}
