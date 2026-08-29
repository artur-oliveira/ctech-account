# cdk/ — ctech-account Infrastructure (AWS CDK, TypeScript)

Provisions all AWS infrastructure for `ctech-account`: the OAuth 2.0 / OIDC identity
service. **The implementation here is the source of truth — cross-check against code
before trusting any doc.**

Entry point: `bin/ctech-account.ts`. App: `CtechAccount-{ENV}-<Stack>`.

> **Divergence vs older CLAUDE.md/AGENTS.md (now fixed):** this repo does **not**
> use a single-table design. It provisions **eight separate DynamoDB tables** (see
> §Tables). There is **no Lambda and no API Gateway** anywhere in this CDK — the API
> runs on an **EC2 Auto Scaling Group routed by the CTech HAProxy edge load balancer**, and
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
| `ApiStack` | `lib/api-stack.ts` | EC2 ASG + Launch Template + nginx, registered with **HAProxy** |
| `FrontendStack` | `lib/frontend-stack.ts` | S3 (static export) + CloudFront + URL-rewrite function — **retired**, see § 6 |

**`lib/s3-stack.ts` (`S3Stack`) is NOT instantiated** in `bin/ctech-account.ts`. The
deployment/logs buckets are instead the shared `ctech-cdk` buckets passed in as env
vars (`CTECH_DEPLOYMENTS_BUCKET`, `CTECH_LOGS_BUCKET`, `bin/ctech-account.ts:28`).
Treat `S3Stack` as dead code unless/until it is wired in. (Hypothesis: kept as a
template or leftover from a single-repo-buckets design.)

Stack dependencies (`bin/ctech-account.ts:92`): `IAM → {DynamoDB, KYC}`,
`Compute → IAM`.

---

## 2. Compute — EC2 ASG + HAProxy route (`lib/api-stack.ts`)

- **No ALB listener, listener rule, or target group is created or imported.** HAProxy
  discovers the healthy ASG instances from its bootstrap route
  `/ctech/{env}/lbalancer/routes/account`, owned by `ctech-lbalancer`. Its default
  registration targets this ASG, port 8080, `/v1.0/health-check`, HTTP 200, and
  `autoHeal: true`.
- `HaproxyEc2Service` from `@aoctech/cdk` owns the common security group, log
  groups, encrypted launch template, ASG and CPU target tracking. Route creation is
  intentionally omitted because `/ctech/{env}/lbalancer/routes/account` remains
  owned by `ctech-lbalancer`.
- The retained `/ctech/{env}/network/alb-sg-id` parameter now identifies the shared
  edge SG trusted by service instances. Its historical name is intentionally kept
  until every service has migrated without downtime.
- The service uses a `t4g.micro`, encrypted 3-GiB gp3 root volume, private IPv4,
  and IPv6 egress (no NAT gateway). Account-specific nginx, bootstrap/deploy
  scripts and alarms remain local.
- **Capacity:** min 1, max **3 in prod**, max 1 otherwise.
- **Health check:** HAProxy probes `/v1.0/health-check` and accepts HTTP 200. With
  `autoHeal: true`, three unhealthy reconciliations request ASG replacement.
- **User data** installs nginx + CloudWatch/SSM agents, downloads only the
  official Cloudflare Origin CA RSA root, verifies its pinned SHA-256 and
  installs it into the system trust store for
  `*.internal.aoctech.app`, writes
  an nginx config that listens on `:8080` and reverse-proxies to the Go binary on
  `:8000`, then a `start.sh` that (a) pulls secrets from SSM and (b) execs
  `/opt/app/current/bootstrap` (the Go binary). `deploy.sh` pulls a release zip from
  the deployments bucket and restarts the `app` systemd service
  (`compute-stack.ts:261`).
- `buildCloudWatchAgentConfig` publishes four bounded 60-second host series under
  `CtechAccount/<env>/Host`: memory %, swap %, root-disk %, and application RSS.
  EC2's native `CPUUtilization`/`CPUCreditBalance` remain the CPU source.
- **Runtime config is read from SSM inside `start.sh`** (`compute-stack.ts:229`) and
  exported as environment variables — the Go API itself reads plain env vars (see
  `api/internal/config/config.go`). SSM paths resolved at boot:
  - `/ctech-account/{env}/internal-token`
  - `/ctech-account/{env}/base-url`
  - `/ctech-account/{env}/allowed-origins`
  - `/ctech-account/{env}/app-url`
  - `/ctech-account/{env}/webauthn-rpid` (optional; defaults to the `app-url` hostname)
  - `/ctech-account/{env}/google-client-id`
  - `/ctech-account/{env}/google-client-secret` (SecureString)
  - `/ctech-account/{env}/cookie-domain`
  - `/ctech-account/{env}/from-email`
  - Valkey URL from `valkeyUrlSsmPath` = **`/ctech/{env}/valkey/url`**
    (`bin/ctech-account.ts:107`); only fetched when that path is provided.
