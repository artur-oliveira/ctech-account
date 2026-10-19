# CLAUDE.md — cdk (ctech-account)

AWS CDK infrastructure — TypeScript. Provisions all AWS resources for ctech-account.

**Before any task:** Read `../README.md` (resource requirements, env vars), `../PLAN.md` (current sprint state).

---

## Role

Defines and deploys all AWS infrastructure for the ctech-account service: **sixteen**
DynamoDB tables, an EC2 ASG (Go API) routed by the **CTech HAProxy edge**,
IAM roles, GitHub Actions OIDC, a private KYC documents
bucket, a private personal-data export bucket, and the shared deployment/logs buckets (owned by `ctech-cdk`).

There is **no Lambda and no API Gateway** — the API is a long-running Go binary on the
ASG. KYC review normally runs through manager-only API/UI routes backed by the
`kyc-level-index`; `api/cmd/kyc` remains the break-glass fallback. See `README.md`
(§Tables, §Compute) for the authoritative layout.

Family infra rules (CDK frozen, Terraform for new infra, no NAT/ALB/custom metrics/alarms, CI gate without
`|| true`) live in the `ctech-dev-standards` skill; this file only covers what is specific to ctech-account.

---

## Directory Structure

```
cdk/
├── bin/
│   └── ctech-account.ts        # CDK app entry point — instantiates the 6 stacks (Oidc, DynamoDB, KYC, Exports, IAM, Api)
├── lib/
│   ├── types.ts                # `Environment = 'dev'|'stage'|'prod'`
│   ├── dynamodb-stack.ts       # SIXTEEN DynamoDB tables + GSIs (OnDemand), incl. platform
│   │                           #   organizations/memberships/invitations/companies
│   ├── api-stack.ts        # EC2 ASG + Launch Template registered with HAProxy
│   ├── kyc-stack.ts            # Private S3 bucket for KYC identity documents
│   ├── exports-stack.ts        # Private S3 bucket for personal-data export zips (8-day lifecycle)
│   ├── iam-stack.ts            # Instance profile + least-privilege inline policies
│   ├── oidc-stack.ts           # GitHub Actions OIDC deploy + infra roles
│   └── s3-stack.ts             # S3Stack — UNUSED (shared ctech-cdk buckets used instead)
└── test/                       # compute-stack, dynamodb-stack, exports-stack, iam-stack, oidc-stack (*.test.ts)
```

> `S3Stack` (`lib/s3-stack.ts`) is defined but **not instantiated** in
> `bin/ctech-account.ts`. Treat it as dead code.

---

## Mandatory Workflow

1. Read `../README.md` before starting.
2. `rg "..."` — search existing stacks for patterns before creating new constructs.
3. `cdk synth` before any deploy to verify template generation.
4. Plan → Implement → `cdk synth` → deploy to dev first.
5. Update `../README.md` §First Deploy for new setup steps.
6. State cross-project impact (cdk ↔ Go API ↔ ui).
7. Suggest Conventional Commit.

---

## Engineering Rules

### DRY (cross-project context)

This repo is TypeScript/CDK, so the Go-layer DRY rules (reuse `ctech-go-common`
helpers, RFC 7807 `problem` helpers, conditional DynamoDB writes, Valkey-required)
live in `../api/CLAUDE.md` and do **not** apply here directly. The CDK equivalents:

- **Reuse shared `@aoctech/cdk` helpers** (OIDC provider import and dual-stack user-data
  helpers). The EC2/ASG wiring is local because `PrivateIpv4Ec2Service` creates the
  retired ALB target group and listener rule.
- **No duplicate stack constructs.** If two stacks need the same resource shape, extract
  a construct class — don't paste the same `new …` block twice.
- **No magic strings / numbers.** Environment name, table/bucket/role names, SSM paths,
  and the ACM cert ARN all derive from a single source
  (`bin/ctech-account.ts` constants / `ENVIRONMENT`) — never hardcoded inline.
- **Stack-to-stack references use CDK exports/imports** (`Fn.importValue`) — never
  hardcoded ARNs.
