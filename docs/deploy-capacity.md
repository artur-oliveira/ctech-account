# Deploy capacity: account

Derived from `cdk/lib/api-stack.ts`, `cdk/lib/dynamodb-stack.ts`, `cdk/lib/exports-stack.ts`, `cdk/lib/kyc-stack.ts` and the shared `HaproxyEc2Service` in `@aoctech/cdk` (ctech-cdk). The CDK is the source of truth; update this page when it changes. Account is the family IdP: an outage here blocks login in every product.

## API (EC2 ASG)

| Item | Value |
|---|---|
| Instance type | `t4g.nano` (arm64, 0.5 GiB), the `HaproxyEc2Service` default; no override |
| Purchase | On-demand (`onDemand: true`); older Spot hosts were replaced by a launch-template version bump |
| ASG | min 1, max 2, desired = min; the extra instance exists only for rolling replacement |
| Rolling update | `minInstancesInService = 1`: the old instance serves until the new one is healthy |
| Image / disk | Alpine arm64 AMI from SSM, root volume 3 GiB gp3 (shared default), 256 MB swap |
| Process layout | nginx in front of two app processes (`APP_PORT` / `APP_PORT_ALT`); `GOMEMLIMIT` is 75% of RAM split across them |
| Schedule | None (the daytime-window `schedule` is commented out): always on |
| Logs | CloudWatch Logs only (`app`, `nginx`); prod log groups retained |

## Storage

- DynamoDB: on-demand with a ceiling of **1000 RRU / 1000 WRU per table and per GSI**. PITR in prod only; tables `RETAIN` in prod, `DESTROY` elsewhere. Existing tables are never recreated (Terraform import is a separate change).
- S3: data-export bucket objects expire after 8 days in prod (2 elsewhere), incomplete multipart uploads after 1 day (`exports-stack.ts`); KYC bucket expires objects after 5 years and noncurrent versions after 30 days (`kyc-stack.ts`).
- No Lambdas of its own: the account-deletion saga producer and ack endpoints run inside the API.

## Known limits

- One `t4g.nano` serves all traffic; during a deploy or an instance loss only the surviving instance (max 2) serves. No scaling policy: raise min/max in CDK.
- 0.5 GiB RAM with 256 MB swap leaves little headroom: measure before adding concurrency or in-process caches.
- The 1000 RRU/WRU ceiling throttles bursts (for example a mass login or token refresh) above roughly 1000 1 KB writes or 1000 strongly consistent 4 KB reads per second per table.
- Rate limits and token revocation live in the shared Valkey; if it is down, auth routes fail closed (503) by design.
- Synth in CI still ends with `|| true` (needs AWS credentials); a broken CDK change is only caught at deploy.
