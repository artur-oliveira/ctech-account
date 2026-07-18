# AGENTS.md — api (ctech-account)

Go API — OAuth 2.0 + OpenID Connect Identity Provider. Go 1.26, Fiber v3, DynamoDB, Valkey.

**Before any task:** Read `../README.md` and `../PLAN.md`.

---

## Repository Map

| Path                                   | Purpose                                                                       |
|----------------------------------------|-------------------------------------------------------------------------------|
| `cmd/api/main.go`                      | App bootstrap — Fiber config, route registration, error handler, health check |
| `internal/apierror/problem.go`         | RFC 7807 engine — all error constructors live here                            |
| `internal/validate/validate.go`        | Validator singleton — use `validate.Struct()`, never instantiate your own     |
| `internal/crypto/jwt.go`               | JWT signing/verification (RS256)                                              |
| `internal/domain/*/repository.go`      | Repository interfaces + DynamoDB implementations                              |
| `internal/domain/*/service.go`         | Business logic — only layer that calls repositories                           |
| `internal/handler/*.go`                | HTTP handlers — only layer that calls services                                |
| `internal/handler/testhelpers_test.go` | In-memory repos + test app builder shared by all handler tests                |

---

## Architecture

**Layering:** `handler → service → repository`. Services take repository **interfaces**, never concrete types.

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
    mfa/totp/ mfa/passkey/
    oauth/client/ oauth/code/
    session/
    user/
  handler/        HTTP handlers — one file per route group
  middleware/     RequireAuth / OptionalAuth JWT middleware
  validate/       go-playground/validator singleton
```

---

## Mandatory Workflow

1. Read `../README.md` and relevant domain files before starting.
2. `rg "..."` — search for existing implementations before creating new code.
3. Plan → Implement → Run affected tests.
4. Update `../README.md` for new endpoints or config vars; update `../CONDUCT.md` for new constraints.
5. State cross-project impact (ui, cdk, ctech-dfe).
6. Suggest Conventional Commit (`feat:` / `fix:` / `refactor:` / `docs:` / `chore:`, no emojis).

---

## Non-Negotiable Rules

1. **Every HTTP error must be `*apierror.Problem`** — never raw `fiber.Error`, never `fmt.Errorf` returned to handler.
2. **`validate.Struct(req)` in every handler that parses a body.** Check `validate.IsValidationError` → 422.
3. **Services take repository interfaces**, not concrete types. Add to the interface first, then implement.
4. **`c.Context()` everywhere** — never `c.UserContext()`.
5. **Write tests before marking complete.** Unit test for every new service method. Integration test for every new route.

---

## Adding a New Route

1. Define request struct with `validate:"..."` tags on every field.
2. Parse with `c.BodyParser`, then `validate.Struct`. Return `apierror.ValidationFailed` on error.
3. Call exactly one service method. Return exactly one schema.
4. Register in the handler's `Register(r fiber.Router)` method.
5. Add integration test in `internal/handler/<group>_test.go` using `newTestApp(t)`.
6. Update the API table in `../README.md`.

---

## Adding a New Domain Service Method

1. Add the method signature to the `Repository` interface in `repository.go`.
2. Implement it on `*dynamoRepository`.
3. Add the corresponding method to the `Service` struct in `service.go`.
4. Add it to the in-memory mock in `internal/handler/testhelpers_test.go`.
5. Write a unit test in `internal/domain/<package>/service_test.go`.

---

## Engineering Rules

### DRY

- Before writing any function, search `internal/` for existing implementations.
- One validator singleton — never instantiate `validator.New()` inline.
- Never duplicate error constructors — check `apierror/` before adding new ones.

### Constants — no magic strings

- OAuth `grant_type` strings, PKCE method names, cache key prefixes, DynamoDB attribute names → named constants.
- Never hardcode table names — always from `config.DynamoTable`.

### Error Handling

- `apierror.ValidationFailed(ve.Detail(), c.Path())` for 422s.
- `apierror.InvalidCredentials(...)` for 401 auth failures.
- `.WithOAuth(code, description)` on token endpoint errors (RFC 6749).
- `apierror.TokenReuse` for refresh token replay detection.

### Session Handling

- `session.Service.ReplaceRefreshToken` → first code exchange (no prior token).
- `session.Service.Rotate` → all subsequent refresh grants (validates before replacing).

### Fiber v3

- `c.Context()` not `c.UserContext()`.
- `c.Redirect().Status(302).To(url)` not `c.Status(302).Redirect(url)`.

---

## Common Pitfalls

| Mistake                                                      | Correct approach                                          |
|--------------------------------------------------------------|-----------------------------------------------------------|
| `c.Status(400).JSON(err)`                                    | `apierror.InvalidRequest(...).Send(c)`                    |
| `return fiber.ErrUnauthorized`                               | `return apierror.Unauthorized(..., c.Path())`             |
| Instantiating `validator.New()` inline                       | Import `validate` package, call `validate.Struct()`       |
| `c.UserContext()`                                            | `c.Context()`                                             |
| `c.Status(302).Redirect(url)`                                | `c.Redirect().Status(302).To(url)`                        |
| Adding a method to concrete repo without adding to interface | Add to interface first, then implement                    |
| Calling `session.Rotate` on first code exchange              | Use `session.ReplaceRefreshToken` — no prior token exists |

---

## Testing

```bash
go test ./internal/domain/...  # unit tests only
go test ./internal/handler/... # integration tests only
go test ./... -v -count=1      # all tests verbose
```

No real AWS resources needed. All integration tests run against in-memory implementations.

| Change              | Required                              |
|---------------------|---------------------------------------|
| New service method  | Unit test                             |
| New route           | Integration test                      |
| Bug fix             | Regression test                       |
| Auth flow change    | Integration test (full handler flow)  |

---

## Known Constraints

- `errors.AsType[*T]` requires Go 1.26 — do not downgrade.
- Valkey disabled mode: operations are no-ops, not errors — design accordingly.
- RS256 only — no HS256, no `SECRET_KEY`.
- `GET /v1.0/health-check` returns `application/health+json` — not `application/json`.
- KID rotation affects all downstream JWT consumers (ctech-dfe) — coordinate carefully.

---

## Secrets

Never commit: RSA private keys (`key.pem`), JWT secrets, AWS credentials, real user data.

---

## Completion Checklist

- [ ] `go build ./...` compiles
- [ ] `go test ./...` passes
- [ ] No duplication introduced
- [ ] All constants named (no magic strings)
- [ ] All errors via `apierror.*` + `problem.Send(c)`
- [ ] `validate.Struct(req)` in every body-parsing handler
- [ ] `../README.md` updated if routes or config vars changed
- [ ] Cross-project impact reviewed (ui ↔ cdk ↔ ctech-dfe)
