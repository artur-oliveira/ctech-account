# Membership Unification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ctech-account` becomes the only record of who may reach a company; `ctech-dfe` keeps only what a person may do once there.

**Architecture:** the DF-e asks `ctech-account` "may this user act for this company" through a scope-gated internal route — the pattern `/v1.0/internal/kyc` already established — and answers "what may they do" from its own `(company, user)` row. A row with no edge grants nothing, and the check fails closed.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB, OAuth2 client-credentials with `internal:*` scopes.

**Spec:** [ctech-billing ADR 0023](../../../ctech-billing/docs/adr/0023-membership-in-account-authorization-in-the-product.md), which amends [ADR 0022](../../../ctech-billing/docs/adr/0022-company-identity-in-account.md).

## Global Constraints

- **A product row with no edge grants nothing, and the check fails closed.** A product that fell back to its own row when the edge was unreadable would reinvent the second lineage on its first outage — the thing this unification removes.
- **The role and the permission vocabulary never leave the product.** A role is a named bundle of permissions; the moment `OWNER` lives in `ctech-account`, billing either couples to it or the platform holds a value one product reads.
- **The company's owner is the product's `OWNER` row**, never the organization's `owner_user_id`. An accountant owning a workspace of forty CNPJs would own every company in it.
- **`OWNER` (dfe) and `owner` (account) are different vocabularies with the same word.** Never map one onto the other; ADR 0023 records why neither is renamed.
- Commit messages carry **no** `Co-Authored-By` trailer.

## Ordering, and why it is not negotiable

ADR 0023 fixes it: **decide the owner → extend the invitation → migrate the grants.** Moving the grants first hands them to nobody empowered to change them.

Tasks 1–3 are `ctech-account` and change no behaviour until something calls them. Tasks 4–6 are `ctech-dfe`. Task 7 is the migration and Task 8 the retirement — and Task 8 ships a release after Task 7, never in the same one, because the rollback for everything above it is "the product still has its own rows".

