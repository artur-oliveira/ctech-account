# Internal OAuth client route (console self-service) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let ctech-billing check who owns an OAuth client before it accepts that client as a merchant's API credential. This adds `GET /v1.0/internal/oauth-clients/{client_id}` → `{ "client_id", "owner_user_id", "allowed_scopes", "active" }`, which never returns the secret. The route sits behind a new internal scope, `internal:account:oauth-client-read`, and that scope is granted to the M2M client billing already uses for its membership reads.

**Second goal (Tasks 5–6, owner decision "option A"):** let `POST /v1.0/token` serve `client_credentials` to confidential **developer-page** (non-first-party) clients. This is allowed only when every scope the client holds is one its resource server marked for machine-to-machine use with a new per-scope manifest field, `"m2m": true`, and only while the client is active (same derivation as the route). First-party clients keep today's behaviour exactly.

**Architecture:** A small read service, `client.InternalService.Describe`, is added to the OAuth client domain. It reads the client row and derives `active`, because the model has no status field (see decision P1). It asks the user domain whether the owner can still be acted for, through a one-method interface (`OwnerStatus`) so the client package does not import `user`. The route gets a new handler file (`oauth_client_internal.go`) and is mounted in `main.go` the way the membership routes are: `RequireAuth` plus `RequireInternalScope`, attached to the route itself. The scope is published through the embedded Account manifest, which is already installed at boot by `BootstrapAccount`. No command can currently add a scope to an existing M2M client: `cmd/createclient` only creates clients. So this plan adds `OperatorService.GrantM2MScopes` and a `-grant` mode on `cmd/createclient`, and the runbook uses them in each environment.

Tasks 5–6 add an optional boolean `m2m` to `scopes.ScopeDefinition` (the manifest entry). It is carried into the catalog's `ScopeEntry` and checked by a new `CatalogService.ValidateMachineGrantable`. The token handler's `clientCredentials` keeps the first-party branch unchanged and adds a third-party branch: authenticate, check the owner is active, require every held scope to be `m2m`, require every requested scope to be held. Anything else refuses the **whole** request with a specific OAuth error. It never issues a subset.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB (`api/`), standard `testing` + `net/http/httptest`.

**Spec:** `ctech-billing/docs/specs/2026-10-10-console-self-service.md` (another repository: `github.com/artur-oliveira/ctech-billing`). This plan implements § 6.3 and § 9 step 1, plus D5 (in § 2) for this repository's side.

**Cross-project impact:**
- JWT signing, JWKS, OAuth flows and existing tokens: no change. A token can carry the new scope only if its client is granted it.
- ctech-billing gets a new route and a new scope on its existing client, `/ctech-billing/{env}/billing/account-client-id`. Billing's own work (spec § 9 parts 2–3) consumes it, and nothing in billing breaks if this ships first.
- ctech-billing, **resource-server manifest**: to let merchants' developer-page clients obtain tokens, billing must add `"m2m": true` to its integration scopes in `api/internal/oauthresource/scope-manifest.json`. The exact list is in § "What ctech-billing must publish" below. That change belongs to billing's part 3 plan, not this one. Until billing publishes it, no developer-page client can get a `client_credentials` token, which is today's behaviour.
- Every other resource server (ctech-dfe, wallet, poker): no change. A manifest without `m2m` keeps its current hash, so its next publish is a no-op (`omitempty`).
- `POST /v1.0/token`: for first-party clients, no change. For a confidential non-first-party client, the response changes from today's blanket `403 unauthorized_client` to a token whenever the m2m rules hold. The order of checks also changes: the secret is now verified **before** the first-party decision, so a wrong secret answers `401 invalid_client` for every confidential client.
- ctech-dfe: none directly. However, the new `createclient -grant` mode removes the reason ctech-dfe's organization-subscription plan provisions a second client (`ctech-dfe-workspaces`): it could grant the membership scopes to its existing client instead. That is that plan's call; this plan only states the option.
- ctech-wallet: none.
- `ui`: none.
- `cdk`: none. No new env var, no new table, no new SSM parameter.

## Decisions this plan takes where the spec is silent or does not fit the code

- **P1: what `active` means.** `OAuthClient` has no status field. A deleted client is hard-deleted, so it answers 404. `active` is therefore derived. It is `true` when the client can still authenticate as itself on behalf of a live owner:
  - the client is confidential and holds a secret hash, and
  - its owner is the platform (`owner_user_id = "system"`), or an account that exists, is enabled (`is_enabled`) and is not locked by a deletion request (`deletion_state` empty).

  A public client is never active, because it cannot authenticate a server-to-server call. A locked or disabled owner makes the client inactive. Today account deletion's `AccountLocker.Lock` revokes sessions and API keys but leaves OAuth clients alone, so without this rule billing would accept a credential from a person whose account is being deleted.
- **P2: an id longer than 128 characters, or empty, is a 404 without a DynamoDB read.** 128 is `maxOperatorClientIDLength`, the longest id either creation path can produce: self-service ids are UUIDs, operator ids are 3–128. Without this, an over-long key would reach `GetItem` and come back as a 500.
- **P3: the response is a typed struct, not `fiber.Map`.** The four fields are the whole contract. A struct means a new field is a visible diff, and the secret hash, redirect URIs, audience and `first_party` cannot leak through a careless map.
- **P4: the grant is a `-grant` mode on `cmd/createclient`, not a new command.** It reuses `parseScopes`, the AWS/Valkey wiring and the operator service. It is add-only and idempotent, so it can be re-run safely. It refuses self-service clients (that would hand a third party an internal scope) and scope-registry publishers (those are bound to exactly one permission by design).
- **P5: no `Cache-Control` and no caching here.** Billing calls the route once, when a person connects a credential (spec § 5.1). It is not on any request path.

## The `client_credentials` gap, and how Tasks 5–6 close it

`POST /v1.0/token` with `grant_type=client_credentials` refuses any client that is not first-party (`api/internal/handler/token.go:174`). Clients created on the developer page (`Service.Create`) never are, so a merchant's own client could not call billing's M2M API. The owner chose **option A**: a resource server marks the scopes it accepts from machine clients, and only those scopes open the grant to developer-page clients. Decisions:

- **P6: the field is `"m2m": true` on a manifest scope entry** (`ScopeDefinition.M2M`, `json:"m2m,omitempty"`, `dynamodbav:"m2m,omitempty"`). It is optional and absent means `false`. It is one lowercase word, like `visibility` and `status`, and matches the spec's own wording ("`billing:` M2M scope"). It is allowed only on `public` scopes. `internal` scopes are already machine-only and are never grantable to developer-page clients, so marking one would read as a permission that does not exist, and `ValidateManifest` refuses it. On a `deprecated` scope the flag is kept but ignored: deprecated scopes are never grantable.
- **P7: refuse the whole request, never a subset.** A developer-page client may use `client_credentials` only when **every** scope it holds is an active public `m2m` scope. A single non-m2m scope (e.g. `billing:my-invoices:read`) gets `403 unauthorized_client` naming that scope, whatever is requested. A requested scope the client does not hold gets `400 invalid_scope` naming it. Issuing the m2m subset silently would let a client that also serves user sign-in mint machine tokens nobody meant it to have. The rule: one client for user sign-in, a separate one for machine access.
- **P8: the owner must be active** (`InternalService.Active`, the same derivation the route reports). A developer-page client of a person who is disabled, mid-deletion or gone gets `403 unauthorized_client`. This is checked on every token request, so a lock takes effect at the next token (at most 15 minutes for tokens already issued).
- **P9: first-party clients are untouched.** Same scope clamping (`FilterScopes`), no `m2m` requirement, internal scopes still allowed. The only first-party-visible change is the check order: the secret is verified right after the public-client check, for every confidential client.
- **P10: `m2m` is visible in `GET /v1.0/scopes`** (it is part of `ScopeEntry`, `omitempty`). This lets the developer page show which scopes work for machine access. `ui` adoption is out of scope.

## Global Constraints

- Route: `GET /v1.0/internal/oauth-clients/:client_id`. Path segments are in English (D7).
- Response `200`: exactly `{ "client_id": string, "owner_user_id": string, "allowed_scopes": string[], "active": bool }`. `allowed_scopes` is `[]` when empty, never `null`. Never the secret, the secret hash, redirect URIs, audience, `first_party` or `managed_resource_id`.
- An unknown client answers `404`, an RFC 7807 problem via `apierror.NotFound`.
- Scope: `internal:account:oauth-client-read`, visibility `internal`, status `active`, in `api/internal/scopes/account-scope-manifest.json`, with Go constant `scopes.InternalAccountOAuthClientRead`.
- Guard: `middleware.RequireAuth(jwtSvc)` + `middleware.RequireInternalScope(scopesPkg.InternalAccountOAuthClientRead)` on the route, never on an `/internal` group. A token with a session id (any user token) is refused with `403`, and so is a service token without this exact scope.
- The scope is granted to the client whose id is stored at `/ctech-billing/{env}/billing/account-client-id`, in `dev`, `stage` and `prod`. That client already holds `internal:account:org-member` and `internal:account:user-organizations`. The grant only adds: existing scopes stay.
- Manifest field: `"m2m": true` on a scope entry, optional, default `false`, allowed only with `"visibility": "public"`. It is serialized `omitempty` everywhere (JSON, DynamoDB, the Valkey catalog cache), so manifests without it keep their hash.
- `client_credentials` for a confidential client with `first_party` false: refused with `403 unauthorized_client` unless the owner is active **and** every held scope is an active public `m2m` scope. Any requested scope not held is refused with `400 invalid_scope`. The whole request is refused, never a subset. Internal scopes can never reach such a token.
- `client_credentials` for a first-party client: today's behaviour, byte for byte (clamping, internal scopes, audiences).
- Layering: the handler calls one service method; the service holds the logic; the repository is unchanged. Errors are `*apierror.Problem` sent via `.Send(c)`.
- Every behaviour change comes with its documentation in the same task: `README.md` route table and provisioning section, `api/ENDPOINTS.md`, `PLAN.md`.
- Go: `gofmt` clean on touched files, `go vet ./...` clean. Four files are already not gofmt-clean on `main`: `internal/domain/mfa/totp/model.go`, `internal/domain/session/model.go`, `internal/domain/session/service_test.go`, `internal/geoupdater/updater_test.go`. Leave them alone.
- Commit messages carry no attribution trailer of any kind.

## Review Focus

1. **A client whose owner is mid-deletion, disabled or gone.** The route must report `active: false`, and the token endpoint must refuse a developer-page client of that owner, because the deletion lock does not touch OAuth clients. Covered by Task 2 `TestDescribeIsInactiveWhenTheOwnerCannotBeActedFor`, Task 3 `TestTheOAuthClientIsInactiveWhenItsOwnerCannotSignIn`, and Task 6 `TestClientCredentialsRefusesADeveloperClientWhoseOwnerIsNotActive`.
2. **A developer-page client that also holds a user scope, or an internal one, or asks for a scope it does not hold.** The whole request must be refused with an error naming the scope, never a token with the m2m subset. Covered by Task 6 `TestClientCredentialsRefusesADeveloperClientHoldingAUserScope`, `TestClientCredentialsNeverGivesADeveloperClientAnInternalScope` and `TestClientCredentialsRefusesADeveloperClientAskingForAnUngrantedScope`.
3. **First-party clients after the change.** Wallet, billing and every createclient client must get exactly what they got before: silent clamping, internal scopes, the same audiences. Covered by Task 6 `TestClientCredentialsFirstPartyIsUnchanged` and the existing `TestClientCredentials*` in `kyc_test.go`.
4. **A leak through the route's response.** The body must hold exactly four keys and never the secret hash, even when the row has redirect URIs, an audience and `first_party`. Covered by Task 3 `TestTheOAuthClientReadAnswersTheContractAndNeverTheSecret`.
5. **A manifest marking an internal scope `m2m`, or an old manifest republished.** The first must be rejected at publish. The second must keep its hash, so nothing changes. Covered by Task 5 `TestValidateManifestRejectsM2MOnAnInternalScope` and `TestManifestHashIsStableWithoutM2M`.

