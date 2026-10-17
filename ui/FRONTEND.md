# Frontend — `ctech-account` (ui/)

Next.js 16 **static-export SPA** (no server, no Server Actions / Route Handlers —
see `ui/CLAUDE.md` and `next.config.ts` `output:'export'`). React 19, TanStack
Query, Shadcn 4 / Base UI, `@aoctech/auth-client` for the OAuth/PKCE dance.
Anchored to the code as of 2026-07-20.

## Build / routing shape
- Every page is a Client Component (`'use client'`). Data flows through
  `lib/queries.ts` (reads) and `lib/mutations.ts` (writes), both called via
  TanStack Query from `lib/axios.ts`'s shared `api` instance. No raw `fetch`/`axios`
  outside those two files (per `ui/CLAUDE.md`).
- `next.config.ts`: in dev, `rewrites()` proxies `/v1.0/*` and `/.well-known/*`
  → `DEV_API_ORIGIN` (default `http://localhost:8001`). Deployed builds do not:
  the export is served by **Cloudflare Workers Static Assets** and the browser
  calls `NEXT_PUBLIC_API_URL` (`https://accounts-api.aoctech.app`) directly, so
  **CORS applies**. The auth cookies survive it — `ctech_rt`/`ctech_auth` are
  `SameSite=Lax` and the two hosts share `aoctech.app`, so the call is
  cross-origin but same-site; `withCredentials: true` in `lib/axios.ts` and
  `AllowCredentials` on the API are both load-bearing.
- **`/.well-known/*` is no longer served on the app host.** OIDC discovery is at
  `https://accounts-api.aoctech.app/.well-known/openid-configuration`. The SPA
  never read it; only external OIDC clients notice.
- Mock mode (`NEXT_PUBLIC_MOCK_API=true`): a custom axios adapter in `lib/mock.ts`
  answers every call; `startOAuthFlow` short-circuits to a fake token.

## Routes / pages (`app/`)
Top-level:
- `/` landing (`app/page.tsx`); `/login`, `/login/mfa`, `/login/callback`,
  `/register`, `/register/verify`, `/forgot-password`, `/reset-password`,
  `/verify-email`, `/consent`, `/accept-terms`.
- `/account/*` — **guarded** account hub: `profile`, `security` (+`security/totp`,
  `security/passkeys`), `sessions`, `api-keys`, `oauth-clients`,
  `connected-apps`, `activity`, `identity` (KYC), `delete` (account deletion).
