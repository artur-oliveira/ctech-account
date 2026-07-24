# Product

## Register

product

## Platform

web

## Users

Two co-primary audiences share one console:

- **Account owners** — end users of the aoctech.app platform managing their own identity: sign in, MFA and passkeys, active sessions, connected apps and consents, KYC, and profile. They are not security experts; they need to understand their account state at a glance.
- **Developers / integrators** — users who register OAuth applications, create and rotate API keys, and wire external services into the platform. They need depth and precision (scopes, secrets, redirect URIs, audiences) without leaving the same console.

The product is Brazilian in context (pt-BR locale, CPF, PIX, KYC verification), so the console must read clearly for both Portuguese and English speakers and handle legally-sensitive identity flows with care.

## Product Purpose

The BFF-style frontend for the ctech-account OAuth 2.0 + OpenID Connect identity provider. It is where users authenticate and where they own and operate their identity: log in (password, passkey, Google), enforce MFA, review sessions and consents, verify identity (KYC), and — for integrators — register and manage OAuth clients and API keys. The Go API is the source of truth; this UI never touches tokens from the browser and only speaks to the API through Server Components, Server Actions, and Route Handlers.

Success looks like: a user signs in securely with minimal friction, sees exactly what has access to their account, and a developer can stand up and operate an integration without leaving the console or filing a ticket.

## Positioning

Secure by default — strong authentication (MFA, passkeys, token rotation, step-up) is built in and frictionless, not a nag the user has to opt into.

## Brand Personality

Secure, modern, approachable.

## Anti-references

- **Generic pastel SaaS** — airy, thin, substanceless startup look.
- **Gamified consumer** — playful, cartoonish, reward-driven UX.
- **Dense enterprise gray** — cramped, gray, legacy back-office density.
- **Dark-mode hacker** — heavy dark-only "terminal" aesthetics that exclude non-technical users.

## Design Principles

1. **Security is the product, not a banner.** MFA, passkeys, rotation, and step-up are first-class and presented as normal and easy — never as scary warnings or afterthoughts.
2. **Trust through clarity.** Security states (verified, pending, revoked, linked) use plain language and unmistakable status. Jargon only where a user has no plainer word.
3. **One identity, two audiences.** Serve account owners and developers from the same console without diluting either; respect each context's required depth.
4. **Calm confidence.** Sober, exact, dependable. Never flashy, never playful, never cold.
5. **Accessible by standard.** WCAG 2.2 AA is a floor, not a feature — contrast, keyboard, focus order, and target size are baked in, and state is never conveyed by color alone.

## Accessibility & Inclusion

- **WCAG 2.2 AA** minimum across all surfaces.
- Bilingual (English + Portuguese / pt-BR); copy and layout must hold both without truncation or overflow.
- Identity flows (CPF, KYC, PIX) are legally sensitive — never expose raw identifiers; mask by default (e.g. CPF `***.***.***-XX`).
- Reduced-motion support required; security-critical state conveyed by icon + text, not color alone.
