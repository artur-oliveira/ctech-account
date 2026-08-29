# Organization handoff

## Outcome

A person using `ctech-dfe` who needs a company that does not exist yet is sent to
`ctech-account` to create the organization, and comes straight back to where they
were, with the new `organization_id` in hand. The DF-e never writes an organization;
`ctech-account` stays the only writer of tenancy.

This extends [platform organizations](2026-08-29-platform-organizations.md) and the
[organizations UI](2026-08-29-organizations-ui.md), which covered the past (the
[migration](../plans/2026-08-29-dfe-organization-migration-runbook.md)) but not the
future: how an organization gets created from a product that is not this one.

## The decision, and the two it beats

**A product never writes tenancy. It redirects, and gets a redirect back.**

Two alternatives were rejected:

**The DF-e calls `POST /organizations` with the user's token.** Today
`RequireClientID(SelfClientID)` refuses exactly this. Loosening it would let *any*
registered OAuth client create organizations in a user's name, so one compromised
client becomes a tenancy-creation primitive. The guard is the feature.

**The DF-e calls it with a service credential.** Technically available, but it makes
the DF-e a co-owner of an accounts resource: two products would then need an answer
for renaming, transfer, and "the owner left the company", and the second answer
always drifts. The migration just finished collapsing two organization models into
one; this would start a third.

## The organization stays thin

`ctech-account` stores an organization's **name, its owner, and its people**. Nothing
fiscal. The DF-e keeps CNPJ, razão social, inscrição estadual, certificates and notes
in its own tables, keyed by `organization_id`.

The reason is not size, it is ownership. Inscrição Estadual is a fiscal-domain concept
with fiscal-domain rules, and `ctech-account` has no business validating it or holding
it correct over time. `ctech-billing` will key off the same `organization_id` and must
not inherit fiscal rules to do so.

**The consequence, stated plainly:** creating an organization is *not* creating a
company. The handoff returns an `organization_id` and nothing else; the DF-e still has
its own "dados da empresa" step afterwards. The flow must not read as if the accounts
screen finished the job.

## The flow

```
DF-e     user has no company, taps "Nova empresa"
         browser → {ACCOUNTS_APP}/account/organizations/new
                     ?client_id=dfe
                     &return_to=https://dfe.example/empresas/vincular
                     &state=<opaque, DF-e's own>

Accounts GET /v1.0/organizations/handoff?client_id&return_to   (validates, names)
         screen: create, with a banner naming the DF-e
         on success → return_to?organization_id=org_xxx&state=<echoed>
         on cancel  → return_to?cancelled=1&state=<echoed>

DF-e     reads organization_id, links it, continues its own flow
```

### Parameters

| Param | Direction | Rule |
|---|---|---|
| `client_id` | in | must resolve to a registered, **first-party** OAuth client |
| `return_to` | in | must share scheme+host with one of that client's `redirect_uris` |
| `state` | in, echoed | opaque to accounts; never parsed, never logged, capped at 512 bytes |
| `organization_id` | out | the new organization, on success only |
| `cancelled` | out | `1`, when the person backed out |

`state` is echoed rather than invented because the DF-e already has to survive the
round trip and knows what it was doing; accounts holding that context would be a
second session store for one screen.

## The security rules

These three are the whole reason this is not an open redirect. Each gets a test.

**1. `return_to` is validated against the client's registered origins.** Not a new
allowlist — the existing `redirect_uris` on `OAuthClient`. The comparison is
scheme+host, the same one `IsPostLogoutRedirectAllowed` already makes for RP-initiated
logout (`api/internal/domain/oauth/client/model.go:63`). That method is renamed
`IsRegisteredOrigin` and both callers use it; a second copy of an origin check is how
the two drift apart and one of them ends up permissive.

A `return_to` that fails is a **422 with a problem type**, never a redirect to a
default and never a silent drop. The person is told the product that sent them is
misconfigured, and given a link into `/account/organizations`, so a bad integration
strands nobody.

**2. Only first-party clients may hand off.** `FirstParty` on the client model already
marks the products the platform itself operates and is not settable through the
self-service API (`model.go:20`). A third-party client sending a user to create
organizations is not a flow we have designed, so it is refused rather than
half-supported.

