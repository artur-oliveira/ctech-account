# CLAUDE.md — cdk (ctech-account)

AWS CDK infrastructure — TypeScript. Provisions all AWS resources for ctech-account.

**Before any task:** Read `../README.md` (resource requirements, env vars), `../PLAN.md` (current sprint state).

---

## Role

Defines and deploys all AWS infrastructure for the ctech-account service: **eight**
DynamoDB tables, an EC2 ASG (Go API) behind a **shared** Application Load Balancer,
S3 + CloudFront (frontend), IAM roles, GitHub Actions OIDC, a private KYC documents
bucket, and the shared deployment/logs buckets (owned by `ctech-cdk`).

There is **no Lambda and no API Gateway** — the API is a long-running Go binary on the
ASG, and KYC review is a CLI (`api/cmd/kyc`), not an HTTP route. See `README.md`
(§Tables, §Compute) for the authoritative layout.

> **Stale-doc correction:** older text here said "single-table design /
> `ctech-account-{environment}`" and referenced `/rsa-private-key` + "Lambda + SQS".
> All three are wrong — verify against `lib/dynamodb-stack.ts`, `lib/compute-stack.ts`,
> and `bin/ctech-account.ts`.

---

## Directory Structure

```
cdk/
├── bin/
│   └── ctech-account.ts        # CDK app entry point — instantiates the 7 stacks
├── lib/
│   ├── types.ts                # `Environment = 'dev'|'stage'|'prod'`
│   ├── dynamodb-stack.ts       # EIGHT DynamoDB tables + GSIs (OnDemand)
│   ├── compute-stack.ts        # EC2 ASG + Launch Template behind the SHARED ALB
│   ├── frontend-stack.ts       # S3 + CloudFront (accounts.aoctech.app)
│   ├── kyc-stack.ts            # Private S3 bucket for KYC identity documents
│   ├── iam-stack.ts            # Instance profile + least-privilege inline policies
│   ├── oidc-stack.ts           # GitHub Actions OIDC deploy + infra roles
│   └── s3-stack.ts             # S3Stack — UNUSED (shared ctech-cdk buckets used instead)
└── test/                       # ABSENT — `test: jest` exists but no tests are written
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

- **Reuse shared `@aoctech/cdk` constructs** (`PrivateIpv4Ec2Service`, OIDC provider
  import, dual-stack helpers) instead of re-implementing EC2/ASG/ALB wiring. This is
  the CDK analogue of "reuse `ctech-go-common`" — one pattern across ctech-dfe,
  ctech-wallet, ctech-account.
- **No duplicate stack constructs.** If two stacks need the same resource shape, extract
  a construct class — don't paste the same `new …` block twice.
- **No magic strings / numbers.** Environment name, table/bucket/role names, listener
  priorities, SSM paths, and the ACM cert ARN all derive from a single source
  (`bin/ctech-account.ts` constants / `ENVIRONMENT`) — never hardcoded inline.
- **Stack-to-stack references use CDK exports/imports** (`Fn.importValue`) — never
  hardcoded ARNs.
- **Valkey is mandatory in non-dev.** CDK only *supplies* the URL via SSM
  (`/ctech/{env}/valkey/url`); the **Go API refuses to boot without it** outside dev
  (`api/cmd/api/main.go:70`). Don't add any runtime path that assumes Valkey is
  optional outside dev.

### Constants — no magic strings

- Environment name (`development`, `staging`, `production`) flows from a single context/env variable.
- All resource names derived from that prefix — never hardcoded full names.
- SSM parameter paths follow a consistent pattern (`/ctech-account/{env}/...`).

### Environment Rules (critical)

| Environment | Removal Policy    | PITR   |
|-------------|-------------------|--------|
| development | `DESTROY`         | No     |
| staging     | `RETAIN`          | No     |
| production  | `RETAIN`          | Yes    |

- `RemovalPolicy.DESTROY` is **development-only**. Never set it for staging or production.
- **Never run `cdk destroy` without explicit environment confirmation from the user.**
- PITR enabled only in staging/production.

### IAM — least privilege

- Every EC2 instance role must have minimum permissions: DynamoDB table access, SSM parameter read, S3 deployment bucket read.
- No wildcard resource ARNs on sensitive permissions.
- IAM roles defined in `iam-stack.ts` — never inline in compute stacks.

### DynamoDB

- **Eight separate tables**, one per environment prefix (`{env}_account_users`,
  `_account_sessions`, `_account_oauth_clients`, `_account_api_keys`, `_account_mfa`,
  `_account_passkeys`, `_account_audit`, `_ctech_scopes`). See `lib/dynamodb-stack.ts`.
- All tables are **OnDemand** with warm-throughput caps (1000 RU/WU each).
- GSIs are justified by access patterns in `lib/dynamodb-stack.ts` (email lookup,
  refresh-token hash, owner index, API-key hash).
- PITR only in **production**.

### EC2 / ASG (compute-stack)

- Pattern mirrors `ApiStackV2` from ctech-dfe: EC2 ASG + ALB target group.
- Combined EC2 + ELB health checks with `gracePeriod: 120s`.
- Go binary deployed as `ctech-account` (systemd service, auto-bootstrap from S3).
- `AutoRollback: true` on instance refresh.

### CloudFront (frontend-stack)

- S3 origin with OAC (Origin Access Control) — bucket not publicly accessible.
- Custom domain: `accounts.aoctech.app`.

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

---

## Deployment

```bash
cd cdk && npm install
cdk synth                                          # Always verify first
cdk deploy --all                                   # All stacks (dev)
ENVIRONMENT=production cdk deploy StackName        # Specific stack (prod)
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
  Runtime config (base-url, allowed-origins, app-url, google-*, cookie-domain,
  from-email, internal-token) lives under `/ctech-account/{env}/*`; the shared ALB/VPC
  params under `/ctech/{env}/*`; the Valkey URL under `/ctech/{env}/valkey/url`.
- `accounts` OAuth client (SPA default client id) must be seeded in DynamoDB after first deploy (see `../README.md`).
- Go binary must be named `ctech-account` on EC2 (systemd service name matches).
- CloudFront distribution requires ACM certificate in `us-east-1` regardless of deploy region.
- `S3Stack` is unused; deployments/logs buckets are shared `ctech-cdk` buckets.
- No jest tests exist despite the `test` script. `cdk synth` is the only automated gate.

---

## Critical Areas (require analysis before touching)

- DynamoDB table definition (schema changes are destructive without migration)
- IAM instance profile (least privilege — over-permissioning is a security risk)
- ASG / ALB health check config (rolling deploy safety)
- `RemovalPolicy` on any resource
- CloudFront + S3 OAC config

Before touching: identify blast radius, verify environment, confirm with user for production.

---

## Completion Checklist

- [ ] `cdk synth` succeeds cleanly
- [ ] `npm test` passes (snapshot tests)
- [ ] No magic strings — all names derived from environment
- [ ] `RemovalPolicy.DESTROY` only in development
- [ ] IAM permissions are least-privilege
- [ ] New resources documented in `../README.md`
- [ ] Cost impact assessed
- [ ] Cross-project impact reviewed (cdk ↔ Go API ↔ ui)
