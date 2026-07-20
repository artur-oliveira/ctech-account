# docs/ — ctech-account Design & Planning Records

This directory holds the **design specs, implementation plans, and security reviews**
for `ctech-account`. These are decision records and working notes — they may lag the
code. **The implementation in `api/`, `ui/`, `cdk/` is always the source of truth.**
When a spec disagrees with shipped code, trust the code and flag it.

Cross-links:
- Root [`README.md`](../README.md) — authoritative feature/endpoint/config reference.
- [`PLAN.md`](../PLAN.md) — current sprint state.
- Per-layer docs: [`api/ENDPOINTS.md`](../api/ENDPOINTS.md),
  [`ui/FRONTEND.md`](../ui/FRONTEND.md), [`cdk/README.md`](../cdk/README.md).

---

## Security reviews

| File | Topic |
|------|-------|
| [`2026-07-16-security-review.md`](2026-07-16-security-review.md) | General security review findings |
| [`specs/2026-07-19-api-security-audit-remediation.md`](specs/2026-07-19-api-security-audit-remediation.md) | Remediation plan for the API security audit |

---

## Design specs

| File | Topic |
|------|-------|
| [`specs/2026-07-10-account-hardening-design.md`](specs/2026-07-10-account-hardening-design.md) | Account-hardening design (sessions, MFA, etc.) |
| [`specs/2026-07-10-kyc-design.md`](specs/2026-07-10-kyc-design.md) | KYC design (document-based, manual review) |
| [`specs/2026-07-15-kyc-manual.md`](specs/2026-07-15-kyc-manual.md) | Manual KYC review process |
| [`specs/2026-07-15-kyc-manual.md`](specs/2026-07-15-kyc-manual.md) | KYC manual-review addendum |
| [`specs/2026-07-13-cloudfront-ranges-ipv6-design.md`](specs/2026-07-13-cloudfront-ranges-ipv6-design.md) | CloudFront IPv6 / origin-range realip design |
| [`specs/2026-07-10-kyc-design.md`](specs/2026-07-10-kyc-design.md) | (see above) |

---

## Implementation plans

| File | Topic |
|------|-------|
| [`plans/2026-07-10-kyc.md`](plans/2026-07-10-kyc.md) | KYC implementation plan |
| [`plans/2026-07-10-audit-log.md`](plans/2026-07-10-audit-log.md) | Audit-log implementation plan |
| [`plans/2026-07-10-step-up-auth.md`](plans/2026-07-10-step-up-auth.md) | Step-up authentication plan |
| [`plans/2026-07-10-jwks-rotation.md`](plans/2026-07-10-jwks-rotation.md) | JWKS key-rotation plan |
| [`plans/2026-07-15-kyc-manual.md`](plans/2026-07-15-kyc-manual.md) | Manual KYC plan |

---

## Roadmap

- [`ROADMAP.md`](ROADMAP.md) — longer-term direction.

> To add a record: drop a dated file under `specs/` (design) or `plans/` (execution),
> then add a row above. Keep filenames `YYYY-MM-DD-<slug>.md`.
