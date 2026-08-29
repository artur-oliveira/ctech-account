# Support Tickets (E-mail via SES) — Design Spec

## 2026-08-28 realtime and agent-workspace amendment

The asynchronous v1 described below remains the durable HTTP/e-mail baseline. The following rules supersede its
earlier reopen and WebSocket deferrals:

- `GET /v1.0/support/tickets/:id/ws` uses binary `proto/support.proto` frames. The first frame authenticates with an
  access JWT or the opaque anonymous ticket token; the service then verifies ticket ownership or the caller's live
  `support_role`. Agents also subscribe to a separate internal channel. Valkey Pub/Sub fans events across ASG
  instances; the HTTP thread remains authoritative after reconnect.
- Closing is a terminal lifecycle transition. Users and agents cannot reply, change status, escalate, or add notes to
  a closed ticket. A later question starts a new ticket. The admin UI uses an irreversible-action confirmation.
- Internal notes are stored under the ticket's `NOTE_` sort-key namespace and are returned only by the admin detail
  endpoint. Escalation is explicit: `none`, `specialist`, or `engineering`.
- `{env}_account_support_metrics` stores pre-aggregated day/month/year/all-time buckets. Ticket creation atomically
  increments created volume and its product counter; terminal closure atomically increments resolution count and
  total resolution seconds. Product distribution therefore includes every created ticket, not only resolved ones.
- New admin routes: `POST .../:id/notes`, `PUT .../:id/escalation`, and `GET /v1.0/admin/support/metrics`.

**Date:** 2026-08-22 **Status:** Implemented (including the 2026-08-28 amendment) **Cross-project impact:** None. No changes to JWT signing,
JWKS, OAuth flows, or token claims. `ctech-dfe` and `ctech-wallet` are unaffected. Purely additive within
`ctech-account` (api + ui). No `ctech-cdk` shared-construct changes expected — this repo's own `cdk/` gets one new
DynamoDB table + two SSM parameters.

---

## 1. Problem

Support currently has no structured channel. Zendesk is out of budget. The goal is a minimal, self-hosted support-ticket
system:

- Public submission form (logged-in or anonymous) → stored ticket, notified by e-mail via SES.
- Bot protection via Cloudflare Turnstile.
- History retained (ticket + full message thread) per ticket.
- Admin/agent UI (in the existing `ui/` app, under `/admin`) to view and reply to tickets — replies go out over SES
  `From: support@aoctech.app`, never through a personal inbox.
- Users can see their own ticket history when logged in; anonymous submitters get a tokenized link.

## 2. Non-Goals (explicitly deferred)

These were raised during brainstorming and are real future work, but are **out of scope for this spec** — each needs its
own design when picked up:

- **Real-time chat (WebSocket, protobuf, presence)** — the poker-style live-connection model. v1 is async or ticket,
  like e-mail support anywhere else. Revisit if support volume/latency needs justify the infra cost.
- **Inbound e-mail ingestion (SES receiving → parse → append to ticket)** — a user replying directly to the notification
  e-mail today lands in the existing Cloudflare-routed personal inbox, not in the ticket. The ticket's own e-mails
  always tell the recipient to reply via the portal link. Building a parser for inbound SES ("reply above this line") is
  a natural v2 once volume makes manual cross-referencing painful.
- **Ticket claim/assignment** — v1 is an open pool; any `support_role` holder can answer any ticket. Claim semantics
  only pay off with more than one active agent.
- **Multi-organization RBAC** — `support_role` (see §4.2) is deliberately scoped to this feature and is not a general
  permissions system. When DF-e/Billing need per-organization roles, that will be a membership table
  (`org_id + user_id + role`), not a reuse of this field.
- **Presence audit events** ("agent entrou/saiu da conversa") — these are meaningful once there's a live connection to
  enter/leave. v1's audit trail is a system-authored message per lifecycle event (created, replied, status changed,
  closed), not connection presence.

## 3. Data Model

### 3.1 New DynamoDB table: `{env}_account_support_tickets`

Single-table item-collection design, same shape as `account_sessions`:

| Attribute | Type | Notes                                                                     |
|-----------|------|---------------------------------------------------------------------------|
| `pk`      | S    | `TICKET_{ulid}`                                                           |
| `sk`      | S    | `META` for the ticket record itself, `MSG_{rfc3339nano}` for each message |

**Ticket item (`sk = META`):**

