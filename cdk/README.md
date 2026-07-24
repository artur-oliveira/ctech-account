# cdk/ — ctech-account Infrastructure (AWS CDK, TypeScript)

Provisions all AWS infrastructure for `ctech-account`: the OAuth 2.0 / OIDC identity
service. **The implementation here is the source of truth — cross-check against code
before trusting any doc.**

Entry point: `bin/ctech-account.ts`. App: `CtechAccount-{ENV}-<Stack>`.

> **Divergence vs older CLAUDE.md/AGENTS.md (now fixed):** this repo does **not**
> use a single-table design. It provisions **eight separate DynamoDB tables** (see
> §Tables). There is **no Lambda and no API Gateway** anywhere in this CDK — the API
> runs on an **EC2 Auto Scaling Group behind a shared Application Load Balancer**, and
> the SSM signing-key path is `/ctech-account/{env}/jwk/*` (not `rsa-private-key`).

---

## 1. Stacks

All stacks are instantiated in `bin/ctech-account.ts`. `Environment` ∈
`dev | stage | prod` (`lib/types.ts:1`). Domains: prod = `*.aoctech.app`; dev/stage =
`*-{env}.aoctech.app` (`bin/ctech-account.ts:37`).

| Stack | File | Resources |
|-------|------|-----------|
| `OidcStack` (global, deployed once) | `lib/oidc-stack.ts` | GitHub Actions OIDC deploy role + infra role (`AdministratorAccess`) |
| `DynamoDBStack` | `lib/dynamodb-stack.ts` | 8 DynamoDB tables + GSIs (OnDemand) |
| `KYCStack` | `lib/kyc-stack.ts` | 1 private S3 bucket for KYC identity documents |
| `IAMStack` | `lib/iam-stack.ts` | EC2 instance profile + least-privilege inline policies |
| `ComputeStack` | `lib/compute-stack.ts` | EC2 ASG + Launch Template + nginx, behind the **shared** ALB |
| `FrontendStack` | `lib/frontend-stack.ts` | S3 (static export) + CloudFront + URL-rewrite function |

**`lib/s3-stack.ts` (`S3Stack`) is NOT instantiated** in `bin/ctech-account.ts`. The
deployment/logs buckets are instead the shared `ctech-cdk` buckets passed in as env
vars (`CTECH_DEPLOYMENTS_BUCKET`, `CTECH_LOGS_BUCKET`, `bin/ctech-account.ts:28`).
Treat `S3Stack` as dead code unless/until it is wired in. (Hypothesis: kept as a
template or leftover from a single-repo-buckets design.)

Stack dependencies (`bin/ctech-account.ts:92`): `IAM → {DynamoDB, KYC}`,
`Compute → IAM`.

---

## 2. Compute — EC2 ASG + shared ALB (`lib/compute-stack.ts`)

- **Shared ALB, not owned here.** The HTTPS listener ARN and ALB security group are
  imported from SSM at synth time (`compute-stack.ts:55`):
  - `/ctech/{env}/alb/https-listener-arn`
  - `/ctech/{env}/network/alb-sg-id`
- **Listener-rule priority = 25** (`bin/ctech-account.ts:108`), chosen so it does not
  collide with `py-dfe-api` (15) or `ctech-wallet-api` (35).
- Uses the shared `@aoctech/cdk` `PrivateIpv4Ec2Service` construct
  (`compute-stack.ts:361`) — an EC2 ASG + ALB target group pattern (no NAT gateway).
  **The instance type is set inside `@aoctech/cdk` and is not visible in this repo**
  (hypothesis: a `t3`/`t4g` small/medium — verify in that package before cost math).
- **Capacity:** min 1, max **3 in prod**, max 1 otherwise (`compute-stack.ts:379`).
- **Health check:** `/v1.0/health-check`, healthy HTTP 200 (`compute-stack.ts:376`).
- **User data** (`compute-stack.ts:81`) installs nginx + CloudWatch/SSM agents, writes
  an nginx config that listens on `:8080` and reverse-proxies to the Go binary on
  `:8000`, then a `start.sh` that (a) pulls secrets from SSM and (b) execs
  `/opt/app/current/bootstrap` (the Go binary). `deploy.sh` pulls a release zip from
  the deployments bucket and restarts the `app` systemd service
  (`compute-stack.ts:261`).