- **nginx rate limiting** (`compute-stack.ts:134`): `limit_req_zone` 20 r/s per
  `$binary_remote_addr` (real viewer IP, rewritten by the realip module from HAProxy),
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
| `account_users` | `{env}_account_users` | `pk` (string) | — | `email-index` (pk=`email`); `kyc-level-index` (pk=`kyc_level`, sk=`kyc_submitted_at`) | `:24` |
| `account_sessions` | `{env}_account_sessions` | `pk` / `sk` | `expires_at` | `token-hash-index` (pk=`refresh_token_hash`) | `:48` |
| `account_oauth_clients` | `{env}_account_oauth_clients` | `pk` | — | `owner-index` (pk=`owner_user_id`) | `:74` |
| `account_api_keys` | `{env}_account_api_keys` | `pk` / `sk` | `expires_at` | `key-hash-index` (pk=`key_hash`) | `:98` |
| `account_mfa` | `{env}_account_mfa` | `pk` / `sk` | — | — (TOTP `sk=TOTP_default`, passkey `sk=PASSKEY_{id}`) | `:125` |
| `account_passkeys` | `{env}_account_passkeys` | `pk` / `sk` | — | — | `:140` |
| `account_audit` | `{env}_account_audit` | `pk` / `sk` | `expires_at` (400-day) | — (append-only; `pk=USER_{id}|ANON_{ip}`, `sk=EVT_{ts}_{rand}`) | `:157` |
| `ctech_scopes` | `{env}_ctech_scopes` | `pk` / `sk` | — | — (`SERVICE` legacy/built-ins, `RESOURCE_SERVER` current manifests, immutable `RESOURCE_SERVER_HISTORY#{id}` revisions) | `:176` |

Notes:
- `account_users` uses `email-index` for login lookup and `kyc-level-index` for the manager review queue. The KYC
  index projects full user records for the protected API after authorization; it is never queried by the browser.
- Refresh tokens are stored per `(session, client)` in `account_sessions`
  (`token-hash-index`).
- `ctech_scopes` deliberately breaks the `{env}_account_*` convention because it is
  the platform-wide scope catalog shared by every ctech service. Downstream
  manifests reuse this table; no new table or index is required.

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
| `ssm:GetParameter` | `/ctech-account/{env}/*`, `/ctech/{env}/*` | runtime config + shared network params |
| `ssm:PutParameter` | `/ctech-account/{env}/jwk/*` | JWK auto-rotation writes |
| `ses:SendEmail/SendRawEmail` | `arn:aws:ses:*:*:identity/*` | verification / password-reset emails |
| `s3:GetObject` | `{deploymentsBucket}/ctech-account/*` | pull release artifacts |
| `s3:PutObject` | `{logsBucket}/ctech-account/*` | upload rotated app/nginx logs |
| `s3:PutObject/s3:GetObject` | `{kycBucket}/kyc/*` | presign + confirm KYC uploads |
| `ec2:DescribeManagedPrefixLists`, `ec2:GetManagedPrefixListEntries` | `*` (read-only, no resource-level support) | `update-realip.sh` CloudFront prefix list |

---

## 6. Frontend — retired (`lib/frontend-stack.ts`)

> **Nothing routes through this stack.** `accounts.aoctech.app` is served by **Cloudflare Workers
> Static Assets**, deployed by `.github/workflows/frontend.yml` calling `ctech-cdk`'s reusable
> `frontend-cloudflare.yml`. The browser calls `accounts-api.aoctech.app` directly and CORS applies;
> `/.well-known/*` is reachable on the API host only. The stack is still deployed because the
> teardown has not run — that is Phase 4 of
> `ctech-cdk/docs/plans/2026-08-20-frontend-cloudflare-migration.md`. What follows describes what it
> used to do.

- `createNextjsStaticFrontend` from `@aoctech/cdk` creates the private S3 bucket,
  OAC, route KVS, rewrite function, security headers and distribution. Static
  export comes from `ui/` (no server); this stack only supplies account-specific
  API behaviors and CSP additions.