| Field                                     | Type         | Notes                                                                                 |
|-------------------------------------------|--------------|---------------------------------------------------------------------------------------|
| `ticket_number`                           | N            | Human-readable sequential number (see §3.2), used in e-mail subjects and UI (`#1042`) |
| `user_id`                                 | S, omitempty | Set when submitted while authenticated                                                |
| `anonymous_email`                         | S, omitempty | Set when submitted anonymously — required in that case                                |
| `anonymous_token`                         | S, omitempty | Opaque random token; grants read/reply access to this ticket via link, no login       |
| `subject_category`                        | S            | Enum, see §3.3                                                                        |
| `subject_other`                           | S, omitempty | Free text, required when `subject_category = "other"`                                 |
| `priority`                                | S            | Enum: `low` \| `medium` \| `high` \| `urgent` \| `critical`. Default `low`            |
| `status`                                  | S            | Enum: `open` \| `answered` \| `closed`                                                |
| `created_at` / `updated_at` / `closed_at` | S (RFC3339)  | `closed_at` empty until closed                                                        |
| `last_message_at`                         | S (RFC3339)  | Used for sorting/inbox views                                                          |
| `root_ses_message_id`                     | S            | `Message-ID` of the first (confirmation) e-mail — the thread root for `References`    |
| `last_ses_message_id`                     | S            | `Message-ID` of the most recent outbound e-mail — the immediate `In-Reply-To`         |
| `nps_score`                               | N, omitempty | 1–5, set once after `closed`                                                          |
| `nps_message`                             | S, omitempty | Required, min 15 chars (trimmed) when `nps_score <= 3` — see §3.6. Optional otherwise. |
| `nps_requested_at`                        | S, omitempty | Set when the NPS e-mail goes out (dedupes double sends)                               |

**Message item (`sk = MSG_{ts}`):**

| Field            | Type         | Notes                                                                                               |
|------------------|--------------|-----------------------------------------------------------------------------------------------------|
| `author_type`    | S            | `user` \| `agent` \| `system`                                                                       |
| `author_id`      | S, omitempty | `user_id` or agent's `user_id`; empty for `system`                                                  |
| `body`           | S            | Plain text, rendered escaped in both e-mail and UI                                                  |
| `created_at`     | S (RFC3339)  |                                                                                                     |
| `ses_message_id` | S, omitempty | Set on messages that triggered an outbound e-mail (`user`-anonymous confirmation excluded — see §5) |

`system` messages carry a fixed `body` drawn from a small set of event templates (ticket created, status changed to X,
ticket closed, NPS submitted) — this is the audit trail (§2, last bullet), folded into the existing message stream
instead of a second table.

### 3.2 Ticket numbering

A second, single-item counter (`pk = "COUNTER", sk = "TICKET_NUMBER"`, attribute `value`) in the same table, incremented
via `UpdateItem` with `ADD value :one` (atomic, no read-modify-write race — same pattern as
`internal/database.ConditionalUpdate` already requires elsewhere in this codebase).

### 3.3 `subject_category` enum

Fixed catalog, product-aligned (per user decision), rendered as the UI select plus a fixed "Outros":

```
account   — Conta / Login
kyc       — KYC / Verificação
wallet    — Wallet
dfe       — DF-e
billing   — Billing
poker     — Poker
other     — Outros (requires subject_other)
```

Validated server-side against this list — never free-typed, same principle as the existing OAuth scope catalog
(`GET /v1.0/scopes`).

### 3.4 GSIs

| Index              | PK                | SK                | Purpose                                                                                |
|--------------------|-------------------|-------------------|----------------------------------------------------------------------------------------|
| `status-index`     | `status`          | `last_message_at` | Admin queue: list by status, newest activity first                                     |
| `user-index`       | `user_id`         | `created_at`      | "Meus tickets" (logged-in users)                                                       |
| `anon-token-index` | `anonymous_token` | —                 | Sparse index (only anonymous tickets have this attribute); resolves the tokenized link |
| `ticket-number-index` | `ticket_number` | —                 | Look up a ticket by its human-readable number (agent references it verbally/by phone, needs a fast lookup) |

All on-demand billing, `ALL` projection — consistent with the other tables in `dynamodb-stack.ts`.

### 3.5 Input Validation & Sanitization

Every free-text field this feature accepts (`body`, `subject_other`, `nps_message`, and any future
message-thread text) goes through the same rules, enforced server-side in the `support.Service`
layer (never trusted from the client):

