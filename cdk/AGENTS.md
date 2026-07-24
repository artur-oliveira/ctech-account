# AGENTS.md — cdk (ctech-account)

AWS CDK infrastructure — TypeScript. Provisions all AWS resources for ctech-account.

**Before any task:** Read `../README.md` (resource requirements, env vars), `../PLAN.md` (current sprint state).

---

## Role

Defines and deploys all AWS infrastructure for the ctech-account service: **eight**
DynamoDB tables, an EC2 ASG (Go API) behind a **shared** ALB, S3 + CloudFront
(frontend), IAM roles, GitHub Actions OIDC, a private KYC documents bucket, and the
shared deployment/logs buckets. **No Lambda / API Gateway.** Authoritative layout in
`README.md`; this file is the quick reference.

---

## Directory Structure

```
cdk/
├── bin/
│   └── ctech-account.ts        # CDK app entry — instantiates the 7 stacks
├── lib/
│   ├── types.ts                # `Environment = 'dev'|'stage'|'prod'`
│   ├── dynamodb-stack.ts       # EIGHT DynamoDB tables + GSIs (OnDemand)
│   ├── compute-stack.ts        # EC2 ASG + Launch Template behind the SHARED ALB
│   ├── frontend-stack.ts       # S3 + CloudFront (accounts.aoctech.app)
│   ├── kyc-stack.ts            # Private S3 bucket for KYC identity documents
│   ├── iam-stack.ts            # Instance profile + least-privilege inline policies
│   ├── oidc-stack.ts           # GitHub Actions OIDC deploy + infra roles
│   └── s3-stack.ts             # S3Stack — UNUSED (shared ctech-cdk buckets instead)
└── test/                       # ABSENT — `test: jest` exists but no tests written
```

---

## Mandatory Workflow

1. Read `../README.md` before starting.
2. `rg "..."` — search existing stacks for patterns before creating new constructs.
3. `cdk synth` before any deploy to verify template generation.
4. Plan → Implement → `cdk synth` → deploy to dev first.
5. Update `../README.md` for new resources or setup steps.
6. State cross-project impact (cdk ↔ Go API ↔ ui).
7. Suggest Conventional Commit.

---

## Non-Negotiable Rules

1. **`RemovalPolicy.DESTROY` is development-only** — never staging or production.
2. **Never run `cdk destroy` without explicit environment confirmation from the user.**
3. **IAM least privilege** — no wildcard resource ARNs on sensitive permissions.
4. **All resource names derive from `ENVIRONMENT`** — never hardcoded full names.
5. **`cdk synth` must pass cleanly before any proposed deploy.**

---

## Environment Rules

| Environment | Removal Policy    | PITR   | Tables                          |
|-------------|-------------------|--------|---------------------------------|
| development | `DESTROY`         | No     | 8 × `{env}_account_*` / `_ctech_scopes` |
| staging     | `RETAIN`          | No     | 8 × `{env}_account_*` / `_ctech_scopes` |
| production  | `RETAIN`          | Yes    | 8 × `{env}_account_*` / `_ctech_scopes` |

Table names derive from `ENVIRONMENT` in `lib/dynamodb-stack.ts` — there is no single
`ctech-account-{environment}` table.

---

## Common Pitfalls

| Mistake                                     | Correct approach                              |
|---------------------------------------------|-----------------------------------------------|
| `RemovalPolicy.DESTROY` in any stack        | Only in development environment block         |
| Hardcoded ARN in cross-stack reference      | Use CDK export/import (`Fn.importValue`)      |
| Wildcard `*` in IAM resource ARN            | Scope to the specific table/bucket ARN        |
| Adding IAM policy inline in compute stack   | Define in `iam-stack.ts`, pass role as prop   |
| Re-implementing EC2/ASG/ALB wiring          | Reuse `@aoctech/cdk` `PrivateIpv4Ec2Service`  |
| Reusing ALB listener priority 15/35         | ctech-account uses **25** (dfe=15, wallet=35) |
| `cdk deploy --all` without `cdk synth` first | Always `cdk synth` → review → deploy         |

---

## Deployment Commands

```bash
cd cdk && npm install
cdk synth                                    # Always run first
cdk deploy --all                             # Dev
ENVIRONMENT=production cdk deploy StackName  # Prod (specific stack)
```

---

## Testing

```bash
npm test   # CDK snapshot tests (jest)
cdk synth  # CloudFormation synthesis — must succeed cleanly
```

---

## Known Constraints

- SSM parameters must exist before deploying compute stack.
- ACM certificate for CloudFront must be in `us-east-1` regardless of deploy region.
- Go binary on EC2 must be named `ctech-account`.
- `accounts` OAuth client (SPA default client id) must be seeded in DynamoDB after first deploy.

---

## Completion Checklist

- [ ] `cdk synth` succeeds cleanly
- [ ] `npm test` passes
- [ ] No magic strings — all names from environment variable
- [ ] `RemovalPolicy.DESTROY` only in development
- [ ] IAM permissions are least-privilege
- [ ] New resources documented in `../README.md`
- [ ] Cost impact assessed
- [ ] Cross-project impact reviewed (cdk ↔ Go API ↔ ui)
