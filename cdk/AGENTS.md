# AGENTS.md — cdk (ctech-account)

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
│   ├── compute-stack.ts        # EC2 ASG + Launch Template
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

| Environment | Removal Policy    | PITR   | Table Name              |
|-------------|-------------------|--------|-------------------------|
| development | `DESTROY`         | No     | `ctech-account-development` |
| staging     | `RETAIN`          | No     | `ctech-account-staging` |
| production  | `RETAIN`          | Yes    | `ctech-account-production` |

---

## Common Pitfalls

| Mistake                                     | Correct approach                              |
|---------------------------------------------|-----------------------------------------------|
| `RemovalPolicy.DESTROY` in any stack        | Only in development environment block         |
| Hardcoded ARN in cross-stack reference      | Use CDK export/import (`Fn.importValue`)      |
| Wildcard `*` in IAM resource ARN            | Scope to the specific table/bucket ARN        |
| Adding IAM policy inline in compute stack   | Define in `iam-stack.ts`, pass role as prop   |
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
- `accounts-ui` OAuth client must be seeded in DynamoDB after first deploy.

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