Also covered in the owning tasks: scope stacking both ways (Task 3), an over-long `client_id` → 404 (Tasks 2–3), and a re-run or misdirected grant (Task 4).

---

## API tasks (`api/`)

All Go commands run from `api/`.

### Task 1: The `internal:account:oauth-client-read` scope

**Files:**
- Modify: `api/internal/scopes/catalog.go` (after `InternalAccountUserOrganizations`, ~line 81)
- Modify: `api/internal/scopes/account-scope-manifest.json` (append after the `internal:account:user-organizations` entry, ~line 212)
- Test: `api/internal/scopes/catalog_test.go:27-33` and `:58-64`

**Interfaces:**
- Consumes: nothing.
- Produces: `scopes.InternalAccountOAuthClientRead = "internal:account:oauth-client-read"` (untyped string const), published in the embedded Account manifest (visibility `internal`, status `active`). Tasks 3 and 4 use it.

- [ ] **Step 1: Write the failing test**

In `api/internal/scopes/catalog_test.go`, add the constant to both lists.

In `TestEveryInternalAccountScopeIsInTheManifest`:

```go
	for _, scope := range []string{
		InternalAccountKYC,
		InternalAccountScopeRegistryWrite,
		InternalAccountCompanyActor,
		InternalAccountOrgMember,
		InternalAccountUserOrganizations,
		InternalAccountOAuthClientRead,
	} {
```

In `TestEveryInternalManifestScopeHasAConstant`:

```go
	declared := map[string]bool{
		InternalAccountKYC:                true,
		InternalAccountScopeRegistryWrite: true,
		InternalAccountCompanyActor:       true,
		InternalAccountOrgMember:          true,
		InternalAccountUserOrganizations:  true,
		InternalAccountOAuthClientRead:    true,
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scopes/ -run 'TestEveryInternal' -v`
Expected: FAIL, build error `undefined: InternalAccountOAuthClientRead`.

- [ ] **Step 3: Add the constant**

In `api/internal/scopes/catalog.go`, directly after `InternalAccountUserOrganizations`:

```go
// InternalAccountOAuthClientRead lets a product read one OAuth client's owner,
// granted scopes and whether it is active, never its secret, so it can check
// that the person connecting a client to the product owns it (ctech-billing
// console self-service spec § 6.3, D5).
//
// Its own scope: reading any client by id is not a membership question, and a
// client that resolves memberships must not gain it by sharing a grant.
const InternalAccountOAuthClientRead = "internal:account:oauth-client-read"
```

- [ ] **Step 4: Run test to verify the manifest check now fails**

Run: `go test ./internal/scopes/ -run 'TestEveryInternal' -v`
Expected: FAIL, `"internal:account:oauth-client-read" is declared and not in the manifest; no client can be granted it`.

- [ ] **Step 5: Publish it in the manifest**

In `api/internal/scopes/account-scope-manifest.json`, add a comma after the closing `}` of the `internal:account:user-organizations` entry, then append before the closing `]`:

```json
    {
      "name": "internal:account:oauth-client-read",
      "descriptions": {
        "en": "Read an OAuth client's owner, granted scopes and status, never its secret.",
        "pt-BR": "Consultar o dono, os escopos concedidos e o estado de um cliente OAuth, nunca o segredo."
      },
      "visibility": "internal",
      "status": "active"
    }
```

- [ ] **Step 6: Run the scope package tests**

Run: `go test ./internal/scopes/ -v`
Expected: PASS, including `TestEveryInternalAccountScopeIsInTheManifest`, `TestEveryInternalManifestScopeHasAConstant`, and the manifest-validation tests in `registry_test.go`. The embedded manifest still passes `ValidateManifest`.

- [ ] **Step 7: Commit**

```bash
git add api/internal/scopes/catalog.go api/internal/scopes/account-scope-manifest.json api/internal/scopes/catalog_test.go
git commit -m "feat(scopes): internal:account:oauth-client-read for reading one OAuth client"
```

---

### Task 2: `InternalService.Describe` and `user.Service.IsActive`

**Files:**
- Create: `api/internal/domain/oauth/client/internal_view.go`
- Create: `api/internal/domain/oauth/client/internal_view_test.go`
- Modify: `api/internal/domain/user/service.go` (new method after `GetByID`, ~line 165)
- Test: `api/internal/domain/user/service_test.go` (append)

**Interfaces:**
- Consumes: `client.Repository.GetByID(ctx, clientID) (*OAuthClient, error)`, `client.ErrNotFound`, `client.SystemOwnerUserID`, `client.maxOperatorClientIDLength` (all existing).
- Produces:
  - `func (s *user.Service) IsActive(ctx context.Context, userID string) (bool, error)`. An unknown user returns `(false, nil)`.
  - `type client.OwnerStatus interface { IsActive(ctx context.Context, userID string) (bool, error) }`
  - `type client.InternalView struct { ClientID string; OwnerUserID string; AllowedScopes []string; Active bool }`
  - `func client.NewInternalService(repo Repository, owners OwnerStatus) *InternalService`
  - `func (s *InternalService) Describe(ctx context.Context, clientID string) (*InternalView, error)`. Returns `client.ErrNotFound` for an unknown, empty or over-long id. `AllowedScopes` is never nil.
  - `func (s *InternalService) Active(ctx context.Context, c *OAuthClient) (bool, error)`: the derivation from P1, exported for Task 6.

- [ ] **Step 1: Write the failing user test**

Append to `api/internal/domain/user/service_test.go`:

```go
// IsActive is the owner check behind the internal OAuth client route: a client
// whose owner cannot sign in must not be accepted as anyone's credential.
func TestIsActive(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	ctx := context.Background()
	u, err := svc.Register(ctx, "active@example.com", "password123", "Ana", "Lima")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if ok, err := svc.IsActive(ctx, u.ID()); err != nil || !ok {
		t.Fatalf("a fresh account: IsActive = %v, %v; want true, nil", ok, err)
	}

	repo.byID[u.ID()].DeletionState = user.DeletionStatePending
	if ok, err := svc.IsActive(ctx, u.ID()); err != nil || ok {
		t.Fatalf("an account locked for deletion: IsActive = %v, %v; want false, nil", ok, err)
	}

	repo.byID[u.ID()].DeletionState = ""
	repo.byID[u.ID()].IsEnabled = false
	if ok, err := svc.IsActive(ctx, u.ID()); err != nil || ok {
		t.Fatalf("a disabled account: IsActive = %v, %v; want false, nil", ok, err)
	}

	// Unknown is an answer, not an outage: the caller read the id from another row.
	if ok, err := svc.IsActive(ctx, "no-such-user"); err != nil || ok {
		t.Fatalf("an unknown account: IsActive = %v, %v; want false, nil", ok, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/domain/user/ -run TestIsActive -v`
Expected: FAIL, build error `svc.IsActive undefined (type *user.Service has no field or method IsActive)`.

- [ ] **Step 3: Implement `IsActive`**

In `api/internal/domain/user/service.go`, directly after `GetByID`:

```go
// IsActive reports whether the account can still be acted for: it exists, is
// enabled and is not locked by a deletion request. These are the same gates
// Login applies after the password. An unknown user is (false, nil), never an
// error: the caller holds an owner id read from another row, and a missing
// owner is an answer, not an outage.
func (s *Service) IsActive(ctx context.Context, userID string) (bool, error) {
	u, err := s.repo.GetByID(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("loading user: %w", err)
	}
	return u.IsEnabled && u.DeletionState == "", nil
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/domain/user/ -run TestIsActive -v`
Expected: PASS.

- [ ] **Step 5: Write the failing client-domain tests**

Create `api/internal/domain/oauth/client/internal_view_test.go`:

```go
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
```

- [ ] **Step 6: Run them to verify they fail**

Run: `go test ./internal/domain/oauth/client/ -run TestDescribe -v`
Expected: FAIL, build error `undefined: InternalService` / `undefined: NewInternalService`.

- [ ] **Step 7: Implement `InternalService`**

Create `api/internal/domain/oauth/client/internal_view.go`:

```go
package client

import (
	"context"
	"errors"
	"fmt"
)

// OwnerStatus answers whether a client's owner account can still be acted for.
// user.Service satisfies it (IsActive). The interface keeps this package free
// of the user domain.
type OwnerStatus interface {
	IsActive(ctx context.Context, userID string) (bool, error)
}

// InternalView is everything another service may know about a client. It never
// holds the secret hash, the redirect URIs, the audience, first-party status
// or the managed resource.
type InternalView struct {
	ClientID      string
	OwnerUserID   string
	AllowedScopes []string
	Active        bool
}

// InternalService serves the service-to-service read of one client
// (GET /v1.0/internal/oauth-clients/:client_id; ctech-billing console
// self-service spec § 6.3).
type InternalService struct {
	repo   Repository
	owners OwnerStatus
}

func NewInternalService(repo Repository, owners OwnerStatus) *InternalService {
	return &InternalService{repo: repo, owners: owners}
}

// Describe returns the client's owner, granted scopes and whether it is
// active. ErrNotFound covers an unknown client and an id no creation path can
// produce (empty or longer than maxOperatorClientIDLength); the second kind is
// refused without a read.
//
// Active means the client can still authenticate as itself for a live owner:
// it is confidential with a secret, and its owner is the platform
// (SystemOwnerUserID) or an account that exists, is enabled and is not locked
// by a deletion request. Account deletion does not touch OAuth clients, so
// without the owner check a locked person's client would still look usable.
func (s *InternalService) Describe(ctx context.Context, clientID string) (*InternalView, error) {
	if clientID == "" || len(clientID) > maxOperatorClientIDLength {
		return nil, ErrNotFound
	}
	c, err := s.repo.GetByID(ctx, clientID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading client: %w", err)
	}
	active, err := s.Active(ctx, c)
	if err != nil {
		return nil, err
	}
	return &InternalView{
		ClientID:      c.ID(),
		OwnerUserID:   c.OwnerUserID,
		AllowedScopes: append([]string{}, c.AllowedScopes...),
		Active:        active,
	}, nil
}

// Active is the derivation Describe reports, exported because the token
// endpoint applies the same rule to developer-page clients asking for
// client_credentials (Task 6). One rule, so the route and the token endpoint
// cannot disagree about whether a client is usable.
func (s *InternalService) Active(ctx context.Context, c *OAuthClient) (bool, error) {
	if c.IsPublic() || c.ClientSecretHash == "" {
		return false, nil
	}
	switch c.OwnerUserID {
	case SystemOwnerUserID:
		return true, nil
	case "":
		return false, nil
	}
	ok, err := s.owners.IsActive(ctx, c.OwnerUserID)
	if err != nil {
		return false, fmt.Errorf("checking client owner: %w", err)
	}
	return ok, nil
}
```

- [ ] **Step 8: Run the domain tests**

Run: `go test ./internal/domain/oauth/client/ ./internal/domain/user/ -v`
Expected: PASS, all `TestDescribe*`, `TestIsActive`, and every existing test in both packages.

- [ ] **Step 9: Commit**

```bash
git add api/internal/domain/oauth/client/internal_view.go api/internal/domain/oauth/client/internal_view_test.go api/internal/domain/user/service.go api/internal/domain/user/service_test.go
git commit -m "feat(oauth): describe one client for services: owner, scopes and a derived active"
```

