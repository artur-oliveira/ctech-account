# Platform organizations, memberships and company verification

Records the `ctech-account` half of
[ctech-billing ADR 0021](https://github.com/artur-oliveira/ctech-billing/blob/main/docs/adr/0021-platform-organizations-and-companies.md),
which reopens ctech-billing ADR 0007. Read that first: it settles *what* the concepts are and why they are separate.
This settles *how* they are built here.

## Outcome

`ctech-account` becomes the home of the platform's `Organization`, its memberships and its invitations, and the place
where a person proves they may act for a company. `ctech-billing` and
`ctech-dfe` stop owning tenant identity and reference `organization_id`.

## What belongs here, and what does not

| Here                                                 | Not here                                                          |
|------------------------------------------------------|-------------------------------------------------------------------|
| `Organization` — id, display name, owner, created_at | invoice numbering, dunning policy, payout gate (billing)          |
| `Membership` — user × organization × role            | fiscal identity: certificate, tax regime, service codes (dfe)     |
| `Invitation` — pending membership by e-mail          | what a company *is* for a product, and how many are enabled (dfe) |
| `CompanyClaim` — user × tax id, verified or not      | company configuration and emission history (dfe)                  |

The asymmetry is deliberate: identity and access live here; everything a product does with a tenant stays in that
product. Anything fiscal reaching this repository is the duplication ADR 0007 exists to prevent.

## Data model

### `account_organizations`

```
pk = ORG#<organization_id>            sk = META
    display_name, owner_user_id, created_at, updated_at
    lookup_pk = OWNER#<owner_user_id>       (GSI lookup-index, sparse)
```

`organization_id` is an opaque, time-ordered id (UUIDv7) and never a slug. A human-chosen id is a name, and names are renamed; billing's partition
key already carries this value
([ADR 0003](https://github.com/artur-oliveira/ctech-billing/blob/main/docs/adr/0003-tenant-and-livemode-partition-key.md)),
so it must outlive every rename.

### `account_memberships`

```
pk = ORG#<organization_id>            sk = MEMBER#<user_id>
    role, created_at, invited_by
    lookup_pk = USER#<user_id>              (GSI lookup-index)
```

Two access paths, one row: an organization's members are a query on the partition, and a person's organizations are a
query on the index. Neither is a scan and neither is a second copy.

**Roles are four and fixed:** `owner`, `admin`, `member`, `viewer`. Not configurable, and no per-action permission
matrix. `ctech-dfe` already has an `action.resource` model (`repositories/roles.go`); replicating it here before anybody
has asked for a custom role is how this record becomes the second RBAC lineage ADR 0007 forbids. A fixed ladder is
refusable later; a permission engine is not.

**Exactly one `owner` per organization**, enforced on write. Ownership transfers as one conditional transaction that
demotes the old owner and promotes the new one — never as two writes that can leave an organization with none.

### `account_invitations`

```
pk = ORG#<organization_id>            sk = INVITE#<lowercased_email>
    role, invited_by, expires_at (ttl), token_hash
    lookup_pk = INVITE#<token_hash>         (GSI lookup-index)
```

The e-mail is the sort key, so inviting the same address twice replaces the pending invitation rather than creating a
second one. The **hash** of the token is stored, never the token: an invitation row read out of the table must not be
usable to accept the invitation. TTL expires it without a job.

Accepting is conditional on the invitation existing and on the accepting account's verified e-mail matching the address
it was sent to. An invitation is not a bearer capability to join any organization — it is an offer to one address.

### `account_company_claims`

```
pk = USER#<user_id>                   sk = COMPANY#<tax_id>
    organization_id, tax_id_type (cnpj|cpf), status, level,
    submitted_at, reviewed_at, reviewer_id, reviewer_name, rejection_code,
    documents[]   (S3 keys + metadata, never URLs)
    lookup_pk = ORGCOMPANY#<organization_id>#<tax_id>   (GSI lookup-index)
```

This is the edge, not the node (ADR 0021). The subject is the **person's claim over a tax id**, so two people claiming
the same CNPJ are two rows and the second inherits nothing.

It reuses the vocabulary the person-level KYC already speaks — `pending` / `verified` / `rejected`, a level, documents
in the private bucket, an explicit audited access action, one fixed rejection code plus optional details. Same shapes,
same review workspace, one more queue. Deliberately **not**
a second document store: `/admin/kyc` grows a Companies tab rather than `/admin/kyb` being born.

The company record itself — certificate, tax regime, enablement per product — stays in `ctech-dfe`, keyed by
`(organization_id, tax_id)`.

## Routes

All under the SPA's own client (`RequireClientID(SELF_CLIENT_ID)`) unless noted: machine tokens do not manage
memberships.

```
POST   /v1.0/organizations                         create; the caller becomes owner
GET    /v1.0/organizations                         the caller's organizations, with their role
GET    /v1.0/organizations/:id                     detail; any member
PATCH  /v1.0/organizations/:id                     display name; owner|admin
GET    /v1.0/organizations/:id/members             any member
POST   /v1.0/organizations/:id/invitations         owner|admin
DELETE /v1.0/organizations/:id/invitations/:email  owner|admin
PATCH  /v1.0/organizations/:id/members/:user_id    change role; owner only
DELETE /v1.0/organizations/:id/members/:user_id    remove; owner|admin, never the last owner
POST   /v1.0/organizations/:id/transfer            owner only, to an existing member
POST   /v1.0/invitations/accept                    body: token
GET    /v1.0/internal/organizations/:id/members    M2M, for products resolving access
```

### Company claims

```
POST   /v1.0/organizations/:id/companies/:tax_id/claim     submit documents
GET    /v1.0/organizations/:id/companies                   claims and their status
POST   /v1.0/admin/kyc/companies/:claim_id/documents/access  manager+, audited
POST   /v1.0/admin/kyc/companies/:claim_id/decision           manager+, approve|reject
```

`GET /v1.0/internal/companies/:organization_id/:tax_id/claim` answers `verified` for the products that gate on it.
Products **ask**; they never store their own copy of the answer.

## Authorization

The role is read from the membership row on every request, never from a token claim. A role in a JWT is a role that
survives its own revocation for the life of the token, and removing somebody from an organization has to take effect on
the next request.

`GET /account/profile` may publish the caller's organizations and roles for the SPA to render with — an affordance only,
exactly as `support_role` already is in the KYC admin work. Nothing authorizes on it.

## Migration

Four phases. Each is deployable alone and none of them is reversible by the next, so they run in order.

**1. Born here, unused.** Tables, routes and the admin queue ship. Nothing else changes; no product reads any of it. An
organization created in this phase exists and does nothing, which is the correct state for a phase that can be rolled
back.

**2. Billing references it.** `ctech-billing` stops writing its local organization and stores
`organization_id`. Its existing tenants are backfilled one-for-one: each becomes an organization here with the same id
where possible, and its `owner_user_id` becomes the single `owner` membership. Billing keeps numbering, dunning policy,
payout gate and the issuer block, and reads membership from here (ADR 0021).

*Known trap, and it is live today:* `ctech-billing`'s tenant plan (`api/tenants/ctech.json`) carries no `owner_user_id`,
and its provisioner is create-or-skip — so re-running the seed with the field added changes nothing, and no human can
open the console at all. The backfill must write both
`owner_user_id` **and** the sparse `lookup_pk`; the field alone leaves the row unreachable.

**3. dfe splits company from organization.** `ctech-dfe`'s `Organization` becomes `Company` keyed by
`(organization_id, tax_id)` instead of the global `CNPJ_…`. Its OWNER membership is dropped in favour of this
repository's; `owner_user_id` — today "the account whose subscription pays for it" — becomes the organization that is
billed.

This is the expensive one: it changes a primary key in a repository that is in production. It is last for that reason,
and it is the phase to plan separately rather than inside this document.

**4. Enablement and quota.** dfe gains an explicit *enable this company for DF-e* action, and counts **enabled**
companies against `quota_companies` — not registered ones. A company registered and not enabled costs nothing, which is
what makes ten CNPJs and one DF-e seat expressible.

## Cross-project impact

- **ctech-account:** the tables above, the routes, and a Companies queue in the existing
  `/admin/kyc` workspace. CDK adds `account_memberships`, `account_invitations`,
  `account_company_claims` and their `lookup-index`. Deploy DynamoDB before the API that queries it, as the KYC index
  already required.
- **ctech-billing:** phase 2. No change to invoicing, collection or the console's screens — the console resolves its
  organization from a membership here instead of from a local row.
- **ctech-dfe:** phase 3 and 4, and the only repository whose primary key changes.
- **ctech-wallet:** none. This adds no claim to a token and changes no OAuth flow.

## Explicitly out of scope

Nested organizations, per-action permissions, SSO domain capture, a company shared between organizations as one record,
and automatic verification from the QSA. The first four are named in ADR 0021's reopen conditions; the last needs the
paragraph below.

## On CNPJ lookup, and why it is not verification

`ctech-dfe` already looks a CNPJ up — [cnpja](https://open.cnpja.com)'s open endpoint, with a SEFAZ fallback
(`ui/src/lib/hooks/useCnpjLookup.ts`). It fills a form in: name, address, status. That is a convenience and it should
stay one.

Two reasons it is not evidence for a claim, and they are independent:

- **It is called from the browser.** A response that reached the server through the page that asked for it is a response
  the page could have written. Anything a review decision rests on has to be fetched server-side, from the server's own
  credential.
- **It proves the wrong thing.** A lookup says this CNPJ exists and is active. Whether *this person*
  may act for it is a different question, and public registry data cannot answer it — which is the whole reason a claim
  is reviewed by a person rather than resolved by an API.

So a server-side lookup is worth having, later, and worth being honest about what it buys: it pre-fills the reviewer's
screen and catches an inactive or non-existent CNPJ before a human spends time on it. It shortens the queue; it does not
replace it. When it is added it is one more piece of evidence attached to the claim, alongside the documents, with the
source and the fetch timestamp recorded — never a field that decides the outcome.

`ctech-rfb` is not the path here: it is not in production and is not planned for it soon.
