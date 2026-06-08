# Frontend Guidelines — ctech-account UI

Next.js 16 + React 19 + ShadCN 4 + Tailwind v4 BFF for the ctech-account identity service.

---

## Architecture

```
src/
  app/
    api/auth/          # Route Handlers (BFF layer — token exchange, cookie management)
    account/           # Protected pages (Server Components + Client action files)
    login/ register/   # Public auth pages (Client Components)
  components/          # Shared UI components
  lib/
    actions.ts         # Server Actions ('use server') — all mutations
    api.ts             # Server-only fetch helpers (reads ctech_at cookie)
    types.ts           # TypeScript types aligned with backend JSON fields
    format.ts          # Date formatting helpers
    pkce.ts            # PKCE + state generation (Node crypto)
  proxy.ts             # Auth guard (replaces middleware.ts in Next.js 16)
```

**Data flow:**
- Reads: Server Components call `lib/api.ts` → Bearer token from `ctech_at` cookie → Go API
- Mutations: Client Components call Server Actions in `lib/actions.ts` → Go API
- Auth: Route Handlers in `app/api/auth/` handle token exchange, store in httpOnly cookies
- Route protection: `proxy.ts` redirects unauthenticated `/account/*` requests to `/login`

---

## Next.js 16 Rules

These are breaking changes from Next.js 14/15:

| API | Correct |
|-----|---------|
| Route protection | `proxy.ts` (not `middleware.ts`) |
| `cookies()` / `headers()` | `await cookies()` — they return Promises |
| `params` in page props | `const { id } = await params` |
| `searchParams` in page props | `const { q } = await searchParams` |
| Caching | No default cache — opt in with `use cache` directive |
| Cache invalidation | `revalidatePath(path)` inside Server Actions |

`useSearchParams()` requires a `<Suspense>` boundary around the component that calls it. Pattern:

```tsx
export default function Page() {
  return (
    <Suspense fallback={<div className="animate-pulse h-8 bg-muted rounded" />}>
      <InnerForm />  {/* InnerForm calls useSearchParams() */}
    </Suspense>
  )
}
```

---

## React 19 Patterns

**Server Actions with `useActionState`:**
```tsx
const [state, action] = useActionState(myServerAction, null)
// state is the return value of myServerAction
// action is the form action / callable
```

**Never copy action state into separate `useState` via `useEffect`:**
```tsx
// WRONG — triggers react-hooks/set-state-in-effect
useEffect(() => { setX(state.x) }, [state])

// CORRECT — derive directly
const x = state?.success ? state.x : null
```

**`useEffect` in action components is for side effects only** (toasts, focus, navigation):
```tsx
useEffect(() => {
  if (state?.success) toast.success('Done.')
  if (state?.error) toast.error(state.error)
}, [state])
```

**Submit button pending state via `useFormStatus`:**
```tsx
function SubmitButton() {
  const { pending } = useFormStatus()
  return <Button type="submit" disabled={pending}>{pending ? 'Saving…' : 'Save'}</Button>
}
```

---

## ShadCN 4 / @base-ui Rules

ShadCN 4 uses `@base-ui/react` instead of Radix UI. **`asChild` does not exist.**

Use `render` prop instead:

```tsx
// WRONG
<Button asChild><Link href="/foo">Go</Link></Button>
<DialogTrigger asChild><Button>Open</Button></DialogTrigger>

// CORRECT
<Button render={<Link href="/foo" />}>Go</Button>
<DialogTrigger render={<Button />}>Open</DialogTrigger>
```

---

## Tailwind v4

Import via CSS only — no `tailwind.config.js`:
```css
@import "tailwindcss";
```

Use `size-*` instead of `w-* h-*` for square elements. Use `text-muted-foreground` for secondary text.

---

## TypeScript Conventions

- All types live in `lib/types.ts`. Field names match backend JSON exactly.
- `ProblemDetail` for error responses — check `status` field, not shape.
- `server-only` import at top of `lib/api.ts` prevents accidental client-side import.
- Use `unknown` not `any` for untyped data. Cast at the boundary with type guards.

---

## Fetch Patterns

**Server-side reads (Server Components):**
```ts
// lib/api.ts — always use these helpers, never raw fetch in page files
const profile = await getProfile()  // returns null on 404, redirects on 401
```

**Client-side mutations (Server Actions):**
```ts
// lib/actions.ts
'use server'
// All mutations go here. Use revalidatePath() to refresh stale data.
```

**Client-side imperative fetch (Route Handlers):**
```ts
// Used for auth flows where cookie-setting is required
const res = await fetch('/api/auth/login', { method: 'POST', body: JSON.stringify(data) })
```

Never call `API_URL` directly from browser code. All external API calls go through Server Components, Server Actions, or Route Handlers.

---

## Error Handling

Backend returns RFC 7807 `application/problem+json`. Check `res.ok` first, then parse:

```ts
if (!res.ok) {
  const problem: ProblemDetail = await res.json()
  return { error: problem.detail }
}
```

Surface errors in UI via:
1. Inline `<Alert variant="destructive">` for form validation errors
2. `toast.error(message)` from `sonner` for transient errors

---

## File Naming Conventions

| Pattern | Use |
|---------|-----|
| `page.tsx` | Route page (Server or Client Component) |
| `layout.tsx` | Layout (usually Server Component) |
| `*-actions.tsx` | Client Component with action buttons for a page |
| `route.ts` | Route Handler (BFF — token/cookie management) |
| `components/*.tsx` | Shared components, kebab-case filenames |

---

## Security

- **Never** log tokens, cookies, or passwords.
- **Never** expose `API_URL` to the browser — use `NEXT_PUBLIC_API_URL` only for non-sensitive public URLs.
- `ctech_at` and `ctech_rt` cookies are `httpOnly; Secure; SameSite=Lax` — never readable from JS.
- PKCE verifier is generated server-side in the login Route Handler and never sent to the browser.
- WebAuthn credential IDs are base64url-encoded; ArrayBuffers used at the API boundary.

---

## Production Constraints

- `proxy.ts` runs as Edge middleware — no Node.js APIs, no `require()`.
- `lib/api.ts` is `server-only` — importing it in a Client Component is a build error.
- `use cache` directive opts pages into static caching — do not use on account pages (user-specific data).
- Route Handler `setTokenCookies` is exported and reused by the MFA route — keep it pure (no side effects).
- `useFormStatus` only works inside a component that is a child of a `<form>` with a Server Action.
