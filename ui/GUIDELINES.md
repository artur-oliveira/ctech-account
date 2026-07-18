# Frontend Guidelines — ctech-account UI

Next.js 16 + React 19 + ShadCN 4 + Tailwind v4 static-export SPA for the ctech-account identity service.

---

## Architecture

```
src/
  app/
    account/           # Protected pages ('use client', guarded by account/layout.tsx)
    login/ register/   # Public auth pages (Client Components)
  components/          # Shared UI components
  store/
    auth.ts            # Zustand — in-memory access token only
    step-up.ts          # Zustand — step-up MFA dialog bridge
  lib/
    axios.ts           # The `api` axios instance — auth header, 401 refresh, step-up retry
    queries.ts         # Reads, called via TanStack Query
    mutations.ts       # Writes, called via TanStack Query
    auth-flow.ts       # OAuth/PKCE redirect kickoff
    pkce.ts            # PKCE verifier/challenge + state (Web Crypto, in-browser)
    types.ts           # TypeScript types aligned with backend JSON fields
    format.ts          # Date formatting helpers
```

`next.config.ts` sets `output: 'export'` in production — there is no Next.js server at runtime.
That rules out Server Components with data, Server Actions, Route Handlers, and
`middleware.ts`/`proxy.ts`; none exist in this codebase and none should be added.

**Data flow:**

- Reads: Client Components call `useQuery`/`useInfiniteQuery` against functions in `lib/queries.ts`,
  which call the Go API through `lib/axios.ts`'s `api` instance.
- Mutations: Client Components call `useMutation` against functions in `lib/mutations.ts`, same `api` instance.
- Auth: `store/auth.ts` (Zustand) holds the access token in memory only. The Go API sets the httpOnly
  refresh cookie directly (Next.js never sets or reads it) and a non-secret `ctech_auth` hint cookie
  (`lib/auth-hint.ts`) the SPA reads to decide whether a silent refresh is worth attempting.
- Same-origin in both environments: CloudFront forwards `/v1.0/*` and `/.well-known/*` to the ALB in
  production; `next dev`'s `rewrites()` does the same against `DEV_API_ORIGIN` locally. Both keep the
  browser's calls same-origin, so CORS never applies and cookies stay first-party.
- Route protection: a `useEffect` in `account/layout.tsx` reads `store/auth.ts` and redirects
  unauthenticated `/account/*` visits to `/login`.

---

## Next.js 16 Rules

These are breaking changes from Next.js 14/15. Most don't apply directly to this project (no Server
Components read request data here), but know them if you ever touch a genuinely static, non-personalized page:

| API                          | Correct                                              |
|------------------------------|--------------------------------------------------------|
| `cookies()` / `headers()`    | `await cookies()` — they return Promises (Server Components only) |
| `params` in page props       | `const { id } = await params`                        |
| `searchParams` in page props | `const { q } = await searchParams`                   |
| Caching                      | No `use cache` directive anywhere — there's no server to cache against |

`useSearchParams()` requires a `<Suspense>` boundary around the component that calls it. Pattern:

```tsx
export default function Page() {
  return (
    <Suspense fallback={<div className="animate-pulse h-8 bg-muted rounded"/>}>
      <InnerForm/> {/* InnerForm calls useSearchParams() */}
    </Suspense>
  )
}
```

---

## React 19 Patterns

**TanStack Query, not Server Actions** — there are no Server Actions in this project. Reads use
`useQuery`, writes use `useMutation`, both against plain async functions in `lib/queries.ts` /
`lib/mutations.ts`:

```tsx
const { data, isError, error, refetch } = useQuery({ queryKey: ['profile'], queryFn: fetchProfile })
const { mutate, isPending } = useMutation({ mutationFn: revokeSessionAPI })
```

**Never copy query/mutation state into separate `useState` via `useEffect`:**

