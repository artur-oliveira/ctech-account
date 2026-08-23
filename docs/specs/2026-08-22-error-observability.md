# Error observability audit

Date: 2026-08-22

## Objective

No operational error may disappear without either being returned to a caller that logs it or being
logged at the point where it is handled. Logs must support production debugging without leaking
secrets or personal data.

This work is logging and correlation only. It does not add OpenTelemetry, exporters, collectors,
custom metrics or tracing costs.

## Policy

- The Fiber boundary assigns or preserves `X-Request-ID`, attaches it to `context.Context`, and
  exposes it to browser clients through CORS.
- Every RFC 7807 response is logged once by `Problem.Send`: `WARN` for `4xx`, `ERROR` for `5xx`.
- An internal cause is carried with `Problem.WithCause`. It is logged but is never serialized.
- A function that returns an error delegates logging to its caller. A function that consumes,
  suppresses, retries or degrades after an error must log it locally.
- Best-effort and detached work must retain the request context with `context.WithoutCancel` when
  the work should survive the response; this preserves correlation without retaining cancellation.
- Expected absence and cancellation are branches, not operational errors. They are not logged as
  failures unless they produce an HTTP rejection, which the boundary logs.
- Never log passwords, authorization codes, access/refresh/session tokens, cookies, request or
  response bodies, private keys, Turnstile values, raw OAuth query strings, email addresses or
  identity-document contents. Prefer stable internal IDs.

## Audit coverage

The audit covered API handlers and middleware, domain services and repositories, email/Turnstile
integration, cache and session cleanup, KYC object cleanup, key storage and rotation, GeoIP
downloads, health probes, startup configuration and operator commands. Ignored return values and
best-effort goroutines were reviewed explicitly.

The support flow now logs failures to resolve the recipient, send confirmation/reply/NPS email, and
persist provider Message-IDs. These logs include `ticket_id` and `request_id` when available, but
not the recipient address. Thus a ticket may still be accepted when an email provider is degraded,
while the delivery failure remains diagnosable.

The browser layer logs final Axios failures in a sanitized shape and includes the response
`X-Request-ID`. Browser-only failures use the same de-duplication path. Expected aborts and user
cancellations are excluded.

## Shared package boundary

The reusable primitives belong in the sibling `ctech-go-common` module
(`gopkg.aoctech.app/api-commons`) so `ctech-account`, `ctech-poker`, `ctech-wallet`, `ctech-dfe` and
`ctech-billing` apply the same contract. The shared package should contain:

- request-ID context helpers;
- structured `Error`/`Warn` helpers that enrich from context;
- Fiber middleware that propagates request IDs into `context.Context` and CORS;
- a safe HTTP-error logging hook or adapter.

Domain event names, entity attributes, retry policy and the choice of which expected errors are
normal control flow remain in each API. Frontend logging also remains local because it is a
TypeScript concern.

The account implementation now consumes the shared `api-commons/observability` and
`api-commons/observability/fiber` packages. `ctech-go-common` must be released as `v1.7.0` before
the updated consumer dependency can be fetched outside the coordinated local workspace. Each
consumer still requires its own swallowed-error audit; copying only the middleware is insufficient.

## Cross-project impact

This change does not alter JWT signing, JWKS contents, OAuth protocol behavior, API response bodies
or infrastructure resources. The UI gains access to `X-Request-ID`; CDK, `ctech-dfe` and
`ctech-wallet` have no compatibility change. The future common-package migration affects build
dependencies in all five APIs but must not change their external contracts.
