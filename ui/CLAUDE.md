# CLAUDE.md — ui (ctech-account)

Next.js 16 + React 19 + ShadCN 4 + Tailwind v4 — accounts.aoctech.app frontend.

**Before any task:** Read `GUIDELINES.md` (Next.js 16 / React 19 / ShadCN 4 specifics), `../README.md` (API surface).

---

## Role

**Static-export client-rendered SPA** for the ctech-account identity service. `next.config.ts` sets
`output: 'export'` in production — there is no Next.js server at runtime, so no Server Components
with data, no Server Actions, and no Route Handlers. Every page is a Client Component. All API calls
go directly from the browser to the Go API through the shared `api` axios instance in `lib/axios.ts`.

In production, CloudFront forwards `/v1.0/*` and `/.well-known/*` to the ALB so the browser's calls
stay same-origin (no CORS, cookies stay first-party). In dev, `next dev`'s `rewrites()` in
`next.config.ts` does the same thing against `DEV_API_ORIGIN` (default `http://localhost:8001`).
`rewrites()` and `output: 'export'` are mutually exclusive, which is why they're gated on
`NODE_ENV === 'production'` in `next.config.ts`.

Handles login, registration, MFA, passkeys, account management (sessions, API keys, OAuth clients,
connected apps, KYC/identity), and the OAuth 2.0 + PKCE authorization-code dance — all client-side.

---

## Directory Structure

```
ui/src/
├── app/
│   ├── account/            # Protected pages (all 'use client', guarded in account/layout.tsx)
│   │   ├── profile/        # Edit name, change password
│   │   ├── security/       # MFA methods list, TOTP setup, passkeys
│   │   ├── sessions/       # List + revoke sessions
│   │   ├── api-keys/       # List, create, revoke API keys
│   │   ├── oauth-clients/  # Register/manage OAuth clients (developer/integrator surface)
│   │   ├── connected-apps/ # Consent grants (revoke third-party access)
│   │   ├── activity/       # Paginated audit/activity log
│   │   └── identity/       # KYC verification flow
│   ├── login/              # Password login + passkey login
│   ├── login/callback/     # OAuth code exchange (client-side POST to /v1.0/token)
│   ├── login/mfa/          # TOTP code input
│   ├── register/           # Account creation
│   ├── forgot-password/    # Password reset request
│   ├── reset-password/     # Token-based reset form
│   └── verify-email/       # Email verification
├── components/             # Shared UI components (kebab-case filenames)
├── store/
│   ├── auth.ts             # Zustand — in-memory access token only (no persistence, no cookie)
│   └── step-up.ts          # Zustand — bridges the axios 403 step-up interceptor and its dialog
└── lib/
    ├── axios.ts            # The `api` axios instance — auth header injection, 401 refresh, step-up retry
    ├── queries.ts          # Reads — plain async functions called via TanStack Query in pages
    ├── mutations.ts        # Writes — plain async functions called via TanStack Query mutations
    ├── env.ts              # API_URL / CLIENT_ID env resolution (no other module-local state)
    ├── oauth-client.ts     # Configured `@aoctech/auth-client` OAuthClient singleton + mock-aware hasAuthHint
    ├── auth-flow.ts        # Starts the OAuth/PKCE redirect (delegates to oauth-client.ts; mock bypass kept here)
    ├── types.ts            # TypeScript types aligned with backend JSON fields
    └── format.ts           # Date formatting helpers
```

PKCE, the `ctech_auth` hint cookie, and the guarded/single-flight token refresh are no longer
implemented here — they live in the shared [`@aoctech/auth-client`](../../ctech-oauth-client) package
(sibling repo, same npm package `ctech-dfe/ui` and `ctech-wallet/ui` consume), configured once in
`lib/oauth-client.ts`. Extracted because the three SPAs' independent copies had drifted: this app and
ctech-dfe each gated silent refresh on a local signal before touching `/v1.0/token`, ctech-wallet did
not, and that missing guard fired a doomed refresh on every first visit — burning the same shared
brute-force rate limit that protects login.