- **Valkey is mandatory in non-dev.** CDK only *supplies* the URL via SSM
  (`/ctech/{env}/valkey/url`); the **Go API refuses to boot without it** outside dev
  (`api/cmd/api/main.go:70`). Don't add any runtime path that assumes Valkey is
  optional outside dev.

### Constants — no magic strings

- Environment name (`dev`, `stage`, `prod`) flows from a single context/env variable.
- All resource names derived from that prefix — never hardcoded full names.
- SSM parameter paths follow a consistent pattern (`/ctech-account/{env}/...`).

### Environment Rules (critical)

| Environment | Removal Policy    | PITR   |
|-------------|-------------------|--------|
| dev         | `DESTROY`         | No     |
| stage       | `DESTROY`         | No     |
| prod        | `RETAIN`          | Yes    |

(`Environment = 'dev'|'stage'|'prod'`; `isProd` drives both in `dynamodb-stack.ts`, `kyc-stack.ts` and the log
groups in `api-stack.ts`. `exports-stack.ts` is `DESTROY` in every environment.)

- `RemovalPolicy.DESTROY` only for non-prod. Never set it for production.
- **Never run `cdk destroy` without explicit environment confirmation from the user.**
- PITR enabled only in prod.

### IAM — least privilege

- Every EC2 instance role must have minimum permissions: DynamoDB table access, SSM parameter read, S3 deployment bucket read.
- No wildcard resource ARNs on sensitive permissions.
- IAM roles defined in `iam-stack.ts` — never inline in compute stacks.

### DynamoDB

- **Sixteen separate tables**, one per environment prefix (`{env}` + `_account_users`, `_account_sessions`, `_account_oauth_clients`, `_account_api_keys`, `_account_mfa`,
  `_account_passkeys`, `_account_audit`, `_account_support_tickets`, `_account_support_metrics`,
  `_account_deletion_requests`, `_account_data_exports`, `_ctech_scopes`, `_account_organizations`,
  `_account_memberships`, `_account_invitations`, `_account_companies`). See `lib/dynamodb-stack.ts`.
- All tables are **OnDemand** with warm-throughput caps (1000 RU/WU each).
- GSIs are justified by access patterns in `lib/dynamodb-stack.ts` (email lookup,
  refresh-token hash, owner index, API-key hash).
- PITR only in **prod**.

### EC2 / ASG (`api-stack.ts`)

- Native EC2 ASG + launch template, registered in `ctech-lbalancer`'s account route.
- HAProxy performs application health
  checks and requests replacement through its `autoHeal` route policy.
- Go binary deployed as `ctech-account` (systemd service, auto-bootstrap from S3).
- **No custom CloudWatch metrics.** The CloudWatch agent config is logs-only and there are
  no `logs.MetricFilter`s — EC2 already publishes CPUUtilization/CPUCreditBalance for free.
  Don't reintroduce `buildCloudWatchAgentConfig`'s `metricNamespace` here.
- **Deploys are a rolling SSM RunCommand restart, not an instance refresh.**
  `.github/workflows/api.yml` uploads the artifact to S3, then calls `ctech-cdk`'s reusable
  `deploy-backend` action, which runs `aws ssm send-command` (`AWS-RunShellScript`,
  `/opt/app/deploy.sh <artifact-key>`) against every InService instance in the ASG. No
  instances are replaced. `autoscaling:StartInstanceRefresh`/`DescribeInstanceRefreshes`/
  `CancelInstanceRefresh` permissions exist on the deploy role only as an unused
  operational fallback (see `README.md` §First Deploy / IAM).
