# Implementation Plan: Manual-only KYC with Selfie

- **Status:** Approved (2026-07-15)
- **Spec:** `docs/specs/kyc-manual.md`
- **Project:** ctech-account (Go) + `ui/` (Next.js)
- **Supersedes:** `docs/plans/2026-07-10-kyc.md` (PIX-era plan, now dead)

## 1. Backend tasks

### `internal/domain/kyc/model.go`
- Delete `LevelBasic`; keep `LevelNone=""`, `LevelVerified="verified"`.
- Delete `MethodPIX`; keep `MethodDocument`.
- Replace `DocTypeSelfie` with `DocTypeSelfieUp/Down/Left/Right` (four short
  head-pose clips instead of one still frame — a printed photo can't turn on
  command, so this is the liveness signal; reviewer still decides).
- Add `RequiredDocTypes = [DocTypeIDFront, DocTypeIDBack, DocTypeSelfieUp, DocTypeSelfieDown, DocTypeSelfieLeft, DocTypeSelfieRight]`.
- Bump `MaxDocuments` 6 → 10 (6 required + headroom for re-takes).
- Add `video/webm`, `video/mp4` to `allowedContentTypes`.
- Keep `DocStatus*`, `Decision*`, `cpf.go`, `address.go`.

### `internal/domain/kyc/service.go`
- `Submit`: drop `LevelBasic` write; require all `RequiredDocTypes` present in
  `u.KYCDocuments` → else `ErrNoDocuments`; set `kyc_method=document`,
  `kyc_doc_status=pending_review`, `kyc_submitted_at`, `kyc_expires_at`;
  `kyc_level` stays `""`.
- Rewrite `state(u)` (no `LevelBasic`): verified → rejected → under_review →
  awaiting_files → not_started.
- Rewrite `isLocked(u)`: locked while `doc_status ∈ {awaiting_files, pending_review}` and not expired.
- **Delete** `Confirm` (PIX) and the `MethodPIX` branch in `assertAcceptsDocuments`.
- Keep `PresignDocument`, `ConfirmDocument`, `DocumentsEnabled`, `DocumentURLs`, `Get`, `GetUser`, `Review`.

### `internal/domain/kyc/repository.go`
- `SaveSubmission`: stop writing `kyc_level=basic`.
- **Add** `ListPendingKYC(ctx) ([]*user.User, error)` — DynamoDB **Scan** filtered on
  `kyc_doc_status = pending_review`. `ponytail:` offline operator tool, not a request
  path; a GSI on `kyc_doc_status` is the scale upgrade.

### `internal/handler/kyc.go`
- Keep `get`, `submit`, `presignDocument`, `confirmDocument`, `internalGet`, `sendStatus`.
- **Delete** `confirm`, `review`, `internalDocuments` + their request structs.
- `Register`: unchanged (4 user routes).
- `RegisterInternal`: **delete**; the slim GET is mounted in `cmd/api/main.go` (below).
- `sendKYCError`: drop PIX-only cases (`ErrCPFMismatch`, `ErrWrongMethod`); keep doc/state errors.

### `internal/scopes/catalog.go`
- Delete `InternalKYC = "internal:kyc"` const + catalog entry. Keep `KYC = "kyc"`.

### `internal/legal/version.go`
- `CurrentToSVersion = "3.0"`, `CurrentPrivacyVersion = "3.0"`.

### `internal/domain/audit/events.go`
- Delete `EventKYCConfirmFailed` (PIX-only). Keep Submitted / DocumentUploaded / Verified / Rejected.

### `cmd/api/main.go`
- Keep `kycRepo`, `kycPresigner` (S3 still needed), `kycSvc`, `kycH`.
- Replace `kycH.RegisterInternal(..., RequireInternalScope(InternalKYC))` with:
  `v1.Get("/internal/kyc/:user_id", RequireAuth(jwtSvc), RequireInternalScope(scopesPkg.InternalWalletConfirmDeposit), kycH.internalGet)`.