```tsx
// WRONG — triggers react-hooks/set-state-in-effect
useEffect(() => {
  setX(data.x)
}, [data])

// CORRECT — derive directly
const x = data?.x ?? null
```

**`useEffect` is for side effects only** (toasts, focus, navigation, the auth redirect guard):

```tsx
useEffect(() => {
  if (isError) toast.error(error.message)
}, [isError, error])
```

**Pending state from a mutation:**

```tsx
function SubmitButton({ pending }: { pending: boolean }) {
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
<Button render={<Link href="/foo"/>}>Go</Button>
<DialogTrigger render={<Button/>}>Open</DialogTrigger>
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
- `ProblemDetail` for error responses — check the RFC 7807 `type`/`status` fields, not response shape.
- Use `unknown` not `any` for untyped data. Cast at the boundary with type guards.

---

## Fetch Patterns

**Reads (`lib/queries.ts`, called via `useQuery`):**

```ts
// lib/queries.ts — always call these through TanStack Query, never raw axios/fetch in page files
export async function fetchProfile(): Promise<User> {
  const { data } = await api.get<User>('/v1.0/account/profile')
  return data
}
```

**Writes (`lib/mutations.ts`, called via `useMutation`):**

```ts
// lib/mutations.ts
export async function revokeSessionAPI(sessionId: string) {
  await api.delete(`/v1.0/account/sessions/${sessionId}`)
}
```

Never construct a raw `axios`/`fetch` call to the API outside `lib/queries.ts` / `lib/mutations.ts` —
always go through the shared `api` instance in `lib/axios.ts` so the Bearer-token header injection,
401 silent-refresh, and step-up-required retry interceptors apply.

---

## Error Handling

Backend returns RFC 7807 `application/problem+json`. Axios rejects on non-2xx — catch and check:

```ts
try {
  await revokeSessionAPI(id)
} catch (error) {
  if (isAxiosError(error)) {
    const problem = error.response?.data as ProblemDetail
    return { error: problem.detail }
  }
  throw error
}
```

Surface errors in UI via:

1. Inline `<Alert variant="destructive">` for form validation errors
2. `toast.error(message)` from `sonner` for transient errors

---

## File Naming Conventions

| Pattern            | Use                                             |
|--------------------|---------------------------------------------------|
| `page.tsx`         | Route page (always a Client Component here)      |
| `layout.tsx`       | Layout (Client Component; `account/layout.tsx` also carries the auth guard) |
| `*-actions.tsx`    | Client Component with action buttons for a page (calls `lib/mutations.ts`) |
| `components/*.tsx` | Shared components, kebab-case filenames         |

---

## Security

- **Never** log tokens, cookies, or passwords.
- **Never** expose `API_URL` to the browser for anything beyond the public, non-secret
  `NEXT_PUBLIC_API_URL`/`NEXT_PUBLIC_OAUTH_CLIENT_ID` env vars already used in `lib/axios.ts`.
- The access token lives only in memory (`store/auth.ts`, Zustand) — never persisted to
  `localStorage`/`sessionStorage`, never set as a cookie by this app.
- The refresh cookie is `httpOnly; Secure; SameSite=Lax`, set directly by the Go API — this app never
  sets or reads it, only the non-secret `ctech_auth` hint cookie (`lib/auth-hint.ts`).
- PKCE verifier/challenge and OAuth `state` are generated **client-side** in `lib/pkce.ts` (Web
  Crypto) and held in `sessionStorage` only for the redirect round-trip.
- WebAuthn credential IDs are base64url-encoded; ArrayBuffers used at the API boundary.

---

## Production Constraints

- Production build is `next build` with `output: 'export'` — static files only, no Node.js server,
  no ISR, no on-demand revalidation, no Edge middleware.
- `NEXT_PUBLIC_MOCK_API=true` (`.env.local`) swaps `lib/axios.ts`'s adapter for `lib/mock.ts` and
  auto-authenticates as a mock user — local iteration only, never rely on it for auth-flow testing.
