# ctech-account

Centralized OAuth 2.0 + OpenID Connect Identity Provider for the arturocarvalho.com platform.

Built with **Go 1.26** and **Fiber v3**. Runs on AWS Lambda via API Gateway or on EC2/ECS.

---

## Features

- **OAuth 2.0** — Authorization Code flow with PKCE
- **OpenID Connect** — Discovery document, JWKS, UserInfo endpoint
- **Persistent sessions** — Cookie-based refresh tokens with automatic rotation and token-reuse detection
- **API keys** — Long-lived scoped tokens for programmatic access
- **TOTP / MFA** — Time-based one-time passwords (Sprint 2)
- **WebAuthn / Passkeys** — Passwordless authentication (Sprint 2)
- **RFC 7807 Problem Details** — All error responses use `application/problem+json`
- **RFC Health Check** — `/healthz` responds with `application/health+json`
- **DynamoDB** — Single-table design; sessions, users, API keys, OAuth clients and codes
- **Valkey** — Optional cache for MFA tokens and session data (disabled when `VALKEY_URL` is unset)

---

## Project Layout

```
ctech-account/
├── cmd/api/          # Entry point — Fiber app wiring
├── cdk/              # AWS CDK infrastructure
└── internal/
    ├── apierror/     # RFC 7807 Problem Details types + constructors
    ├── cache/        # Valkey client wrapper
    ├── config/       # Environment-driven configuration
    ├── crypto/       # JWT signing (RS256), bcrypt, PKCE helpers
    ├── database/     # DynamoDB client wrapper
    ├── domain/       # Core business logic
    │   ├── apikey/   # API key entity, repository interface, service
    │   ├── mfa/totp/ # TOTP secret management
    │   ├── oauth/    # OAuth client entity + repository interface
    │   │   ├── client/
    │   │   └── code/
    │   ├── session/  # Session entity, repository interface, service
    │   └── user/     # User entity, repository interface, service
    ├── handler/      # HTTP handlers (one file per route group)
    ├── middleware/   # RequireAuth JWT middleware
    └── validate/     # go-playground/validator singleton
```

---

## API

| Method   | Path                                | Auth     | Description                         |
|----------|-------------------------------------|----------|-------------------------------------|
| `POST`   | `/v1/auth/register`                 | —        | Create a new account                |
| `POST`   | `/v1/auth/login`                    | —        | Password login; sets session cookie |
| `POST`   | `/v1/auth/logout`                   | Optional | Revoke current session cookie       |
| `GET`    | `/v1/authorize`                     | Session  | OAuth authorization endpoint        |
| `POST`   | `/v1/token`                         | —        | OAuth token endpoint                |
| `GET`    | `/v1/userinfo`                      | Bearer   | OIDC UserInfo                       |
| `GET`    | `/v1/account/profile`               | Bearer   | Get profile                         |
| `PATCH`  | `/v1/account/profile`               | Bearer   | Update profile                      |
| `POST`   | `/v1/account/profile/password`      | Bearer   | Change password                     |
| `GET`    | `/v1/account/sessions`              | Bearer   | List active sessions                |
| `DELETE` | `/v1/account/sessions`              | Bearer   | Revoke all other sessions           |
| `DELETE` | `/v1/account/sessions/:id`          | Bearer   | Revoke a specific session           |
| `GET`    | `/v1/account/api-keys`              | Bearer   | List API keys                       |
| `POST`   | `/v1/account/api-keys`              | Bearer   | Create API key                      |
| `DELETE` | `/v1/account/api-keys/:id`          | Bearer   | Revoke API key                      |
| `GET`    | `/.well-known/openid-configuration` | —        | OIDC Discovery document             |
| `GET`    | `/.well-known/jwks.json`            | —        | JSON Web Key Set                    |
| `GET`    | `/healthz`                          | —        | Health check (`application/health+json`) |

---

## Error Format

All errors follow [RFC 7807](https://www.rfc-editor.org/rfc/rfc7807):

```json
{
  "type": "https://accounts.arturocarvalho.com/problems/invalid-credentials",
  "title": "Invalid Credentials",
  "status": 401,
  "detail": "The email or password is incorrect.",
  "instance": "/v1/auth/login"
}
```

Token endpoint errors additionally include `error` and `error_description` (RFC 6749).

---

## Configuration

All configuration is read from environment variables at startup.

| Variable             | Required | Description                                           |
|----------------------|----------|-------------------------------------------------------|
| `ENVIRONMENT`        | Yes      | `production`, `staging`, or `development`             |
| `BASE_URL`           | Yes      | Public base URL, e.g. `https://accounts.arturocarvalho.com` |
| `PORT`               | No       | HTTP port (default `8080`)                            |
| `DYNAMO_TABLE`       | Yes      | DynamoDB table name                                   |
| `RSA_PRIVATE_KEY`    | Yes      | PEM-encoded RSA private key for JWT signing (RS256)   |
| `PUBLIC_KEY_KID`     | Yes      | Key ID included in JWKS                               |
| `VALKEY_URL`         | No       | Redis-compatible URL; cache disabled when absent or invalid |
| `ACCESS_TOKEN_TTL`   | No       | Access token lifetime in seconds (default `900`)      |
| `REFRESH_TOKEN_TTL`  | No       | Refresh token lifetime in seconds (default `2592000`) |

---

## Running Locally

```bash
# Start DynamoDB Local
docker run -p 8000:8000 amazon/dynamodb-local

# Export required vars
export ENVIRONMENT=development
export BASE_URL=http://localhost:8080
export DYNAMO_TABLE=ctech-account-dev
export RSA_PRIVATE_KEY="$(cat key.pem)"
export PUBLIC_KEY_ID=dev-key

# Run
go run ./cmd/api
```

---

## Testing

```bash
# Unit tests — all domain services
go test ./internal/domain/...

# Integration tests — all HTTP handlers (no AWS required)
go test ./internal/handler/...

# All tests
go test ./...
```

Integration tests use in-memory repository implementations — no real DynamoDB or Valkey needed.

---

## License

Elastic License 2.0 — see [LICENSE.md](LICENSE.md).
