# Platform companies

## Outcome

`ctech-account` learns a fourth noun: **Company** — a CNPJ (or CPF) that an
organization acts for. It holds who the company *is* and who may act for it, and
nothing about how a fiscal document gets issued. `ctech-dfe` keeps the issuing, keyed
by the platform `company_id`; `ctech-billing` bills to one of them.

This amends [ctech-billing ADR 0021](../../../ctech-billing/docs/adr/0021-platform-organizations-and-companies.md),
which put Company's home in `ctech-dfe`. It builds on
[platform organizations](2026-08-29-platform-organizations.md) and is a prerequisite
for the [organization handoff](2026-08-29-organization-handoff.md), which currently
returns an empty organization and leaves the person a second form to fill.

## Why this amends ADR 0021

ADR 0021 assigned Company to `ctech-dfe`. Three of its own paragraphs argue the other
way, and they won:

**"Two counters, never one: companies that exist in an organization, and companies
enabled for a given product."** If existence is separable from a product's enablement,
existence is a platform fact. A registered, un-enabled company is not a DF-e object;
it is an object the DF-e has not been asked to use.

**"Verification attaches to the edge: this person may act for this company."** That
edge is `User ↔ Company` — the same shape as membership, and served by the same
machinery `ctech-account` already runs for people: document upload, a manager-only
review queue at `/admin/kyc`, audited access to what a reviewer opened. Putting company
verification in `ctech-dfe` means building that machinery a second time.

**Billing needs a company to bill.** With Company in the DF-e, `ctech-billing` would
depend on the fiscal emitter to know whom it invoices. Billing must not depend on the
DF-e to bill somebody who does not use the DF-e.

Three consumers and an owner that is one of them. The corrected split is not "move
Company to accounts" — it is that **Company was two things wearing one name**.

| | Identity — `ctech-account` | Issuance — `ctech-dfe` |
|---|---|---|
| Answers | who this company is, and who may act for it | how this company emits |
| Holds | tax id, legal name, trade name, the verified `User ↔ Company` edge | inscrição estadual, CRT/regime, fiscal address, série and numbering, CSC/CSRT, the A1 certificate |
| Consumers | dfe, billing, and the account UI | the DF-e alone |

The A1 certificate is a private key. It never leaves `ctech-dfe`, not even as a field
saying one exists.

## Where the same tax id in two organizations is real

The rule inherited from ADR 0021 — a company is keyed `(organization_id, tax_id)`, and
two organizations may each hold the same CNPJ — is the part most likely to look wrong.
The cases that need it:

**An accountant and their client, at the same time.** The office carries the client's
CNPJ for SPED and monthly closing; the client's own staff issues day-to-day notes. Two
workspaces, two teams, two subscriptions, one CNPJ. This is the ordinary shape of the
Brazilian market, not an edge case — and it is the customer this product is for.

**Changing accountants.** A client leaves office A for office B. Under a globally
unique tax id, B cannot register the CNPJ until A deletes it: the former accountant
holds a departed client hostage, and deleting destroys the records A is required to
keep for five years. This case alone settles it.

**Outsourced issuance (BPO fiscal).** Same shape, different service.

**A group or franchise** where the holding issues for a unit that also issues for
itself.

**What this is not:** matriz and filial. A filial has its own CNPJ — same root, a
different order — so it is simply a second company, needing no duplication at all.

### The hazard duplication actually creates, and where it belongs

ADR 0021 lists "duplicated certificates" as the accepted cost. That understates it. An
NF-e is unique by **(CNPJ, modelo, série, número, ambiente)**. Two organizations
issuing under the same CNPJ *on the same série* collide at the SEFAZ: a duplicate
rejection, or worse, a gap in numbering somebody has to justify.

Note where the hazard lives — in issuance, not in identity. That is precisely the split
above:

- **In `ctech-account`, duplicate identity is free.** A CNPJ is public data; anyone can
  type yours. Registering is a claim, not a capability.
- **In `ctech-dfe`, two enabled companies sharing a tax id must not share a série.**
  A fiscal-domain rule, enforced by the fiscal domain, at enablement.

