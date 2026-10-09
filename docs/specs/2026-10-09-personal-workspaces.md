# Personal workspaces

Status: **Design approved, not implemented** · 2026-10-09 · Consumer: `ctech-billing`
(`ctech-billing/docs/specs/2026-10-09-shared-spaces-design.md`, ADR 0027 there) · Extends
[platform organizations](2026-08-29-platform-organizations.md),
[organizations UI](2026-08-29-organizations-ui.md) and
[organization handoff](2026-08-29-organization-handoff.md)

## Outcome

A person can create a lightweight **space** — for their household, a shared budget, a side project —
and invite people to it at two access levels, with no company, no CNPJ and none of the vocabulary of an
organization. ctech-billing uses it to share finance spaces. Since an organization is already a
**workspace** here (ADR 0021 in ctech-billing), a space is the same record with a different kind, not a
new model.

This repository stays the only writer of tenancy: products redirect here to create a space and to
manage its people, exactly as the DF-e does for organizations.

## 1. `kind` on the workspace

`Organization` gains `Kind string` (`dynamodbav:"kind,omitempty"`):

| Kind | Meaning |
|---|---|
| `organization` | today's organization. An absent field reads as this, so **nothing is migrated**. |
| `personal` | a space. **Never links a company**: the company routes refuse a `personal` workspace with 409. |

The kind is set at creation and never changes. Converting a space into an organization later is a new
decision, not a field update.

## 2. Roles on `personal`

| Shown as | Role | May |
|---|---|---|
| Dono | `owner` | everything, including inviting, changing roles, removing, renaming, transferring |
| Acesso total | `member` | belong; what that allows is the product's decision (ADR 0023) |
| Leitura | `viewer` | belong; what that allows is the product's decision |

- `admin` is **refused** on a `personal` workspace: an invitation or role change naming it answers
  422. A space has two levels and the ladder must not quietly grow a third.
- **Only the owner** invites, changes roles and removes on a `personal` workspace. The organization
  rule (an `admin` may) does not apply, because there is no admin.
- Transferring ownership on a `personal` workspace is allowed only to a `member`. A `viewer` is
  promoted first.

These are checks in `organization.Service`, keyed on the workspace's kind, beside the existing
`require` calls.

## 3. Handoffs

Both follow every rule of the [organization handoff](2026-08-29-organization-handoff.md), unchanged:
the client must be **first-party**, `return_to` must match one of its registered origins
(`IsRegisteredOrigin`), `state` is echoed and never parsed (512 bytes max), and **no token ever travels
on `return_to`**. A failed validation is a 422 page with a link into the account, never a redirect.

### 3.1 Create — `/account/spaces/new?client_id&return_to&state`

- A screen with **the name only**, and a banner naming the product that sent the person ("Criando um
  espaço para o Billing").
- Creates a workspace with `kind: personal`, owner = the signed-in user.
- Success: `return_to?organization_id={id}&state=…`. Cancel: `return_to?cancelled=1&state=…`.
- Validation: `GET /v1.0/spaces/handoff?client_id&return_to`, the same shape as
  `/v1.0/organizations/handoff` (it may be the same handler with a kind parameter).

### 3.2 People — `/account/spaces/{id}/people?client_id&return_to&state`

- The roster, invitations (with the existing e-mail-verified, hashed-token flow), role changes
  between *Acesso total* and *Leitura*, removal and ownership transfer.
- Only the owner may act. Anyone else gets the read-only roster, or 404 if not a member.
- "Voltar ao {product}" returns to `return_to?state=…`.

## 4. Internal routes answer `kind`

| Route | Today | Adds |
|---|---|---|
| `GET /internal/organizations/:organization_id/members/:user_id` | `{member, role}` | `kind` |
| `GET /internal/users/:user_id/organizations` | `[{id, display_name, role}]` | `kind` per item |

Scopes are unchanged. A non-member still answers `{member:false}` with no kind, so the response
reveals nothing about which workspaces exist.

## 5. Where `personal` must not appear

- **The "Organizações" list** in the account UI and `GET /v1.0/organizations` show
  `kind = organization` only. Spaces get their own section, "Espaços".
- **The DF-e** (and any product that lists organizations for a fiscal purpose) filters
  `kind = personal`, by reading `kind` on the internal route. This is a ctech-dfe change, tracked
  there.
- **The organization handoff** (`/account/organizations/new`) never creates a `personal` workspace.

## 6. Account deletion

No new rule — the [deletion spec](2026-10-06-account-deletion-ctech-account.md) applies to both kinds:
- a workspace whose only member is the person is frozen into `organizations[]`, erased, and listed on
  `user.erase` (ctech-billing purges its finance data);
- a workspace with other members blocks deletion until ownership is transferred. On a `personal`
  workspace, the consequences screen offers transfer to a `member` only (§ 2).

**Deleting a space** (as opposed to a person) does not exist, as it does not exist for organizations.
It needs a per-workspace purge across products and is a later spec.

## 7. Plan limits (later)

ctech-billing will sell limits on spaces per owner and people per space. They are enforced **here**, at
creation and at invitation — the two writes — by asking ctech-billing for the owner's entitlement. Not
in this spec: until the plans spec exists, nothing is limited.

## 8. Tests

- An absent `kind` reads as `organization`; existing organizations behave exactly as before.
- A company cannot be linked to a `personal` workspace.
- `admin` is refused on `personal`; a `member` or `viewer` cannot invite, change roles or remove.
- Transfer on `personal` is refused to a `viewer`.
- Both handoffs refuse a third-party client, an unregistered `return_to`, and put no token on the
  redirect.
- Both internal routes return `kind`; a non-member returns no kind.
- `GET /v1.0/organizations` excludes `personal`.

## Deploy order

This repository first. ctech-billing treats a missing `kind` as `organization`, the direction that
grants fewer verbs, so deploying billing first is safe but leaves spaces unusable until this ships.