---

### Task 3: The route `GET /v1.0/internal/oauth-clients/:client_id`

**Files:**
- Create: `api/internal/handler/oauth_client_internal.go`
- Create: `api/internal/handler/oauth_client_internal_test.go`
- Modify: `api/cmd/api/main.go:527-529` (mount next to `orgH.RegisterInternal`)
- Modify: `README.md` (route table, after the `/v1.0/internal/users/:user_id/organizations` row, ~line 132)
- Modify: `api/ENDPOINTS.md` (new section after `## Internal — GET /v1.0/internal/kyc/:user_id`, ~line 398)

**Interfaces:**
- Consumes: `client.NewInternalService(repo, owners)`, `(*InternalService).Describe`, `client.ErrNotFound` (Task 2); `scopes.InternalAccountOAuthClientRead` (Task 1); `user.Service` as `client.OwnerStatus` (Task 2); `middleware.RequireAuth`, `middleware.RequireInternalScope`, `apierror.NotFound`, `apierror.ServerError` (existing). Test helpers (existing): `newTestApp`, `(*testApp).doWithToken`, `issueServiceToken`, `issueToken`, `registerUser`, `ta.clientRepo` (`*memClientRepo`), `ta.userRepo.byID`, `newInternalMembershipApp`, `(*companyTestApp).do`, `seedOrg`.
- Produces: `handler.NewOAuthClientInternalHandler(svc *oauthclient.InternalService) *OAuthClientInternalHandler` and `(*OAuthClientInternalHandler).RegisterInternal(v1 fiber.Router, auth, scope fiber.Handler)`. The HTTP contract is in Global Constraints.

- [ ] **Step 1: Write the failing integration tests**

Create `api/internal/handler/oauth_client_internal_test.go`:

```go
package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

// newOAuthClientInternalApp mounts the route the way main.go does, with the
// same guards, so the tests exercise the gate and not just the handler.
func newOAuthClientInternalApp(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	handler.NewOAuthClientInternalHandler(oauthclient.NewInternalService(ta.clientRepo, ta.userSvc)).
		RegisterInternal(ta.app.Group("/v1.0"),
			middleware.RequireAuth(ta.jwtSvc),
			middleware.RequireInternalScope(scopes.InternalAccountOAuthClientRead))
	return ta
}

func seedInternalClient(t *testing.T, ta *testApp, c *oauthclient.OAuthClient) {
	t.Helper()
	if err := ta.clientRepo.Create(context.Background(), c); err != nil {
		t.Fatalf("seeding client: %v", err)
	}
}

func merchantOAuthClient(id, owner string) *oauthclient.OAuthClient {
	return &oauthclient.OAuthClient{
		PK:               oauthclient.BuildPK(id),
		Name:             "Loja da Ana",
		ClientType:       oauthclient.TypeConfidential,
		ClientSecretHash: "hash-must-not-leak",
		RedirectURIs:     []string{"https://loja.example.com/callback"},
		AllowedScopes:    []string{"billing:invoices:write", "billing:customers:read"},
		Audience:         []string{"https://billing.aoctech.app"},
		OwnerUserID:      owner,
	}
}

const internalClientPath = "/v1.0/internal/oauth-clients/"

func TestTheOAuthClientReadNeedsItsOwnInternalScope(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	owner := ta.registerUser(t, "client-gate@example.com", "Sup3rSecret!pass", "Ana")
	seedInternalClient(t, ta, merchantOAuthClient("cli_gate", owner.ID()))
	path := internalClientPath + "cli_gate"

	// A signed-in person's token never reaches a service route, whatever it carries.
	if resp := ta.doWithToken(http.MethodGet, path, nil, ta.issueToken(t, owner.ID())); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a user token read a client: status %d", resp.StatusCode)
	}
	for name, scope := range map[string]string{
		"org-member":         scopes.InternalAccountOrgMember,
		"user-organizations": scopes.InternalAccountUserOrganizations,
		"company-actor":      scopes.InternalAccountCompanyActor,
		"kyc":                scopes.InternalAccountKYC,
	} {
		tok := ta.issueServiceToken(t, []string{scope})
		if resp := ta.doWithToken(http.MethodGet, path, nil, tok); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("the %s scope read a client: status %d", name, resp.StatusCode)
		}
	}
	ok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})
	if resp := ta.doWithToken(http.MethodGet, path, nil, ok); resp.StatusCode != http.StatusOK {
		t.Fatalf("the right scope: status %d, want 200", resp.StatusCode)
	}
}

func TestTheOAuthClientReadAnswersTheContractAndNeverTheSecret(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	owner := ta.registerUser(t, "client-contract@example.com", "Sup3rSecret!pass", "Ana")
	seedInternalClient(t, ta, merchantOAuthClient("cli_contract", owner.ID()))
	tok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})

	resp := ta.doWithToken(http.MethodGet, internalClientPath+"cli_contract", nil, tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw := bodyString(resp)
	if strings.Contains(raw, "hash-must-not-leak") || strings.Contains(raw, "secret") {
		t.Fatalf("the response carries secret material: %s", raw)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	if len(body) != 4 {
		t.Fatalf("got keys %v, want exactly client_id, owner_user_id, allowed_scopes, active", body)
	}
	if body["client_id"] != "cli_contract" || body["owner_user_id"] != owner.ID() || body["active"] != true {
		t.Fatalf("got %+v", body)
	}
	got, _ := body["allowed_scopes"].([]any)
	if len(got) != 2 || got[0] != "billing:invoices:write" || got[1] != "billing:customers:read" {
		t.Fatalf("allowed_scopes = %v", body["allowed_scopes"])
	}
}

func TestAnUnknownOAuthClientIs404(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	tok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})
	for _, id := range []string{"cli_missing", strings.Repeat("a", 200)} {
		resp := ta.doWithToken(http.MethodGet, internalClientPath+id, nil, tok)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("id of length %d: status %d, want 404", len(id), resp.StatusCode)
		}
	}
}

func TestTheOAuthClientIsInactiveWhenItsOwnerCannotSignIn(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	ctx := context.Background()
	tok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})

	leaving := ta.registerUser(t, "client-leaving@example.com", "Sup3rSecret!pass", "Bia")
	if err := ta.userSvc.MarkPendingDeletion(ctx, leaving.ID(), "req_1"); err != nil {
		t.Fatalf("MarkPendingDeletion: %v", err)
	}
	disabled := ta.registerUser(t, "client-disabled@example.com", "Sup3rSecret!pass", "Caio")
	ta.userRepo.byID[disabled.ID()].IsEnabled = false
	live := ta.registerUser(t, "client-live@example.com", "Sup3rSecret!pass", "Dora")
	public := merchantOAuthClient("cli_public", live.ID())
	public.ClientType, public.ClientSecretHash = oauthclient.TypePublic, ""

	seedInternalClient(t, ta, merchantOAuthClient("cli_leaving", leaving.ID()))
	seedInternalClient(t, ta, merchantOAuthClient("cli_disabled", disabled.ID()))
	seedInternalClient(t, ta, merchantOAuthClient("cli_system", oauthclient.SystemOwnerUserID))
	seedInternalClient(t, ta, public)

	for id, want := range map[string]bool{
		"cli_leaving":  false,
		"cli_disabled": false,
		"cli_public":   false,
		"cli_system":   true,
	} {
		resp := ta.doWithToken(http.MethodGet, internalClientPath+id, nil, tok)
		var body struct {
			Active bool `json:"active"`
		}
		readJSON(t, resp, &body)
		if body.Active != want {
			t.Fatalf("%s: active = %v, want %v", id, body.Active, want)
		}
	}
}

// The reverse gate: holding oauth-client-read must not answer membership or
// enumeration questions just because both routes live under /internal.
func TestTheOAuthClientScopeOpensNoOtherInternalRoute(t *testing.T) {
	a := newInternalMembershipApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "client-scope-sep@example.com")
	tok := a.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})
	for _, path := range []string{
		"/v1.0/internal/organizations/" + orgID + "/members/" + ownerID,
		"/v1.0/internal/users/" + ownerID + "/organizations",
	} {
		if resp := a.do(t, http.MethodGet, path, tok, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s answered the oauth-client-read scope: status %d", path, resp.StatusCode)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/handler/ -run 'TestTheOAuthClient|TestAnUnknownOAuthClient' -v`
Expected: FAIL, build error `undefined: handler.NewOAuthClientInternalHandler`.

- [ ] **Step 3: Implement the handler**

Create `api/internal/handler/oauth_client_internal.go`:

```go
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
)

// OAuthClientInternalHandler serves the service-to-service read of one OAuth
// client. Its first consumer is ctech-billing, which checks that the person
// connecting a client as a merchant credential owns it (console self-service
// spec § 6.3, D5).
type OAuthClientInternalHandler struct {
	svc *oauthclient.InternalService
}

func NewOAuthClientInternalHandler(svc *oauthclient.InternalService) *OAuthClientInternalHandler {
	return &OAuthClientInternalHandler{svc: svc}
}

// RegisterInternal mounts
//
//   - GET /internal/oauth-clients/:client_id
//     behind auth = RequireAuth and
//     scope = RequireInternalScope(scopes.InternalAccountOAuthClientRead)
//
// The guards are attached to the route, never to an /internal prefix group, so
// they cannot stack with another internal route's.
func (h *OAuthClientInternalHandler) RegisterInternal(v1 fiber.Router, auth, scope fiber.Handler) {
	v1.Get("/internal/oauth-clients/:client_id", auth, scope, h.get)
}

// internalOAuthClientResponse is the whole contract. It is a struct, not a
// map, so a new field shows up as a diff here, and the secret hash, redirect
// URIs, audience and first-party status cannot leak through a careless copy
// of the model.
type internalOAuthClientResponse struct {
	ClientID      string   `json:"client_id"`
	OwnerUserID   string   `json:"owner_user_id"`
	AllowedScopes []string `json:"allowed_scopes"`
	Active        bool     `json:"active"`
}

func (h *OAuthClientInternalHandler) get(c fiber.Ctx) error {
	view, err := h.svc.Describe(c.Context(), c.Params("client_id"))
	if errors.Is(err, oauthclient.ErrNotFound) {
		return apierror.NotFound("OAuth client not found.", c.Path()).Send(c)
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	return c.JSON(internalOAuthClientResponse{
		ClientID:      view.ClientID,
		OwnerUserID:   view.OwnerUserID,
		AllowedScopes: view.AllowedScopes,
		Active:        view.Active,
	})
}
```

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./internal/handler/ -run 'TestTheOAuthClient|TestAnUnknownOAuthClient' -v`
Expected: PASS, all five tests.

- [ ] **Step 5: Mount the route in `main.go`**

In `api/cmd/api/main.go`, directly after the `orgH.RegisterInternal(...)` call (lines 527-529) and before `scopeRegistryH.Register(...)`:

```go
	handler.NewOAuthClientInternalHandler(oauthclientDomain.NewInternalService(oauthClientRepo, userSvc)).
		RegisterInternal(v1, middleware.RequireAuth(jwtSvc), middleware.RequireInternalScope(scopesPkg.InternalAccountOAuthClientRead))