- **Runtime config is read from SSM inside `start.sh`** (`compute-stack.ts:229`) and
  exported as environment variables — the Go API itself reads plain env vars (see
  `api/internal/config/config.go`). SSM paths resolved at boot:
  - `/ctech-account/{env}/internal-token`
  - `/ctech-account/{env}/base-url`
  - `/ctech-account/{env}/allowed-origins`
  - `/ctech-account/{env}/app-url`
  - `/ctech-account/{env}/google-client-id`
  - `/ctech-account/{env}/google-client-secret` (SecureString)
  - `/ctech-account/{env}/cookie-domain`
  - `/ctech-account/{env}/from-email`
  - Valkey URL from `valkeyUrlSsmPath` = **`/ctech/{env}/valkey/url`**
    (`bin/ctech-account.ts:107`); only fetched when that path is provided.
- **nginx rate limiting** (`compute-stack.ts:134`): `limit_req_zone` 20 r/s per
  `$binary_remote_addr` (real viewer IP, rewritten by the realip module from the ALB),
  `burst=200`, plus `limit_conn_zone` 100 conn/IP. Applies to all non-health routes.
- **Valkey required in non-dev** is enforced by the **Go API at boot**
  (`api/cmd/api/main.go:70`), not by CDK. CDK just supplies the URL via SSM.

---

## 3. DynamoDB — eight tables (`lib/dynamodb-stack.ts`)

All `TableV2`, **OnDemand billing** with warm throughput caps
(`maxReadRequestUnits`/`maxWriteRequestUnits` = 1000 each). PITR + `RETAIN` **only in
prod**; `DESTROY` otherwise (`dynamodb-stack.ts:17`). Table name prefix = `{env}`.

| Logical key | Table name | PK / SK | TTL attr | GSIs | File |
|-------------|-----------|---------|----------|------|------|
| `account_users` | `{env}_account_users` | `pk` (string) | — | `email-index` (pk=`email`) | `:24` |
| `account_sessions` | `{env}_account_sessions` | `pk` / `sk` | `expires_at` | `token-hash-index` (pk=`refresh_token_hash`) | `:48` |
| `account_oauth_clients` | `{env}_account_oauth_clients` | `pk` | — | `owner-index` (pk=`owner_user_id`) | `:74` |
| `account_api_keys` | `{env}_account_api_keys` | `pk` / `sk` | `expires_at` | `key-hash-index` (pk=`key_hash`) | `:98` |
| `account_mfa` | `{env}_account_mfa` | `pk` / `sk` | — | — (TOTP `sk=TOTP_default`, passkey `sk=PASSKEY_{id}`) | `:125` |
| `account_passkeys` | `{env}_account_passkeys` | `pk` / `sk` | — | — | `:140` |
| `account_audit` | `{env}_account_audit` | `pk` / `sk` | `expires_at` (400-day) | — (append-only; `pk=USER_{id}|ANON_{ip}`, `sk=EVT_{ts}_{rand}`) | `:157` |
| `ctech_scopes` | `{env}_ctech_scopes` | `pk` / `sk` | — | — (single partition `pk=SERVICE`; shared platform-wide scope catalog) | `:176` |

Notes:
- `account_users` is the only table with a GSI on `email` (login lookup).
- Refresh tokens are stored per `(session, client)` in `account_sessions`
  (`token-hash-index`).
- `ctech_scopes` deliberately breaks the `{env}_account_*` convention because it is
  the platform-wide scope catalog shared by every ctech service (`dynamodb-stack.ts:173`).

---

## 4. KYC documents bucket (`lib/kyc-stack.ts`)

- Private S3 bucket `{env}-ctech-account-kyc-documents` (`kyc-stack.ts:33`):
  `BLOCK_ALL`, S3-managed encryption, `enforceSSL`, versioned, `RETAIN` in prod.
- Lifecycle: expire objects after **5 years**, noncurrent after 30 days (`kyc-stack.ts:43`).
- CORS: `PUT` from the frontend origin (`https://accounts{+-env}.aoctech.app`) **and**
  `http://localhost:3001` (marked `TODO: remove this, test only`, `kyc-stack.ts:9`).
