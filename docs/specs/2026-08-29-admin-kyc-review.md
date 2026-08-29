# Admin KYC review

## Outcome

Authenticated support managers and admins can review Enhanced KYC submissions at `/admin/kyc`. The workspace exposes
pending and completed queues, full identity details, explicit short-lived access to private documents, approve/reject
actions, and an audit trail identifying who accessed documents or completed the review.

## Authorization boundary

The `support_role` in `GET /account/profile` is an affordance only: it controls whether the SPA renders links. It is not
an authorization credential. Every `/v1.0/admin/kyc/*` request runs, in order:

1. `RequireAuth`, which validates the signed access token and establishes the server-derived user ID.
2. `RequireClientID(SELF_CLIENT_ID)`, which rejects API-key, machine and delegated-client tokens even when their subject
   belongs to a privileged account. This applies to the existing support admin routes too.
3. `RequireSupportRole(userSvc, manager)`, which reloads that user from DynamoDB and compares the stored role rank.
4. The handler, which derives reviewer ID/name/role from that authenticated account; request bodies never accept an
   actor ID or role.

An `agent` receives `403` for list, detail, document access and decisions. `manager` and `admin` are allowed. Revocation
takes effect on the next request without waiting for access-token expiry. A forged profile response or modified Zustand
state can reveal UI chrome only; it cannot reveal KYC data.

## Data access and audit

- Queue reads use the `account_users/kyc-level-index` GSI (`kyc_level`, `kyc_submitted_at`) instead of scanning users.
- List responses contain summary fields only. Raw CPF, phone, birth date, address, risk signals and document metadata are
  confined to the protected detail endpoint.
- S3 remains private. `POST .../documents/access` is an explicit auditable action that returns ten-minute presigned GET
  URLs. The API records `kyc.documents_viewed` against the subject with reviewer ID/name/role, request IP and user-agent.
- URLs are bearer capabilities: the UI never persists them and opens them with `noopener noreferrer`.
- Reviewer GET URLs support direct browser navigation and sign only `host`. Optional S3 response-checksum negotiation is
  disabled for these presigns because a new browser tab cannot attach `x-amz-checksum-mode`; integrity and authorization
  remain protected by HTTPS, SigV4, the private bucket policy and the ten-minute expiration.
- Rejection accepts one fixed code (`document_unreadable`, `document_incomplete`, `document_mismatch`, `selfie_mismatch`,
  `data_mismatch`, `suspected_fraud`, `other`) plus optional details capped at 255 characters. `other` requires details.
- Approve/reject records `kyc.verified` or `kyc.rejected`. The latest reviewer ID/name, decision and timestamp are also
  denormalized onto the user so completed queues remain attributable if an audit query is temporarily unavailable.

## Concurrency and state

The repository decision update is conditional on `kyc_level=enhanced AND kyc_status=pending`. Two reviewers may inspect
the same submission, but only the first decision commits; later attempts receive `409`. A rejected decision clears the
document list and asynchronously deletes S3 objects. Approval retains document metadata and objects under the existing
five-year bucket lifecycle.

## Cross-project impact

- **UI:** adds the authenticated Admin menu entry and `/admin/kyc` workspace; agents see support only.
- **API:** adds manager-only review endpoints and audit attribution. No client-supplied role is trusted.
- **CDK:** adds `kyc-level-index` to `account_users`; deploy DynamoDB before releasing API code that queries it.
- **ctech-dfe / ctech-wallet:** no code change. JWT signing, JWKS, OAuth flows and the token-facing `kyc_level` contract
  remain unchanged (`""`, `"basic"`, `"verified"`). A newly approved user receives `verified` on their next token refresh,
  matching the existing CLI review behavior.
