# AGENTS.md — ui (ctech-account)

Next.js 16 + React 19 + ShadCN 4 + Tailwind v4 — accounts.aoctech.app frontend.

**Before any task:** Read `GUIDELINES.md` (Next.js 16 / React 19 / ShadCN 4 specifics), `../README.md` (API surface).

---

## Role

**Static-export client-rendered SPA** for the ctech-account identity service. `next.config.ts` sets
`output: 'export'` in production — there is no Next.js server at runtime, so no Server Components
with data, no Server Actions, and no Route Handlers. Every page is a Client Component. All API calls
go directly from the browser to the Go API through the shared `api` axios instance in `lib/axios.ts`.

Deployed to **Cloudflare Workers Static Assets**, not S3+CloudFront. Nothing proxies the API at
the edge any more: the browser calls `NEXT_PUBLIC_API_URL` (`https://accounts-api.aoctech.app` in
production) directly and **CORS applies**. The API allows it because `APP_URL` — the SPA's own
origin — is prepended to the CORS allowlist (`api/cmd/api/main.go:278`) and `AllowCredentials` is
on. The auth cookies still travel: `ctech_rt` and `ctech_auth` are `SameSite=Lax`
(`api/internal/handler/helpers.go:106`), and `accounts.aoctech.app` and `accounts-api.aoctech.app`
share the registrable domain `aoctech.app`, so the request is cross-origin but **same-site** — Lax
is sent, given `withCredentials: true` (`lib/axios.ts:16`). Dropping either that flag or the
API's `AllowCredentials` silently breaks every refresh.

`/.well-known/*` is no longer reachable on the app host. OIDC discovery lives on the API host
(`https://accounts-api.aoctech.app/.well-known/openid-configuration`); the SPA never fetched it, so
only external OIDC clients are affected — an accepted limit of the migration.

In dev, `next dev`'s `rewrites()` in `next.config.ts` still proxies `/v1.0/*` and `/.well-known/*`
to `DEV_API_ORIGIN` (default `http://localhost:8001`), so local work needs no CORS configuration.
That is the one place dev and prod differ on purpose. `rewrites()` and `output: 'export'` are
mutually exclusive, which is why they're gated on `NODE_ENV === 'production'` in `next.config.ts`.

Handles login, registration, MFA, passkeys, account management, and the OAuth 2.0 + PKCE
authorization-code dance — all client-side.

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
    ├── mutations.ts         # Writes — plain async functions called via TanStack Query mutations
    ├── env.ts              # API_URL / CLIENT_ID env resolution (no other module-local state)
    ├── oauth-client.ts      # Configured `@aoctech/auth-client` OAuthClient singleton + mock-aware hasAuthHint
    ├── auth-flow.ts         # Starts the OAuth/PKCE redirect (delegates to oauth-client.ts; mock bypass kept here)
    ├── types.ts            # TypeScript types aligned with backend JSON fields
    └── format.ts           # Date formatting helpers
```

PKCE, the `ctech_auth` hint cookie, and the guarded/single-flight token refresh are not implemented
here — they live in the shared [`@aoctech/auth-client`](../../ctech-oauth-client) package (sibling
repo; the same npm package `ctech-dfe/ui` and `ctech-wallet/ui` consume), configured once in
`lib/oauth-client.ts`.

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

## Non-Negotiable Rules

1. **All account API calls go through `lib/axios.ts`'s `api` instance** — reached only from
   `lib/queries.ts` (reads) and `lib/mutations.ts` (writes). The sole third-party exception is the
   public CNPJA lookup: `lib/queries.ts` uses `lib/axios.ts`'s credential-free `cnpjaApi` client so
   account tokens, cookies, refresh, and step-up behavior can never reach `open.cnpja.com`.
2. **No Server Components with data, Server Actions, or Route Handlers** — the app is a static export;
   they don't exist and can't be added (see `output: 'export'`).
3. **`render` prop instead of `asChild`** — ShadCN 4 uses `@base-ui/react`, `asChild` does not exist.
4. **Never copy action/query state to `useState` via `useEffect`** — derive directly from
   `useQuery`/`useMutation` result.
5. **`useSearchParams()` requires `<Suspense>`** around the component that calls it.

---

## Data Flow

| Operation          | Where                                                        | Forbidden                                       |
|--------------------|--------------------------------------------------------------|--------------------------------------------------|
| Read (page load)   | `useQuery`/`useInfiniteQuery` calling `lib/queries.ts` in a Client Component | Raw `fetch`/`axios` bypassing `lib/axios.ts`'s `api` |
| Mutation           | `useMutation` calling `lib/mutations.ts` in a Client Component | Raw `fetch`/`axios` bypassing `api`; Server Actions (unsupported under `output: 'export'`) |
| Auth token storage | Zustand `store/auth.ts` (in-memory access token) + httpOnly refresh cookie set directly by the Go API | Persisting the access token to `localStorage`/`sessionStorage`; Next.js Route Handlers (none exist) |

---

## Next.js 16 Quick Reference

| API                        | Correct                                              |
|----------------------------|------------------------------------------------------|
| Route protection           | Client-side `useEffect` guard in `account/layout.tsx` (no `proxy.ts`/`middleware.ts`) |
| `cookies()` / `headers()`  | Irrelevant here — no server reading request data (still apply if you ever add a truly static page) |
| `params` in page props     | `const { id } = await params`                        |
| `searchParams` in props    | `const { q } = await searchParams`                   |
| Caching                    | No default cache — opt in with `use cache` directive (no server to cache against under static export) |
| Build output               | `next build` with `output: 'export'` — no Node.js server |

---

## React 19 Quick Reference

```tsx
// Query/mutation state — CORRECT
const { data, isPending } = useQuery({ queryKey: ['profile'], queryFn: getProfile })
const value = data ?? null  // derive directly

