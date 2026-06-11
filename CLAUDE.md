# CLAUDE.md — ctech-account

OAuth 2.0 + OpenID Connect Identity Provider — Go 1.26, Fiber v3, DynamoDB, Valkey.

**Before any task:** Read `README.md` and `PLAN.md`.

---

## Projects

| Path   | Role                                           | Full guidelines   |
|--------|------------------------------------------------|-------------------|
| root   | Go API — OAuth 2.0 + OIDC backend              | This file         |
| `ui/`  | Next.js 16 frontend — accounts.arturocarvalho.com | `ui/CLAUDE.md` |
| `cdk/` | AWS CDK infrastructure — TypeScript            | `cdk/CLAUDE.md`   |

**Always read the relevant subproject CLAUDE.md before making any change outside the Go API.**

---

## Architecture

```
cmd/api/          Entry point — Fiber app wiring, error handler, health check
internal/
  apierror/       RFC 7807 Problem types — ALL errors must use this package
  cache/          Valkey client (disabled when VALKEY_URL is absent)
  config/         Env-driven Config struct
  crypto/         JWTService (RS256), bcrypt helpers, PKCE
  database/       DynamoDB client wrapper
  domain/         Business logic only — no HTTP, no AWS SDK in service layer
    apikey/
    mfa/totp/
    mfa/passkey/
    oauth/client/
    oauth/code/
    session/
    user/
  handler/        HTTP handlers — one file per route group
  middleware/     RequireAuth / OptionalAuth JWT middleware
  validate/       go-playground/validator singleton
```

**Layering:** `handler → service → repository`. Services take repository **interfaces**, never concrete types.

---

## Mandatory Workflow

1. Read `README.md` and relevant domain files before starting.
2. `rg "..."` — search for existing implementations before creating new code.
3. Plan → Implement → Run affected tests.
4. Update `README.md` for new endpoints or config vars; update `CONDUCT.md` for new constraints.
5. State cross-project impact (ui, cdk, ctech-dfe).
6. Suggest Conventional Commit (`feat:` / `fix:` / `refactor:` / `docs:` / `chore:`, no emojis).

---

## Scope Control

Implement only what was requested. No opportunistic refactors, no added abstractions, no unrelated fixes.

---

## Engineering Rules

### DRY

- Before writing any function, search `internal/` for existing implementations.
- Never duplicate error constructors, validation patterns, or repository logic.
- One validator singleton (`validate` package) — never instantiate `validator.New()` inline.

### Constants — no magic strings/numbers

- All token TTLs, cache key prefixes, DynamoDB attribute names, and HTTP header names must be named constants.
- OAuth `grant_type` strings, `response_type` values, and PKCE method names must be constants — not inline literals.
- Never hardcode table names — always from `config.DynamoTable`.

### Error Handling (MUST follow RFC 7807)

- Every HTTP error MUST be an `*apierror.Problem` returned via `problem.Send(c)`.
- **Never** use `c.Status(...).JSON(...)` for errors.
- Use the most specific constructor: `InvalidCredentials`, `ValidationFailed`, `TokenReuse`, etc.
- For validation errors: `apierror.ValidationFailed(ve.Detail(), c.Path())`.
- For OAuth token endpoint: chain `.WithOAuth(code, description)` for RFC 6749 compatibility.
- Never return `apierror.ServerError` for domain errors the caller caused.

### Validation

- All request structs MUST be validated via `validate.Struct(req)`.
- Check `validate.IsValidationError(err)` to produce a 422 with field-level detail.
- Struct tags use JSON field names (registered in `validate/validate.go` init).

### Layer Separation (strictly enforced)

| Layer      | Allowed                                  | Forbidden                                  |
|------------|------------------------------------------|--------------------------------------------|
| Handler    | Parse request, call ONE service method, respond | Business logic, direct DynamoDB calls |
| Service    | Business logic, cache management         | HTTP parsing, AWS SDK calls                |
| Repository | DynamoDB read/write only                 | Business logic, HTTP concerns              |

### Dependency Injection

- Services take repository **interfaces** — never concrete types.
- Every domain package exports a `Repository` interface.
- Concrete structs (`dynamoRepository`) are unexported and returned via `NewRepository()`.

### Fiber v3 Specifics

- Use `c.Context()` — never `c.UserContext()` (breaks in this environment).
- Redirect: `c.Redirect().Status(fiber.StatusFound).To(location)` — not `c.Status(...).Redirect()`.
- `c.BodyParser(&req)` returns a parse error on empty body — check before validating.
- Error handler uses `errors.AsType[*T]` (Go 1.26 generic errors) — do not downgrade.

### Session Handling

- `session.Service.ReplaceRefreshToken` — use on first OAuth code exchange (no prior token exists).
- `session.Service.Rotate` — validates presented token before replacing; use on all subsequent refresh grants.
- Token-reuse detection triggers `Delete` + returns `apierror.TokenReuse`.

### DynamoDB

- `GetItem` > `Query` > `Scan`. **No production scans.**
- OAuth authorization codes stored in Valkey (TTL 60s) — not DynamoDB.
- MFA tokens stored in Valkey (TTL 5 min).

### Secrets

Never commit: RSA private keys (`key.pem`), JWT secrets, AWS credentials, real user data, real passwords.

---

## Testing (MANDATORY)

| Change              | Required                              |
|---------------------|---------------------------------------|
| New service method  | Unit test in `internal/domain/*/service_test.go` |
| New route           | Integration test in `internal/handler/*_test.go` |
| New repository method | Add to in-memory mock in `testhelpers_test.go` |
| Bug fix             | Regression test                       |
| Auth flow           | Integration test (full handler flow)  |

**Every core function must have an integration test.**

Integration tests use in-memory repo implementations — no real AWS needed.

```bash
go test ./internal/domain/...  # unit tests
go test ./internal/handler/... # integration tests
go test ./...                  # all tests
```

---

## Known Constraints

- `errors.AsType[*T]` requires Go 1.26 — do not downgrade.
- Fiber v3 `c.JSON()` sets `Content-Type: application/json` unconditionally; use `c.Send(b)` + `c.Set()` for non-JSON content types (e.g. `application/health+json`, `application/problem+json`).
- Valkey client: when `VALKEY_URL` is absent or invalid, client is in disabled mode — operations are no-ops, not errors.
- `crypto.JWTService` signs RS256 only. No HS256, no `SECRET_KEY`.
- `GET /healthz` returns `application/health+json` — status is `"pass"` only when DynamoDB and Valkey respond to ping.
- Rate limiting: 5 failed logins / 15 min per IP (Valkey counter), 100 req/min per authenticated user.

---

## Critical Areas (require analysis before touching)

- JWT signing/verification (`crypto/jwt.go`)
- Session rotation and token-reuse detection (`domain/session/`)
- OAuth authorization code flow and PKCE (`handler/authorize.go`, `handler/token.go`)
- MFA gate in login (`handler/auth.go`)
- JWKS endpoint — KID rotation impacts all downstream services (ctech-dfe)

Before touching: identify risks + side effects, verify backward compatibility with ctech-dfe JWT consumers.

---

## Completion Checklist

- [ ] `go build ./...` compiles
- [ ] `go test ./...` passes
- [ ] No duplication introduced (searched before creating)
- [ ] All constants named (no magic strings)
- [ ] All errors returned via `apierror.*` + `problem.Send(c)`
- [ ] `validate.Struct(req)` called in every handler that parses a body
- [ ] `README.md` updated if routes or config vars changed
- [ ] Cross-project impact reviewed (ui ↔ cdk ↔ ctech-dfe)
