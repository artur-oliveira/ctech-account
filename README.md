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
    │   ├── mfa/
    │   │   ├── passkey/ # WebAuthn credential model, repository, service
    │   │   └── totp/    # TOTP secret management
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

| Method   | Path                                           | Auth     | Description                                |
|----------|------------------------------------------------|----------|--------------------------------------------|
| `POST`   | `/v1.0/auth/register`                          | —        | Create a new account                       |
| `POST`   | `/v1.0/auth/login`                             | —        | Password login; sets session cookie        |
| `POST`   | `/v1.0/auth/logout`                            | Optional | Revoke current session cookie              |
| `GET`    | `/v1.0/authorize`                              | Session  | OAuth authorization endpoint               |
| `POST`   | `/v1.0/token`                                  | —        | OAuth token endpoint                       |
| `GET`    | `/v1.0/userinfo`                               | Bearer   | OIDC UserInfo                              |
| `GET`    | `/v1.0/account/profile`                        | Bearer   | Get profile                                |
| `PATCH`  | `/v1.0/account/profile`                        | Bearer   | Update profile                             |
| `POST`   | `/v1.0/account/profile/password`               | Bearer   | Change password                            |
| `GET`    | `/v1.0/account/sessions`                       | Bearer   | List active sessions                       |
| `DELETE` | `/v1.0/account/sessions`                       | Bearer   | Revoke all other sessions                  |
| `DELETE` | `/v1.0/account/sessions/:id`                   | Bearer   | Revoke a specific session                  |
| `GET`    | `/v1.0/account/api-keys`                       | Bearer   | List API keys                              |
| `POST`   | `/v1.0/account/api-keys`                       | Bearer   | Create API key                             |
| `DELETE` | `/v1.0/account/api-keys/:id`                   | Bearer   | Revoke API key                             |
| `POST`   | `/v1.0/auth/mfa/challenge`                     | —        | Exchange MFA token + TOTP code for session |
| `POST`   | `/v1.0/auth/passkeys/authenticate/begin`       | —        | WebAuthn discoverable login challenge      |
| `POST`   | `/v1.0/auth/passkeys/authenticate/complete`    | —        | Validate assertion → session cookie        |
| `GET`    | `/v1.0/account/mfa/totp/setup`                 | Bearer   | Generate TOTP provisioning URI             |
| `POST`   | `/v1.0/account/mfa/totp/confirm`               | Bearer   | Activate TOTP + get backup codes           |
| `DELETE` | `/v1.0/account/mfa/totp`                       | Bearer   | Remove TOTP from account                   |
| `POST`   | `/v1.0/account/mfa/totp/backup-codes`          | Bearer   | Regenerate backup codes                    |
| `GET`    | `/v1.0/account/mfa/passkeys`                   | Bearer   | List registered passkeys                   |
| `POST`   | `/v1.0/account/mfa/passkeys/register/begin`    | Bearer   | WebAuthn registration challenge            |
| `POST`   | `/v1.0/account/mfa/passkeys/register/complete` | Bearer   | Validate attestation → persist credential  |
| `DELETE` | `/v1.0/account/mfa/passkeys/:id`               | Bearer   | Remove a passkey                           |
| `GET`    | `/.well-known/openid-configuration`            | —        | OIDC Discovery document                    |
| `GET`    | `/.well-known/jwks.json`                       | —        | JSON Web Key Set                           |
| `GET`    | `/healthz`                                     | —        | Health check (`application/health+json`)   |

---

## Error Format