No `app/api/`, no `proxy.ts`, no `middleware.ts` exist in this project — route protection is a
client-side `useEffect` guard in `account/layout.tsx` that redirects to `/login` when the Zustand
auth store has no access token.

---

## Mandatory Workflow

1. Read `GUIDELINES.md` before writing any Next.js 16 / React 19 / ShadCN 4 code.
2. `rg "..."` — search for existing components, queries, and mutations before creating new ones.
3. Plan → Implement → **Run ESLint → Run build (`npm run build`)**.
4. State cross-project impact (ui ↔ Go API ↔ cdk).
5. Suggest Conventional Commit.

---

## Engineering Rules

### ESLint + Build (MUST pass before any commit)

```bash
npx eslint src --ext .ts,.tsx   # zero errors, zero warnings
npm run build                   # must compile cleanly (static export)
```

### DRY

- Never duplicate query/mutation functions or components.
- All external API calls go through the shared `api` instance in `lib/axios.ts`, called from
  `lib/queries.ts` (reads) or `lib/mutations.ts` (writes).
- **Never construct a raw `axios`/`fetch` call to the API outside `lib/queries.ts` / `lib/mutations.ts`** —
  route through `api` so the auth header, 401-refresh, and step-up interceptors apply.
- If two pages share the same form pattern, extract a shared component.

### Constants — no magic strings

- Storage keys (`lib/constants.ts`: `MFA_TOKEN_KEY`, `MFA_METHODS_KEY`, `CONTINUE_URL_KEY`, ...),
  OAuth param names, and API path segments must be named constants. The auth-hint cookie name is
  owned by `@aoctech/auth-client` (not duplicated here).
- Never hardcode `NEXT_PUBLIC_API_URL` or any URL inline in components — use env vars or constants.
- Error message strings that repeat across pages must be defined once in a shared constants file.

### Data Flow (MUST follow)

| Operation          | Where                                                        | Forbidden                                       |
|---------------------|--------------------------------------------------------------|--------------------------------------------------|
| Read (page load)    | `useQuery`/`useInfiniteQuery` calling `lib/queries.ts` in a Client Component | Raw `fetch`/`axios` bypassing `lib/axios.ts`'s `api` |
| Mutation            | `useMutation` calling `lib/mutations.ts` in a Client Component | Raw `fetch`/`axios` bypassing `api`; Server Actions (unsupported under `output: 'export'`) |
| Auth token storage  | Zustand `store/auth.ts` (in-memory access token) + httpOnly refresh cookie set directly by the Go API | Persisting the access token to `localStorage`/`sessionStorage`; Next.js Route Handlers (none exist, none work under static export) |

### Next.js 16 Rules (from GUIDELINES.md — strictly enforced)

- This project targets `output: 'export'` in production — Server Actions, Route Handlers, and
  `middleware.ts`/`proxy.ts` are all unsupported there and none exist in this codebase. Don't add them.
- `await cookies()` / `await headers()` / `await params` are irrelevant here since there are no
  Server Components reading request data — but still apply if you ever add one for a truly static
  (non-personalized) page.
- No `use cache` directive anywhere; there is no server to cache against.
- `useSearchParams()` requires a `<Suspense>` boundary around the component that calls it
  (see `login/callback/page.tsx` for the pattern).

### React 19 Rules (from GUIDELINES.md — strictly enforced)

- There are no Server Actions in this project — use TanStack Query's `useMutation` for all writes,
  not `useActionState`/`useFormStatus` against a server action.
- Derive values directly from query/mutation state — do not copy them into `useState` via `useEffect`.
- `useEffect` is for side effects only (toasts, focus, navigation, the auth redirect guard) — never
  to sync derived state.

### ShadCN 4 / @base-ui Rules (from GUIDELINES.md — strictly enforced)