```

`oauthClientRepo` (already used by `NewHandoffHandler`) and `userSvc` are both in scope at that point. `oauthclientDomain` is the existing import alias.

- [ ] **Step 6: Build and run the whole API test suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build and vet print nothing; `ok` for every package, no `FAIL`.

- [ ] **Step 7: Document the route in `README.md`**

In the route table, directly after the `/v1.0/internal/users/:user_id/organizations` row:

```markdown
| `GET`    | `/v1.0/internal/oauth-clients/:client_id` | Service token (`internal:account:oauth-client-read`) | One OAuth client for a product that must know who owns it: `200 {"client_id","owner_user_id","allowed_scopes","active"}` — never the secret, redirect URIs, audience or first-party status. `active` is derived (there is no status field): the client is confidential with a secret **and** its owner is `system` or an account that is enabled and not locked by a deletion request. Unknown client (or an id longer than 128 characters) → `404`. First consumer: ctech-billing, which admits a merchant's client as an API credential only when the person connecting it is `owner_user_id` (billing console self-service spec § 6.3). |
```

- [ ] **Step 8: Document the route in `api/ENDPOINTS.md`**

After the `## Internal — GET /v1.0/internal/kyc/:user_id` section (before its trailing `---`), add:

```markdown
## Internal — `GET /v1.0/internal/oauth-clients/:client_id`  *(M2M; `RequireAuth` + `RequireInternalScope(internal:account:oauth-client-read)`)*
Service-to-service read used by **ctech-billing** to verify who owns an OAuth
client before admitting it as a merchant credential (`oauth_client_internal.go`).
Requires a machine token (empty `sid`) carrying `internal:account:oauth-client-read`;
any other internal scope is `403`. Returns exactly
`{ client_id, owner_user_id, allowed_scopes, active }` — `allowed_scopes` is `[]`
when empty. `active` = confidential with a secret hash **and** owner is `system`
or an existing, enabled account with no `deletion_state`
(`domain/oauth/client/internal_view.go`). Unknown client, empty id or an id over
128 characters → `404` problem (the last without a DynamoDB read). The secret hash,
redirect URIs, audience, `first_party` and `managed_resource_id` are never returned.
```

- [ ] **Step 9: Commit**

```bash
git add api/internal/handler/oauth_client_internal.go api/internal/handler/oauth_client_internal_test.go api/cmd/api/main.go README.md api/ENDPOINTS.md
git commit -m "feat(internal): read one OAuth client for products (internal:account:oauth-client-read)"
```

---

### Task 4: Granting a scope to an existing M2M client (`createclient -grant`)

**Files:**
- Modify: `api/internal/domain/oauth/client/operator.go` (extract `validateM2MScopes` and `requireRegistered`; add `GrantM2MScopes` and two errors)
- Test: `api/internal/domain/oauth/client/operator_test.go` (append)
- Modify: `api/cmd/createclient/main.go` (`-grant` flag, `validateGrantFlags`, `formatGrantResult`, `main` branches)
- Test: `api/cmd/createclient/main_test.go` (append)
- Modify: `README.md` (§ "Provisioning confidential M2M clients", ~line 381)
- Modify: `PLAN.md` (new section before `## Pending Decisions`)

**Interfaces:**
- Consumes: `OperatorService` (`repo Repository`, `catalog OperatorScopeCatalog`), `ValidateM2MInput`, `ErrScopesRequired`, `ErrOIDCScopeForM2M`, `ErrDuplicateScope`, `ErrInvalidScope`, `ErrNotFound`, `operatorRepoStub`, `operatorCatalogStub` (existing); `scopes.InternalAccountOAuthClientRead` (Task 1).
- Produces:
  - `var ErrNotM2MClient error` and `var ErrPublisherClient error`
  - `func (s *OperatorService) GrantM2MScopes(ctx context.Context, clientID string, scopesToAdd []string) (*OAuthClient, []string, error)`. The second return is the scopes actually added, in request order, and is nil when nothing changed.
  - CLI: `go run ./cmd/createclient -grant -client-id <id> -scopes <a,b>` prints `client_id: <id>` then `granted: <a,b>` or `no change: every scope was already granted`.

- [ ] **Step 1: Write the failing operator tests**

Append to `api/internal/domain/oauth/client/operator_test.go`:

```go
// updateCountingRepo proves a no-op grant writes nothing.
type updateCountingRepo struct {
	*operatorRepoStub
	updates int
}

func (r *updateCountingRepo) Update(ctx context.Context, id string, updates map[string]any) error {
	r.updates++
	return r.operatorRepoStub.Update(ctx, id, updates)
}

func newGrantOperatorService(t *testing.T) (*OperatorService, *updateCountingRepo) {
	t.Helper()
	repo := &updateCountingRepo{operatorRepoStub: &operatorRepoStub{clients: make(map[string]*OAuthClient)}}
	catalog := operatorCatalogStub{services: []scopes.ServiceScopes{{
		Service: "internal:account", Internal: true,
		Scopes: []scopes.ScopeEntry{
			{Scope: scopes.InternalAccountOrgMember},
			{Scope: scopes.InternalAccountUserOrganizations},
			{Scope: scopes.InternalAccountOAuthClientRead},
		},
	}}}
	service := NewOperatorService(repo, catalog)
	if _, _, err := service.CreateM2M(context.Background(), "billing-account", "Billing",
		[]string{scopes.InternalAccountOrgMember, scopes.InternalAccountUserOrganizations}); err != nil {
		t.Fatalf("CreateM2M: %v", err)
	}
	return service, repo
}

func TestGrantM2MScopesAddsOnlyWhatIsMissingAndKeepsTheRest(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	client, added, err := service.GrantM2MScopes(context.Background(), "billing-account",
		[]string{scopes.InternalAccountOrgMember, scopes.InternalAccountOAuthClientRead})
	if err != nil {
		t.Fatalf("GrantM2MScopes: %v", err)
	}
	if len(added) != 1 || added[0] != scopes.InternalAccountOAuthClientRead {
		t.Fatalf("added = %v, want only the new scope", added)
	}
	want := []string{scopes.InternalAccountOrgMember, scopes.InternalAccountUserOrganizations, scopes.InternalAccountOAuthClientRead}
	stored := repo.clients["billing-account"].AllowedScopes
	if !slices.Equal(client.AllowedScopes, want) || !slices.Equal(stored, want) {
		t.Fatalf("returned %v, stored %v, want %v", client.AllowedScopes, stored, want)
	}
}

func TestGrantM2MScopesIsIdempotent(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	ctx := context.Background()
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", []string{scopes.InternalAccountOAuthClientRead}); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	before := repo.updates
	_, added, err := service.GrantM2MScopes(ctx, "billing-account", []string{scopes.InternalAccountOAuthClientRead})
	if err != nil {
		t.Fatalf("second grant: %v", err)
	}
	if added != nil || repo.updates != before {
		t.Fatalf("a repeated grant added %v and wrote %d times, want nothing", added, repo.updates-before)
	}
}

// A self-service client must never be handed an internal scope: that is a
// third party holding a service-to-service grant.
func TestGrantM2MScopesRefusesSelfServiceAndPublicClients(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	repo.clients["cli_merchant"] = &OAuthClient{PK: BuildPK("cli_merchant"), ClientType: TypeConfidential, OwnerUserID: "usr_1", AllowedScopes: []string{}}
	repo.clients["cli_spa"] = &OAuthClient{PK: BuildPK("cli_spa"), ClientType: TypePublic, FirstParty: true, AllowedScopes: []string{}}
	for _, id := range []string{"cli_merchant", "cli_spa"} {
		_, _, err := service.GrantM2MScopes(context.Background(), id, []string{scopes.InternalAccountOAuthClientRead})
		if !errors.Is(err, ErrNotM2MClient) {
			t.Fatalf("%s: err = %v, want ErrNotM2MClient", id, err)
		}
		if len(repo.clients[id].AllowedScopes) != 0 {
			t.Fatalf("%s was changed: %v", id, repo.clients[id].AllowedScopes)
		}
	}
}

func TestGrantM2MScopesRefusesAPublisher(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	repo.clients["dfe-publisher"] = &OAuthClient{
		PK: BuildPK("dfe-publisher"), ClientType: TypeConfidential, FirstParty: true,
		ManagedResourceID: "dfe", AllowedScopes: []string{scopes.InternalAccountScopeRegistryWrite},
	}
	_, _, err := service.GrantM2MScopes(context.Background(), "dfe-publisher", []string{scopes.InternalAccountOAuthClientRead})
	if !errors.Is(err, ErrPublisherClient) {
		t.Fatalf("err = %v, want ErrPublisherClient", err)
	}
}

func TestGrantM2MScopesRejectsBadInput(t *testing.T) {
	service, _ := newGrantOperatorService(t)
	ctx := context.Background()
	if _, _, err := service.GrantM2MScopes(ctx, "no-such-client", []string{scopes.InternalAccountOAuthClientRead}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown client: err = %v, want ErrNotFound", err)
	}
	var scopeErr ErrInvalidScope
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", []string{"internal:account:not-published"}); !errors.As(err, &scopeErr) {
		t.Fatalf("unpublished scope: err = %v, want ErrInvalidScope", err)
	}
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", []string{"openid"}); !errors.Is(err, ErrOIDCScopeForM2M) {
		t.Fatalf("OIDC scope: err = %v, want ErrOIDCScopeForM2M", err)
	}
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", nil); !errors.Is(err, ErrScopesRequired) {
		t.Fatalf("no scopes: err = %v, want ErrScopesRequired", err)
	}
}
```

Add `"slices"` to the test file's import block.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/domain/oauth/client/ -run TestGrantM2MScopes -v`
Expected: FAIL, build error `service.GrantM2MScopes undefined` / `undefined: ErrNotM2MClient`.

- [ ] **Step 3: Extract the shared validation in `operator.go`**

Add the two errors to the existing `var (...)` block:

```go
	ErrNotM2MClient    = errors.New("client is not a confidential first-party machine-to-machine client")
	ErrPublisherClient = errors.New("a scope-registry publisher is bound to its single scope and cannot be granted others")
```

Replace the scope loop at the end of `ValidateM2MInput` with a call to a new helper. The function becomes:

```go
func ValidateM2MInput(clientID, name string, allowedScopes []string) error {
	clientID = strings.TrimSpace(clientID)
	name = strings.TrimSpace(name)

	if len(clientID) < minOperatorClientIDLength || len(clientID) > maxOperatorClientIDLength || !operatorClientIDPattern.MatchString(clientID) {
		return ErrInvalidClientID
	}
	if name == "" || len(name) > maxOperatorClientNameLen {
		return ErrInvalidClientName
	}
	return validateM2MScopes(allowedScopes)
}

// validateM2MScopes is the scope half of ValidateM2MInput, shared with
// GrantM2MScopes: grammar, no OIDC identity scopes, no duplicates.
func validateM2MScopes(allowedScopes []string) error {
	if len(allowedScopes) == 0 {
		return ErrScopesRequired
	}
	seen := make(map[string]struct{}, len(allowedScopes))
	for _, scope := range allowedScopes {
		if !scopes.IsValid(scope) {
			return ErrInvalidScope{Scope: scope}
		}
		if scopes.IsOIDC(scope) {
			return fmt.Errorf("%w: %q", ErrOIDCScopeForM2M, scope)
		}
		if _, exists := seen[scope]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateScope, scope)
		}
		seen[scope] = struct{}{}
	}
	return nil
}
```

In `createM2M`, replace the block from `catalog, err := s.catalog.Catalog(ctx)` through the end of the `for _, scope := range allowedScopes { ... }` registration loop with:

```go
	if err := s.requireRegistered(ctx, allowedScopes); err != nil {
		return nil, "", err
	}
```

and add the helper:

```go
// requireRegistered refuses any scope that is not in the runtime catalog,
// internal ones included (operator paths may grant them; self-service never can).
func (s *OperatorService) requireRegistered(ctx context.Context, requested []string) error {
	catalog, err := s.catalog.Catalog(ctx)
	if err != nil {
		return fmt.Errorf("consulting scope catalog: %w", err)
	}
	registered := make(map[string]struct{})
	for _, service := range catalog {
		for _, entry := range service.Scopes {
			registered[entry.Scope] = struct{}{}
		}
	}
	for _, scope := range requested {
		if _, ok := registered[scope]; !ok {
			return ErrInvalidScope{Scope: scope}
		}
	}
	return nil
}
```

