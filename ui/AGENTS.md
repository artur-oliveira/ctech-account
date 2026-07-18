# AGENTS.md — ui (ctech-account)

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
│   ├── login/              # Password login + passkey login
│   ├── login/mfa/          # TOTP code input
│   ├── register/           # Account creation
│   ├── forgot-password/
│   ├── reset-password/
│   └── verify-email/
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
2. `rg "..."` — search for existing components and actions before creating new ones.
3. Plan → Implement → **Run ESLint → Run build**.
4. State cross-project impact (ui ↔ Go API ↔ cdk).
5. Suggest Conventional Commit.

---

## Non-Negotiable Rules

1. **No direct API calls from the browser** — use `lib/api.ts` (reads), `lib/actions.ts` (mutations), or Route Handlers (cookie ops).
2. **`await cookies()` and `await headers()`** — they return Promises in Next.js 16.
3. **`render` prop instead of `asChild`** — ShadCN 4 uses `@base-ui/react`, `asChild` does not exist.
4. **Never copy action state to `useState` via `useEffect`** — derive directly from `useActionState` result.
5. **`useSearchParams()` requires `<Suspense>` boundary** around the component that calls it.

---

## Data Flow

| Operation          | Where                             | Forbidden                             |
|--------------------|-----------------------------------|---------------------------------------|
| Read (page load)   | Server Component via `lib/api.ts` | `useEffect` + `fetch` from client     |
| Mutation           | Server Action in `lib/actions.ts` | Direct `fetch` from Client Component  |
| Auth (cookies)     | Route Handler in `app/api/auth/`  | Setting cookies from Server Actions   |

---

## Next.js 16 Quick Reference

| API                        | Correct                                              |
|----------------------------|------------------------------------------------------|
| Route protection           | `proxy.ts` (not `middleware.ts`)                     |
| `cookies()` / `headers()`  | `await cookies()` — they return Promises             |
| `params` in page props     | `const { id } = await params`                        |
| `searchParams` in props    | `const { q } = await searchParams`                   |
| Caching                    | No default cache — opt in with `use cache` directive |
| Cache invalidation         | `revalidatePath(path)` inside Server Actions         |

---

## React 19 Quick Reference

```tsx
// Action state — CORRECT
const [state, action] = useActionState(myServerAction, null)
const value = state?.success ? state.value : null  // derive directly

// Action state — WRONG
useEffect(() => { setX(state.x) }, [state])  // never sync via effect

// Submit pending state
function SubmitButton() {
  const { pending } = useFormStatus()
  return <Button type="submit" disabled={pending}>{pending ? 'Saving…' : 'Save'}</Button>
}
```

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
- All mutations in `lib/actions.ts` — never scattered across page files.
- All types in `lib/types.ts` — never inline type definitions that duplicate backend fields.

### Constants — no magic strings

- Cookie names (`ctech_at`, `ctech_rt`), localStorage keys, OAuth param names → named constants.
- Never hardcode API URLs or client IDs inline.

### Error Handling

- Parse RFC 7807 `ProblemDetail` after checking `res.ok`.
- `<Alert variant="destructive">` for form errors; `toast.error(message)` for transient errors.

### Security

- `ctech_at` and `ctech_rt` cookies are `httpOnly; Secure; SameSite=Lax` — never readable from JS.
- PKCE verifier generated server-side in the login Route Handler — never sent to the browser.
- Never log tokens, cookies, or passwords.
- `proxy.ts` is Edge middleware — no Node.js APIs, no `require()`.
- `lib/api.ts` is `server-only` — importing it in a Client Component is a build error.

---

## Testing

```bash
npm test          # all tests
npm run build     # must succeed cleanly
npx eslint src --ext .ts,.tsx  # zero errors/warnings
```

---

## Common Pitfalls

| Mistake                                         | Correct approach                              |
|-------------------------------------------------|-----------------------------------------------|
| `fetch(API_URL, ...)` in a Client Component     | Server Action or Route Handler                |
| `asChild` on ShadCN component                   | `render` prop                                 |
| `cookies()` without `await`                     | `const c = await cookies()`                   |
| Syncing action state via `useEffect + setState` | Derive directly from `useActionState` result  |
| `use cache` on account pages                    | Remove — user-specific data must not cache    |
| `middleware.ts` for auth guard                  | `proxy.ts`                                    |

---

## Completion Checklist

- [ ] `npx eslint src --ext .ts,.tsx` passes with zero errors/warnings
- [ ] `npm run build` succeeds
- [ ] No duplicate components or actions introduced
- [ ] All constants named (no magic strings)
- [ ] No browser-side direct API calls
- [ ] `render` prop used (not `asChild`)
- [ ] `await cookies()` / `await params` used correctly
- [ ] Tokens and cookies never logged or exposed to client JS
- [ ] Cross-project impact reviewed (ui ↔ Go API ↔ cdk)