- The browser uploads documents straight to S3 via a presigned PUT; the API only
  mints the URL and `HeadObject`s to confirm. IAM is scoped to `kyc/*` (§5).

---

## 5. IAM — instance profile (`lib/iam-stack.ts`)

Role `${env}-ctech-account-role`, assumed by `ec2.amazonaws.com`, plus managed
policies `AmazonSSMManagedInstanceCore` + `CloudWatchAgentServerPolicy`
(`iam-stack.ts:23`). Inline policies (all least-privilege, **no `*` on data resources**):

| Action(s) | Resource | Purpose |
|-----------|----------|---------|
| `dynamodb:GetItem/PutItem/UpdateItem/DeleteItem/Query/BatchGetItem/BatchWriteItem/TransactWriteItems/DescribeTable` | every table ARN + `*/index/*` | all read/write + CPF uniqueness transaction |
| `ssm:GetParameter` | `/ctech-account/{env}/*`, `/ctech/{env}/*` | runtime config + shared ALB/VPC params |
| `ssm:PutParameter` | `/ctech-account/{env}/jwk/*` | JWK auto-rotation writes |
| `ses:SendEmail/SendRawEmail` | `arn:aws:ses:*:*:identity/*` | verification / password-reset emails |
| `s3:GetObject` | `{deploymentsBucket}/ctech-account/*` | pull release artifacts |
| `s3:PutObject` | `{logsBucket}/ctech-account/*` | upload rotated app/nginx logs |
| `s3:PutObject/s3:GetObject` | `{kycBucket}/kyc/*` | presign + confirm KYC uploads |
| `ec2:DescribeManagedPrefixLists`, `ec2:GetManagedPrefixListEntries` | `*` (read-only, no resource-level support) | `update-realip.sh` CloudFront prefix list |

---

## 6. Frontend — S3 + CloudFront (`lib/frontend-stack.ts`)

- S3 bucket `{env}-ctech-account-frontend`, `BLOCK_ALL`, OAC
  (`frontend-stack.ts:50`). Static export from `ui/` (no server).
- CloudFront distribution `accounts.aoctech.app` (prod) with cert `us-east-1`
  (`bin/ctech-account.ts:20`, `frontend-stack.ts:179`). `PriceClass_100`, TLS 1.2 2021.
- **Path routing** (`frontend-stack.ts:22`):
  - Default behavior → S3 (static site), with a **URL-rewrite CloudFront Function**
    (`frontend-stack.ts:76`) that maps clean URLs to `.html` using a **KeyValueStore**
    (`{env}-ctech-account-routes`) populated by the frontend CI after sync. Unknown
    routes → `/404.html`.
  - `/v1.0/*` and `/.well-known/*` → API origin `accounts-api.aoctech.app` (the shared
    ALB), `CACHING_DISABLED`, `ALL_VIEWER_EXCEPT_HOST_HEADER` (forwards cookies,
    Authorization, body), `ALLOW_ALL` methods. Service-to-service callers use
    `accounts-api.aoctech.app` directly (no edge round trip).
- **Security headers policy** (`frontend-stack.ts:115`): HSTS (preload, include
  subdomains), X-Frame-Options DENY, X-Content-Type-Options nosniff,
  Referrer-Policy strict-origin-when-cross-origin, and a CSP with `script-src
  'self' 'unsafe-inline'` / `style-src 'self' 'unsafe-inline'` (temporary debt — no
  nonce/hash pipeline yet) and `connect-src 'self'` + optional extra origins via the
  `securityExtraConnectSrc` CDK context (e.g. `viacep` address lookup).

---

## 7. OIDC / CI roles (`lib/oidc-stack.ts`)

- **GitHub OIDC provider** is owned by `py-dfe-cdk` and imported by ARN
  (`oidc-stack.ts:17`). Trust matches both legacy and immutable-ID `sub` formats.
- `ctech-account-github-deploy-role`: S3 (artifacts + frontend), SSM `GetParameter`
  on `/ctech/*`, ASG/EC2 describe, SSM `SendCommand` (rolling deploy via RunCommand),
  `cloudfront:CreateInvalidation`, KeyValueStore update, and `cloudformation:*` +
  `sts:AssumeRole` (CDK deploy).