All errors follow [RFC 7807](https://www.rfc-editor.org/rfc/rfc7807):

```json
{
  "type": "https://accounts.arturocarvalho.com/problems/invalid-credentials",
  "title": "Invalid Credentials",
  "status": 401,
  "detail": "The email or password is incorrect.",
  "instance": "/v1.0/auth/login"
}
```

Token endpoint errors additionally include `error` and `error_description` (RFC 6749).

---

## Configuration

All configuration is read from environment variables at startup.

| Variable            | Required | Description                                                 |
|---------------------|----------|-------------------------------------------------------------|
| `ENVIRONMENT`       | Yes      | `production`, `staging`, or `development`                   |
| `BASE_URL`          | Yes      | Public base URL, e.g. `https://accounts.arturocarvalho.com` |
| `PORT`              | No       | HTTP port (default `8080`)                                  |
| `DYNAMO_TABLE`      | Yes      | DynamoDB table name                                         |
| `RSA_PRIVATE_KEY`   | Yes      | PEM-encoded RSA private key for JWT signing (RS256)         |
| `PUBLIC_KEY_KID`    | Yes      | Key ID included in JWKS                                     |
| `VALKEY_URL`        | No       | Redis-compatible URL; cache disabled when absent or invalid |
| `ACCESS_TOKEN_TTL`  | No       | Access token lifetime in seconds (default `900`)            |
| `REFRESH_TOKEN_TTL` | No       | Refresh token lifetime in seconds (default `2592000`)       |

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

## First Deploy Checklist

Run these once before the first production deployment. Order matters.

### 1 — Generate RSA key pair (RS256 for JWT signing)
```bash
# 4096-bit RSA key, no passphrase (Lambda/ECS reads it from env)
openssl genrsa -out key.pem 4096
openssl rsa -in key.pem -pubout -out key.pub

# Verify
openssl rsa -in key.pem -check -noout
```

### 2 — Store secrets in AWS SSM Parameter Store
```bash
REGION=eu-west-1
ENV=production

# RSA private key
aws ssm put-parameter \
  --name "/$ENV/ctech-account/RSA_PRIVATE_KEY" \
  --value "$(cat key.pem)" \
  --type SecureString --region $REGION

# Assign a key ID (any stable string, e.g. year + env)
aws ssm put-parameter \
  --name "/$ENV/ctech-account/PUBLIC_KEY_KID" \
  --value "2026-$ENV" \
  --type String --region $REGION

# Delete local private key after storing
rm key.pem
```

### 3 — Deploy CDK infrastructure
```bash
cd cdk
npm install
npx cdk bootstrap aws://ACCOUNT_ID/$REGION
npx cdk deploy --all
```
This creates: DynamoDB table, Lambda function, API Gateway, IAM roles, SSM read permissions.

### 4 — Seed the `accounts-ui` OAuth client in DynamoDB
The frontend BFF uses client ID `accounts-ui` for the authorization code flow. Write this item once:

```bash
TABLE=ctech-account-production  # adjust to your CDK output

aws dynamodb put-item --table-name $TABLE --region $REGION --item '{
  "pk":           {"S": "OAUTH_CLIENT#accounts-ui"},
  "sk":           {"S": "OAUTH_CLIENT#accounts-ui"},
  "client_id":    {"S": "accounts-ui"},
  "redirect_uris":{"SS": ["https://accounts.arturocarvalho.com/api/auth/login"]},
  "scopes":       {"SS": ["openid", "profile", "email"]},
  "public":       {"BOOL": true}
}'
```

### 5 — Configure Next.js environment (Vercel / ECS / EC2)
```bash
API_URL=https://api-id.execute-api.eu-west-1.amazonaws.com/prod  # your API GW URL
NEXT_PUBLIC_API_URL=$API_URL
OAUTH_CLIENT_ID=accounts-ui
BASE_URL=https://accounts.arturocarvalho.com
```
Set these in Vercel dashboard → Settings → Environment Variables, or in your ECS task definition.

### 6 — Deploy Next.js frontend
```bash
cd ui
npm run build  # verify clean build before deploy
# then: vercel deploy --prod  OR  docker build + push + ECS service update
```

### 7 — Smoke test
```bash
# Backend health
curl -s https://<api-gw-url>/healthz | jq .

# OIDC discovery
curl -s https://<api-gw-url>/.well-known/openid-configuration | jq .issuer

# JWKS (confirm your kid matches PUBLIC_KEY_KID)
curl -s https://<api-gw-url>/.well-known/jwks.json | jq '.keys[0].kid'

# Frontend
curl -sI https://accounts.arturocarvalho.com/login  # expect 200
```

### 8 — Post-deploy
- Rotate the RSA key annually: generate new pair, update SSM, redeploy, update `PUBLIC_KEY_KID`.
- Enable DynamoDB Point-in-Time Recovery on the table.
- Set CloudWatch alarm on Lambda error rate > 1%.

---

## License

Elastic License 2.0 — see [LICENSE.md](LICENSE.md).