- **`asChild` does not exist** in ShadCN 4 — use `render` prop instead:
  ```tsx
  // WRONG: <Button asChild><Link href="/foo">Go</Link></Button>
  // CORRECT:
  <Button render={<Link href="/foo"/>}>Go</Button>
  <DialogTrigger render={<Button/>}>Open</DialogTrigger>
  ```

### Tailwind v4

- Import via CSS only — no `tailwind.config.js`.
- `size-*` for square elements (not `w-* h-*`).

### TypeScript

- All types in `lib/types.ts` — field names match backend JSON exactly.
- Use `unknown` not `any` for untyped data. Cast at boundaries with type guards.

### Error Handling

- Backend returns RFC 7807 `application/problem+json`. Axios rejects on non-2xx, so catch and check
  `isAxiosError(error)` (from `lib/axios.ts`), then read `error.response?.data` as a `ProblemDetail`.
- Surface in UI: `<Alert variant="destructive">` for form validation; `toast.error(message)` for transient errors.

### Security

- The access token lives only in memory (`store/auth.ts`, Zustand, no persistence) — a hard refresh
  clears it and the app silently re-derives a new one from the httpOnly refresh cookie if the
  non-secret `ctech_auth` hint cookie (`@aoctech/auth-client`'s `hasAuthHint()`, wrapped in
  `lib/oauth-client.ts`) says a session may exist.
- The refresh cookie is `httpOnly; Secure; SameSite=Lax`, set directly by the Go API — Next.js never
  sets or reads it.
- PKCE verifier/challenge and OAuth `state` are generated **client-side** (inside `@aoctech/auth-client`,
  Web Crypto) and held in `sessionStorage` only for the duration of the redirect round-trip — never
  sent anywhere but the Go API's `/v1.0/token` exchange.
- Never log tokens, cookies, or passwords.

### Secrets

Never commit: access tokens, refresh tokens, OAuth secrets, real user data.

---

## Testing

| Change             | Required                                 |
|--------------------|-------------------------------------------|
| New component      | Component test (Vitest + RTL)            |
| New query/mutation | Integration test                          |
| Auth flow          | Integration test (full login → callback) |
| Bug fix            | Regression test                          |

Run: `npm test` from `ui/`.

---

## Known Constraints

- Production build is `next build` with `output: 'export'` — no Node.js server, no ISR, no on-demand
  revalidation. Anything that needs a server doesn't belong in this project.
- `NEXT_PUBLIC_MOCK_API=true` (`.env.local`) routes all API calls through `lib/mock.ts`'s axios adapter
  and auto-authenticates as a mock user — never rely on this for anything but local UI iteration.
- `accounts` OAuth client (`NEXT_PUBLIC_OAUTH_CLIENT_ID`) must be registered in DynamoDB before first
  login (see `../README.md` §First Deploy).

---

## Critical Areas (require analysis before touching)

- `lib/axios.ts` — the `api` instance's 401-refresh (delegates to `@aoctech/auth-client`) and step-up retry logic
- `account/layout.tsx` — the client-side auth guard for all `/account/*` routes
- `lib/oauth-client.ts`, `lib/auth-flow.ts`, `login/callback/page.tsx` — the OAuth/PKCE dance
- [`@aoctech/auth-client`](../../ctech-oauth-client) itself — shared by ctech-dfe and ctech-wallet too;
  a change here ships to all three SPAs once they bump the dependency
- `lib/mutations.ts` — all mutations
- Login MFA flow and passkey authentication

Before touching: identify risks + side effects, verify backward compatibility.

---

## Completion Checklist

- [ ] `npx eslint src --ext .ts,.tsx` passes with zero errors/warnings
- [ ] `npm run build` succeeds (static export)
- [ ] No duplicate components, queries, or mutations introduced
- [ ] All constants named (no magic strings)
- [ ] No raw `fetch`/`axios` call bypassing `lib/axios.ts`'s `api` instance
- [ ] `render` prop used instead of `asChild` for ShadCN components
- [ ] Tokens and cookies never logged or exposed beyond what's already documented above
- [ ] Cross-project impact reviewed (ui ↔ Go API ↔ cdk)