- `/account/delete` (entry point: `DeleteAccountCard` at the bottom of `/account/profile`): explains the
  consequences, takes the typed phrase `EXCLUIR MINHA CONTA` (`DELETION_PHRASE`, verbatim in every
  language) and, when `profile.has_password`, the password (always asked then: R14 keeps the page
  independent of token freshness and never triggers the step-up dialog). `409 deletion-blocked`
  renders `blockers[]` with the copy at `deletion.blockers.<code>` (unknown codes fall back to a
  generic line naming the code) and the participant's `action_url` as "Resolve"; `503` means a CTech
  product could not be checked, try again. Every participant blocker code needs its line in both locales
  (billing's: `billing.invoice_open`, `billing.invoice_uncollectible`, `billing.merchant_tenant`).
- `/account-deletion/confirm?request=&token=` and `/account-deletion/cancel?request=&token=` (public,
  opened from the e-mail links): both are `DeletionLinkPage` and **act only when the button is
  pressed** (a POST), never on load, so e-mail scanners that prefetch links cannot confirm or cancel
  a deletion. Cancel distinguishes `409` ("can no longer be cancelled") from an invalid link. Confirm
  clears the in-memory auth state: every session was just revoked.
- `/login` shows `errors.accountPendingDeletion` for the `account-pending-deletion` problem (password
  and passkey) and for the Google redirect `?error=account_pending_deletion`, instead of the
  unverified-e-mail resend flow.
- `/account/oauth-clients?new=1&scopes=a,b,c` is a cross-repo link contract (first caller:
  ctech-billing's console credentials page). `new=1` opens the "new client" dialog; `scopes` replaces
  the default `openid profile email` with exactly the listed scopes, keeping only scope-shaped tokens
  (`resource:…`, so identity scopes cannot be injected), deduplicated, at most 32
  (`app/account/oauth-clients/prefill.ts`). The server re-validates every scope on create, so the link
  is a convenience, never authority. Closing the dialog `router.replace`s the bare path, so reopening
  starts from the defaults and Back does not reopen it.
- Static/legal: `/terms`, `/terms/v1`, `/terms/v2`, `/privacy`, `/privacy/v1`,
  `/privacy/v2`, `/privacy/v3`, `/cookies`, `/legal`, `/kyc-policy`, `/data-processing`,
  `/security-policy`, `/acceptable-use`, `/responsible-disclosure`,
  `/transparency`, `/products/*`.

## Layouts
- `app/layout.tsx` (root): wraps the tree in `QueryProvider` (which mounts
  `AuthInitializer`) and `I18nProvider`. No auth logic here.
- `app/account/layout.tsx`: the **client-side auth guard**. While
  `!isInitialized || !accessToken` it renders a loading state; once initialized
  with no token it pushes to `/login?continue=<path>` (`account/layout.tsx:24`).
  It fetches the profile (`useQuery(['profile'], fetchProfile)`), and if
  `user.terms_pending` is set it renders `<TermsGate>` instead of the account UI.
  Renders `<StepUpDialog/>` once at the bottom of the account shell.

## Providers
- **`QueryProvider`** (`providers/query-provider.tsx`) — TanStack Query client
  (staleTime 30 s, retry 1). Mounts `AuthInitializer`.
- **`AuthInitializer`** (inside the provider) — boot-time auth bootstrap (below).
- **`I18nProvider`** (`providers/i18n-provider.tsx`) — react-i18next (en/pt).
- There is **no** dedicated "auth provider" component. Auth state lives in the
  Zustand store `useAuthStore` (`store/auth.ts`): `{ accessToken, isInitialized,
  setAccessToken, clearAuth, setInitialized }`. The axios interceptor reads/writes
  it directly.

## Stores / hooks
- `store/auth.ts` — in-memory access token (lost on hard refresh, re-derived).
- `store/step-up.ts` — bridges the axios 403 step-up interceptor and the dialog:
  `request()` opens the dialog and returns a promise that settles on `succeed()`
  (retry original request) or `cancel()` (reject).
- `hooks/use-redirect-if-authenticated.ts` — bounces already-authed users off
  `/login`, `/register`, etc.
- `hooks/use-session-item.ts` — small `sessionStorage` helper (MFA token handoff).

## Key components
- `components/step-up-dialog.tsx` — re-auth modal opened by the interceptor.
- `components/terms-gate.tsx` — blocks the account area until ToS/Privacy accepted.
- `components/google-sign-in-button.tsx` — kicks off `GET /v1.0/auth/google`.
- `components/account-nav.tsx`, `components/account-mobile-nav.tsx`,
  `components/user-menu.tsx`, `components/language-switcher.tsx`.

---

## Auth flow (detailed)

### 0. Boot — `AuthInitializer` (`providers/query-provider.tsx:35`)
1. If the JS-readable **`ctech_auth`** hint cookie is absent → no session to
   refresh → `clearAuth()` + `setInitialized()`, done.
2. Otherwise call **`oauthClient.refresh()`** (guarded + single-flight inside
   `@aoctech/auth-client`, so concurrent 401s / a boot refresh never fire duplicate
   `/token` calls). On success → `setAccessToken(result.accessToken)`.
3. On refresh failure: `clearAuth()` + `clearAuthHint()`. If the current path is
   **not** an auth page (`/login`, `/register`, `/forgot-password`,
   `/reset-password`, `/verify-email`, `/consent`, `/accept-terms`) →
   `startOAuthFlow(path)` to bootstrap tokens (otherwise a dead session would loop).

### 1. Login — `/login` (`app/login/page.tsx`)
- `POST /v1.0/auth/login` `{ email, password }` via `api`.
- If `requires_mfa` + `mfa_token`: stash `mfa_token` / `mfa_methods` /
  `continue` in `sessionStorage` and navigate to `/login/mfa`.
- Else (no MFA enrolled) → `startOAuthFlow(continueURL)` to begin the OAuth round-trip
  (so the SPA gets a proper bearer token, not just a session cookie).
- Passkey login: `beginPasskeyAuthAPI` → `buildAssertionCredential` →
  `completePasskeyAuthAPI`; same MFA-or-OAuth branching.
- Google: `GoogleSignInButton` → `GET /v1.0/auth/google`.

### 2. OAuth / PKCE — `@aoctech/auth-client` (via `lib/oauth-client.ts`)
- The `OAuthClient` is configured once (`lib/oauth-client.ts:5`) with
  `baseUrl: API_URL`, **`clientId: CLIENT_ID`** (`ui/src/lib/env.ts:6` =
  `NEXT_PUBLIC_OAUTH_CLIENT_ID ?? 'accounts'`),
  `redirectUri: <origin>/login/callback`, and `ACCOUNT_OAUTH_SCOPE`: the three
  OIDC scopes plus the explicit `account:*` permissions used by every Account
  management screen.
- `startOAuthFlow` (`lib/auth-flow.ts`) calls `oauthClient.startOAuthFlow(continue)`.
  The package generates the **PKCE verifier/challenge + `state`** in
  `sessionStorage`, then redirects the browser to
  `/v1.0/authorize?client_id=<CLIENT_ID>&redirect_uri=<origin>/login/callback
  &response_type=code&scope=openid+profile+email+account%3A...&state=...&code_challenge=...
  &code_challenge_method=S256`.
- **`max_age=0` step-up from a sibling app**: if `continueURL` already starts
  with `/v1.0/` (e.g. a wallet `authorize?client_id=wallet&max_age=0`
  request), `startOAuthFlow` navigates **directly** to that API path
  (`auth-flow.ts:12`) — routing it through accounts' own `client_id=accounts`
  round-trip first would burn the caller's `max_age` window and bounce step-up
  back to `/login`.

### 3. Authorization — `GET /v1.0/authorize` (API)
- Authenticated by the **`ctech_session`** SSO cookie (not a bearer). Validates
  client/redirect_uri, enforces PKCE for public clients, applies `max_age`
  (forces re-login if the SSO session is older than demanded), the terms gate,
  and (for third-party clients) the consent gate. Issues a single-use auth `code`
  and `302`s back to `redirect_uri?code=...&state=...`.

### 4. Callback — `/login/callback` (`app/login/callback/page.tsx`)
- `oauthClient.exchangeCode(code, state)` → `POST /v1.0/token`
  `grant_type=authorization_code` (sends the PKCE `code_verifier` from
  `sessionStorage`; the API sets the **`ctech_rt`** HttpOnly cookie here).
- Returns `{ accessToken, returnTo }`. `setAccessToken(accessToken)`.
- If `returnTo` starts with `/v1.0/` → `window.location.href = API_URL+returnTo`
  (hand the step-up back to the API); else `router.replace(returnTo)`.

### 5. Silent refresh — `oauthClient.refresh()`
- `POST /v1.0/token` `grant_type=refresh_token`, sending the **`ctech_rt`**
  HttpOnly cookie. Returns a fresh `accessToken` (15-min TTL). Single-flight
  guarded inside the package. Runs at boot and after every step-up.

### 6. axios interceptor — `lib/axios.ts`
- **Request**: if `useAuthStore.accessToken` set, attach `Authorization: Bearer`.
- **Response 401** (not on `/auth/*` or `/token`, and only if the `ctech_auth`
  hint exists): `oauthClient.refresh()`, re-attach, retry once; on failure →
  clear auth + redirect `/login`.
- **Response 403 `step-up-required`** (`type` ends with `step-up-required`):
  open `StepUpDialog` via `useStepUpStore.request()`; after the user proves MFA
  (dialog already refreshed the token) retry the original request once.
- **Response 403 `insufficient-scope`**: refresh and retry once. This migrates
  pre-Resource-Server refresh grants of the trusted Account client; the API
  widens only that system-owned `SELF_CLIENT_ID`, never third-party grants.

### 7. Step-up — `components/step-up-dialog.tsx`
- Opened by the interceptor. Offers TOTP (`stepUpTOTPAPI` →
  `POST /v1.0/auth/step-up` `{ method:"totp", code }`) or passkey
  (`beginStepUpPasskeyAPI` / `completeStepUpPasskeyAPI`).
- On success it calls **`oauthClient.refresh()`** so the in-memory token carries a
  fresh `last_mfa_at` claim (the API's `RequireRecentMFA` reads that claim, not
  a cookie), then `useStepUpStore.succeed()` resolves the pending interceptor
  retry. If the API answers `mfa-enrollment-required` / `403`, it switches to
  the "enroll MFA" state.

---

## Cookies & token storage (critical)
| Cookie / store | Where | Notes |
|---|---|---|
| `ctech_rt` | **HttpOnly** (set by API on `/token`) | Per-client refresh token. The SPA never reads it in JS; `@aoctech/auth-client` sends it on refresh. XSS can't exfiltrate it. |
| `ctech_session` | **HttpOnly** (set by API on login/register/MFA/social) | SSO session token; authenticates `GET /authorize` and `/auth/end-session`. Independent of `ctech_rt` so a server-side exchange by another client can't log the browser out. |
| `ctech_auth` | **non-HttpOnly** hint | JS-readable `"1"` marker only — tells the SPA a session *may* exist so it knows whether to attempt a silent refresh. Carries no secret. |
| access token | **in-memory** Zustand `useAuthStore` | Lost on hard refresh; re-derived from `ctech_rt` via silent refresh. Never persisted to `localStorage`/`sessionStorage` (only PKCE state lives in `sessionStorage`, transiently). |

> `@aoctech/auth-client` is **stateless with respect to tokens**: the tokens
> themselves live in the IdP's HttpOnly cookies (`ctech_rt`, `ctech_session`).
> The package stores only the PKCE verifier/challenge + `state` in
> `sessionStorage` for the duration of the redirect round-trip, and orchestrates the
> authorize→callback→refresh choreography above. It is shared by ctech-dfe and
> ctech-wallet too, so a change here ships to all three SPAs.

## Account client id and Resource Server scopes
The SPA's OAuth `client_id` is `CLIENT_ID` (`lib/env.ts:6`) = env
`NEXT_PUBLIC_OAUTH_CLIENT_ID` **defaulting to `'accounts'`** — **not** `accounts-ui`.
This matters for the API's `RequireClientID(SELF_CLIENT_ID)` gate on the
SPA-specific `/v1.0/auth/step-up/*` protocol: the token's `azp` claim must equal
`SELF_CLIENT_ID` (API default `"accounts"`, `api/internal/config/config.go`).
Account Resource Server endpoints themselves are governed by audience and the
exact route permission from
`ACCOUNT_USER_SCOPES`; missing permissions return RFC 7807
`insufficient-scope` plus a Bearer `WWW-Authenticate` challenge.

The API startup reconciles this system-owned public OAuth client automatically,
including `${APP_URL}/login/callback` and the full scope set. If a deployment
overrides `NEXT_PUBLIC_OAUTH_CLIENT_ID`, it must set the same `SELF_CLIENT_ID` in
the API; otherwise step-up calls return `403`.