- **Trim** leading/trailing whitespace before any length check or persistence.
- **Length bounds**: `subject_other` 3–120 chars, ticket/reply `body` 15–4000 chars, `nps_message`
  15–1000 chars (only enforced when required — see below). Bounds match common support-form
  conventions (short enough to stop pastes-of-everything, long enough to require an actual sentence).
- **Low-signal rejection**: reject input that is a single repeated character or punctuation run
  (e.g. `aaaaaaaaaaaaaaaa`, `............`), and input with no alphabetic content at all. Implemented
  as one small validator — collapse runs of 4+ identical characters and check what remains has at
  least a minimum ratio of letters — not a heavyweight profanity/spam model. This is a bot/low-effort
  filter, not a content-quality judge.
- This validator is written as a standalone, reusable function (`internal/validate/freetext.go` or
  similar, exposed as a `go-playground/validator` custom tag) precisely because the user flagged it
  as a **global** concern — nothing today in this codebase applies length/trim/junk-pattern rules to
  free-text input, and this is the first feature to need it. Once written here, it is available to
  register on other free-text fields (e.g. KYC rejection notes, OAuth client names) without
  duplicating the logic — but retrofitting existing fields is out of scope for this change.

`nps_message` additionally becomes **required** (subject to the same length rule) whenever
`nps_score <= 3` — a low score with no explanation is not actionable. The `POST
/v1.0/support/tickets/:id/nps` handler enforces this conditionally, not via a blanket
required-field tag.

### 3.6 `account_users` addition

New attribute `support_role` on the existing user item: `""` (default, absent) \| `"agent"` \|
`"manager"` \| `"admin"`. No new table, no self-service write path — see §4.2.

## 4. Backend (`api/`)

New domain package `internal/domain/support/` — `model.go`, `repository.go`, `service.go`,
`service_test.go` — following the existing `session`/`apikey` layering (`handler → service →
repository`, repository interface injected).

### 4.1 Turnstile

Server-side `siteverify` call on ticket creation only (the one unauthenticated write in this feature). Implemented per
the `turnstile-spin` skill during the implementation phase: new config
`TURNSTILE_SECRET_KEY` (SSM `/ctech-account/{env}/turnstile-secret-key`), verification helper in a new
`internal/turnstile` package, called from the ticket-creation handler before touching the repository. A failed
verification returns `apierror.ValidationFailed` (422), not a generic 400 — same error-shape convention as the rest of
the API.

### 4.2 `support_role` provisioning

New CLI `cmd/supportrole` (same shape as `cmd/kyc`/`cmd/createclient`):

```bash
go run ./cmd/supportrole set <user_id> -role agent|manager|admin
go run ./cmd/supportrole revoke <user_id>
go run ./cmd/supportrole list
```

No HTTP endpoint sets or reads this outside `GET /v1.0/account/profile`, which starts returning
`support_role` (empty string when absent) so the UI/middleware can gate on it. This mirrors how
`terms_pending`/`has_password` already ride on the profile response.

### 4.3 New middleware: `RequireSupportRole`

`internal/middleware/support.go` — `RequireSupportRole(userSvc, minRole)` runs after `RequireAuth`, reads the caller's
`user_id` (`middleware.GetUserID(c)`), loads the user via `userSvc.GetByID`, and checks `support_role` against a fixed
ordering (`agent < manager < admin`). Rejects with `apierror.Forbidden` on a role too low or absent, `apierror.ServerError`
if the lookup fails.

This deliberately does **not** add `support_role` as a JWT claim. `crypto.JWTService.SignAccessToken` is a Critical Area
(`api/CLAUDE.md`) with call sites in every login/refresh/passkey/social-callback path — plumbing a new claim through all
of them to save one DynamoDB read on the handful of admin-only routes this feature adds is not a good trade. A plain
`GetByID` per admin request is the simpler, lower-blast-radius choice, and admin traffic volume makes the extra read
free in practice.

### 4.4 API routes

**Public** (`OptionalAuth` — works with or without a bearer token):

