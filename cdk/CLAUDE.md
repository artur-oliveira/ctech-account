# CLAUDE.md — cdk (ctech-account)

AWS CDK infrastructure — TypeScript. Provisions all AWS resources for ctech-account.

**Before any task:** Read `../README.md` (resource requirements, env vars), `../PLAN.md` (current sprint state).

---

## Role

Defines and deploys all AWS infrastructure for the ctech-account service: DynamoDB table,
EC2 ASG (Go API), S3 + CloudFront (frontend), IAM roles, GitHub Actions OIDC, and S3 for deployment artifacts.

---

## Directory Structure

```
cdk/
├── bin/
│   └── ctech-account.ts        # CDK app entry point
├── lib/
│   ├── types.ts                # Shared types / interfaces across stacks
│   ├── dynamodb-stack.ts       # Single DynamoDB table + GSIs
│   ├── compute-stack.ts        # EC2 ASG + Launch Template (clone of ApiStackV2 pattern)
│   ├── frontend-stack.ts       # S3 + CloudFront (accounts.aoctech.app)
│   ├── iam-stack.ts            # Instance profile + DynamoDB/SSM/S3 permissions
│   ├── oidc-stack.ts           # GitHub Actions OIDC role
│   └── s3-stack.ts             # Deployment artifacts bucket
└── test/
```

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

### DRY

- Never duplicate stack constructs. Extract shared patterns (e.g., Lambda + SQS) into a construct class.
- Table name, bucket names, and function names follow a single naming convention derived from `ENVIRONMENT`.
- Stack-to-stack references use CDK exports/imports — never hardcoded ARNs.

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

- Single-table design — one table per environment.
- Table name: `ctech-account-{environment}`.
- GSIs must be justified by documented access patterns before creation.
- PITR only in staging/production.

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

- SSM parameters (`/ctech-account/{env}/rsa-private-key`, etc.) must be created before deploying compute stack.
- `accounts-ui` OAuth client must be seeded in DynamoDB after first deploy (see `../README.md`).
- Go binary must be named `ctech-account` on EC2 (systemd service name matches).
- CloudFront distribution requires ACM certificate in `us-east-1` regardless of deploy region.

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
