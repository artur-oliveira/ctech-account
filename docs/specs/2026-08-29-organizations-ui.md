# Organizations UI

## Outcome

A signed-in person can see the organizations they belong to, create one, manage its
members and invitations, hand it over, and leave it — at `/account/organizations`, in
the same shell as every other account screen. An invited person can accept an
invitation whether or not they already have a CTech account.

This is the surface for [platform organizations](2026-08-29-platform-organizations.md);
the API it drives is the one built in
[the plan](../plans/2026-08-29-platform-organizations.md), Tasks 1–8. The decision behind
the model is [ctech-billing ADR 0021](../../../ctech-billing/docs/adr/0021-platform-organizations-and-companies.md).

## Why here, and what this is not

Membership lives in `ctech-account`, so the screens that edit membership live here too.
Somebody removing a colleague is not thinking about invoices, and putting the roster in
`ctech-billing`'s console would mean a person with no billing account cannot manage
their own team.

**This is not a tenant switcher.** Nothing in `ctech-account` is scoped to a "current
organization" — every screen here takes the organization from the URL, and the account
screens either side of it are about the person, not the workspace. A switcher is a
`ctech-billing` console concern (its phase 2), and building one here would be chrome for
a context this app does not have.

**There is no delete.** The API exposes none, deliberately: a `ctech-billing` partition
key and, later, a fiscal company hang off `organization_id`. The destructive actions here
are *transfer* and *leave*, and the UI must not imply a third.

## Routes

Production is a static export (`next.config.ts:20`, `output: 'export'`), so there are no
dynamic segments. The organization id travels as a query parameter, exactly as
`/admin/kyc/review` already does.

| Route | Auth | Purpose |
|---|---|---|
| `/account/organizations` | account shell | the list, and the only place one is created |
| `/account/organizations/detail?id={id}` | account shell | one organization: members, invitations, settings |
| `/invite?token={token}` | its own page, outside `/account` | accept an invitation |

`/invite` sits outside the account shell on purpose: the recipient may have no account
yet, and the account layout redirects anyone without a token straight to `/login`,
which would lose the invitation.

A `detail` page opened with a missing or unknown `id` renders the same thing a
non-member sees — see *What a non-member is told*.

## The three states of "which organizations"

`GET /v1.0/organizations` returns `{ organizations: [{ id, display_name, owner_user_id,
role, joined_at }] }`, ordered by name. The list screen has three states and they are
different screens, not one screen with a count:

- **None.** Not an error and not an empty table. A single card explaining what an
  organization is for, with the create form as the only affordance. This is the state a
  brand-new account is in, and it is the state the user hit in production when
  `/console/settings` said *"Esta conta não tem uma organização"* — that message was a
  dead end; this one is the way out of it.
- **One.** The list still renders as a list. Auto-forwarding to the single organization
  would make the create action unreachable and the URL lie about where you are.
- **Several.** The list, each row carrying the caller's own role.

## Screens

### `/account/organizations`

Header block in the house style (`h1` + `text-muted-foreground text-sm mt-1`), create
trigger right-aligned in a `flex items-center justify-between`, exactly as
`/account/api-keys` does.

Rows through `ResponsiveDataList` — stacked cards below `md`, a table at `md+` — with
columns: name (`title: true`, links to the detail page), the caller's role as a `Badge`,
and joined-at. No actions column: everything an organization can do lives on its own
page, and a destructive action in a list row is a misclick waiting for a name it
resembles.

Loading is the inline skeleton (`animate-pulse bg-muted rounded-lg`), never a spinner.
Errors are `<QueryError onRetry={refetch} />`. Empty is the `empty` prop.

Create is a dialog: one required `display_name`, `maxLength={120}` matching the server's
`validate:"required,max=120"`. On success, invalidate `['organizations']` and navigate to
the new organization's detail page — the person who just created a workspace wants to
invite somebody, and returning them to a list of one is a step they did not ask for.