- [ ] **Step 4: Implement `GrantM2MScopes`**

Append to `operator.go`:

```go
// GrantM2MScopes adds scopes to an existing confidential first-party M2M
// client and returns the scopes it actually added, in request order. It only
// adds: existing grants are kept and nothing is removed. A call that adds
// nothing writes nothing, so a runbook can re-run it.
//
// A self-service client is refused (ErrNotM2MClient): granting one an
// internal scope would hand a third party a service-to-service grant. A
// scope-registry publisher is refused (ErrPublisherClient): it is bound to
// exactly one permission by design (CreateResourcePublisher).
func (s *OperatorService) GrantM2MScopes(ctx context.Context, clientID string, scopesToAdd []string) (*OAuthClient, []string, error) {
	clientID = strings.TrimSpace(clientID)
	if err := validateM2MScopes(scopesToAdd); err != nil {
		return nil, nil, err
	}
	if err := s.requireRegistered(ctx, scopesToAdd); err != nil {
		return nil, nil, err
	}

	client, err := s.repo.GetByID(ctx, clientID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading client %q: %w", clientID, err)
	}
	if client.IsPublic() || !client.FirstParty {
		return nil, nil, ErrNotM2MClient
	}
	if client.ManagedResourceID != "" {
		return nil, nil, ErrPublisherClient
	}

	allowed := append([]string(nil), client.AllowedScopes...)
	var added []string
	for _, scope := range scopesToAdd {
		if !slices.Contains(allowed, scope) {
			allowed = append(allowed, scope)
			added = append(added, scope)
		}
	}
	if len(added) == 0 {
		return client, nil, nil
	}
	if err := s.repo.Update(ctx, clientID, map[string]any{"allowed_scopes": allowed}); err != nil {
		return nil, nil, fmt.Errorf("updating client scopes: %w", err)
	}
	client.AllowedScopes = allowed
	return client, added, nil
}
```

`slices` and `strings` are already imported by `operator.go`.

- [ ] **Step 5: Run the operator tests**

Run: `go test ./internal/domain/oauth/client/ -v`
Expected: PASS. All `TestGrantM2MScopes*` pass. Every existing `TestOperator*` / `ValidateM2MInput` test also passes, which proves the extraction in Step 3 changed no behaviour. Task 2's `TestDescribe*` passes too.

- [ ] **Step 6: Commit the domain half**

```bash
git add api/internal/domain/oauth/client/operator.go api/internal/domain/oauth/client/operator_test.go
git commit -m "feat(oauth): GrantM2MScopes adds scopes to an existing M2M client"
```

- [ ] **Step 7: Write the failing CLI tests**

Append to `api/cmd/createclient/main_test.go`:

```go
// -grant changes no name and issues no secret, so an SSM path there would be a
// silent no-op an operator reads as "stored".
func TestValidateGrantFlagsRefusesCreateOnlyFlags(t *testing.T) {
	if err := validateGrantFlags("", "", ""); err != nil {
		t.Fatalf("bare -grant: %v", err)
	}
	for _, tc := range []struct{ name, client, secret string }{
		{"Billing", "", ""},
		{"", "/ctech-billing/dev/billing/account-client-id", ""},
		{"", "", "/ctech-billing/dev/billing/account-client-secret"},
	} {
		if err := validateGrantFlags(tc.name, tc.client, tc.secret); err == nil {
			t.Fatalf("validateGrantFlags(%q, %q, %q) = nil, want an error", tc.name, tc.client, tc.secret)
		}
	}
}

func TestFormatGrantResult(t *testing.T) {
	got := formatGrantResult("billing-account", []string{"internal:account:oauth-client-read"})
	want := "client_id: billing-account\ngranted: internal:account:oauth-client-read\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = formatGrantResult("billing-account", nil)
	want = "client_id: billing-account\nno change: every scope was already granted\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 8: Run them to verify they fail**

Run: `go test ./cmd/createclient/ -run 'TestValidateGrantFlags|TestFormatGrantResult' -v`
Expected: FAIL, build error `undefined: validateGrantFlags` / `undefined: formatGrantResult`.

- [ ] **Step 9: Implement `-grant` in `cmd/createclient/main.go`**

Update the package doc comment to add the grant form after the existing example:

```go
// Command createclient manually provisions a confidential first-party OAuth
// client for the client_credentials (machine-to-machine) grant.
//
//	AWS_REGION=... TABLE_PREFIX=production_ go run ./cmd/createclient \
//	  -client-id wallet-worker -name "Wallet worker" \
//	  -scopes internal:account:kyc
//
// The client secret is printed once after the DynamoDB write succeeds.
//
// With -grant it instead adds -scopes to an existing confidential first-party
// M2M client, keeping its current scopes and secret (re-runnable):
//
//	AWS_REGION=... TABLE_PREFIX=prod go run ./cmd/createclient -grant \
//	  -client-id <id> -scopes internal:account:oauth-client-read
```

Replace `main()` with:

```go
func main() {
	clientID := flag.String("client-id", "", "stable OAuth client_id (required)")
	name := flag.String("name", "", "human-readable client name (required unless -grant)")
	scopeList := flag.String("scopes", "", "comma-separated service scopes (required)")
	ssmPathClient := flag.String("ssm-path-client", "", "optional SSM path for the client_id (String)")
	ssmPathSecret := flag.String("ssm-path-secret", "", "optional SSM path for the client_secret (SecureString)")
	grant := flag.Bool("grant", false, "add -scopes to the existing M2M client -client-id instead of creating one")
	flag.Parse()

	if flag.NArg() != 0 {
		log.Fatalf("unexpected positional arguments: %s", strings.Join(flag.Args(), " "))
	}
	requestedScopes, err := parseScopes(*scopeList)
	if err != nil {
		log.Fatal(err)
	}
	if *grant {
		if strings.TrimSpace(*clientID) == "" {
			log.Fatal("invalid arguments: -client-id is required")
		}
		if err := validateGrantFlags(*name, *ssmPathClient, *ssmPathSecret); err != nil {
			log.Fatalf("invalid arguments: %v", err)
		}
	} else {
		if err := oauthclient.ValidateM2MInput(*clientID, *name, requestedScopes); err != nil {
			log.Fatalf("invalid arguments: %v", err)
		}
		if err := validateSSMPaths(*ssmPathClient, *ssmPathSecret); err != nil {
			log.Fatalf("invalid arguments: %v", err)
		}
	}

	tablePrefix := os.Getenv("TABLE_PREFIX")
	if tablePrefix == "" {
		tablePrefix = os.Getenv("ENVIRONMENT")
	}
	tablePrefix = strings.TrimSuffix(tablePrefix, "_")
	if tablePrefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}

	ctx := context.Background()
	db, err := database.New(ctx, os.Getenv("AWS_REGION"))
	if err != nil {
		log.Fatalf("dynamodb client: %v", err)
	}
	cacheClient, err := cache.New(os.Getenv("VALKEY_URL"))
	if err != nil {
		log.Fatalf("valkey client: %v", err)
	}
	catalog := scopes.NewCatalogService(scopes.NewRepository(db, tablePrefix), cacheClient)
	service := oauthclient.NewOperatorService(oauthclient.NewRepository(db, tablePrefix), catalog)

	if *grant {
		_, added, err := service.GrantM2MScopes(ctx, *clientID, requestedScopes)
		if err != nil {
			log.Fatalf("granting scopes: %v", err)
		}
		fmt.Print(formatGrantResult(*clientID, added))
		return
	}

	created, secret, err := service.CreateM2M(ctx, *clientID, *name, requestedScopes)
	if err != nil {
		log.Fatalf("creating M2M client: %v", err)
	}

	if *ssmPathClient != "" || *ssmPathSecret != "" {
		awsCfg, cfgErr := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(os.Getenv("AWS_REGION")))
		if cfgErr != nil {
			printCreatedCredentials(created.ID(), secret)
			log.Fatalf("client created, but loading AWS config for SSM failed: %v", cfgErr)
		}
		if storeErr := storeCredentials(ctx, ssm.NewFromConfig(awsCfg), *ssmPathClient, *ssmPathSecret, created.ID(), secret); storeErr != nil {
			printCreatedCredentials(created.ID(), secret)
			log.Fatalf("client created, but storing credentials in SSM failed: %v", storeErr)
		}
	}

	fmt.Printf("client_id: %s\n", created.ID())
	if *ssmPathClient != "" {
		fmt.Printf("client_id stored in SSM: %s\n", *ssmPathClient)
	}
	if *ssmPathSecret != "" {
		fmt.Printf("client_secret stored in SSM: %s\n", *ssmPathSecret)
	} else {
		fmt.Printf("client_secret: %s\n", secret)
		fmt.Println("Store client_secret securely; it cannot be recovered.")
	}
}
```

Then add the two helpers after `parseScopes`:

```go
// validateGrantFlags refuses the create-only flags in -grant mode: a grant
// changes no name and issues no secret, so an SSM path there would be a
// silent no-op an operator reads as "stored".
func validateGrantFlags(name, ssmPathClient, ssmPathSecret string) error {
	if name != "" || ssmPathClient != "" || ssmPathSecret != "" {
		return fmt.Errorf("-grant takes only -client-id and -scopes")
	}
	return nil
}

func formatGrantResult(clientID string, added []string) string {
	if len(added) == 0 {
		return fmt.Sprintf("client_id: %s\nno change: every scope was already granted\n", clientID)
	}
	return fmt.Sprintf("client_id: %s\ngranted: %s\n", clientID, strings.Join(added, ","))
}
```

- [ ] **Step 10: Run the CLI and operator tests, then the whole suite**

Run: `go test ./cmd/createclient/ -v && go build ./... && go vet ./... && go test ./...`
Expected: PASS everywhere, with `ok` for every package and no `FAIL`.

- [ ] **Step 11: Document the grant in `README.md`**

In § "Provisioning confidential M2M clients", after the paragraph that ends "...rather than configured on the client.", add:

````markdown
To add scopes to an **existing** M2M client (keeping its secret and current
scopes), run the same command with `-grant`. It only adds, it writes nothing
when every scope is already present (safe to re-run), and it refuses
self-service clients and scope-registry publishers:

```bash
AWS_REGION=us-east-1 TABLE_PREFIX=prod go run ./cmd/createclient -grant \
  -client-id <client_id> -scopes internal:account:oauth-client-read
# client_id: <client_id>
# granted: internal:account:oauth-client-read      (or: no change: every scope was already granted)
```

`-grant` takes only `-client-id` and `-scopes`. The scopes must already be in
the runtime catalog: after a deploy that publishes a new scope, run it once the
new revision has booted (log line `Account Resource Server ready (... changed=true ...)`).
With `VALKEY_URL` set, also wait up to 5 minutes for the catalog cache to expire.
Leave `VALKEY_URL` unset to read the table directly.

**ctech-billing's client** (the id at `/ctech-billing/{env}/billing/account-client-id`)
holds `internal:account:org-member`, `internal:account:user-organizations` and
`internal:account:oauth-client-read`, granted per environment as in
`docs/plans/2026-10-10-oauth-client-internal-route.md` § Deploy.
````

- [ ] **Step 12: Record it in `PLAN.md`**

Before `## Pending Decisions`, add:

```markdown
## Internal OAuth client route (ctech-billing console self-service)

Spec: `ctech-billing/docs/specs/2026-10-10-console-self-service.md` § 6.3. Plan: `docs/plans/2026-10-10-oauth-client-internal-route.md`.

- [ ] Scope `internal:account:oauth-client-read` in the Account manifest
- [ ] `GET /v1.0/internal/oauth-clients/:client_id` → `{client_id, owner_user_id, allowed_scopes, active}`, unknown → 404
- [ ] `createclient -grant` (add scopes to an existing M2M client)
- [ ] Deploy: scope granted to billing's client in dev, stage, prod
- [ ] `m2m` scope marker in the manifest; `client_credentials` for developer-page clients holding only `m2m` scopes with an active owner (first-party unchanged)
- [ ] ctech-billing marks its integration scopes `m2m` (billing part 3 plan), after this deploy
```

- [ ] **Step 13: Commit**

```bash
git add api/cmd/createclient/main.go api/cmd/createclient/main_test.go README.md PLAN.md
git commit -m "feat(createclient): -grant adds scopes to an existing M2M client"
```

---

### Task 5: The `m2m` scope marker (manifest, registry, catalog)

**Files:**
- Modify: `api/internal/scopes/resource.go` (`ScopeDefinition`, `ValidateManifest` visibility switch ~line 131)
- Modify: `api/internal/scopes/catalog.go` (`ScopeEntry`)
- Modify: `api/internal/scopes/service.go` (`mergeResourceServers` ~line 179; new `ValidateMachineGrantable` after `ValidateGrantable`)
- Test: `api/internal/scopes/registry_test.go` (append)
- Modify: `docs/resource-server-scope-registry.md` (new section "Machine-to-machine scopes (`m2m`)")

**Interfaces:**
- Consumes: `ScopeDefinition`, `ScopeEntry`, `ValidateManifest`, `ManifestHash`, `mergeResourceServers`, `CatalogService`, and the test stubs `registryRepoStub`, `catalogV2RepoStub`, `dfeManifest`, `publicScope` (all existing).
- Produces:
  - `ScopeDefinition.M2M bool` with tags `json:"m2m,omitempty" dynamodbav:"m2m,omitempty"`
  - `ScopeEntry.M2M bool` with tags `json:"m2m,omitempty" dynamodbav:"m2m,omitempty"`
  - `func (s *CatalogService) ValidateMachineGrantable(ctx context.Context, ss []string) (string, error)`. It returns the first scope in `ss` that is not an active, public, `m2m` catalog scope, or `""` when all are.

- [ ] **Step 1: Write the failing tests**

Append to `api/internal/scopes/registry_test.go`:

```go
func m2mScope(name string) ScopeDefinition {
	s := publicScope(name)
	s.M2M = true
	return s
}

// Internal scopes are machine-only already and never grantable to a
// developer-page client: marking one m2m would read as a permission that does
// not exist.
func TestValidateManifestRejectsM2MOnAnInternalScope(t *testing.T) {
	internal := ScopeDefinition{Scope: "internal:dfe:sync", Descriptions: map[string]string{"en": "x", "pt-BR": "x"}, Visibility: VisibilityInternal, Status: StatusActive, M2M: true}
	if err := ValidateManifest(dfeManifest(internal)); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("m2m on an internal scope: err = %v, want ErrInvalidResource", err)
	}
	if err := ValidateManifest(dfeManifest(m2mScope("dfe:nfes:read"))); err != nil {
		t.Fatalf("m2m on a public scope: %v", err)
	}
}

// Every manifest published before this field existed must keep its hash, or
// its next publish would advance a revision for nothing. Marking a scope m2m
// must change the hash, or the publish would be a silent no-op.
func TestManifestHashIsStableWithoutM2M(t *testing.T) {
	plain := dfeManifest(publicScope("dfe:nfes:read"))
	canonical, err := json.Marshal(canonicalManifest(plain))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), "m2m") {
		t.Fatalf("a manifest without m2m serializes the field: %s", canonical)
	}
	before, _ := ManifestHash(plain)
	after, _ := ManifestHash(dfeManifest(m2mScope("dfe:nfes:read")))
	if before == after {
		t.Fatal("marking a scope m2m did not change the manifest hash")
	}
}

func TestPublishingM2MReachesTheCatalog(t *testing.T) {
	repo := &registryRepoStub{}
	registry := NewRegistryService(repo, nil)
	ctx := context.Background()
	if _, err := registry.Provision(ctx, "dfe", "CTech DF-e", "https://dfe.example.test", "scope-publisher-dfe"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Publish(ctx, "scope-publisher-dfe", dfeManifest(publicScope("dfe:nfes:read")), 0, "", ""); err != nil {
		t.Fatal(err)
	}
	next, changed, err := registry.Publish(ctx, "scope-publisher-dfe", dfeManifest(m2mScope("dfe:nfes:read")), 1, "", "")
	if err != nil || !changed || next.Revision != 2 || !next.Scopes[0].M2M {
		t.Fatalf("publishing m2m: resource=%+v changed=%v err=%v", next, changed, err)
	}
	catalog := NewCatalogService(catalogV2RepoStub{resource: []ResourceServer{*repo.resource}}, nil)
	if bad, err := catalog.ValidateMachineGrantable(ctx, []string{"dfe:nfes:read"}); err != nil || bad != "" {
		t.Fatalf("published m2m scope not machine-grantable: bad=%q err=%v", bad, err)
	}
}

func TestValidateMachineGrantable(t *testing.T) {
	repo := catalogV2RepoStub{
		legacy: []ServiceScopes{{Service: "internal:wallet", Internal: true, Scopes: []ScopeEntry{{Scope: "internal:wallet:credit"}}}},
		resource: []ResourceServer{{SK: "billing", DisplayName: "Billing", Audience: "https://billing.example.test", Scopes: []ScopeDefinition{
			{Scope: "billing:invoices:write", Description: "w", DescriptionPT: "w", Visibility: VisibilityPublic, Status: StatusActive, M2M: true},
			{Scope: "billing:my-invoices:read", Description: "r", DescriptionPT: "r", Visibility: VisibilityPublic, Status: StatusActive},
			{Scope: "billing:old:write", Description: "o", DescriptionPT: "o", Visibility: VisibilityPublic, Status: StatusDeprecated, M2M: true},
		}}},
	}
	catalog := NewCatalogService(repo, nil)
	ctx := context.Background()
	if bad, err := catalog.ValidateMachineGrantable(ctx, []string{"billing:invoices:write"}); err != nil || bad != "" {
		t.Fatalf("m2m scope: bad=%q err=%v", bad, err)
	}
	for _, scope := range []string{"billing:my-invoices:read", "billing:old:write", "internal:wallet:credit", "billing:unknown:read"} {
		bad, err := catalog.ValidateMachineGrantable(ctx, []string{"billing:invoices:write", scope})
		if err != nil || bad != scope {
			t.Fatalf("%s: bad=%q err=%v, want it refused", scope, bad, err)
		}
	}
	if bad, err := catalog.ValidateMachineGrantable(ctx, nil); err != nil || bad != "" {
		t.Fatalf("no scopes: bad=%q err=%v, want nothing refused", bad, err)
	}
}

// The catalog is cached in Valkey as JSON: a field dropped there would make
// every m2m scope ungrantable for five minutes after each cache fill (the
// ScopeEntry.Audience bug this struct's comment records).
func TestM2MSurvivesTheCatalogCacheEncoding(t *testing.T) {
	in := []ServiceScopes{{Service: "billing", Scopes: []ScopeEntry{{Scope: "billing:invoices:write", M2M: true}}}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out []ServiceScopes
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out[0].Scopes[0].M2M {
		t.Fatalf("m2m lost in the cache encoding: %s", raw)
	}
}
```

Add `"encoding/json"` and `"strings"` to the test file's import block.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/scopes/ -run 'M2M|MachineGrantable' -v`
Expected: FAIL, build error `unknown field M2M in struct literal of type ScopeDefinition` / `catalog.ValidateMachineGrantable undefined`.

- [ ] **Step 3: Add the field to both structs**

In `api/internal/scopes/resource.go`, `ScopeDefinition` becomes:

```go
type ScopeDefinition struct {
	Scope         string            `json:"name"                  dynamodbav:"scope"`
	Descriptions  map[string]string `json:"descriptions,omitempty" dynamodbav:"-"`
	Description   string            `json:"description,omitempty"  dynamodbav:"description"`
	DescriptionPT string            `json:"description_pt,omitempty" dynamodbav:"description_pt"`
	Visibility    string            `json:"visibility"            dynamodbav:"visibility"`
	Status        string            `json:"status"                dynamodbav:"status"`
	// M2M marks a public scope the resource server accepts from machine
	// clients: a confidential developer-page client may use client_credentials
	// only when every scope it holds is marked. omitempty everywhere, so a
	// manifest without the field keeps its hash. Refused on internal scopes.
	M2M bool `json:"m2m,omitempty" dynamodbav:"m2m,omitempty"`
}
```

In `api/internal/scopes/catalog.go`, `ScopeEntry` becomes:

```go
type ScopeEntry struct {
	Scope         string `json:"scope"          dynamodbav:"scope"`
	Description   string `json:"description"    dynamodbav:"description"`    // English
	DescriptionPT string `json:"description_pt" dynamodbav:"description_pt"` // Brazilian Portuguese
	// M2M is ScopeDefinition.M2M carried into the catalog. Serialized in JSON
	// (not json:"-") because the Valkey catalog cache round-trips it.
	M2M bool `json:"m2m,omitempty" dynamodbav:"m2m,omitempty"`
}
```

- [ ] **Step 4: Validate the marker**

In `ValidateManifest` (`resource.go`), inside the `case VisibilityInternal:` branch, after the namespace check:

```go
		case VisibilityInternal:
			if !strings.HasPrefix(entry.Scope, InternalServicePrefix+":"+m.ResourceServerID+":") {
				return fmt.Errorf("%w: internal scope %q is outside namespace %q", ErrInvalidResource, entry.Scope, m.ResourceServerID)
			}
			if entry.M2M {
				return fmt.Errorf("%w: internal scope %q cannot be marked m2m; m2m is for public scopes machine clients may hold", ErrInvalidResource, entry.Scope)
			}
```

- [ ] **Step 5: Carry it into the catalog and add the check**

In `mergeResourceServers` (`service.go`), the entry line becomes:

```go
			entry := ScopeEntry{Scope: scope.Scope, Description: scope.Description, DescriptionPT: scope.DescriptionPT, M2M: scope.M2M}
```

After `ValidateGrantable`, add:

```go
// ValidateMachineGrantable returns the first scope in ss that a developer-page
// (non-first-party) client may not hold for client_credentials, or "" when it
// may hold them all. A scope qualifies only if it is active, public and marked
// m2m by its resource server. Deprecated scopes are merged into the internal
// bucket, so they never qualify. Lookup errors fail closed.
func (s *CatalogService) ValidateMachineGrantable(ctx context.Context, ss []string) (string, error) {
	services, err := s.Catalog(ctx)
	if err != nil {
		return "", err
	}
	index := make(map[string]struct{})
	for _, svc := range services {
		if svc.Internal {
			continue
		}
		for _, e := range svc.Scopes {
			if e.M2M {
				index[e.Scope] = struct{}{}
			}
		}
	}
	for _, sc := range ss {
		if _, ok := index[sc]; !ok {
			return sc, nil
		}
	}
	return "", nil
}
```

- [ ] **Step 6: Run the scope package tests**

Run: `go test ./internal/scopes/ -v`
Expected: PASS. The new tests pass, and so do all existing registry, catalog and manifest tests. The embedded Account manifest has no `m2m` and still validates.

- [ ] **Step 7: Document the field in `docs/resource-server-scope-registry.md`**

After the "Reconciliation semantics" section, add:

````markdown
## Machine-to-machine scopes (`m2m`)

A manifest scope may carry `"m2m": true`:

