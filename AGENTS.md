# AGENTS.md — ctech-account

Multi-agent guidance for automated coding assistants working on this repository.

---

## Quick Start

Before doing anything, every agent MUST read:

1. `CLAUDE.md` — architecture, layering rules, error conventions, testing requirements
2. `README.md` — API surface, configuration, project layout
3. `PLAN.md` — implementation roadmap and sprint status

Then narrow scope: read only the domain package(s) and handler(s) relevant to the task.

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

## Non-Negotiable Rules

1. **Every error must be `*apierror.Problem`** — never raw `fiber.Error`, never `fmt.Errorf` returned to the handler.
2. **Use `validate.Struct(req)` in every handler that parses a request body.** Check `validate.IsValidationError` to
   produce a 422.
3. **Services take repository interfaces**, not concrete types. If you add a new repository method, add it to the
   interface first.
4. **`c.Context()` everywhere** — never `c.UserContext()` (linter rewrites it incorrectly in this environment).
5. **Write tests before marking a task complete.** Unit test for every new service method. Integration test for every
   new route.

---

## Adding a New Route

1. Define a request struct in the handler file with `validate:"..."` tags on every field.
2. Parse with `c.BodyParser`, then `validate.Struct`. Return `apierror.ValidationFailed` on error.
3. Call exactly one service method. Return exactly one schema.
4. Register the route in the handler's `Register(r fiber.Router)` method.
5. Add an integration test in `internal/handler/<group>_test.go` using `newTestApp(t)`.
6. Update the API table in `README.md`.

---

## Adding a New Domain Service Method

1. Add the method signature to the `Repository` interface in `repository.go`.
2. Implement it on `*dynamoRepository`.
3. Add the corresponding method to the `Service` struct in `service.go`.
4. Add it to the in-memory mock in `internal/handler/testhelpers_test.go` (it implements the interface).
5. Write a unit test in `internal/domain/<package>/service_test.go`.

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

## Testing Reference

```bash
# Unit tests only
go test ./internal/domain/...

# Handler integration tests only
go test ./internal/handler/...

# All tests with verbose output
go test ./... -v -count=1
```

No real AWS resources are needed. All integration tests run against in-memory implementations.

---

## Commit Message Convention

```
feat: add WebAuthn registration endpoint
fix: handle empty refresh token on first code exchange
refactor: extract PKCE validation into crypto package
docs: document Valkey MFA token TTL
chore: upgrade fiber to v3.3.1
```

No emojis. No issue references in the subject line.