### `/account/organizations/detail?id={id}`

Three tabs (`components/ui/tabs`): **Members**, **Invitations**, **Settings**.

**Members.** `GET /organizations/:id/members`. A `ResponsiveDataList` of user id, role,
joined-at. For an admin and above, each row carries a role `Select` and a remove button;
the owner's row carries neither, and shows the role as static text — the API refuses to
re-role or remove an owner, and rendering a control that always fails is worse than
rendering none. Removal is a `ConfirmDialog variant="destructive"`.

**Invitations.** `GET /organizations/:id/invitations`, admin and above only; a viewer or
member does not see the tab. Each row: address, role, expiry, and a revoke button.

Inviting takes an address and a role (`admin | member | viewer` — never owner; the API
rejects it and the `Select` must not offer it). **`POST` returns the token exactly once,
and nothing e-mails it**: the response is rendered read-only with a copy button, in the
same one-shot-reveal pattern the created API key already uses
(`api-keys/api-key-actions.tsx`). The copied value is the full URL —
`{origin}/invite?token={token}` — because a bare token is something the recipient cannot
act on. Copy shows a `toast.success`; closing the dialog loses the link for good, and the
dialog must say so before it is closed, not after.

> Sending the invitation by e-mail is a later addition, not a blocker: `internal/email`
> exists and the token is already minted server-side. It is out of scope here because it
> changes who holds the secret, and that deserves its own decision rather than riding
> along with a screen.

**Settings.** Rename (admin and above) — an uncontrolled form with
`key={organization.id}` so `defaultValue` resets when the query resolves, the same guard
`profile/page.tsx:50` uses.

Below it, the actions that are hard to undo, visually separated:

- **Transfer ownership** — owner only. A `Select` of existing members (never a free-text
  user id: the API requires an existing membership, and typing an id is how an
  organization gets handed to a stranger). `ConfirmDialog`, whose description names the
  person and states plainly that the caller becomes an admin and cannot undo this alone.
- **Leave** — everyone except the owner. The owner sees, in its place, one sentence
  saying they must transfer the organization first. Not a disabled button: a disabled
  control with no explanation is a question the UI refuses to answer.

## What each role may see and do

The server decides; the UI only chooses what to render. Every affordance below is
already refused by `RequireOrgRole` and the service if it is called anyway, so a
forged role in the client reveals chrome and nothing else — the same boundary
[the admin KYC spec](2026-08-29-admin-kyc-review.md) draws around `support_role`.

| | viewer | member | admin | owner |
|---|---|---|---|---|
| see the organization and its roster | ✓ | ✓ | ✓ | ✓ |
| see the invitations tab | | | ✓ | ✓ |
| invite, revoke an invitation | | | ✓ | ✓ |
| change a role, remove a member | | | ✓ | ✓ |
| rename | | | ✓ | ✓ |
| transfer ownership | | | | ✓ |
| leave | ✓ | ✓ | ✓ | |

### Reach: who may act on whom

Holding admin is not enough on its own. Three further rules apply to every role
change, removal and invitation, and all three exist to stop an accident rather
than an attack:

1. **Nobody changes their own role.** Demoting yourself is one wrong click in a
   column of dropdowns, and the person who did it may no longer hold the role
   needed to undo it.
2. **You may only act on somebody you strictly outrank.** Two admins able to
   edit each other is a disagreement that resolves as a race. This covers
   removal as well as demotion — without it the rule is a formality, because an
   admin refused a demotion could remove the person outright, which is worse.
3. **You may only grant a role you strictly outrank.** An admin promoting
   somebody to admin creates a peer who can then act back on them. **Inviting is
   granting**, so it obeys the same limit: otherwise an admin walks around all of
   the above by inviting a new admin instead of promoting an existing member.

So an admin manages members and viewers, and hands out member and viewer. Only
the owner hands out admin. The owner's own row stays untouchable — ownership
moves through transfer.