```json
{ "name": "billing:invoices:write", "descriptions": { "en": "…", "pt-BR": "…" },
  "visibility": "public", "status": "active", "m2m": true }
```

It means the resource server accepts this scope from machine clients. A
confidential **developer-page** client (not first-party) may use
`grant_type=client_credentials` only when **every** scope it holds is an
active, public, `m2m` scope and its owner's account is active. Otherwise the
whole request is refused (`403 unauthorized_client` naming the offending held
scope, or `400 invalid_scope` naming a requested scope the client does not hold),
never answered with a subset. First-party clients are unaffected.

- Optional, default `false`, and only valid on `public` scopes: `internal`
  scopes are machine-only already and are never grantable to developer-page
  clients, so the registry rejects `m2m` on them.
- Serialized `omitempty`: manifests without it keep their hash.
- Publish order: Account must be deployed with this field **before** a resource
  server publishes it. An older Account silently drops the unknown field, and
  because the stored hash then matches, a later identical publish changes
  nothing. If that happened, re-run the resource server's publish after
  Account is deployed.
- Shown in `GET /v1.0/scopes` (`"m2m": true` on the entry), so the developer
  page can tell which scopes work for machine access.
````

- [ ] **Step 8: Commit**

```bash
git add api/internal/scopes/resource.go api/internal/scopes/catalog.go api/internal/scopes/service.go api/internal/scopes/registry_test.go docs/resource-server-scope-registry.md
git commit -m "feat(scopes): m2m marker on manifest scopes, carried into the catalog"
```

---

### Task 6: `client_credentials` for developer-page clients

**Files:**
- Modify: `api/internal/apierror/problem.go` (two constructors next to `UnauthorizedClient` / `InvalidScope`)
- Modify: `api/internal/handler/token.go` (`TokenHandler` struct and `NewTokenHandler` ~lines 60-95; `clientCredentials` ~lines 157-212; new helper `machineScopesForDeveloperClient`)
- Modify: `api/internal/handler/testhelpers_test.go:585-598` (`memScopesRepo.LoadResources` adds a test `billing` resource)
- Create: `api/internal/handler/client_credentials_m2m_test.go`
- Modify: `README.md` (§ "Service-to-service tokens (`client_credentials`)", ~line 406)
- Modify: `api/ENDPOINTS.md` (`client_credentials` paragraph, ~line 222)

**Interfaces:**
- Consumes: `client.NewInternalService`, `(*InternalService).Active` (Task 2); `CatalogService.ValidateMachineGrantable` (Task 5); `OAuthClient.HasScope`, `FilterScopes`, `IsPublic`, `FirstParty` (existing); test helpers `newOAuthTestApp`, `(*oauthTestApp).postForm`, `ta.clientRepo`, `ta.userRepo`, `clientCredentialsForm`, `decodeJWTPayload`, `readJSON`, `bodyString`, `seedM2MClient` (existing, `kyc_test.go` / `oauth_security_test.go`).
- Produces:
  - `func apierror.UnauthorizedClientBecause(detail, instance string) *Problem`, a 403 with slug `unauthorized-client` and OAuth `unauthorized_client`
  - `func apierror.InvalidScopeBecause(detail, instance string) *Problem`, a 400 with slug `invalid-scope` and OAuth `invalid_scope`
  - HTTP behaviour per Global Constraints.

- [ ] **Step 1: Give the handler tests a resource with m2m scopes**