// WRONG (syncing derived state via effect)
useEffect(() => { setX(data) }, [data])  // never sync via effect
```

Writes use TanStack Query's `useMutation` — there are no Server Actions in this project.

---

## ShadCN 4 Quick Reference

```tsx
// WRONG (asChild does not exist)
<Button asChild><Link href="/foo">Go</Link></Button>

// CORRECT (render prop)
<Button render={<Link href="/foo"/>}>Go</Button>
<DialogTrigger render={<Button/>}>Open</DialogTrigger>
```

---

## Engineering Rules

### DRY

- Before creating any component, search `src/components/` for an existing one.
- All account API calls go through the shared `api` instance in `lib/axios.ts`, called from
  `lib/queries.ts` (reads) or `lib/mutations.ts` (writes). Public CNPJA enrichment uses only the
  isolated `cnpjaApi` client from the same module and remains in `lib/queries.ts`.
- All types in `lib/types.ts` — field names match backend JSON exactly.

### Constants — no magic strings

- Storage keys (`lib/constants.ts`), OAuth param names, and API path segments → named constants.
- Never hardcode `NEXT_PUBLIC_API_URL` or any URL inline in components — use env vars or constants.

### Error Handling

- Parse RFC 7807 `ProblemDetail` after checking `res.ok` / `isAxiosError(error)`.
- `<Alert variant="destructive">` for form errors; `toast.error(message)` for transient errors.

### Security

- The access token lives only in memory (`store/auth.ts`, Zustand, no persistence) — a hard refresh
  clears it and the app silently re-derives a new one from the httpOnly refresh cookie if the
  non-secret `ctech_auth` hint cookie (`@aoctech/auth-client`'s `hasAuthHint()`) says a session exists.
- The refresh cookie is `httpOnly; Secure; SameSite=Lax`, set directly by the Go API — Next.js never
  sets or reads it.
- Refresh cookies are isolated per OAuth client. `OAuthTransientError` preserves
  local identity/auth hints; only a `null` result represents a terminal session.
- PKCE verifier/challenge and OAuth `state` are generated **client-side** (inside `@aoctech/auth-client`,
  Web Crypto) and held in `sessionStorage` only for the redirect round-trip.
- Never log tokens, cookies, or passwords.

---

## Testing

```bash
npm test          # all tests (Vitest + RTL)
npm run build     # must succeed cleanly (static export)
npx eslint src --ext .ts,.tsx  # zero errors/warnings
```

---

## Common Pitfalls

| Mistake                                         | Correct approach                              |
|-------------------------------------------------|-----------------------------------------------|
| `fetch(API_URL, ...)` in a Client Component     | `lib/queries.ts` / `lib/mutations.ts` via the `api` axios instance |
| `asChild` on ShadCN component                   | `render` prop                                 |
| Syncing query/mutation state via `useEffect`    | Derive directly from `useQuery`/`useMutation` |
| `use cache` on account pages                    | Remove — user-specific data must not cache    |
| Adding `proxy.ts` / `middleware.ts` for a guard | Client-side `useEffect` guard in `account/layout.tsx` |
| Server Actions for a mutation                   | `useMutation` calling `lib/mutations.ts`      |

---

## Completion Checklist

- [ ] `npx eslint src --ext .ts,.tsx` passes with zero errors/warnings
- [ ] `npm run build` succeeds (static export)
- [ ] No duplicate components, queries, or mutations introduced
- [ ] All constants named (no magic strings)
- [ ] No raw `fetch`/`axios` call; account traffic uses `api`, and public CNPJA reads use only `cnpjaApi`
- [ ] `render` prop used (not `asChild`)
- [ ] Tokens and cookies never logged or exposed to client JS
- [ ] Cross-project impact reviewed (ui ↔ Go API ↔ cdk)

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.

<!-- BEGIN:nextjs-agent-rules -->

# This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` (resolved from this file's directory; in monorepos the `next` package may not be visible from the repo root) before writing any code. Heed deprecation notices.

This block is written and re-added by `next dev` — verify at `node_modules/next/dist/server/lib/generate-agent-files.js`. Removing it from a diff only re-creates the uncommitted change; committing it with your work keeps the tree clean.

<!-- END:nextjs-agent-rules -->