Leaving is exempt from rule 1: it removes yourself, and the self rule is about
roles, not about the door. The owner still cannot leave; they transfer first.

`AssignableRoles(callerRole)` on the server and `assignableRoles()` in
`types.ts` are the same function twice, and must stay that way — a dropdown that
offers a choice the server refuses teaches people the product is broken. Both
return nothing below admin: a member outranks a viewer but manages nobody.

A control appears only where it can succeed. Your own row, a peer's row and the
owner's row all render the role as static text rather than a disabled `Select`:
the server refuses each of those, and a dead control is an invitation to try.

## What a non-member is told

`RequireOrgRole` answers the same `403` with the same body for "you are not in this
organization" and "this organization does not exist", so the API cannot be walked to
discover ids. The UI must not undo that: one message for both, and no "not found"
variant. A `detail` page whose query fails with 403 renders that message plus a link
back to the list.

## The invitation, end to end

This is the flow with the most ways to go wrong, and the one worth writing down fully.

1. An admin copies `{origin}/invite?token=…` and sends it however they send things.
2. The recipient opens it. **`/invite` shows nothing about the organization while signed
   out** — not its name, not who invited them. There is no unauthenticated endpoint that
   would reveal it, and adding one would turn a leaked link into a read capability for
   the workspace's name. The page says an invitation is waiting and offers *sign in* and
   *create an account*, both carrying `?continue=/invite?token=…` through the existing
   `safe-redirect` helper.
3. They sign in or register, and land back on `/invite` with the token intact.
4. The page `POST`s `/v1.0/invitations/accept` with the token alone. **The address is
   never in the body** — the server reads it from the account record. A body-supplied
   address would make the token a bearer capability that whoever found the link could
   spend.
5. On success it shows the organization's name and forwards to its detail page.

The failures, and what each must say:

- **E-mail not verified.** The server refuses, because an unverified address is a claim
  rather than evidence. The page must say exactly that and offer *resend verification*
  (`resendVerificationAPI` already exists), then let the person retry without losing the
  token.
- **Invitation invalid, expired, already used, or addressed to somebody else.** The
  server answers all four alike on purpose — each distinction is a hint to whoever is
  guessing. The page says the invitation is no longer valid and offers a link to ask the
  person who sent it for a new one. It must not guess which of the four happened.
- **Already a member.** `409`. Not an error to apologise for: say they are already in,
  and link to the organization.

## The problem types this screen branches on

`accept` answers three refusals with `403`, and the page says something different for
each. It branches on the RFC 7807 `type` and never on `detail`, which is prose that gets
rewritten:

| `type` | what the page does |
|---|---|
| `…/problems/email-not-verified` | says the address must be verified, offers *resend* (`resendVerificationAPI`), keeps the token |
| `…/problems/forbidden` | says the invitation is no longer valid, offers a link to ask for a new one |
| `…/problems/conflict` (409) | says they are already a member, links to the organization |

