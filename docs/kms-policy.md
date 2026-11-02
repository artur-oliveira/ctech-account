# KMS policy: SecureStrings, DynamoDB, S3

The KMS bill must stay ~0 (free tier 20k requests/month). An SSM SecureString read
with decryption and every request to a table on the `aws/dynamodb` key is a billed KMS request.

1. **Decrypt at boot, cache.** Never decrypt in a loop. The JWKS keystore
   (`api/internal/keystore/ssm.go`) reloads every `CheckInterval` (5 min, CAC-012:
   a rotated key must propagate before any token signed with it can be rejected), so
   it reads `jwk/active` and `jwk/previous` WITHOUT decryption first (ciphertext +
   Version, no KMS call) and decrypts only when the Version changed. Rotation latency
   is unchanged (<= 5 min); steady state is 0 KMS requests (before: 2 per tick, ~17k/month per instance).
   Everything else (`start.sh` env) is read once per service start.
2. **DynamoDB uses the AWS-owned key.** `cdk/lib/dynamodb-stack.ts` sets no
   `encryption` (the TableV2 default is the owned key); `cdk/test/dynamodb-stack.test.ts`
   fails if any table opts into KMS. All 17 prod account tables are on the owned key today.
3. **S3 is SSE-S3**: `S3_MANAGED` buckets (`s3-stack`, `kyc-stack`, `exports-stack`), and
   `api/internal/storage/s3.go` never sets `ServerSideEncryption`.
4. One-shot CI/manual reads with `--with-decryption` (`publish-resource-scopes.yml`,
   `cmd/rotatekeys`) are per run, not periodic, and are allowed.