- `ctech-account-gha-infra`: **`AdministratorAccess`** — used only by
  `.github/workflows/infra.yml` to run `cdk deploy` (mirrors ctech-wallet/ctech-dfe).

---

## 8. Deploy flow

```bash
cd cdk && npm install
cdk synth                                   # ALWAYS verify first
ENVIRONMENT=prod npx cdk deploy --all --profile ctech --require-approval never
# or per-stack: ENVIRONMENT=prod npx cdk deploy CtechAccount-prod-Compute
```

- `ENVIRONMENT` selects table/bucket prefixes and `RemovalPolicy`/PITR.
- **Bootstrap once:** `cdk bootstrap aws://868899309401/us-east-1`
  (`bin/ctech-account.ts:17`).
- Order matters: deploy `DynamoDB` + `KYC` first, then `IAM`, then `Compute`
  (depends on IAM), then `Frontend`. `OidcStack` is global (deploy once).
- EC2 user-data pulls the first release from `ctech-account/current.zip` in the
  deployments bucket; subsequent deploys run `deploy.sh` via SSM RunCommand from
  GitHub Actions.
- **There are no `cdk` snapshot/jest tests in this repo** (the `test: jest` script
  exists but `test/` is absent) — `cdk synth` is the only automated gate.

### First-deploy prerequisites (outside CDK)
1. Seed signing keys in SSM `/ctech-account/{env}/jwk/active` (+ `/jwk/previous`) via
   `api/cmd/rotatekeys` (see root `README.md` §First Deploy).
2. Seed the `accounts` OAuth client (the SPA default — `SELF_CLIENT_ID`/`NEXT_PUBLIC_OAUTH_CLIENT_ID` both default to `accounts`) in `{env}_account_oauth_clients`
   (`CLIENT_accounts`, `first_party: true`).
3. Seed the scope catalog in `{env}_ctech_scopes` via `api/cmd/seedscopes`.
4. Set the SSM params listed in §2 (base-url, allowed-origins, app-url,
   google-*, cookie-domain, from-email, internal-token) and `/ctech/{env}/valkey/url`.
5. Enable DynamoDB PITR on the 8 tables in prod.

---

## 9. Rough monthly cost (estimates — us-east-1)

> Illustrative only; instance type lives in `@aoctech/cdk` (unknown from this repo).
> Verify against the actual launch template before budgeting.

| Resource | Driver | Est. monthly |
|----------|--------|--------------|
| EC2 ASG (1–3 × general-purpose, e.g. t3.small/medium) | always-on, prod max 3 | ~$15–$60 |
| Shared ALB (cost shared across ctech services) | hourly + LCU | ~$16–$25 (shared) |
| CloudFront (PriceClass_100, S3 + API passthrough) | requests + egress | ~$1–$20 (low traffic) |
| S3 (frontend + deployments + logs + KYC docs) | storage + GETs | ~$1–$10 |
| DynamoDB OnDemand (8 tables, warm cap 1000 RU/WU each) | request units | ~$5–$40 at low volume |
| Data transfer / NAT (no NAT GW — dual-stack) | egress | ~$1–$10 |
| **Total (single env, low traffic)** | | **~$40–$185 / mo** |

DynamoDB cost scales with request volume, not table count; the 1000-RU/WU warm caps
bound the bill. KYC bucket lifecycle (5-yr expire) keeps storage bounded.

---

## 10. Known constraints & divergences

- **8 tables, not 1** — older docs saying "single-table / `ctech-account-{environment}`"
  are wrong.
- **No Lambda / API Gateway** — KYC is a private S3 bucket + presigned uploads; review
  is a CLI (`api/cmd/kyc`), not an HTTP route.
- **`S3Stack` (`lib/s3-stack.ts`) is unused** — deployments/logs buckets come from
  shared `ctech-cdk`.
- **No jest tests present** despite the `test` script.
- **`gha-infra` role is `AdministratorAccess`** — intentional for `cdk deploy`, scoped
  to the infra workflow only.
- Shared ALB + listener priority 25 means `ctech-account` coexists with `py-dfe-api`
  (15) and `ctech-wallet-api` (35) on the same listener — do not reuse those
  priorities.