In `api/internal/handler/testhelpers_test.go`, `memScopesRepo.LoadResources` returns a second, test-only resource after the Account one (no handler test asserts the public catalog's contents, so adding a service is safe):

```go
func (memScopesRepo) LoadResources(_ context.Context) ([]scopesPkg.ResourceServer, error) {
	manifest, err := scopesPkg.AccountManifest()
	if err != nil {
		return nil, err
	}
	for i := range manifest.Scopes {
		manifest.Scopes[i].Description = manifest.Scopes[i].Descriptions["en"]
		manifest.Scopes[i].DescriptionPT = manifest.Scopes[i].Descriptions["pt-BR"]
	}
	scope := func(name string, m2m bool) scopesPkg.ScopeDefinition {
		return scopesPkg.ScopeDefinition{Scope: name, Description: name, DescriptionPT: name,
			Visibility: scopesPkg.VisibilityPublic, Status: scopesPkg.StatusActive, M2M: m2m}
	}
	return []scopesPkg.ResourceServer{
		{
			SK: scopesPkg.AccountResourceID, DisplayName: manifest.DisplayName,
			Audience: "http://localhost", Scopes: manifest.Scopes,
		},
		// A stand-in for ctech-billing's published manifest: integration scopes
		// marked m2m, a user scope not.
		{
			SK: "billing", DisplayName: "CTech Billing (test)", Audience: "https://billing.example.test",
			Scopes: []scopesPkg.ScopeDefinition{
				scope("billing:invoices:write", true),
				scope("billing:customers:read", true),
				scope("billing:customers:write", true),
				scope("billing:my-invoices:read", false),
			},
		},
	}, nil
}
```

- [ ] **Step 2: Write the failing token tests**

Create `api/internal/handler/client_credentials_m2m_test.go`:

```go
package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/crypto"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

// seedDeveloperClient stores a confidential, NOT first-party client owned by
// ownerID, as the developer page creates it, and returns its secret.
func seedDeveloperClient(t *testing.T, ta *oauthTestApp, id, ownerID string, allowed []string) string {
	t.Helper()
	hash, err := crypto.HashPassword(m2mSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK(id), Name: id, ClientType: oauthclient.TypeConfidential,
		ClientSecretHash: hash, AllowedScopes: allowed, OwnerUserID: ownerID,
	}); err != nil {
		t.Fatal(err)
	}
	return m2mSecret
}

func seedOwner(t *testing.T, ta *oauthTestApp, email string) *userDomain.User {
	t.Helper()
	u := &userDomain.User{Email: email, IsEnabled: true, EmailVerified: true}
	if err := ta.userRepo.Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func assertOAuthError(t *testing.T, resp *http.Response, status int, code, mentions string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, status, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["error"] != code {
		t.Fatalf("error = %v, want %s (body %v)", body["error"], code, body)
	}
	if mentions != "" && !strings.Contains(body["detail"].(string), mentions) {
		t.Fatalf("detail %q does not name %q", body["detail"], mentions)
	}
}

func TestClientCredentialsIssuesATokenToADeveloperClientWithOnlyM2MScopes(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "merchant@example.com")
	secret := seedDeveloperClient(t, ta, "cli_merchant", owner.ID(), []string{"billing:invoices:write", "billing:customers:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_merchant", secret, "billing:invoices:write"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["scope"] != "billing:invoices:write" {
		t.Fatalf("scope = %v", body["scope"])
	}
	if _, hasRefresh := body["refresh_token"]; hasRefresh {
		t.Fatal("client_credentials must not issue a refresh token")
	}
	claims := decodeJWTPayload(t, body["access_token"].(string))
	if claims["sub"] != "cli_merchant" || claims["azp"] != "cli_merchant" || claims["sid"] != "" {
		t.Fatalf("sub/azp/sid = %v/%v/%v", claims["sub"], claims["azp"], claims["sid"])
	}
	auds, _ := claims["aud"].([]any)
	found := false
	for _, a := range auds {
		found = found || a == "https://billing.example.test"
	}
	if !found {
		t.Fatalf("aud %v lacks billing's audience", claims["aud"])
	}
}

// One user scope on the client refuses the grant, even when only m2m scopes
// are requested: one client for sign-in, another for machine access.
func TestClientCredentialsRefusesADeveloperClientHoldingAUserScope(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "mixed@example.com")
	secret := seedDeveloperClient(t, ta, "cli_mixed", owner.ID(), []string{"billing:invoices:write", "billing:my-invoices:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_mixed", secret, "billing:invoices:write"))
	assertOAuthError(t, resp, http.StatusForbidden, "unauthorized_client", "billing:my-invoices:read")
}

// Refuse, never clamp: a developer client asking for a scope it does not hold
// gets no token at all.
func TestClientCredentialsRefusesADeveloperClientAskingForAnUngrantedScope(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "ask@example.com")
	secret := seedDeveloperClient(t, ta, "cli_ask", owner.ID(), []string{"billing:invoices:write"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_ask", secret, "billing:invoices:write billing:customers:write"))
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_scope", "billing:customers:write")
}

func TestClientCredentialsNeverGivesADeveloperClientAnInternalScope(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "internal@example.com")
	// Not creatable through self-service (ValidateGrantable skips internal
	// services), but if a row ever held one the grant must still refuse.
	secret := seedDeveloperClient(t, ta, "cli_internal", owner.ID(), []string{"billing:invoices:write", "internal:account:kyc"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_internal", secret, "internal:account:kyc"))
	assertOAuthError(t, resp, http.StatusForbidden, "unauthorized_client", "internal:account:kyc")
}

func TestClientCredentialsRefusesADeveloperClientWhoseOwnerIsNotActive(t *testing.T) {
	ta := newOAuthTestApp(t)
	locked := seedOwner(t, ta, "locked@example.com")
	locked.DeletionState = userDomain.DeletionStatePending
	disabled := seedOwner(t, ta, "disabled@example.com")
	disabled.IsEnabled = false

	for id, owner := range map[string]string{
		"cli_locked":   locked.ID(),
		"cli_disabled": disabled.ID(),
		"cli_orphan":   "no-such-user",
	} {
		secret := seedDeveloperClient(t, ta, id, owner, []string{"billing:invoices:write"})
		resp := ta.postForm("/v1.0/token", clientCredentialsForm(id, secret, "billing:invoices:write"))
		assertOAuthError(t, resp, http.StatusForbidden, "unauthorized_client", "not active")
	}
}

// The secret is checked before anything about scopes or owners is revealed.
func TestClientCredentialsChecksADeveloperClientsSecretFirst(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "secret@example.com")
	seedDeveloperClient(t, ta, "cli_secret", owner.ID(), []string{"billing:my-invoices:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_secret", "wrong-secret", "billing:my-invoices:read"))
	assertOAuthError(t, resp, http.StatusUnauthorized, "invalid_client", "")
}

// First-party clients keep today's behaviour: no m2m requirement, internal
// scopes allowed, unrequested-but-unheld scopes clamped away silently.
func TestClientCredentialsFirstPartyIsUnchanged(t *testing.T) {
	ta := newOAuthTestApp(t)
	secret := seedM2MClient(t, ta.clientRepo, "wallet", "confidential", true, []string{"internal:account:kyc", "billing:my-invoices:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("wallet", secret, "internal:account:kyc billing:my-invoices:read billing:customers:write"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["scope"] != "internal:account:kyc billing:my-invoices:read" {
		t.Fatalf("scope = %v, want the held scopes clamped as before", body["scope"])
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/handler/ -run 'TestClientCredentials' -v`
Expected: FAIL. `TestClientCredentialsIssuesATokenToADeveloperClientWithOnlyM2MScopes` gets `status = 403` (today's blanket refusal). The two `...Refuses...UngrantedScope` / `...UserScope` detail checks fail on today's generic detail. `TestClientCredentialsChecksADeveloperClientsSecretFirst` gets `status = 403, want 401`. `TestClientCredentialsFirstPartyIsUnchanged` and the existing `kyc_test.go` cases pass.

- [ ] **Step 4: Add the two problem constructors**

In `api/internal/apierror/problem.go`, rewrite `UnauthorizedClient` on top of a detailed variant and add the scope one next to `InvalidScope`:

```go
func UnauthorizedClient(instance string) *Problem {
	return UnauthorizedClientBecause("This client is not authorized to use this grant type.", instance)
}

// UnauthorizedClientBecause is UnauthorizedClient with the specific reason: the
// client authenticated, but may not use this grant as it is configured.
func UnauthorizedClientBecause(detail, instance string) *Problem {
	return newProblem("unauthorized-client", "Unauthorized Client", http.StatusForbidden, detail, instance).
		WithOAuth("unauthorized_client", detail)
}
```

```go
// InvalidScopeBecause names the scope that made the request invalid.
func InvalidScopeBecause(detail, instance string) *Problem {
	return newProblem("invalid-scope", "Invalid Scope", http.StatusBadRequest, detail, instance).
		WithOAuth("invalid_scope", detail)
}
```

- [ ] **Step 5: Wire the owner check into `TokenHandler`**

In `api/internal/handler/token.go`, add a field to `TokenHandler`:

```go
	// clients derives whether a developer-page client is usable (owner
	// active). It is the same rule GET /internal/oauth-clients reports.
	clients *oauthclient.InternalService
```

and set it in `NewTokenHandler`'s returned struct (no signature change, built from two arguments it already takes):

```go
		clients:    oauthclient.NewInternalService(clientRepo, userSvc),
```

- [ ] **Step 6: Split `clientCredentials` into the first-party and developer-page paths**

Replace the body of `clientCredentials`, from the `if oauthClient.IsPublic() || !oauthClient.FirstParty {` check through `if len(scp) == 0 { ... }`, with:

```go
	if oauthClient.IsPublic() {
		return apierror.UnauthorizedClient(c.Path()).Send(c)
	}
	// The secret is verified before anything about scopes or owners is
	// revealed, for first-party and developer-page clients alike.
	ok, verifyErr := crypto.VerifyPassword(clientSecret, oauthClient.ClientSecretHash)
	if verifyErr != nil {
		return apierror.ServerError(c.Path()).WithCause(verifyErr).Send(c)
	}
	if !ok {
		return apierror.InvalidClient("Invalid client_secret.", c.Path()).Send(c)
	}

	requested := strings.Fields(c.FormValue("scope"))
	var scp []string
	if oauthClient.FirstParty {
		// Unchanged: first-party clients are clamped silently to what they hold.
		scp = oauthClient.FilterScopes(requested)
	} else {
		var problem *apierror.Problem
		if scp, problem = h.machineScopesForDeveloperClient(c, oauthClient, requested); problem != nil {
			return problem.Send(c)
		}
	}
	if len(scp) == 0 {
		return apierror.InvalidScope(c.Path()).Send(c)
	}
```

Leave the rest (`AudiencesFor`, `SignAccessToken`, the JSON response) as it is. Update the function's doc comment:

```go
// clientCredentials issues a machine-to-machine token (RFC 6749 §4.4).
// First-party confidential clients: as always, clamped to their scopes,
// internal:* included. Developer-page confidential clients: only when their
// owner is active and every scope they hold is published m2m by its resource
// server; see machineScopesForDeveloperClient. Public clients never.
```

Add the helper below `clientCredentials`:

```go
// machineScopesForDeveloperClient decides client_credentials for a
// confidential client that is not first-party (created on the developer
// page). It refuses the whole request rather than issuing a subset:
//   - the owner must be active (InternalService.Active, the rule the internal
//     route reports);
//   - every scope the client HOLDS must be an active public m2m scope. One
//     sign-in scope means the client is a sign-in client, and it gets no
//     machine token. internal:* scopes never qualify;
//   - every scope REQUESTED must be held.
func (h *TokenHandler) machineScopesForDeveloperClient(c fiber.Ctx, oc *oauthclient.OAuthClient, requested []string) ([]string, *apierror.Problem) {
	active, err := h.clients.Active(c.Context(), oc)
	if err != nil {
		return nil, apierror.ServerError(c.Path()).WithCause(err)
	}
	if !active {
		return nil, apierror.UnauthorizedClientBecause("The account that owns this client is not active.", c.Path())
	}
	bad, err := h.catalogSvc.ValidateMachineGrantable(c.Context(), oc.AllowedScopes)
	if err != nil {
		return nil, apierror.ServerError(c.Path()).WithCause(err)
	}
	if bad != "" {
		return nil, apierror.UnauthorizedClientBecause(fmt.Sprintf(
			"client_credentials requires every scope this client holds to be published for machine-to-machine use; %q is not. Use a separate client for user sign-in.", bad), c.Path())
	}
	for _, scope := range requested {
		if !oc.HasScope(scope) {
			return nil, apierror.InvalidScopeBecause(fmt.Sprintf("Scope %q is not granted to this client.", scope), c.Path())
		}
	}
	return requested, nil
}
```

Add `"fmt"` to `token.go`'s imports if it is not already there.

- [ ] **Step 7: Run the token tests, then the whole suite**

Run: `go test ./internal/handler/ -run 'TestClientCredentials' -v && go build ./... && go vet ./... && go test ./...`
Expected: PASS. All eight new tests pass. The existing `TestClientCredentialsIssuesToken`, `...RejectsPublicClient`, `...RejectsThirdPartyClient` (internal scope → still `403 unauthorized_client`) and `...RejectsBadSecret` pass. Every package reports `ok`.

- [ ] **Step 8: Document the grant**

In `README.md` § "Service-to-service tokens (`client_credentials`)", replace the sentence "Public or third-party clients get `403 unauthorized_client`." with:

```markdown
Public clients get `403 unauthorized_client`. A confidential **developer-page**
client (not first-party) gets a token only when its owner's account is active
**and** every scope it holds is published `"m2m": true` by its resource server
(`docs/resource-server-scope-registry.md`). Otherwise the whole request is
refused: `403 unauthorized_client` naming the held scope that is not m2m (or
"owner not active"), or `400 invalid_scope` naming a requested scope the client
does not hold. Such a client never receives an `internal:*` scope. First-party
clients are clamped silently to their `allowed_scopes`, as before.
```

In `api/ENDPOINTS.md`, replace the `client_credentials` paragraph's second sentence ("Restricted to **confidential first-party** clients (`!IsPublic() && FirstParty`).") with:

```markdown
Public clients → `403 unauthorized_client`. The secret is verified first
(`401 invalid_client`). **First-party** confidential clients: scopes clamped to
`allowed_scopes` (unchanged). **Developer-page** confidential clients
(`machineScopesForDeveloperClient`): owner active (`InternalService.Active`),
every held scope active + public + `m2m` (`CatalogService.ValidateMachineGrantable`),
every requested scope held — else the whole request is refused
(`403 unauthorized_client` / `400 invalid_scope`, the detail naming the scope).
```

- [ ] **Step 9: Commit**

```bash
git add api/internal/apierror/problem.go api/internal/handler/token.go api/internal/handler/testhelpers_test.go api/internal/handler/client_credentials_m2m_test.go README.md api/ENDPOINTS.md
git commit -m "feat(token): client_credentials for developer-page clients holding only m2m scopes"
```

---

## What ctech-billing must publish (billing part 3 plan, not this one)

In `ctech-billing/api/internal/oauthresource/scope-manifest.json`, add `"m2m": true` to exactly these entries. All are `"visibility": "public"`, `"status": "active"`.

| Scope | `m2m` |
|---|---|
| `billing:subscriptions:read` | `true` |
| `billing:subscriptions:write` | `true` |
| `billing:invoices:read` | `true` |
| `billing:invoices:write` | `true` |
| `billing:usage:write` | `true` |
| `billing:customers:read` | `true` |
| `billing:customers:write` | `true` |
| `billing:entitlements:read` | `true` |
| `billing:products:read` | `true` |
| `billing:products:write` | `true` |
| `billing:organization:read` | `true` |

These must **not** carry it, because they are user scopes: `billing:my-invoices:read`, `billing:my-invoices:write`, `billing:my-subscriptions:read`, `billing:my-subscriptions:write`, `billing:finance:read`, `billing:finance:write`, and the new `billing:organization:write` from spec § 6.1.

Example entry:

```json
    {
      "name": "billing:invoices:write",
      "descriptions": { "pt-BR": "…", "en": "…" },
      "visibility": "public",
      "status": "active",
      "m2m": true
    }
```

Notes for that plan:
- **Order.** Publish only after this plan's ctech-account deploy is live in the same environment. An older Account ignores the unknown field, stores the same hash, and a later identical publish changes nothing. If it was published early, re-run billing's `deploy.yml` scopes stage by hand (`workflow_dispatch`, `scopes: true`).
- **Billing's Go struct.** `api/internal/oauthresource/resource.go`'s `Scope` struct may add `M2M bool \`json:"m2m,omitempty"\`` if the RFC 9728 document or the AllScopes test should know about it. The publish workflow sends the raw file either way.
- **When connecting a credential (spec § 5.1), billing should also refuse** a client holding any non-m2m scope, for example with a code like `client_not_machine_only`. Otherwise billing admits a credential that ctech-account will never issue a token to. `allowed_scopes` from the internal route is enough to check this against billing's own manifest.

## Deploy (operator runbook, spec § 9 step 1)

This happens after the PR is merged. Environments are `dev`, `stage` and `prod`, the names in `cdk/bin/ctech-account.ts` and billing's `api.yml`. Account tables use `TABLE_PREFIX=<env>`. Region is `us-east-1`. Do one environment at a time, in that order.

1. **Deploy ctech-account** through the normal pipeline. In the instance log, confirm
   `Account Resource Server ready (revision=<N> changed=true audience=...)`. That line proves the embedded manifest, now holding `internal:account:oauth-client-read`, is installed in `<env>_ctech_scopes`.
2. **Grant the scope to billing's client** from `api/`:

   ```bash
   ENV=dev   # then stage, then prod
   CLIENT_ID=$(aws ssm get-parameter --region us-east-1 \
     --name "/ctech-billing/$ENV/billing/account-client-id" \
     --query Parameter.Value --output text)
   AWS_REGION=us-east-1 TABLE_PREFIX=$ENV go run ./cmd/createclient -grant \
     -client-id "$CLIENT_ID" -scopes internal:account:oauth-client-read
   ```

   Expected: `client_id: <CLIENT_ID>` then `granted: internal:account:oauth-client-read`. On a re-run, expect `no change: every scope was already granted`. If it fails with `invalid scope "internal:account:oauth-client-read"`, step 1 is not live in that environment (or a Valkey catalog cache is less than 5 minutes old). Do not work around it.
3. **Verify the row** (read-only):

   ```bash
   aws dynamodb get-item --region us-east-1 --table-name "${ENV}_account_oauth_clients" \
     --key "{\"pk\":{\"S\":\"CLIENT_$CLIENT_ID\"}}" --projection-expression allowed_scopes
   ```

   Expected: the list holds `internal:account:org-member`, `internal:account:user-organizations` and `internal:account:oauth-client-read`.
4. **Nothing to restart in billing.** The token endpoint clamps the requested scope to `allowed_scopes` at issue time. Billing's next token request that asks for `internal:account:oauth-client-read` gets it, and its existing tokens keep working for the membership routes.
5. **Developer-page `client_credentials` (Tasks 5–6) needs no operator step.** It is live with the deploy and stays inert until a resource server publishes `m2m` scopes. Check that it is inert: no first-party client's behaviour changed (wallet, billing and dfe M2M calls keep working), and no manifest's hash moved in the boot or publish logs.
6. Tick the deploy lines in `PLAN.md` (in a follow-up `docs:` commit). Tell billing that § 9 step 1 is done **and** that it may now publish the `m2m` marks (§ "What ctech-billing must publish"), in each environment only after this deploy is live there.

**Rollback:** revert the PR and redeploy. The route disappears. The extra scope on billing's client grants nothing without the route, so it can stay. Developer-page clients go back to `403 unauthorized_client`. Any `m2m` marks already published stay in the stored manifests: the older Account ignores them, and they take effect again on redeploy.
