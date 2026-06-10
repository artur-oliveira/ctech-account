# CLAUDE.md — ui (ctech-account)

Next.js 16 + React 19 + ShadCN 4 + Tailwind v4 — accounts.arturocarvalho.com frontend.

**Before any task:** Read `GUIDELINES.md` (Next.js 16 / React 19 / ShadCN 4 specifics), `../README.md` (API surface).

---

## Role

BFF-style frontend for the ctech-account identity service. Handles login, registration, MFA,
passkeys, account management, and OAuth redirects. Communicates with the Go API only via
Server Components, Server Actions, and Route Handlers — never direct browser-to-API calls.

---

## Directory Structure

```
ui/src/
├── app/
│   ├── api/auth/           # Route Handlers — BFF: token exchange, cookie management
│   ├── account/            # Protected pages (Server Components + client action files)
│   │   ├── profile/        # Edit name, change password
│   │   ├── security/       # MFA methods list, TOTP setup, passkeys
│   │   ├── sessions/       # List + revoke sessions
│   │   └── api-keys/       # List, create, revoke API keys
│   ├── login/              # Password login + passkey login
│   ├── login/mfa/          # TOTP code input
│   ├── register/           # Account creation
│   ├── forgot-password/    # Password reset request
│   ├── reset-password/     # Token-based reset form
│   └── verify-email/       # Email verification
├── components/             # Shared UI components (kebab-case filenames)
├── lib/
│   ├── api.ts              # Server-only fetch helpers (reads ctech_at cookie)
│   ├── actions.ts          # Server Actions ('use server') — all mutations
│   ├── types.ts            # TypeScript types aligned with backend JSON fields
│   ├── format.ts           # Date formatting helpers
│   └── pkce.ts             # PKCE + state generation (Node crypto)
└── proxy.ts                # Auth guard (replaces middleware.ts in Next.js 16)
```

---

## Mandatory Workflow

1. Read `GUIDELINES.md` before writing any Next.js 16 / React 19 / ShadCN 4 code.
2. `rg "..."` — search for existing components and Server Actions before creating new ones.
3. Plan → Implement → **Run ESLint → Run build (`npm run build`)**.
4. State cross-project impact (ui ↔ Go API ↔ cdk).
5. Suggest Conventional Commit.

---

## Engineering Rules

### ESLint + Build (MUST pass before any commit)

```bash
npx eslint src --ext .ts,.tsx   # zero errors, zero warnings
npm run build                   # must compile cleanly
```

### DRY

- Never duplicate Server Actions, fetch helpers, or components.
- All external API calls go through `lib/api.ts` (server-side reads) or `lib/actions.ts` (mutations).
- **Never call `API_URL` directly from browser code.**
- If two pages share the same form pattern, extract a shared component.

### Constants — no magic strings

- Storage keys, cookie names (`ctech_at`, `ctech_rt`), OAuth param names, and API path segments must be named constants.
- Never hardcode `NEXT_PUBLIC_API_URL` or any URL inline in components — use env vars or constants.
- Error message strings that repeat across pages must be defined once in a shared constants file.

### Data Flow (MUST follow)

| Operation          | Where                           | Forbidden                                |
|--------------------|---------------------------------|------------------------------------------|
| Read (page load)   | Server Component via `lib/api.ts` | `useEffect` + `fetch` on client         |
| Mutation           | Server Action in `lib/actions.ts` | Direct `fetch` from Client Component    |
| Auth (cookie mgmt) | Route Handler in `app/api/auth/`  | Setting cookies from Server Actions     |

### Next.js 16 Rules (from GUIDELINES.md — strictly enforced)

- Route protection: `proxy.ts` — not `middleware.ts`.
- `await cookies()` and `await headers()` — they return Promises.
- `const { id } = await params` — params are async.
- No default caching — opt in with `use cache` directive. **Never** use `use cache` on account pages (user-specific data).
- Cache invalidation via `revalidatePath(path)` inside Server Actions.
- `useSearchParams()` requires `<Suspense>` boundary around the component that calls it.

### React 19 Rules (from GUIDELINES.md — strictly enforced)

- Use `useActionState(serverAction, null)` — never copy action state to `useState` via `useEffect`.
- Derive values directly from action state — do not sync via effects.
- `useEffect` in action components is for side effects only (toasts, focus, navigation).
- Submit button pending state via `useFormStatus` — only works inside `<form>` with a Server Action.

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
- `lib/api.ts` has `server-only` import — never import it in a Client Component.

### Error Handling

- Backend returns RFC 7807 `application/problem+json`. Check `res.ok` first, then parse the `ProblemDetail`.
- Surface in UI: `<Alert variant="destructive">` for form validation; `toast.error(message)` for transient errors.

### Security

- `ctech_at` and `ctech_rt` cookies are `httpOnly; Secure; SameSite=Lax` — never readable from JS.
- PKCE verifier is generated server-side in the login Route Handler — never sent to the browser.
- Never log tokens, cookies, or passwords.
- `proxy.ts` runs as Edge middleware — no Node.js APIs, no `require()`.

### Secrets

Never commit: access tokens, refresh tokens, OAuth secrets, real user data.

---

## Testing

| Change             | Required                                 |
|--------------------|------------------------------------------|
| New component      | Component test (Vitest + RTL)            |
| New Server Action  | Integration test                         |
| Auth flow          | Integration test (full login → callback) |
| Bug fix            | Regression test                          |

Run: `npm test` from `ui/`.

---

## Known Constraints

- `Route Handler setTokenCookies` is reused by the MFA route — keep it pure (no side effects).
- `lib/api.ts` is `server-only` — importing it in a Client Component is a build error.
- `use cache` directive must not be used on account pages (user-specific data).
- `accounts-ui` OAuth client must be registered in DynamoDB before first login (see `../README.md` §First Deploy).

---

## Critical Areas (require analysis before touching)

- `proxy.ts` — auth guard for all `/account/*` routes
- `app/api/auth/` Route Handlers — PKCE OAuth dance, cookie management
- `lib/actions.ts` — all mutations
- Login MFA flow and passkey authentication

Before touching: identify risks + side effects, verify backward compatibility.

---

## Completion Checklist

- [ ] `npx eslint src --ext .ts,.tsx` passes with zero errors/warnings
- [ ] `npm run build` succeeds
- [ ] No duplicate components, actions, or fetch helpers introduced
- [ ] All constants named (no magic strings)
- [ ] No browser-side direct API calls
- [ ] `useActionState` used correctly (no effect-based state sync)
- [ ] `render` prop used instead of `asChild` for ShadCN components
- [ ] Tokens and cookies never logged or exposed to client JS
- [ ] Cross-project impact reviewed (ui ↔ Go API ↔ cdk)