---

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/scopes/catalog.go` (modify) | the `internal:account:company-actor` scope |
| `api/internal/handler/company_internal.go` (create) | the service-to-service reach check |
| `api/internal/domain/organization/model.go` (modify) | `Invitation.CompanyIDs` |
| `api/internal/domain/organization/service.go` (modify) | invite and accept carry companies |
| `ctech-dfe/api/internal/services/reach.go` (create) | the client, its cache and its failure mode |
| `ctech-dfe/api/internal/middleware/rbac.go` (modify) | reach from the edge, verbs from the row |
| `ctech-dfe/api/cmd/migrate-grants/` (create) | the explicit grants, once there is an owner |

---

### Task 1: The internal reach check

**Files:**
- Modify: `api/internal/scopes/catalog.go`
- Create: `api/internal/handler/company_internal.go`
- Modify: `api/cmd/api/main.go`
- Test: `api/internal/handler/company_internal_test.go`

**Interfaces:**
- Produces: `GET /v1.0/internal/companies/:company_id/actors/:user_id` behind `RequireAuth` + `RequireInternalScope(scopes.InternalAccountCompanyActor)`, answering `{"may_act": bool, "organization_id": "..."}`.
- Produces: `const InternalAccountCompanyActor = "internal:account:company-actor"`.

**Why a route and not a token claim.** Minting the edge into the access token would make a revocation wait for the token to expire, which is the same defect `RequireOrgRole` already refuses to have ("a role minted into a JWT outlives its own revocation"). The DF-e caches the answer instead, with a TTL it controls and can invalidate.

**Why `:company_id` alone and not `:org_id/:company_id`.** The caller does not know the organization — that is what it is asking for. The edge's sort key nests the company id, so the answer needs a lookup by company; the route returns the organization it found, which is what the DF-e stores.

- [ ] **Step 1: Write the failing test**

```go
// The whole point: this route is how another product learns reach, so a
// stranger must not be able to ask it.
func TestTheReachCheckNeedsTheInternalScope(t *testing.T) {
	env := newInternalCompanyEnv(t)
	resp := env.get(t, "/v1.0/internal/companies/cmp_1/actors/usr_1", env.tokenWithoutScope)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// An edge that exists answers true and names the organization, because the
// caller does not know it — that is what it is asking for.
func TestAnExistingEdgeAnswersTrueAndNamesTheOrganization(t *testing.T) {
	env := newInternalCompanyEnv(t)
	env.grant(t, "org_1", "cmp_1", "usr_1")
	var got struct {
		MayAct         bool   `json:"may_act"`
		OrganizationID string `json:"organization_id"`
	}
	env.decode(t, env.get(t, "/v1.0/internal/companies/cmp_1/actors/usr_1", env.internalToken), &got)
	if !got.MayAct || got.OrganizationID != "org_1" {
		t.Fatalf("got %+v", got)
	}
}

// A missing edge is false with 200, not 404. "Not permitted" is an answer, and
// a 404 would make the caller treat an outage and a refusal alike.
func TestAMissingEdgeIsFalseNotAnError(t *testing.T) {
	env := newInternalCompanyEnv(t)
	resp := env.get(t, "/v1.0/internal/companies/cmp_nope/actors/usr_1", env.internalToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct{ MayAct bool `json:"may_act"` }
	env.decode(t, resp, &got)
	if got.MayAct {
		t.Error("a company with no edge answered true")
	}
}

// It answers reach and nothing else. A role or a permission list here would be
// the platform holding the product's vocabulary, which ADR 0023 refuses.
func TestTheAnswerCarriesNoRole(t *testing.T) {
	env := newInternalCompanyEnv(t)
	env.grant(t, "org_1", "cmp_1", "usr_1")
	var body map[string]any
	env.decode(t, env.get(t, "/v1.0/internal/companies/cmp_1/actors/usr_1", env.internalToken), &body)
	for _, forbidden := range []string{"role", "permissions", "roles"} {
		if _, present := body[forbidden]; present {
			t.Errorf("the reach answer carries %q, which belongs to the product", forbidden)
		}
	}
}
```

- [ ] **Step 2–5:** run red, add the scope, implement the handler, wire it in `main.go` beside `kycH.RegisterInternalGet`, run green, commit.

```bash
git commit -m "feat(company): the internal reach check

A route rather than a token claim: an edge minted into a JWT outlives its
own revocation, which is the defect RequireOrgRole already refuses to
have. The caller caches with a TTL it controls.

It answers reach and the organization it found, and nothing else. A role
here would be the platform holding the product's vocabulary."
```

---

### Task 2: The company's owner is the product's, and the platform says so

**Files:**
- Modify: `api/internal/domain/company/service.go`
- Test: `api/internal/domain/company/service_test.go`

There is no code to add. There is a **decision to record where somebody will look for it**, and one guard.

- [ ] **Step 1: Document the absence**

On `Actor`, a comment saying the edge carries no role and no ownership on purpose, that "who may grant" is answered by the product's `OWNER` row, and that adding a role here is what ADR 0023's "Reopen if" is watching for.

- [ ] **Step 2: Guard it with a test**

```go
// ADR 0023 keeps the role in the product. A field here would be the platform
// growing a permission model, and the next question is whether billing may use
// it — which is the argument the ADR settles.
func TestTheActorEdgeCarriesNoRole(t *testing.T) {
	var a Actor
	v := reflect.TypeOf(a)
	for i := 0; i < v.NumField(); i++ {
		switch strings.ToLower(v.Field(i).Name) {
		case "role", "roles", "permissions", "owner", "isowner":
			t.Errorf("Actor grew %q; ADR 0023 keeps authorization in the product", v.Field(i).Name)
		}
	}
}
```

A reflection test is unusual and deliberate: the failure it guards is somebody adding a field in good faith, and only a test that reads the struct itself catches that.

- [ ] **Step 3: Commit**

---

### Task 3: An invitation carries the companies it is for

**Files:**
- Modify: `api/internal/domain/organization/model.go`, `repository.go`, `service.go`, `api/internal/handler/organization.go`
- Modify: `ui/src/app/account/organizations/detail/invitations-tab.tsx`, `ui/src/lib/types.ts`
- Test: alongside each

**Interfaces:**
- Produces: `Invitation.CompanyIDs []string` (`dynamodbav:"company_ids,omitempty"`), `Service.Invite(ctx, orgID, actorUserID, email, role string, companyIDs []string)`, and `Accept` writing the membership and those edges in one transaction.

**Optional, deliberately.** Inviting somebody to the workspace with no company is real — a bookkeeper who only reads invoices — and forcing a list would make the common case carry the accountant's problem.

- [ ] **Step 1: Write the failing test**

```go
// The case that pays for this model: an accountant invites a junior who should
// reach five of forty companies. An invitation that cannot say which five
// leaves them inside the workspace able to act for nothing.
func TestAnInvitationCarriesItsCompanies(t *testing.T) { /* invite with two ids, accept, assert both edges */ }

// Membership and edges land together or not at all. A membership written
// without its edges is a person who joined and cannot work.
func TestAcceptWritesTheMembershipAndTheEdgesAtomically(t *testing.T) { /* ... */ }

// A company outside this organization is refused at invite time, not silently
// dropped at accept time — the inviter can still fix it while they are looking.
func TestInvitingToAForeignCompanyIsRefused(t *testing.T) { /* ... */ }

// No companies is valid and grants none.
func TestAnInvitationWithNoCompaniesGrantsNoCompanyAccess(t *testing.T) { /* ... */ }
```

- [ ] **Step 2–6:** red, model, transaction, handler, UI, green, commit.

**The UI carries a rule this plan will not let slip:** an accepted invitation with no companies must *say* the person has no company access yet and who can grant it. Silence there is somebody who joined and cannot work, with nothing on screen explaining why.

---

### Task 4: The DF-e asks, caches, and fails closed

**Files:**
- Create: `ctech-dfe/api/internal/services/reach.go`
- Test: `ctech-dfe/api/internal/services/reach_test.go`

**Interfaces:**
- Produces: `type ReachService struct{...}`, `func (s *ReachService) MayAct(ctx, companyID, userID string) (organizationID string, ok bool, err error)`.

**The three rules, each with a test:**

**It fails closed.** An unreachable `ctech-account` returns an error and the caller refuses. This is the opposite of `BillingService`'s snapshot, which deliberately degrades open so a billing outage does not stop issuance — and the difference is the point: billing decides whether somebody *should* be allowed to pay, reach decides whether they are *who they say they are*. Guessing at the second is the outage becoming an authorization bypass.

**It caches positively and negatively.** A negative answer cached is what keeps a stranger's probe from becoming a request per attempt. Both with a short TTL — long enough to matter on the hot path, short enough that a revocation lands in seconds.

**It never falls back to the DF-e's own row.** That fallback is the whole defect the unification removes, and it is the line somebody adds during an incident.

- [ ] **Step 1: Write the failing test**

```go
// The rule the whole unification exists for. A fallback to the product's own
// row is what somebody adds during an incident, and it is exactly the second
// lineage this removes.
func TestAnUnreachableAccountRefusesRatherThanFallingBack(t *testing.T) { /* ... */ }

// A negative answer is cached too: without it, a stranger probing company ids
// costs one request to ctech-account per attempt.
func TestARefusalIsCached(t *testing.T) { /* ... */ }

// And expires, so a granted edge starts working without a deploy.
func TestACachedRefusalExpires(t *testing.T) { /* ... */ }
```

- [ ] **Steps 2–5:** red, implement with the `oauth2client` credentials the repo already uses, green, commit.

---

### Task 5: Reach from the edge, verbs from the row

**Files:**
- Modify: `ctech-dfe/api/internal/middleware/rbac.go`
- Test: `ctech-dfe/api/internal/middleware/rbac_test.go`

`parseUserOrganizationRole` gains a step. Today it reads the membership and takes both reach and role from it. It now reads the edge for reach and the row for the role — and **the row alone stops being enough**.

- [ ] **Step 1: Write the failing test**

```go
// The invariant. A product row that survived a revoked edge must grant nothing,
// or the unification bought nothing.
func TestARowWithNoEdgeGrantsNothing(t *testing.T) { /* ... */ }

// And an edge with no row grants reach and no verbs — somebody invited to a
// company nobody has given a role in yet.
func TestAnEdgeWithNoRowGrantsNoVerbs(t *testing.T) { /* ... */ }

// The refusals must be indistinguishable from outside, or the API becomes a
// probe for which company ids are real.
func TestBothRefusalsLookTheSame(t *testing.T) { /* ... */ }
```

- [ ] **Steps 2–5:** red, implement, green, commit.

**Behind a flag**, defaulting off. The flip here is not a data migration and has no `-verify`: it is a live authorization change, and the only rehearsal available is turning it on for one account and watching.

---

### Task 6: Retire the DF-e's invitations

**Files:**
- Modify: `ctech-dfe/api/internal/api/v1/invitations.go`, `internal/services/invitations.go`
- Modify: the UI's invitation screens

Two invitation flows for one workspace is two e-mails, two tokens, and two ways to be half-invited. The DF-e's routes answer a problem type pointing at the platform's screen rather than 404 — somebody with the old link in a tab deserves to be told where it moved.

- [ ] **Steps:** red, implement, green, commit.

---

### Task 7: Migrate the explicit grants

**Files:**
- Create: `ctech-dfe/api/cmd/migrate-grants/`

Only now, and the order is ADR 0023's: the owner exists (Task 2), the invitation can express companies (Task 3), so a grant handed over has somebody empowered to change it.

**What it does:** for every `organization_users` row carrying `permissions`, confirm an edge exists in `ctech-account` for the same person and company, and leave the row's grants alone. **It writes nothing to the DF-e** — the grants are already where they belong. What it writes, when `-apply`, is the missing edges.

**What it refuses:** a row with grants and no edge, and no invitation to derive one from. That person is exercising permissions the platform has no record of them being allowed to reach, and inventing the edge is inventing access.

- [ ] **Steps:** plan / apply / verify, same three verbs and the same absent fourth as `rekey-companies`.

---

### Task 8: Delete `organization_users`' access half

**Not in the same release as Task 7.** The rollback for everything above is "the product still has its own rows"; deleting them in the same release deletes the rollback.

What goes: nothing. `organization_users` **stays** — it is the authorization overlay now. What goes is the *belief* that it grants access, and that already went in Task 5.

The row that becomes collectable is the orphan: an overlay whose edge was revoked. ADR 0023 lists this under limits accepted and says nothing collects them yet. A sweep belongs here, and it is the one piece of this plan that can wait indefinitely without costing anything but rows.

---

## Self-Review

**ADR coverage.** The line (identity/reach vs verbs) → Tasks 1, 4, 5. Role stays in the product → Task 2's reflection guard. Owner is the product's `OWNER` → Task 2, recorded; no code, correctly. Invitations carry companies → Task 3. Row with no edge grants nothing → Task 5, and it is the constraint at the top. dfe invitations retired → Task 6. Grants ordered last → Task 7. Orphan overlays → Task 8, named as deferrable.

**Placeholder scan.** Tasks 3–7 carry test *intent* with empty bodies. That is a real weakness and it is flagged rather than hidden: each depends on a shape that is cheaper to read at implementation time than to transcribe wrongly now — the same call I made in the re-key plan, where the two tasks written that way are the two that grew during implementation. Tasks 1 and 2, which carry the invariants somebody could get wrong quietly, are complete.

**What this plan does not decide.** Whether `ctech-billing` uses the same reach check when its console gains multi-organization support. ADR 0023 says it inherits the split; whether it calls the same route or gets its own is a question for that work, and answering it here would be designing for a consumer that does not exist.

**One thing found while writing.** Task 4's "fails closed" is the opposite of the `BillingService` snapshot's deliberate degrade-open, and both live in the same package. Somebody will read one and copy it into the other. The distinction is written into Task 4's own comment, not only here — a rule that lives only in a plan is a rule the code cannot defend.
