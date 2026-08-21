# ctech-account UI

Next.js 16.3.1 + React 19.2.8 static-export SPA for the CTech identity provider at
`accounts.aoctech.app`. It is deployed to **Cloudflare Workers Static Assets**;
there is no Next.js server or Vercel runtime in production.

The browser calls the API **cross-origin** at `NEXT_PUBLIC_API_URL`
(`https://accounts-api.aoctech.app`) — nothing proxies `/v1.0/*` at the edge, so
CORS applies. `/.well-known/*` is served by the API host only. See
[`CLAUDE.md`](CLAUDE.md) for why the auth cookies still work.

## Development and quality gates

```bash
npm ci
npm run dev
npm test
npm run lint
npm run build
npm audit --omit=dev
```

Development listens on `http://localhost:3001`. The production build writes the
static export consumed by the frontend deployment workflow. A change must keep
the production dependency audit at zero known vulnerabilities.

Internal page transitions use the Next router. Redirects that intentionally
leave the SPA for Go API/OAuth endpoints, plus the failed-refresh hard reset,
use document navigation and carry narrow ESLint exceptions explaining why.

---

## Project documentation

This is the `ctech-account` identity-provider front end (accounts.aoctech.app),
not a stock Next.js app. See [`FRONTEND.md`](./FRONTEND.md) for the full
architecture: page/layout tree, providers (`QueryProvider` + `AuthInitializer`),
Zustand auth/step-up stores, and the complete auth flow.

**Auth flow in one paragraph.** It is a static-export SPA (no server). The
in-memory access token lives only in `store/auth.ts` (never `localStorage`);
the refresh token is the HttpOnly `ctech_rt` cookie set by the Go API. Password
and passkey login can either issue the SSO `ctech_session` cookie directly or
return an MFA continuation. The SPA stores that short-lived continuation only
in `sessionStorage`, verifies the TOTP code at `/login/mfa`, then starts the
OAuth PKCE dance via `@aoctech/auth-client` (`lib/oauth-client.ts`). The MFA UI
accepts both the `totp` challenge label and the RFC 8176 `otp` AMR value so
passkey + TOTP works across deployed API versions. OAuth redirects through
`GET /v1.0/authorize` and exchanges the code at `POST /v1.0/token`. A background
`AuthInitializer` silent-refreshes from `ctech_rt` on boot; the `lib/axios.ts`
interceptor auto-refreshes on 401 and opens the step-up (`max_age=0`) dialog on
a `403 step-up-required`. The OAuth `client_id` is `CLIENT_ID` (`lib/env.ts`) =
`NEXT_PUBLIC_OAUTH_CLIENT_ID` (default `accounts`).

Passkey enrollment asks for a recognizable name before opening the browser's
WebAuthn security prompt and persists that name through the existing register
begin/complete API. The current API exposes list, register, and delete only;
renaming an already registered passkey requires a dedicated authenticated API
mutation before the UI can safely offer edit-in-place across devices.

For the API surface, see `../api/ENDPOINTS.md` and `../api/README.md`.