| Method | Path                              | Notes                                                                                                                                                                                                                                                                                                                                                                                                                            |
|--------|-----------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `POST` | `/v1.0/support/tickets`           | Body: `{subject_category, subject_other?, body, priority?, email?, turnstile_token}`. `email` required and validated when unauthenticated; ignored (uses account e-mail) when authenticated. `subject_other`/`body` go through the trim/length/junk-pattern validator (§3.5). Always creates — no dedup with existing enumeration-avoidance pattern needed here (this isn't an identity-guessing surface). Returns `{ticket_id, ticket_number, anonymous_token?}` — token only present for anonymous submissions. |
| `GET`  | `/v1.0/support/tickets/:id`       | Auth: bearer owning the ticket, **or** `?token=` matching `anonymous_token`. Returns ticket + message list (system messages included, rendered as timeline entries).                                                                                                                                                                                                                                                             |
| `POST` | `/v1.0/support/tickets/:id/reply` | Same auth as above. Appends an `author_type=user` message. Does **not** trigger an e-mail (the user is already looking at the portal) — only agent replies and status-close events send mail. If the ticket was `closed`, this reopens it to `open` (a system message records the reopen) — `answered` tickets stay `answered`.                                                                                                  |
| `POST` | `/v1.0/support/tickets/:id/nps`   | Same auth as above, only accepted when `status=closed` and `nps_score` unset. Body `{score: 1-5, message?}` — `message` required (§3.5 length/junk rules) when `score <= 3`.                                                                                                                                                                                                                                                                                                                               |

**Authenticated only:**

| Method | Path                            | Notes                                                                                       |
|--------|---------------------------------|---------------------------------------------------------------------------------------------|
| `GET`  | `/v1.0/account/support/tickets` | Lists the caller's own tickets via `user-index`, cursor-paginated like `/account/activity`. |

**Admin** (`RequireAuth` + `RequireSupportRole("agent")`):

| Method | Path                                     | Notes                                                                                        |
|--------|------------------------------------------|----------------------------------------------------------------------------------------------|
| `GET`  | `/v1.0/admin/support/tickets`            | Query params `?status=&priority=&category=&cursor=`. Backed by `status-index`.               |
| `GET`  | `/v1.0/admin/support/tickets/:id`        | Full ticket + thread, no ownership check beyond the role gate.                               |
| `POST` | `/v1.0/admin/support/tickets/:id/reply`  | Appends `author_type=agent` message, sends the outbound e-mail (§5), sets `status=answered`. |
| `PUT`  | `/v1.0/admin/support/tickets/:id/status` | Body `{status}`. Transitioning to `closed` triggers the NPS e-mail (§5) if not already sent. |

All request bodies validated via the existing `validate.Struct` singleton; all errors via
`apierror`/`problem.Send`. No route here touches DynamoDB directly from the handler layer — handlers call one
`support.Service` method each, per the mandatory layering rule.

## 5. E-mail (SES)

Extends `internal/email` (currently `SendEmail`/`Simple` content only — see `ses.go`). Threading requires raw MIME with
explicit headers, so this feature adds a second send path using
`sesv2.SendEmailInput{Content: &sestypes.EmailContent{Raw: &sestypes.RawMessage{Data: ...}}}`
alongside the existing `Simple` path — the existing four `Send*Email` methods are untouched.

New `Client` methods, sharing the existing `emailLayout`/`ctaButton` helpers (§ses.go) for visual
consistency with the verification/reset/new-device e-mails already sent by this service:

- `SendTicketConfirmationEmail(ctx, to, ticketNumber, subject, portalLink) (messageID string, err error)` — first
  e-mail, no `In-Reply-To`. Its returned `Message-ID` becomes `root_ses_message_id` **and**
  `last_ses_message_id` on the ticket. Content (heading → body → CTA, matching the existing template
  shape):
  - Heading: "Seu ticket #{ticketNumber} foi criado"
  - Body: "Um agente em breve entrará em contato para responder sua dúvida." plus a one-line
    recap of the submitted subject/category.
  - CTA button: "Acompanhar ticket" → `portalLink`, with the existing `ctaButton` helper's plain-text
    fallback line ("Clique neste link se não estiver vendo o botão: {portalLink}") for clients that
    strip button styling.
- `SendTicketReplyEmail(ctx, to, ticketNumber, subject, agentBody, inReplyTo, references, portalLink) (messageID string, err error)` —
  sets `In-Reply-To: <inReplyTo>` and `References: <references>` (accumulated chain), subject
  `Re: [Ticket #{n}] {subject}`. Body: the agent's message plus the same "Acompanhar ticket" CTA.
  Updates `last_ses_message_id`.
- `SendTicketNPSEmail(ctx, to, ticketNumber, npsLink, inReplyTo, references)` — same threading, sent once on close,
  CTA "Avaliar atendimento" → `npsLink`.

`SendEmailInput.Content.Raw.Data` is a full MIME message (`From`, `To`, `Subject`, `In-Reply-To`,
`References`, `Content-Type: text/html`, body) built with `net/mail`/`mime` — SES parses `From`
from the raw message in this mode, so `FromEmailAddress` on the input is omitted and the `From:`
header carries `support@aoctech.app` (reuses the existing `FROM_EMAIL` config value; no new env var needed here —
confirm `FROM_EMAIL` is set to an address the SES-verified domain covers, or add a dedicated `SUPPORT_FROM_EMAIL` if the
two must differ in an environment).

`SendEmail` in `sesv2` returns `MessageId` in its output for both `Simple` and `Raw` content — that's what gets
persisted as `ses_message_id`.

No change to the existing inbound routing (SES → Cloudflare → personal inbox) — that stays exactly as-is and is
unrelated to this feature (see §2 non-goals).

## 6. Frontend (`ui/`)

Every screen below is built/reviewed through the `impeccable` skill (design direction, hierarchy,
accessibility, empty/error states) rather than freehand — this is a brand-new surface (public form,
ticket thread, admin queue), not an edit to an existing screen, so it gets the same design pass the
rest of the account UI already had.

New routes, following the existing Next.js App Router + BFF Route Handler / Server Action structure already used for
`/account/*`:

- `/support` — public ticket form. Fields: `subject_category` select (+ "Outros" reveals a text input), `body` textarea,
  `priority` select (default "Baixa"), Turnstile widget, submit. If a session exists, `email` is omitted from the
  payload and the field is hidden; otherwise an e-mail input is shown and required.
- `/account/support` — "Meus tickets" list (auth-gated by the existing `proxy.ts` pattern), links into
  `/support/ticket?id=:id`.
- `/support/ticket?id=:id` — thread view. Reads `&token=` for anonymous access (stored client-side only for the duration of
  the page — no cookie), or relies on the session for logged-in access. Shows the message timeline (including system
  events) and a reply box for the ticket owner (not for closing — only agents change status).
- `/admin/support` — agent queue: filters (status/priority/category), list sorted by
  `last_message_at`. Gated by `support_role` from `GET /v1.0/account/profile` (redirect to `/account`
  if absent, same pattern `proxy.ts` uses for missing auth).
- `/admin/support/[id]` — thread view + reply box + status `<select>`.

i18n strings (en + pt-BR) added under a new `support` namespace, matching the existing
`forgotPassword`/`resetPassword` precedent.

## 7. Testing

Per the mandatory testing table in `api/CLAUDE.md`:

- `internal/domain/support/service_test.go` — status transitions (`open → answered → closed`, reopen-on-user-reply),
  numbering, threading-ID chaining (each reply's `references` includes every prior `Message-ID`), Turnstile-failure
  path, anonymous-token generation/validation, NPS single-submission guard, NPS-message-required-when-score-≤3.
- `internal/validate/freetext_test.go` (or wherever §3.5's validator lands) — trim, length bounds, repeated-character
  rejection (`aaaaaaaaaaaaaaaa`, `............`), and the letter-ratio floor, each with a table of pass/fail cases.
- `internal/handler/support_test.go` — full HTTP flow for every route in §4.4 against the in-memory repository mock
  (extend `testhelpers_test.go`), including the anonymous-token auth path and the
  `RequireSupportRole` rejection path.
- `internal/email/ses_test.go` — extend with the three new `Send*` methods, asserting the raw MIME carries the expected
  `In-Reply-To`/`References`/`Subject` headers (mirrors the existing assertions on the `Simple` sends).

## 8. Documentation Updates (same change, per repo policy)

- `README.md` — new endpoint rows (§4.4 table), `support_role` in the profile response description, new config vars
  (`TURNSTILE_SECRET_KEY`, and `SUPPORT_FROM_EMAIL` if introduced), new DynamoDB table in the table count/list,
  `cmd/supportrole` alongside the other CLI tools section.
- `PLAN.md` — new sprint entry for this feature.
- `cdk/lib/dynamodb-stack.ts` doc comment / this spec is the design record for the new table and its three GSIs.

## 9. Open Question for Implementation Time

`FROM_EMAIL` vs a dedicated `SUPPORT_FROM_EMAIL` (§5) — depends on whether the existing SES-verified sending identity is
a domain (`aoctech.app`, any local part works) or a single verified address. Check at implementation time; if
domain-verified, no new env var needed and `support@aoctech.app` is used literally.