**3. No token, ever, travels on `return_to`.** The response carries
`organization_id` and the echoed `state`. The DF-e already holds a user token and
reads the organization through the API with it. A redirect URL lands in browser
history, in `Referer`, and in access logs; an id is fine there, a credential is not.

## `GET /v1.0/organizations/handoff`

One endpoint, because the SPA cannot validate anything: it is a static export with no
server, and a check written in the client is a check an attacker skips.

```
GET /v1.0/organizations/handoff?client_id=dfe&return_to=https://dfe.example/x
Authorization: Bearer <user token>          (RequireAuth + RequireClientID(Self))

200 { "client_name": "DF-e", "return_to": "https://dfe.example/x" }
422 problem, type .../organization-handoff-invalid
```

It answers two questions in one call — *is this handoff legitimate* and *what do I
call the product that sent them* — because the screen needs both before it renders,
and two calls would let it render half a banner.

`return_to` is echoed back **normalized** and the screen redirects to the echoed value,
not to the raw query parameter. Whatever the server validated is what the browser
follows; otherwise the two can differ.

One problem type, not four. The distinctions (unknown client, third-party client,
unregistered origin, malformed URL) matter to whoever is fixing the integration and
are carried in `detail` and the logs — never to the person on the screen, who can do
nothing about any of them.

## The screen

A new route, `/account/organizations/new`. The create dialog on
`/account/organizations` stays exactly as it is: that is the path for somebody already
in their account, and it has no return trip to make.

**What is different from the dialog:**

- **A banner naming the sender**, above the form: *"Criando uma organização para o
  DF-e."* Without it, a person who just tapped a button in another product finds
  themselves on a different domain with no explanation, and that reads as a bug or a
  phish. The name comes from the server, never from a query parameter — a client that
  names itself can name itself anything.
- **Cancel is a real action**, not a back button. It returns through `return_to` with
  `cancelled=1`, so the DF-e can put the person back where they were instead of
  guessing.
- **No "what next" copy.** On success the browser leaves immediately; a success toast
  the person never reads is theatre.
- **Without handoff params, it is just the create screen**, returning to
  `/account/organizations`. The route is reachable directly and must not break when it
  is.

Signed out, the account shell already redirects to `/login?continue=…`, which brings
the person back to this URL, parameters intact. That is existing behaviour and must
keep working — a handoff that loses its `return_to` at the login wall is a dead end,
so it gets a test.

## What the DF-e builds

Out of scope for this repo, recorded so the contract is not half-specified:

- A "Nova empresa" affordance that builds the handoff URL with its own `state`.
- A landing route (`return_to`) that reads `organization_id`, links it to a DF-e
  company record, and handles `cancelled=1` by returning the person to where they
  started.
- **Idempotency on the landing route.** A refresh replays the same
  `organization_id`; the second visit must find the existing link, not create a second
  company.
- The DF-e must accept an `organization_id` it has never seen. A person can create
  the organization in accounts directly and only later arrive at the DF-e; there is
  no ordering guarantee, and requiring one would make the accounts UI a trap.

## Tests

Server:

- a `return_to` on a registered origin passes; a different host, a different scheme,
  and a lookalike (`dfe.example.evil.com`) each fail
- a third-party (non-`FirstParty`) client is refused
- an unknown `client_id` and a malformed `return_to` return the same problem type as
  each other
- `IsRegisteredOrigin` keeps every case its post-logout predecessor passed — the
  rename must not quietly change a security predicate

UI:

- the banner shows the server's client name, and a `client_name` smuggled in as a
  query parameter is ignored
- success redirects to the **echoed** `return_to` with `organization_id` and the
  echoed `state`
- cancel redirects with `cancelled=1`
- an invalid handoff renders the error state with a way into `/account/organizations`,
  and does not redirect
- with no handoff parameters the screen creates an organization and lands on
  `/account/organizations`

## Out of scope, deliberately

- **Handing off into an *existing* organization** ("pick one, then come back"). The
  DF-e can already list a person's organizations through the API and choose there,
  without leaving its own product. Only creation needs the accounts screen.
- **Any fiscal field on the accounts organization.** Stated above; repeated here
  because it is the thing most likely to be added later "just for CNPJ".
- **`ctech-billing` using this flow.** The same endpoint will serve it unchanged when
  its console gains multi-organization support; nothing here is DF-e-specific except
  the `client_id` in the examples.
