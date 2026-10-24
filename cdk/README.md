## EC2 capacity (2026-10-01)

The user-data capacity marker versions the launch template to force replacement of existing Spot instances during migration.

The API uses only `t4g.nano` On-Demand instances with `@aoctech/cdk` 0.10.0 (`onDemand: true`). Spot, capacity rebalance and micro fallback are disabled. The ASG retains replacement headroom. EC2 Instance Savings Plans apply automatically to eligible usage; purchase separately for `t4g` in `us-east-1`. Historical Spot notes below are superseded.

# cdk/ — ctech-account Infrastructure (AWS CDK, TypeScript)

Provisions all AWS infrastructure for `ctech-account`: the OAuth 2.0 / OIDC identity
service. **The implementation here is the source of truth — cross-check against code
before trusting any doc.**

Entry point: `bin/ctech-account.ts`. App: `CtechAccount-{ENV}-<Stack>`.

> **Divergence vs older CLAUDE.md/AGENTS.md (now fixed):** this repo does **not**
> use a single-table design. It provisions **sixteen separate DynamoDB tables** (see
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
| `DynamoDBStack` | `lib/dynamodb-stack.ts` | 16 DynamoDB tables + GSIs (OnDemand) |
| `KYCStack` | `lib/kyc-stack.ts` | 1 private S3 bucket for KYC identity documents |
| `ExportsStack` | `lib/exports-stack.ts` | 1 private S3 bucket for personal-data export zips (8-day lifecycle in prod, 2 days elsewhere) |
| `IAMStack` | `lib/iam-stack.ts` | EC2 instance profile + least-privilege inline policies |
| `ApiStack` | `lib/api-stack.ts` | EC2 ASG + Launch Template + nginx, registered with **HAProxy** |

**`lib/s3-stack.ts` (`S3Stack`) is NOT instantiated** in `bin/ctech-account.ts`. The
deployment/logs buckets are instead the shared `ctech-cdk` buckets passed in as env
vars (`CTECH_DEPLOYMENTS_BUCKET`, `CTECH_LOGS_BUCKET`, `bin/ctech-account.ts:28`).
Treat `S3Stack` as dead code unless/until it is wired in. (Hypothesis: kept as a
template or leftover from a single-repo-buckets design.)

Stack dependencies are wired in `bin/ctech-account.ts` (`IAM` takes the DynamoDB/KYC/Exports outputs,
`Api` takes IAM). There is no frontend stack: it was removed (commit 746c42a), see § 6.

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
- The service runs `t4g.nano` On-Demand (`onDemand: true` in `api-stack.ts`), uses an encrypted
  3-GiB gp3 root volume, private IPv4,
  and IPv6 egress (no NAT gateway). Account-specific nginx, bootstrap/deploy
  scripts remain local.
- **Capacity:** min 1, max **2** in every environment (`api-stack.ts:279-283`) — the
  extra instance gives `CapacityRebalance` headroom to launch a replacement before
  terminating a spot-interrupted instance, not prod-specific scaling.
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
  (`api-stack.ts`).
- Logs only: no local code passes a `metricNamespace`, so no custom CloudWatch metrics are configured here.
  EC2's native `CPUUtilization`/`CPUCreditBalance` remain the CPU source.
  EC2's native `CPUUtilization`/`CPUCreditBalance` remain the CPU source.
- **Runtime config is read from SSM inside `start.sh`** (see `api-stack.ts`) and
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
- **nginx rate limiting** (UNVERIFIED, not found in local `lib/`; may live in the shared `@aoctech/cdk` construct): `limit_req_zone` 20 r/s per
  `$binary_remote_addr` (real viewer IP, rewritten by the realip module from HAProxy),
  `burst=200`, plus `limit_conn_zone` 100 conn/IP. Applies to all non-health routes.
- **Valkey required in non-dev** is enforced by the **Go API at boot**
  (`api/cmd/api/main.go:70`), not by CDK. CDK just supplies the URL via SSM.

---

## 3. DynamoDB — sixteen tables (`lib/dynamodb-stack.ts`)

All `TableV2`, **OnDemand billing** with warm throughput caps
(`maxReadRequestUnits`/`maxWriteRequestUnits` = 1000 each). PITR + `RETAIN` **only in
prod**; `DESTROY` otherwise (`dynamodb-stack.ts:17`). Table name prefix = `{env}`.

| Logical key | Table name | PK / SK | TTL attr | GSIs |
|-------------|-----------|---------|----------|------|
| `account_users` | `{env}_account_users` | `pk` (string) | — | `email-index` (pk=`email`); `kyc-level-index` (pk=`kyc_level`, sk=`kyc_submitted_at`) |
| `account_sessions` | `{env}_account_sessions` | `pk` / `sk` | `expires_at` | `token-hash-index` (pk=`refresh_token_hash`) |
| `account_oauth_clients` | `{env}_account_oauth_clients` | `pk` | — | `owner-index` (pk=`owner_user_id`) |
| `account_api_keys` | `{env}_account_api_keys` | `pk` / `sk` | `expires_at` | `key-hash-index` (pk=`key_hash`) |
| `account_support_tickets` | `{env}_account_support_tickets` | `pk` / `sk` | — | `status-index`, `user-index`, `anon-token-index`, `ticket-number-index` |
| `account_support_metrics` | `{env}_account_support_metrics` | `pk` / `sk` | — | — (transactional daily/monthly/yearly/all-time aggregates) |
| `account_mfa` | `{env}_account_mfa` | `pk` / `sk` | — | — (TOTP `sk=TOTP_default`, passkey `sk=PASSKEY_{id}`) |
| `account_passkeys` | `{env}_account_passkeys` | `pk` / `sk` | — | — |
| `account_deletion_requests` | `{env}_account_deletion_requests` | see `lib/dynamodb-stack.ts` | see code | see code |
| `account_data_exports` | `{env}_account_data_exports` | see `lib/dynamodb-stack.ts` | see code | see code |
| `account_audit` | `{env}_account_audit` | `pk` / `sk` | `expires_at` (400-day) | — (append-only; `pk=USER_{id}|ANON_{ip}`, `sk=EVT_{ts}_{rand}`) |
| `ctech_scopes` | `{env}_ctech_scopes` | `pk` / `sk` | — | — (`SERVICE` legacy/built-ins, `RESOURCE_SERVER` current manifests, immutable `RESOURCE_SERVER_HISTORY#{id}` revisions) |
| `account_organizations` | `{env}_account_organizations` | `pk` / `sk` | — | `lookup-index` (pk=`lookup_pk`, sparse — only rows imported via `SOURCE#{system}#{ref}`) |
| `account_memberships` | `{env}_account_memberships` | `pk` (`ORG#{id}`) / `sk` (`MEMBER#{user}`) | — | `lookup-index` (pk=`lookup_pk`=`USER#{user}` — "which orgs may I act in") |
| `account_invitations` | `{env}_account_invitations` | `pk` (`ORG#{id}`) / `sk` (`INVITE#{email}`) | `expires_at` | `lookup-index` (pk=`lookup_pk`=SHA-256 of the invite token, never the raw token) |
| `account_companies` | `{env}_account_companies` | `pk` (`ORG#{id}`) / `sk` (`COMPANY#`/`TAXID#`/`ACTOR#` rows) | — | `lookup-index` (pk=`lookup_pk`; `USER#{id}` or `SOURCE#{system}#{ref}`, sparse) |
| `account_idempotency` | `{env}_account_idempotency` | `pk` (`IDEM#{scope}`) / `sk` (`KEY#{key}`) | `ttl` | — (Idempotency-Key claims and replayable answers; DESTROY everywhere, rows expire by TTL) |

Notes:
- `account_users` uses `email-index` for login lookup and `kyc-level-index` for the manager review queue. The KYC
  index projects full user records for the protected API after authorization; it is never queried by the browser.
- Refresh tokens are stored per `(session, client)` in `account_sessions`
  (`token-hash-index`).
- `ctech_scopes` deliberately breaks the `{env}_account_*` convention because it is
  the platform-wide scope catalog shared by every ctech service. Downstream
  manifests reuse this table; no new table or index is required.
- `account_organizations`/`account_memberships`/`account_invitations`/`account_companies` back the
  platform-organizations / membership-unification / platform-companies work (see
  `../docs/plans/2026-08-29-platform-organizations.md`, `2026-08-29-membership-unification.md`,
  `2026-08-29-platform-companies.md`). `account_companies` uses a same-transaction `TAXID#{canonical}`
  lock row to make CNPJ uniqueness race-safe across concurrent registrations.

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

## 6. Frontend — removed

`lib/frontend-stack.ts` (S3 + CloudFront) was deleted from this repo (commit 746c42a) and `bin/ctech-account.ts`
no longer instantiates it. `accounts.aoctech.app` is served by **Cloudflare Workers Static Assets**, deployed by
`.github/workflows/frontend.yml` calling `ctech-cdk`'s reusable `frontend-cloudflare.yml`. The browser calls
`accounts-api.aoctech.app` directly and CORS applies; `/.well-known/*` is reachable on the API host only.
Whether a previously deployed CloudFront/S3 stack still exists in AWS is not verifiable from this repo.

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
# or per-stack: ENVIRONMENT=prod npx cdk deploy CtechAccount-prod-Api
```

- `ENVIRONMENT` selects table/bucket prefixes and `RemovalPolicy`/PITR.
- **Bootstrap once:** `cdk bootstrap aws://868899309401/us-east-1`
  (`bin/ctech-account.ts:17`).
- Order matters: deploy `DynamoDB` + `KYC` first, then `IAM`, then `Api`
  (depends on IAM). `OidcStack` is global (deploy once).
- The infrastructure workflow deploys `CtechAccount-Global-OIDC` before the environment-scoped stacks, using the
  separate `ctech-account-gha-infra` role. After a CI-role policy fix, rerun the API stage only after this infrastructure
  stage succeeds. For manual recovery, use
  `ENVIRONMENT=prod npx cdk deploy CtechAccount-Global-OIDC --require-approval never` with infrastructure credentials.
- EC2 user-data pulls the release from `ctech-account/current.zip` in the deployments
  bucket. A normal API deploy overwrites that object and invokes `/opt/app/deploy.sh` through
  **SSM Run Command** on each InService instance; the SSM agent is enabled. ASG instance refresh
  permissions remain available as an operational fallback.
  If that fallback is used, `MinHealthyPercentage: 0` means the service is down while the refresh runs.
- **Daytime-only schedule is disabled.** `api-stack.ts` has the `schedule: {enableCron: '55 11 * * *',
  disableCron: '15 13 * * *'}` line commented out, so the ASG runs continuously (min 1, max 2). Re-enabling it would
  stop the service outside 11:55-13:15 America/Sao_Paulo.
- Jest snapshot tests exist under `test/` (`compute-stack`, `dynamodb-stack`, `exports-stack`, `iam-stack`, `oidc-stack`) —
  run `npm test` in addition to `cdk synth`.

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
| EC2 ASG (1–2 × t4g.nano, on-demand) | always-on (`maxCapacity: 2`) | ~$6–$12 |
| HAProxy edge | shared EC2 + request traffic | owned by `ctech-lbalancer` |
| ~~CloudFront (PriceClass_100, S3 + API passthrough)~~ | retired — the frontend is on Cloudflare | $0 after teardown |
| S3 (deployments + logs + KYC docs + exports) | storage + GETs | ~$1–$10 |
| DynamoDB OnDemand (16 tables, warm cap 1000 RU/WU each) | request units | ~$5–$40 at low volume |
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