- **SSM agent is on**: `bin/ctech-account.ts` hardcodes `ENABLE_SSM_AGENT = true` (overriding
  `ApiStack.enableSsmAgent`'s own `false` default) because the deploy mechanism above depends
  on RunCommand reaching every instance. Don't disable it without replacing the deploy path.
- **Daytime-only schedule is disabled.** `api-stack.ts` has the `schedule: {enableCron: '55 11 * * *',
  disableCron: '15 13 * * *'}` line commented out, so the ASG runs continuously (min 1, max 2). Nothing is
  scheduled today; re-enabling it would turn the service off outside 11:55-13:15 America/Sao_Paulo and make
  deploys outside that window exit early.

### Frontend / CloudFront — removed

`lib/frontend-stack.ts` no longer exists and `bin/ctech-account.ts` does not instantiate a frontend stack
(removed in commit 746c42a). `accounts.aoctech.app` is served by Cloudflare Workers Static Assets via
`.github/workflows/frontend.yml` (calls `ctech-cdk`'s reusable workflow). Whether a previously deployed
CloudFront/S3 stack still exists in AWS is not verifiable from this repo; check the console before assuming it.

### Cost Awareness

For every new resource, document:
- Billing model
- Expected usage volume
- Lifecycle policies where applicable

### Secrets

Never commit: AWS credentials, RSA private keys, real account IDs not already in codebase.

---

## Testing

| Change              | Required                              |
|---------------------|---------------------------------------|
| New stack/construct | CDK snapshot test (`jest`)            |
| Table schema change | Update `../README.md` §DynamoDB       |
| IAM change          | Manual review of synthesized policy   |
| Deploy              | `cdk synth` must succeed cleanly      |


Run: `npm test` from `cdk/`. Always run `cdk synth` before proposing a deploy.

- **CI divergence (documented, not fixed):** `.github/workflows/ci.yml` runs `npx cdk synth --quiet 2>&1 | head -5 || true`,
  so a synth failure does not fail the PR check; only `npx tsc --noEmit` and the UI job gate. The skill requires
  synth failures to fail CI. Run `cdk synth` and `npm test` locally.

---

## Deployment

```bash
cd cdk && npm install
cdk synth                                          # Always verify first
cdk deploy --all                                   # All stacks (dev)
ENVIRONMENT=prod cdk deploy StackName        # Specific stack (prod)
```

**Bootstrap (once per account/region):**

```bash
cdk bootstrap aws://ACCOUNT_ID/REGION
```

See `../README.md` §First Deploy for the full ordered checklist.

---

## Known Constraints

- SSM parameters must exist before deploying the compute stack. Signing keys live at
  `/ctech-account/{env}/jwk/active` (+ `/jwk/previous`) — **not** `rsa-private-key`.
  Runtime config (base-url, allowed-origins, app-url, optional webauthn-rpid, google-*, cookie-domain,
  from-email, internal-token) lives under `/ctech-account/{env}/*`; the shared edge-SG/VPC
  params under `/ctech/{env}/*`; the Valkey URL under `/ctech/{env}/valkey/url`.
- `accounts` OAuth client (SPA default client id) must be seeded in DynamoDB after first deploy (see `../README.md`).
- Go binary must be named `ctech-account` on EC2 (systemd service name matches).
- `S3Stack` is unused; deployments/logs buckets are shared `ctech-cdk` buckets.
- Jest snapshot tests exist under `test/` (`compute-stack`, `dynamodb-stack`, `exports-stack`, `iam-stack`, `oidc-stack`) — run `npm test` in addition to `cdk synth`.

---

## Critical Areas (require analysis before touching)

- DynamoDB table definition (schema changes are destructive without migration)
- IAM instance profile (least privilege — over-permissioning is a security risk)
- ASG / HAProxy health check config (rolling deploy safety)
- `RemovalPolicy` on any resource

Before touching: identify blast radius, verify environment, confirm with user for production.

---

## Completion Checklist

- [ ] `cdk synth` succeeds cleanly
- [ ] `npm test` passes (snapshot tests)
- [ ] No magic strings — all names derived from environment
- [ ] `RemovalPolicy.DESTROY` never in prod
- [ ] IAM permissions are least-privilege
- [ ] New resources documented in `../README.md`
- [ ] Cost impact assessed
- [ ] Cross-project impact reviewed (cdk ↔ Go API ↔ ui)

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.
