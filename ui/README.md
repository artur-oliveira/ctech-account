This is a [Next.js](https://nextjs.org) project bootstrapped with [`create-next-app`](https://nextjs.org/docs/app/api-reference/cli/create-next-app).

## Getting Started

First, run the development server:

```bash
npm run dev
# or
yarn dev
# or
pnpm dev
# or
bun dev
```

Open [http://localhost:3000](http://localhost:3000) with your browser to see the result.

You can start editing the page by modifying `app/page.tsx`. The page auto-updates as you edit the file.

This project uses [`next/font`](https://nextjs.org/docs/app/building-your-application/optimizing/fonts) to automatically optimize and load [Geist](https://vercel.com/font), a new font family for Vercel.

## Learn More

To learn more about Next.js, take a look at the following resources:

- [Next.js Documentation](https://nextjs.org/docs) - learn about Next.js features and API.
- [Learn Next.js](https://nextjs.org/learn) - an interactive Next.js tutorial.

You can check out [the Next.js GitHub repository](https://github.com/vercel/next.js) - your feedback and contributions are welcome!

## Deploy on Vercel

The easiest way to deploy your Next.js app is to use the [Vercel Platform](https://vercel.com/new?utm_medium=default-template&filter=next.js&utm_source=create-next-app&utm_campaign=create-next-app-readme) from the creators of Next.js.

Check out our [Next.js deployment documentation](https://nextjs.org/docs/app/building-your-application/deploying) for more details.

---

## Project Documentation

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