## 2. `cmd/kyc/main.go` (NEW)

- Stdlib `flag` subcommands (no new dep).
- Bootstrap `config`, `database.NewClient`, `storage.NewS3` (if `KYC_DOCUMENTS_BUCKET`),
  `kyc.NewRepository`, `kyc.NewService`, `user.NewRepository`, `audit.NewService`.
- `list` → `kycSvc.ListPendingKYC` → print `user_id | legal_name | submitted_at`.
- `show <id>` → `kycSvc.GetUser` (raw CPF) + `kycSvc.DocumentURLs` → print + presigned URLs.
- `approve <id> [--note]` → `kycSvc.Review(ctx, id, approve, note)` + emit `EventKYCVerified`.
- `reject <id> --reason` (reason required) → `kycSvc.Review(ctx, id, reject, reason)` + emit `EventKYCRejected`.

## 3. Frontend tasks (`ui/`)

- **`lib/types.ts`**: `KYCLevel = '' | 'verified'`; `KYCMethod = '' | 'document'`;
  `KYCState` drops `'awaiting_deposit'`.
- **`lib/mutations.ts`**: `KYCSubmission` drops `method` (constant `document`); keep rest.
- **`app/account/identity/page.tsx`**: remove pix/document `Tabs`; `IdentityForm` requires the
  3 uploads before enabling Submit; add `<SelfieCapture/>`.
- **`components/selfie-capture.tsx`** (NEW): `getUserMedia({video:true})` + `MediaRecorder`,
  records 4 short (~1-2s) clips in sequence (up/down/left/right prompts), uploads each via
  `uploadKYCDocumentAPI(blob, type)` with `type` one of `selfie_up|down|left|right`.
- **`components/kyc-document-upload.tsx`**: ensure `id_front`+`id_back`+ all 4 selfie clips required.
- **`locales/{en,pt-BR}.json`**: drop `identity.methodPix*` / `identity.awaitingDeposit`; add
  `identity.selfieUp/Down/Left/Right`, `identity.selfieHint`, `identity.allDocsRequired`.

## 4. Tests

- `internal/domain/kyc/service_test.go`: rewrite — document-only flow, `Submit` rejects without
  the 3 docs, `state()` without `basic`, `Review` approve/reject, `ListPendingKYC`.
- `internal/scopes/scopes_test.go`: drop `InternalKYC` tests.
- `internal/handler/kyc_test.go`: update endpoints; remove confirm/review tests.
- `cmd/kyc`: no unit test (operator tool); domain `Review`/`Submit` coverage covers the logic.

## 5. Docs

- `README.md`: KYC section — manual-only, document method, selfie required, `cmd/kyc` usage,
  removed `internal:kyc` scope, ToS/Privacy `3.0`.
- `CONDUCT.md`: drop `internal:kyc` from scope grammar.

## 6. Verification

- `go build ./...` + `go test ./...` (backend green).
- `cd ui && npx eslint src --ext .ts,.tsx && npm run build` (frontend green).
- Manual: `cmd/kyc list` on a dev table with a pending submission; `show <id>` prints CPF + URLs;
  `approve`/`reject` flips `kyc_level`/`kyc_doc_status`; `GET /account/kyc` reflects it.
- Confirm `ctech-wallet` still reaches `GET /internal/kyc/:user_id` with its
  `internal:wallet:confirm-deposit` token after the re-guard.

## 7. Files touched

```
internal/domain/kyc/{model,service,repository}.go
internal/handler/kyc.go
internal/scopes/catalog.go
internal/legal/version.go
internal/domain/audit/events.go
cmd/api/main.go
cmd/kyc/main.go                (new)
ui/src/lib/{types,mutations}.ts
ui/src/app/account/identity/page.tsx
ui/src/components/{selfie-capture.tsx (new), kyc-document-upload.tsx}
ui/src/locales/{en,pt-BR}.json
README.md, CONDUCT.md
tests: kyc/service_test.go, scopes/scopes_test.go, handler/kyc_test.go
```
