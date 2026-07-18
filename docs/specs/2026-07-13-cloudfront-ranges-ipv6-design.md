# CloudFront origin-facing ranges on IPv6-only instances

**Date:** 2026-07-13
**Repos affected:** ctech-account, ctech-dfe, ctech-wallet

## Problem

`update-realip.sh` (written by the compute/api stacks' user data) downloads
`https://ip-ranges.amazonaws.com/ip-ranges.json` to build nginx `set_real_ip_from`
entries for CloudFront's origin-facing ranges. That hostname has **no AAAA record**
(CNAME to CloudFront, IPv4-only), and the EC2 instances are IPv6-only. The curl
always fails, `realip.conf` is never written, and per-IP rate limiting collapses
onto the ALB's private IP.

## Options considered

1. **Lambda + EventBridge Scheduler mirroring the JSON to S3** — works (S3
   dual-stack speaks IPv6) but adds new infra in ctech-cdk, cross-repo coupling,
   and a silent-staleness failure mode.
2. **DNS64 + NAT64** — rejected: AWS NAT64 requires a NAT Gateway (~$32/mo);
   there is no free NAT64. DNS64 alone yields unreachable synthetic addresses.
3. **AWS-managed prefix list** (chosen) — `com.amazonaws.global.cloudfront.origin-facing`
   contains exactly the ranges the script extracts from the JSON, is maintained by
   AWS, and is readable through the EC2 dual-stack endpoint
   (`ec2.us-east-1.api.aws`). No new infrastructure.

## Design

In each repo's compute/api stack, replace the curl+jq fetch in
`update-realip.sh` with:

```bash
export AWS_USE_DUALSTACK_ENDPOINT=true   # systemd units do not inherit /etc/environment
PL_ID=$(aws ec2 describe-managed-prefix-lists \
  --filters Name=prefix-list-name,Values=com.amazonaws.global.cloudfront.origin-facing \
  --query 'PrefixLists[0].PrefixListId' --output text --region us-east-1)
PREFIXES=$(aws ec2 get-managed-prefix-list-entries --prefix-list-id "$PL_ID" \
  --query 'Entries[].Cidr' --output text --region us-east-1 | tr '\t' '\n')
```

Existing safety guards are kept: refuse to write the file with fewer than 10
prefixes, `nginx -t` validation, conditional reload.

Note: the managed prefix list is IPv4-only, matching reality — CloudFront
connects to origins over IPv4 and `CLOUDFRONT_ORIGIN_FACING` has no
`ipv6_prefixes` entries in ip-ranges.json today, so nothing is lost.

In each repo's iam-stack, the instance role gains:

```
ec2:DescribeManagedPrefixLists, ec2:GetManagedPrefixListEntries on Resource *
```

Both are read-only; `Describe*`/`Get*` EC2 actions do not support
resource-level permissions.

## Testing

- `cdk synth` clean in each repo.
- `npm test` (snapshot tests) updated in each repo.