What separates "I registered it" from "I may issue for it" is the verified
`User ↔ Company` edge. Registration stays cheap and reversible; capability is gated.
This mirrors [ADR 0021's](../../../ctech-billing/docs/adr/0021-platform-organizations-and-companies.md)
"verification gates capability, not existence", now with a home that can enforce it.

## The model

```
Organization (1) ──── (N) Company
                            │
User (N) ──── (M) ──────────┘   the "may act for" edge, verifiable
```

### Company

| Field | Notes |
|---|---|
| `organization_id` | partition; a company never exists outside one |
| `company_id` | UUIDv7, opaque — never the tax id (see below) |
| `tax_id` | canonical on write: mask stripped, letters uppercased. **Not digits** — see below |
| `tax_id_kind` | `cnpj` \| `cpf` |
| `legal_name` | razão social |
| `trade_name` | nome fantasia, optional |
| `source_system` / `source_ref` | migration provenance only, as on Organization |
| `created_at` / `updated_at` | |

**A CNPJ is alphanumeric.** Since the Receita Federal's 2026 change its first twelve
positions may hold letters; only the two check digits stayed numeric. So "digits only" is
no longer the canonical form, and any code that assumes it — a mask, a validator, a
column type, a client parsing the API response — is wrong on a CNPJ issued from now on.
A CPF stayed numeric throughout.

The check digits are verified locally rather than at the registry lookup, because they
are arithmetic and not a fact about the world: a CNPJ issued this morning is unknown to
every public register and must still be accepted. The two documents share the modulus-11
skeleton and nothing else — the CNPJ weights cycle 2..9 from the right, the CPF weights
descend from 10 — and treating them as one sequence still validates most inputs by luck,
which is how that bug survives review.

**`tax_id_kind` is not speculation.** `ctech-dfe` already keys organizations as
`CNPJ_{digits}` *or* `CPF_{digits}` (`api/internal/repositories/organizations.go:16`):
MEI and produtor rural issue under a CPF today. A model with only a CNPJ field would
fail to represent customers who already exist.

**The id is opaque, and the tax id is a unique attribute — not the key.** The DF-e's
present key *is* the CNPJ, which is what makes changing it the expensive half of the
migration. A CNPJ is stable in practice but the record is not: a typo caught after
issuance, or a company re-registered under a different number, would otherwise mean
re-keying every row that references it. Uniqueness within the organization is enforced
by a conditional write on a `(organization_id, tax_id)` lookup row, the same mechanism
`Invitation` already uses for one-invite-per-email.

**No fiscal address, no inscrição estadual, no regime.** Each is state- or
regime-scoped, changes for fiscal reasons, and is validated against rules
`ctech-account` has no business knowing. The line is: *identity* is a fact about the
company that serves everyone; *configuration* is one product knowing how to issue.

### The `User ↔ Company` edge

A row per (company, user) recording that this person may act for this company, plus
its verification state. Two rules:

- **Membership in the organization is necessary but not sufficient.** A viewer in an
  organization does not act for its companies by being there.
- **The edge is verified per person, not per company.** ADR 0021's reason stands: a
  verified boolean on the company means whoever arrives second inherits a verification
  they never passed.

Verification reuses the KYC review surface rather than growing a second one. **The
concrete reuse and its review flow are out of scope here** and get their own spec —
this one records that the edge exists, is keyed this way, and gates capability. What
must not happen is a second review queue.

## What this changes elsewhere

**The handoff spec loses its wart.** [Organization handoff](2026-08-29-organization-handoff.md)
currently says "creating an organization is not creating a company — the DF-e still has
its own step". With a thin Company here, the handoff creates both and returns
`organization_id` **and** `company_id`: the person types the CNPJ once, and the DF-e
asks only for what is its own. That spec's flow, parameters and tests are amended when
this ships, not before.

**`ctech-dfe` re-keys twice, not once.** Its organization PK is `CNPJ_{digits}` and
every fiscal config is a singleton hanging off it (`organization_nfe_configs` and its
siblings are one record per org). Those become one record per **company**. The
organization migration that already ran mapped dfe organizations — which are really
companies — onto platform organizations; this spec is what unfuses them, and the second
migration pass has the same shape and the same `source_system`/`source_ref` idempotency.

**One organization, one company must still read as one thing.** ADR 0021's consequence
holds and gets stricter here: the UI shows a single name until a second company exists.
"Empresa" is a word the product introduces when it becomes true, never at signup. A
person who runs one company must never be asked to understand two nouns to use the
product.

**`ctech-billing` gains a designated billing company** on the organization, and stops
needing the DF-e to know whom it invoices. That field, and what billing copies from it,
belongs to billing's spec.

## The lookup

Registering a company should not mean retyping the Receita Federal. On a CNPJ,
`https://open.cnpja.com/office/:cnpj` fills `legal_name` and `trade_name`.

Three rules:

- **The lookup is a convenience, never a gate.** cnpja being down must not stop a
  registration; the fields stay editable and the form submits without it.
- **Called from the server, not the browser.** A static export calling a third party
  directly hands that party the customer's IP and leaves no audit trail. It also means
  one place to cache.
- **Not scoped to an organization.** `GET /v1.0/companies/lookup`, behind `RequireAuth`
  so it is not an open proxy, but outside `RequireOrgRole`. It reads a public register,
  not organization data, and the create screen needs it *before* an organization
  exists — scoping it to one bought nothing and blocked the caller that needs it most.
- **A failed lookup is not an invalid CNPJ.** The check digit is verified locally; the
  lookup only fills names. Conflating them rejects a valid, newly-issued CNPJ.

On a CPF there is no lookup, and there must be none: `legal_name` is typed. A CPF
holder's name is not ours to fetch from a public registry.

## Tests

- a tax id normalizes (mask stripped) and its check digit is validated, CNPJ and CPF
- the same tax id twice in one organization is refused by the conditional write, with
  the same problem type whichever path raced
- the same tax id in two different organizations is accepted — the accountant case,
  pinned so nobody "fixes" it into a global unique key
- a company cannot be created outside an organization, and a non-member gets the same
  answer as an unknown organization (the existing `RequireOrgRole` rule)
- the `User ↔ Company` edge does not follow from organization membership alone
- a cnpja outage still permits registration with typed names
- a valid CNPJ that cnpja does not know is accepted

## Out of scope, deliberately

- **The verification flow itself** — the document set, the review queue, the states.
  Its own spec; this one only fixes where the edge lives.
- **Anything fiscal.** Repeated from above because it is the thing most likely to be
  added later "just the IE, it's small".
- **Deleting a company.** Fiscal documents reference it and must stay referenceable.
  As with organizations, the reversible actions are the ones that exist.
- **Company-scoped roles.** A person acts for a company or does not; a role ladder
  inside a company is a second permission system, and there is no case for it yet.
