# CLAUDE.md — ctech-account

OAuth 2.0 + OIDC Identity Provider in Go 1.26 + Fiber v3 for the arturocarvalho.com platform.

---

## Before any task

Read `README.md` and `PLAN.md`. Check `PYDFE_MIGRATION.md` if the task touches JWT or auth integration with py-dfe-api.

---

## Architecture

```
cmd/api/          Entry point — app wiring, error handler, health check
internal/
  apierror/       RFC 7807 Problem types — ALL errors must use this package
  cache/          Valkey client (disabled when VALKEY_URL is absent)
  config/         Env-driven Config struct
  crypto/         JWTService (RS256), bcrypt helpers, PKCE
  database/       DynamoDB client wrapper
  domain/         Business logic only — no HTTP, no AWS SDK in service layer
    apikey/
    mfa/totp/
    oauth/client/ oauth/code/
    session/
    user/
  handler/        HTTP handlers — one file per route group
  middleware/     RequireAuth JWT middleware
  validate/       go-playground/validator singleton
```

Layering rule: `handler → service → repository`. Services take repository **interfaces**, never concrete types.

---

## Mandatory Workflow

1. Read README.md and relevant domain files.
2. Search with `rg` before creating anything — reuse existing constructors and error types.
3. Plan → Implement → Run affected tests.
4. Update README.md for new endpoints; update CONDUCT.md for new constraints or workarounds.

---

## Scope Control

Implement only what was requested. No opportunistic refactors, no added abstractions.

---

## Engineering Rules

**Errors — MUST follow RFC 7807:**
- Every HTTP error MUST be an `*apierror.Problem` returned via `problem.Send(c)`.
- Never use `c.Status(...).JSON(...)` for errors — it overrides `Content-Type` to `application/json`.
- Use the most specific constructor: `InvalidCredentials`, `ValidationFailed`, `TokenReuse`, etc.
- For validation errors: `apierror.ValidationFailed(ve.Detail(), c.Path())`.
- For OAuth token endpoint: chain `.WithOAuth(code, description)` for RFC 6749 compatibility.
- Never return `apierror.ServerError` for domain errors the caller caused.

**Validation:**
- All request structs MUST be validated via `validate.Struct(req)`.
- Check `validate.IsValidationError(err)` to produce a 422 response with field-level detail.
- Struct tags use JSON field names (registered in `validate/validate.go` init).

**Fiber v3 specifics:**
- Use `c.Context()` (not `c.UserContext()`) — `*fasthttp.RequestCtx` implements `context.Context`.
- Redirect: `c.Redirect().Status(fiber.StatusFound).To(location)` (not `c.Status(...).Redirect()`).
- `c.BodyParser(&req)` returns a parse error on empty body; check for it before validation.
- Error handler uses `errors.AsType[*T]` (Go 1.26 generic errors).

**Repositories:**
- Every domain package exports a `Repository` interface.
- Concrete structs (`dynamoRepository`) are unexported and returned via `NewRepository()`.
- Services take the interface, never the concrete type.
- DynamoDB: `GetItem` > `Query` > `Scan`. No production scans.

**Sessions:**
- `session.Service.ReplaceRefreshToken` — unconditionally replaces token hash. Use on first OAuth code exchange (no prior API token exists).
- `session.Service.Rotate` — validates the presented token before replacing. Use on subsequent refresh token grants.
- Token-reuse detection triggers `Delete` + return `apierror.TokenReuse`.

**Testing — MANDATORY:**
- Unit tests for every domain service: `internal/domain/*/service_test.go`.
- Integration tests for every handler: `internal/handler/*_test.go`.
- Integration tests use in-memory repo implementations in `testhelpers_test.go`.
- No real AWS credentials needed for any test.
- Run with: `go test ./...`

**Health check:**
- `GET /healthz` returns `application/health+json` (not `application/json`).
- Status is `"pass"` only when both DynamoDB and Valkey (if enabled) respond to ping.

**Never commit:** PEM private keys, JWT secrets, AWS credentials, real user data.

---

## Known Constraints

- `errors.AsType[*T]` requires Go 1.26 — do not downgrade.
- Fiber v3 `c.JSON()` sets `Content-Type: application/json` unconditionally; use `c.Send(b)` with explicit `c.Set()` for non-JSON content types.
- Valkey client: when `VALKEY_URL` is an SSM placeholder or otherwise invalid, the client is created in disabled mode — operations are no-ops, not errors.
- `crypto.JWTService` signs with RS256. Any py-dfe-api migration to accept RS256 tokens is documented in `PYDFE_MIGRATION.md`.

---

## Completion Checklist

- Code compiles (`go build ./...`)
- Relevant tests pass (`go test ./...`)
- No new duplication introduced
- README.md updated if routes or config vars changed
- Cross-project impact stated (py-dfe-api, py-dfe-client)
- Conventional Commit suggested: `feat:` / `fix:` / `refactor:` / `docs:` / `chore:`