The verification gate got its own type in `accept`; the type itself already existed
(`apierror.EmailNotVerified`, used by sign-in for exactly this reason — "clients should
offer to resend the verification link"). Reusing it is what makes the two screens behave
alike.

This is not the leak the merged invitation errors avoid. Unknown, expired, already used
and addressed-to-somebody-else stay a single answer with identical `type` **and**
identical `detail`, pinned by a test — each distinction is a hint to whoever is guessing.
Whether your own address is verified is a fact about your own account, already visible on
`/account`.

## Patterns this must follow

Not suggestions — deviating from these makes the screen look like a different product.

- **Components**: shadcn on `@base-ui/react` from `src/components/ui/`. Composition is
  `render={<Button/>}`, never `asChild`. No new dependency, and `@aoctech/ui` is not
  used in this app.
- **Fetching**: `queries.ts` gains `fetchOrganizations`, `fetchOrganization(id)`,
  `fetchOrganizationMembers(id)`, `fetchOrganizationInvitations(id)` — each unwrapping
  its envelope with a `?? []` default, as every existing one does. No `axios` outside
  `queries.ts` / `mutations.ts` / `axios.ts`.
- **Mutating**: `mutations.ts` gains `createOrganizationAPI`, `renameOrganizationAPI`,
  `inviteMemberAPI`, `revokeInvitationAPI`, `setMemberRoleAPI`, `removeMemberAPI`,
  `transferOwnershipAPI`, `acceptInvitationAPI` — the `<verb><Noun>API` convention.
- **Query keys**: literal arrays, kebab-case. `['organizations']`,
  `['organization', id]`, `['organization-members', id]`,
  `['organization-invitations', id]`. Invalidate the exact key on success.
- **Forms**: uncontrolled, `FormData`, native validation. No form library — this repo has
  none, and adding one for four fields is a dependency somebody maintains forever.
- **Errors**: `toast.error(err.response?.data?.detail ?? t('…'))` in `onError`, *and* an
  inline destructive `Alert` from the mutation's error. Both, as `profile/page.tsx` does.
- **Destructive actions**: `ConfirmDialog variant="destructive"`. No typed confirmation —
  this repo has never asked anyone to type a word, and starting here would single out
  these screens as scarier than revoking an API key, which they are not.
- **i18n**: an `organizations.*` namespace, camelCase leaves, `dialog.*` nested for modal
  copy — in **both** `en.json` and `pt-BR.json`. `pt-BR` is the default and the fallback,
  so a missing key surfaces as Portuguese, never as a raw key.
- **Nav**: `nav.organizations` in both locale files, an item in `account-nav.tsx`
  (`Building2` from lucide, no children), and a `ROUTE_TITLES` entry in
  `route-title.tsx` — most specific prefix first, so `/account/organizations/detail`
  does not fall through to the list's title. **The item is always visible**, including
  with zero organizations: the empty state is how one is created, and hiding the entrance
  behind having already gone through it is the bug that made the console unreachable.

## Mock mode

`mock.ts` gains routes for all nine endpoints, over an in-memory
`state.organizations` / `state.members` / `state.invitations`, plus a localStorage
scenario key so the states can be looked at without a backend:

`mock_organizations_seed` — JSON, merged over the default, minimally `{ count, role }`.
The scenarios that must be reachable: **none** (the empty state), **one as owner**,
**several with mixed roles**, and **one as viewer** (the read-only roster, which is the
easiest layout to get wrong because most of the controls vanish).

`POST /v1.0/invitations/accept` in mock returns success for a fixed token, the
verification refusal for a second, and the invalid refusal for anything else — so all
three branches of `/invite` are reachable from the browser.

## Tests

Colocated `*.test.tsx`, vitest + testing-library, English copy (`vitest.setup.ts` forces
`en`). Two are not optional, because they are the two places a mistake is a security
mistake rather than a cosmetic one:

- **`/invite` never sends an address.** Assert the `accept` call body carries the token
  and nothing else. This is the test that stops somebody "helpfully" adding the e-mail
  field back when a server error mentions an address.
- **A viewer sees no controls.** Render the detail page with a viewer role and assert
  the invite, role, remove and transfer affordances are absent — not disabled, absent.

Each `it()` names the behaviour it locks in, as `confirm-dialog.test.tsx` does.

## Cross-project impact

`ctech-billing`'s console reads the same `GET /v1.0/organizations` when it grows a
switcher (its phase 2), which is why that response carries the display name and the
caller's role together rather than ids alone. Nothing in `ctech-dfe` reads any of this
yet; that is a later migration with its own spec.

## Out of scope, deliberately

- Sending invitations by e-mail (above).
- The company claim and its KYB queue — the model's phase 3.
- An organization switcher, and any per-organization context in this app.
- Deleting an organization. The API has none and this screen must not imply one.