- CloudFront distribution `accounts.aoctech.app` (prod) with cert `us-east-1`
  (`bin/ctech-account.ts:20`, `frontend-stack.ts:179`). `PriceClass_100`, TLS 1.2 2021.
- **Path routing** (`frontend-stack.ts:22`):
  - Default behavior → S3 (static site), with a **URL-rewrite CloudFront Function**
    (`frontend-stack.ts:76`) that maps clean URLs to `.html` using a **KeyValueStore**
    (`{env}-ctech-account-routes`) populated by the frontend CI after sync. Unknown
    routes → `/404.html`.
  - `/v1.0/*` and `/.well-known/*` → API origin `accounts-api.aoctech.app` (HAProxy),
    `CACHING_DISABLED`, `ALL_VIEWER_EXCEPT_HOST_HEADER` (forwards cookies,
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
  on `/ctech/*`, `SendCommand` restricted to `AWS-RunShellScript` and EC2 instances tagged
  `Project=ctech-account`, `GetCommandInvocation`, ASG/EC2 describe, `autoscaling:StartInstanceRefresh` +
  `DescribeInstanceRefreshes` + `CancelInstanceRefresh` (operational fallback),
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
- The infrastructure workflow deploys `CtechAccount-Global-OIDC` before the environment-scoped stacks, using the
  separate `ctech-account-gha-infra` role. After a CI-role policy fix, rerun the API stage only after this infrastructure
  stage succeeds. For manual recovery, use
  `ENVIRONMENT=prod npx cdk deploy CtechAccount-Global-OIDC --require-approval never` with infrastructure credentials.
- EC2 user-data pulls the release from `ctech-account/current.zip` in the deployments
  bucket. A normal API deploy overwrites that object and invokes `/opt/app/deploy.sh` through
  **SSM Run Command** on each InService instance; the SSM agent is enabled. ASG instance refresh
  permissions remain available as an operational fallback.
  If that fallback is used, `MinHealthyPercentage: 0` means the service is down while the refresh runs.
- The ASG only runs between **11:55** and **13:15** America/Sao_Paulo. A deploy outside that
  window exits early and the next scheduled instance picks the artifact up at boot.
- **There are no `cdk` snapshot/jest tests in this repo** (the `test: jest` script
  exists but `test/` is absent) — `cdk synth` is the only automated gate.

### First-deploy prerequisites (outside CDK)
1. From `ctech-cdk`, create/update the shared service URL parameters:
   `CTECH_AWS_PROFILE=ctech ./scripts/configure-service-url-parameters.sh {env}`.
   Internal transport/JWKS parameters use `*.internal.aoctech.app`; public
   issuer/browser parameters remain public by design.
2. Seed signing keys in SSM `/ctech-account/{env}/jwk/active` (+ `/jwk/previous`) via
   `api/cmd/rotatekeys` (see root `README.md` §First Deploy).
3. Seed the OIDC/bootstrap scope catalog in `{env}_ctech_scopes` via
   `api/cmd/seedscopes`.
4. Start/deploy the API. Startup reconciles `RESOURCE_SERVER/account` from its
   embedded manifest and creates/updates the system-owned `accounts` OAuth
   client (`SELF_CLIENT_ID`; callback `${APP_URL}/login/callback`) with every
   required `account:*` scope. No direct OAuth-client DynamoDB seed is needed.
5. Provision each downstream Resource Server and bound publisher once with
   `api/cmd/createresource` (DFe, Wallet and Poker); see
   `docs/resource-server-scope-registry.md`.
6. Set the remaining SSM params listed in §2 (base-url, allowed-origins, app-url,
   google-*, cookie-domain, from-email, internal-token) and `/ctech/{env}/valkey/url`.
7. Enable DynamoDB PITR on the 8 tables in prod.

---

## 9. Rough monthly cost (estimates — us-east-1)

> Illustrative only; verify pricing against the deployed launch template before budgeting.

| Resource | Driver | Est. monthly |
|----------|--------|--------------|
| EC2 ASG (1–3 × t4g.micro) | always-on, prod max 3 | ~$6–$20 |
| HAProxy edge | shared EC2 + request traffic | owned by `ctech-lbalancer` |
| ~~CloudFront (PriceClass_100, S3 + API passthrough)~~ | retired — the frontend is on Cloudflare | $0 after teardown |
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
- The account route is currently one of `ctech-lbalancer`'s bootstrap routes. Do not
  create `/ctech/{env}/lbalancer/routes/account` here until its CloudFormation
  ownership has been explicitly transferred; two stacks cannot own the same SSM
  parameter.
