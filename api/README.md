# api/ — ctech-account Go backend (OAuth 2.0 + OIDC)

The identity-provider API for the aoctech.app platform. **Go 1.26, Fiber v3, uber/fx
DI, layered `handler → service → repository`, DynamoDB, Valkey, JWKS/OIDC.**

> The implementation is the source of truth. Cross-check against code before trusting
> any doc. Endpoint-level detail (method, path, auth, request/response shapes, business
> rules, side-effects, traced `handler→service→repository`) lives in
> [`ENDPOINTS.md`](ENDPOINTS.md). Feature/configuration reference is the root
> [`../README.md`](../README.md).

---

## What it does

- OAuth 2.0 (Authorization Code + PKCE), OpenID Connect (discovery, JWKS, UserInfo).
- Persistent cookie sessions with rotation + token-reuse detection.
- API keys, TOTP/WebAuthn MFA, step-up auth, KYC (document-based, manual review).
- RFC 7807 `application/problem+json` for all errors; `application/health+json` at
  `GET /v1.0/health-check`.

## Layout

```
api/
├── cmd/
│   ├── api/            # Entry point — Fiber wiring, route registration, error handler, health
│   ├── rotatekeys/     # One-time / forced JWKS key rotation (SSM)
│   ├── createclient/   # Provision a first-party confidential M2M client
│   ├── seedscopes/     # Seed the platform scope catalog into DynamoDB
│   └── kyc/            # CLI KYC reviewer (list / show / approve / reject)
└── internal/
    ├── apierror/       # RFC 7807 Problem types — ALL errors use this package
    ├── cache/          # Valkey client (disabled no-op when VALKEY_URL absent)
    ├── config/         # Env-driven Config (cmd/api/main.go:47 loads it)
    ├── crypto/         # JWTService (RS256), bcrypt, PKCE
    ├── database/       # DynamoDB wrapper + ConditionalUpdate (conditional.go)
    ├── domain/         # Business logic (services + repository interfaces)
    │   ├── apikey/  audit/  kyc/  mfa/  oauth/  session/  user/
    ├── handler/        # HTTP handlers — one file per route group (+ Register)
    ├── keystore/       # SSM-backed signing keys + hourly auto-rotation
    ├── middleware/     # RequireAuth / RequireClientID / RequireInternalScope /
    │                   #   RequireRecentMFA (step-up) / RateLimit (Valkey)
    ├── scopes/         # Platform scope catalog (DynamoDB + 5-min Valkey cache)
    └── validate/       # go-playground/validator singleton
```

## Hard rules (see [`CLAUDE.md`](CLAUDE.md) / [`AGENTS.md`](AGENTS.md))

- **Valkey is mandatory outside `dev`/`development`** — the binary refuses to boot
  without `VALKEY_URL` (`cmd/api/main.go:70`). OAuth codes, MFA/passkey challenges,
  recovery tokens, and all rate limiting live in Valkey with no DynamoDB fallback.
- **Eight DynamoDB tables** (`{env}_account_users`, `_account_sessions`,
  `_account_oauth_clients`, `_account_api_keys`, `_account_mfa`, `_account_passkeys`,
  `_account_audit`, `_ctech_scopes`), all OnDemand.
- **Conditional writes** (`internal/database.ConditionalUpdate`) for every
  read-modify-write race (token rotation, email/CPF uniqueness, TOTP single-use).
- **All errors are `*apierror.Problem`** → `problem.Send(c)`. Never raw
  `fiber.Error` / `c.Status().JSON()` for errors.
- **Reuse `ctech-go-common` helpers** (D1); rate-limiting is a shared concern (D15).
- Layering is strict: handler parses + calls one service method; service does business
  logic; repository only touches DynamoDB.

## Local run

```bash
cd api
docker run -p 8000:8000 amazon/dynamodb-local
export ENVIRONMENT=development BASE_URL=http://localhost:8080 \
       DYNAMO_TABLE=ctech-account-dev RSA_PRIVATE_KEY="$(cat key.pem)"
go run ./cmd/api
```

## Tests

```bash
go test ./internal/domain/...   # unit (services)
go test ./internal/handler/...  # integration (HTTP, in-memory repos)
go test ./...
```

## Cross-links

- Root: [`../README.md`](../README.md) · [`../PLAN.md`](../PLAN.md) ·
  [`../CLAUDE.md`](../CLAUDE.md) · [`../AGENTS.md`](../AGENTS.md)
- Frontend: [`../ui/README.md`](../ui/README.md) · [`../ui/FRONTEND.md`](../ui/FRONTEND.md)
- Infra: [`../cdk/README.md`](../cdk/README.md)
- Design/plans: [`../docs/README.md`](../docs/README.md)
