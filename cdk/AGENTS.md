# AGENTS.md — cdk (ctech-account)

AWS CDK infrastructure — TypeScript. Provisions all AWS resources for ctech-account.

**Before any task:** Read `../README.md` (resource requirements, env vars), `../PLAN.md` (current sprint state).

---

## Role

Defines and deploys all AWS infrastructure for the ctech-account service: **sixteen**
DynamoDB tables, an EC2 ASG (Go API) routed by the **CTech HAProxy edge**, IAM roles, GitHub Actions OIDC, a private KYC documents
bucket, a private personal-data export bucket, and the shared deployment/logs buckets. **No Lambda / API Gateway.** Authoritative layout in
`README.md`; this file is the quick reference. Family infra rules (CDK frozen, Terraform for new infra, no NAT/ALB/custom metrics/alarms, CI gate without `|| true`) live in the `ctech-dev-standards` skill; this file only covers what is specific to ctech-account.

---

## Directory Structure

```
cdk/
├── bin/
│   └── ctech-account.ts        # CDK app entry — instantiates the 6 stacks (Oidc, DynamoDB, KYC, Exports, IAM, Api)
├── lib/
│   ├── types.ts                # `Environment = 'dev'|'stage'|'prod'`
│   ├── dynamodb-stack.ts       # SIXTEEN DynamoDB tables + GSIs (OnDemand)
│   ├── api-stack.ts        # EC2 ASG + Launch Template registered with HAProxy
│   ├── kyc-stack.ts            # Private S3 bucket for KYC identity documents
│   ├── exports-stack.ts        # Private S3 bucket for personal-data export zips
│   ├── iam-stack.ts            # Instance profile + least-privilege inline policies
│   ├── oidc-stack.ts           # GitHub Actions OIDC deploy + infra roles
│   └── s3-stack.ts             # S3Stack — UNUSED (shared ctech-cdk buckets instead)
└── test/                       # compute-stack, dynamodb-stack, exports-stack, iam-stack, oidc-stack (*.test.ts)
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

1. **`RemovalPolicy.DESTROY` is non-prod only** — never production.
2. **Never run `cdk destroy` without explicit environment confirmation from the user.**
3. **IAM least privilege** — no wildcard resource ARNs on sensitive permissions.
4. **All resource names derive from `ENVIRONMENT`** — never hardcoded full names.
5. **`cdk synth` must pass cleanly before any proposed deploy.**

---

## Environment Rules

| Environment | Removal Policy | PITR | Tables                                   |
|-------------|----------------|------|------------------------------------------|
| dev         | `DESTROY`      | No   | 16 x `{env}_account_*` / `_ctech_scopes` |
| stage       | `DESTROY`      | No   | 16 x `{env}_account_*` / `_ctech_scopes` |
| prod        | `RETAIN`       | Yes  | 16 x `{env}_account_*` / `_ctech_scopes` |

Table names derive from `ENVIRONMENT` in `lib/dynamodb-stack.ts` — there is no single
`ctech-account-{environment}` table.

---

## Common Pitfalls

| Mistake                                     | Correct approach                              |
|---------------------------------------------|-----------------------------------------------|
| `RemovalPolicy.DESTROY` in any stack        | Only for non-prod (see `isProd`)              |
| Hardcoded ARN in cross-stack reference      | Use CDK export/import (`Fn.importValue`)      |
| Wildcard `*` in IAM resource ARN            | Scope to the specific table/bucket ARN        |
| Adding IAM policy inline in compute stack   | Define in `iam-stack.ts`, pass role as prop   |
| Reintroducing ALB listener/target-group wiring | Use the HAProxy route `/ctech/{env}/lbalancer/routes/account` instead |
| `cdk deploy --all` without `cdk synth` first | Always `cdk synth` → review → deploy         |

---

## Deployment Commands

```bash
cd cdk && npm install
cdk synth                                    # Always run first
cdk deploy --all                             # Dev
ENVIRONMENT=prod cdk deploy StackName  # Prod (specific stack)
```

---

## Testing

```bash
npm test   # CDK snapshot tests (jest)
cdk synth  # CloudFormation synthesis — must succeed cleanly
```

---

> **CI divergence (documented, not fixed):** `.github/workflows/ci.yml` runs `npx cdk synth --quiet 2>&1 | head -5 || true`,
> so a synth failure does not fail the PR check; only `npx tsc --noEmit` and the UI job gate. The skill requires
> synth failures to fail CI. Run `cdk synth` and `npm test` locally.

## Known Constraints

- SSM parameters must exist before deploying compute stack.
- Go binary on EC2 must be named `ctech-account`.
- `accounts` OAuth client (SPA default client id) must be seeded in DynamoDB after first deploy.

---

## Completion Checklist

- [ ] `cdk synth` succeeds cleanly
- [ ] `npm test` passes
- [ ] No magic strings — all names from environment variable
- [ ] `RemovalPolicy.DESTROY` never in prod
- [ ] IAM permissions are least-privilege
- [ ] New resources documented in `../README.md`
- [ ] Cost impact assessed
- [ ] Cross-project impact reviewed (cdk ↔ Go API ↔ ui)

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.
