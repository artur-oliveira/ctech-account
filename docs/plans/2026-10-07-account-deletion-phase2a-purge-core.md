# Account Deletion — Phase 2a (blockers, purge orchestration, account tombstone) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish ctech-account's side of a deletion with what the existing repositories already
support:
- refuse (at request) and stop (at grace end) on local blockers, with a `blocked` state the user can cancel from;
- freeze the single-member organizations into the request;
- run an ordered, idempotent purge that removes memberships, company edges, consents, sessions, API keys and MFA;
- end in a tombstone, with the KYC record moved to a retention item (D6).

**Architecture:** `deletion.Service` gains two optional collaborators.
- An `Ownership` port answers "what blocks this user" and "which organizations go with them".
- A `Purger` port runs the account purge. The `locked → purging → purged` steps use it.
- Both default to no-ops, so Phase 1 behaviour holds until they are wired.

`AccountOwnership` and `AccountPurger` implement the ports over existing services. `user.Service.Purge` writes the tombstone in one transaction:
- the tombstone (conditional on the request id);
- the `KYCRET#{sub}` retention item;
- deletes for the e-mail and CPF uniqueness markers.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB, `api-commons` v1.13.1.

**Spec:** `docs/specs/2026-10-06-account-deletion-ctech-account.md` §6–§7 and
`docs/specs/2026-10-06-account-deletion-data-inventory.md` §3 (decisions D4, D6, D10 in the overview).

**Builds on:** Phase 1 (`docs/plans/2026-10-07-account-deletion-phase1-lifecycle.md`, merged).

**Phase 2b (separate plan, after this):**
- erase the frozen organizations (org item, memberships, invitations, companies, company actors);
- anonymize support tickets/messages and audit rows (IP/UA);
- move KYC documents in S3 to `retention/{sub}/` (+ IAM, users-table TTL for `KYCRET#`).

Until 2b ships, the feature flag stays off in production.

## Global Constraints

- Branch `feat/account-deletion-phase2`. Conventional Commits, **no `Co-Authored-By` or Claude attribution**.
- Layering `handler → service → repository`; services take interfaces; no AWS SDK in services.
- Every HTTP error is an `*apierror.Problem`.
- Conditional writes for every read-modify-write race (api/CLAUDE.md).
- No PII in logs, audit metadata or SNS messages.
- The tombstone keeps only: `pk`, `deletion_state="purged"`, `deletion_request_id`, `purged_at`, `cpf_hmac`, ToS/Privacy version + acceptance timestamps, `created_at`, `updated_at`. It must carry **no** `email` and **no** `kyc_level` attribute: those are GSI keys, so their absence drops the item from `email-index` and `kyc-level-index`.
- KYC retention item: `pk = KYCRET#{sub}` in `{env}_account_users`, record under the map attribute `kyc_record`, so none of its attributes is a GSI key. `retain_until` = purge + 5 years (D6). `expires_at` is set to the same instant; the TTL on that table arrives in 2b.
- CPF keyed hash: `hex(HMAC-SHA256(HMAC-SHA256(SECRET_ENC_KEY, "cpf-hmac"), cpf))` via `Sealer.MAC`. Never log the CPF or the hash.
- Blocker codes: `account.organization_shared_owner` (detail `organization_id`), `account.oauth_client_owned` (detail `client_id`).
- Run `cd api && go vet ./... && go test ./...` green after every task.

## Rulings against the spec (recorded in Task 6)

- **R4 Owned (non-first-party) OAuth clients block deletion** instead of being erased. Other users may hold consents and refresh tokens for them, and there is no index from a client to its consents. Cost if wrong: developers must delete their apps first.
- **R5 Sent invitations keep `invited_by`.** It is the opaque `sub`, not PII, and invitations expire on their own. Cost if wrong: a pending invitation shows an unresolvable inviter id until it expires.
- **R6 Owner memberships of frozen (single-member) organizations are left for Phase 2b**, which erases the whole organization. Cost if wrong: an orphan org row survives until 2b ships. The flag stays off in production until then.

## Review Focus

1. **Grace ends while the user still owns a shared organization.** The request must become `blocked` (not `locked`), the user must get an e-mail with a working cancel link, and cancelling from `blocked` must unlock. Test: Task 3 `TestGraceEnd_BlockedNotifiesAndCanCancel`.
2. **The purge fails half-way** (DynamoDB throttles on API keys). The request stays `purging` and due, and the next tick re-runs every step without failing on already-removed items. Tests: Task 3 `TestPurging_FailureRetried` and Task 4 `TestAccountPurger_RunsEveryStepThenTombstone`.
3. **The tombstone write races a newer request**, or is re-run after success. The conditional write refuses a foreign request id, and a second run is a no-op. Test: Task 2 `TestPurge_TombstoneIsIdempotentAndScoped`.
4. **A purged user's e-mail registers again** (D10). The e-mail marker is gone, `GetByEmail` no longer finds the tombstone, and a fresh registration succeeds. Test: Task 2 `TestPurge_FreesEmailForReRegistration`.
5. **A participant-configured environment** (`ERASURE_SERVICES` set). The account must **not** purge before acks exist (Phase 3), so the request waits in `purging`. Test: Task 3 `TestPurging_AwaitsParticipants`.

---

### Task 1: `Sealer.MAC` — keyed hash without a new secret

**Files:** Modify `api/internal/crypto/secret.go`; Test `api/internal/crypto/secret_test.go` (create if missing, `package crypto`).

**Interfaces:** Produces `func (s *Sealer) MAC(label, value string) string` (64 hex chars).

- [ ] **Step 1: Failing test** — append to (or create) `api/internal/crypto/secret_test.go`:

```go
package crypto

import "testing"

func TestSealerMAC(t *testing.T) {
	a := &Sealer{key: []byte("0123456789abcdef0123456789abcdef")}
	b := &Sealer{key: []byte("fedcba9876543210fedcba9876543210")}
	m := a.MAC("cpf-hmac", "12345678909")
	if len(m) != 64 || m != a.MAC("cpf-hmac", "12345678909") {
		t.Fatalf("MAC must be deterministic hex-sha256, got %q", m)
	}
	if m == a.MAC("other-label", "12345678909") {
		t.Fatal("labels must separate keys")
	}
	if m == b.MAC("cpf-hmac", "12345678909") {
		t.Fatal("different sealing keys must give different MACs")
	}
}
```

- [ ] **Step 2: Run, verify it fails** — `cd api && go test ./internal/crypto/ -run TestSealerMAC -count=1` → build FAIL `a.MAC undefined`.

- [ ] **Step 3: Implement** — add to `api/internal/crypto/secret.go` (imports `crypto/hmac`, `crypto/sha256`; `encoding/hex` is already imported):

```go
// MAC returns hex(HMAC-SHA256(k_label, value)), where k_label is derived from
// the sealing key and label. Purpose-specific keyed hashes (e.g. the CPF kept
// on a deleted account's tombstone) therefore need no extra secret, and a
// leak of one label's hashes reveals nothing about another's.
func (s *Sealer) MAC(label, value string) string {
	k := hmac.New(sha256.New, s.key)
	k.Write([]byte(label))
	m := hmac.New(sha256.New, k.Sum(nil))
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}
```

- [ ] **Step 4: Pass** — `go test ./internal/crypto/ -count=1` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/crypto && git commit -m "feat(api): Sealer.MAC for purpose-scoped keyed hashes"`

---

### Task 2: `user.Service.Purge` — tombstone, KYC retention item, marker release

**Files:**
- Modify `api/internal/domain/user/model.go`, `api/internal/domain/user/repository.go`, `api/internal/domain/user/service.go`
- Modify `api/internal/domain/kyc/model.go` (`BuildCPFPK` delegates to `user.CPFMarkerPK`)
- Modify test doubles: `api/internal/domain/user/service_test.go` (`mockRepo`, `errRepo`), `api/internal/handler/testhelpers_test.go` (`memUserRepo`), `api/internal/middleware/support_test.go` (`supportUserRepo`)
- Test `api/internal/domain/user/service_test.go`, `api/internal/handler/auth_test.go`

**Interfaces:**
- Produces:
  - `user.DeletionStatePurged = "purged"`
  - `User.PurgedAt`, `User.CPFHMAC`
  - `type Tombstone struct`, `type KYCRetention struct`, `type KYCRecord struct`
  - `func KYCRetentionPK(userID string) string`, `func EmailMarkerPK(email string) string`, `func CPFMarkerPK(cpf string) string`
  - `Repository.Purge(ctx, tomb *Tombstone, kyc *KYCRetention, markerPKs []string) error`
  - `var ErrPurgeConflict`
  - `func (s *Service) Purge(ctx context.Context, userID, requestID string, cpfMAC func(string) string, at time.Time) error`

- [ ] **Step 1: Teach the doubles `Purge`.**

In `api/internal/domain/user/service_test.go` add:

```go
func (m *mockRepo) Purge(_ context.Context, tomb *user.Tombstone, kyc *user.KYCRetention, markers []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := strings.TrimPrefix(tomb.PK, "USER_")
	cur, ok := m.byID[id]
	if !ok || cur.DeletionRequestID != tomb.DeletionRequestID {
		return user.ErrPurgeConflict
	}
	delete(m.byEmail, cur.Email)
	m.byID[id] = &user.User{PK: tomb.PK, DeletionState: tomb.DeletionState, DeletionRequestID: tomb.DeletionRequestID,
		PurgedAt: tomb.PurgedAt, CPFHMAC: tomb.CPFHMAC, TOSVersion: tomb.TOSVersion, PrivacyVersion: tomb.PrivacyVersion}
	if kyc != nil {
		m.kycRet = append(m.kycRet, kyc)
	}
	m.markersDeleted = append(m.markersDeleted, markers...)
	return nil
}

func (e *errRepo) Purge(_ context.Context, _ *user.Tombstone, _ *user.KYCRetention, _ []string) error {
	return errors.New("db error")
}
```

Add fields to `mockRepo`: `kycRet []*user.KYCRetention` and `markersDeleted []string`, and `"strings"` to the imports if missing.

In `api/internal/handler/testhelpers_test.go`:

```go
func (m *memUserRepo) Purge(_ context.Context, tomb *userDomain.Tombstone, _ *userDomain.KYCRetention, _ []string) error {
	id := strings.TrimPrefix(tomb.PK, "USER_")
	cur, ok := m.byID[id]
	if !ok || cur.DeletionRequestID != tomb.DeletionRequestID {
		return userDomain.ErrPurgeConflict
	}
	delete(m.byEmail, cur.Email)
	m.byID[id] = &userDomain.User{PK: tomb.PK, DeletionState: tomb.DeletionState, DeletionRequestID: tomb.DeletionRequestID, PurgedAt: tomb.PurgedAt}
	return nil
}
```

(Check the field names `byID`/`byEmail` of `memUserRepo` and `mockRepo` in the files; they are the maps used by their `GetByID`/`GetByEmail`.)

In `api/internal/middleware/support_test.go`, after the `ClearDeletionIfRequest` stub:

```go
func (supportUserRepo) Purge(context.Context, *user.Tombstone, *user.KYCRetention, []string) error {
	return nil
}
```

- [ ] **Step 2: Failing tests** — append to `api/internal/domain/user/service_test.go`:

```go
func purgedFixture(t *testing.T) (*user.Service, *mockRepo, *user.User) {
	t.Helper()
	ctx := context.Background()
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, err := svc.Register(ctx, "gone@example.com", "Sup3rSecret!", "Ana", "Silva")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	repo.byID[u.ID()].CPF = "12345678909"
	repo.byID[u.ID()].KYCLevel = "basic"
	repo.byID[u.ID()].LegalName = "Ana Silva"
	if err := svc.MarkPendingDeletion(ctx, u.ID(), "req-1"); err != nil {
		t.Fatalf("MarkPendingDeletion: %v", err)
	}
	return svc, repo, u
}

func fakeMAC(cpf string) string { return "mac(" + cpf + ")" }

func TestPurge_TombstoneIsIdempotentAndScoped(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc, repo, u := purgedFixture(t)

	if err := svc.Purge(ctx, u.ID(), "req-OTHER", fakeMAC, at); err == nil {
		t.Fatal("a purge for another request id must be refused")
	}
	if err := svc.Purge(ctx, u.ID(), "req-1", fakeMAC, at); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	got := repo.byID[u.ID()]
	if got.DeletionState != user.DeletionStatePurged || got.Email != "" || got.CPF != "" || got.CPFHMAC != "mac(12345678909)" {
		t.Fatalf("tombstone %+v", got)
	}
	if len(repo.kycRet) != 1 || repo.kycRet[0].Record.CPF != "12345678909" || repo.kycRet[0].RetainUntil != "2031-10-14T12:00:00Z" {
		t.Fatalf("KYC retention item %+v", repo.kycRet)
	}
	if len(repo.markersDeleted) != 2 {
		t.Fatalf("e-mail and CPF markers must be released, got %v", repo.markersDeleted)
	}
	if err := svc.Purge(ctx, u.ID(), "req-1", fakeMAC, at); err != nil {
		t.Fatalf("second Purge must be a no-op: %v", err)
	}
	if len(repo.kycRet) != 1 {
		t.Fatal("a re-run must not write a second retention item")
	}
}

func TestPurge_FreesEmailForReRegistration(t *testing.T) {
	ctx := context.Background()
	svc, _, u := purgedFixture(t)
	if err := svc.Purge(ctx, u.ID(), "req-1", fakeMAC, time.Now()); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := svc.GetByEmail(ctx, "gone@example.com"); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("GetByEmail after purge: %v, want ErrNotFound", err)
	}
	if _, err := svc.Register(ctx, "gone@example.com", "N3wSecret!!", "Ana", "Nova"); err != nil {
		t.Fatalf("re-registering the e-mail of a purged account: %v", err)
	}
}
```

(Add `"time"` to the test imports if missing.)

- [ ] **Step 3: Run, verify they fail** — `go test ./internal/domain/user/ -run Purge -count=1` → build FAIL (`svc.Purge undefined`, `user.Tombstone undefined`).

- [ ] **Step 4: Implement.**

`api/internal/domain/user/model.go` — add to `User` after `DeletionRequestID`:

```go
	PurgedAt string `dynamodbav:"purged_at,omitempty"` // RFC3339, set on the tombstone
	CPFHMAC  string `dynamodbav:"cpf_hmac,omitempty"`  // keyed hash kept for fraud prevention (D10)
```

Next to `DeletionStatePending`:

```go
// DeletionStatePurged marks a tombstone: the account was erased.
const DeletionStatePurged = "purged"
```

At the end of the file:

```go
// EmailMarkerPK is the uniqueness lock written at registration.
func EmailMarkerPK(email string) string {
	return "EMAIL#" + crypto.HashToken(strings.ToLower(email))
}

// CPFMarkerPK is the CPF uniqueness lock written by KYC Basic.
func CPFMarkerPK(cpf string) string { return "CPF_" + cpf }

func KYCRetentionPK(userID string) string { return "KYCRET#" + userID }

// Tombstone replaces a purged user's item. It deliberately has no email and no
// kyc_level attribute, so the item leaves email-index and kyc-level-index.
type Tombstone struct {
	PK                string `dynamodbav:"pk"`
	DeletionState     string `dynamodbav:"deletion_state"`
	DeletionRequestID string `dynamodbav:"deletion_request_id"`
	PurgedAt          string `dynamodbav:"purged_at"`
	CPFHMAC           string `dynamodbav:"cpf_hmac,omitempty"`
	TOSVersion        string `dynamodbav:"tos_version,omitempty"`
	TOSAcceptedAt     string `dynamodbav:"tos_accepted_at,omitempty"`
	PrivacyVersion    string `dynamodbav:"privacy_version,omitempty"`
	PrivacyAcceptedAt string `dynamodbav:"privacy_accepted_at,omitempty"`
	CreatedAt         string `dynamodbav:"created_at"`
	UpdatedAt         string `dynamodbav:"updated_at"`
}

// KYCRetention keeps the KYC record of a deleted account for PLD (D6), in a
// map attribute so none of its fields is a GSI key. Not readable by any app
// endpoint.
type KYCRetention struct {
	PK          string    `dynamodbav:"pk"`
	RequestID   string    `dynamodbav:"deletion_request_id"`
	RetainUntil string    `dynamodbav:"retain_until"`
	ExpiresAt   int64     `dynamodbav:"expires_at"`
	Record      KYCRecord `dynamodbav:"kyc_record"`
}

type KYCRecord struct {
	CPF                string        `dynamodbav:"cpf,omitempty"`
	LegalName          string        `dynamodbav:"legal_name,omitempty"`
	BirthDate          string        `dynamodbav:"birth_date,omitempty"`
	Address            Address       `dynamodbav:"address,omitempty"`
	PhoneNumber        string        `dynamodbav:"phone_number,omitempty"`
	PhoneVerifiedAt    string        `dynamodbav:"phone_verified_at,omitempty"`
	KYCLevel           string        `dynamodbav:"kyc_level,omitempty"`
	KYCStatus          string        `dynamodbav:"kyc_status,omitempty"`
	KYCBasicVerifiedAt string        `dynamodbav:"kyc_basic_verified_at,omitempty"`
	KYCVerifiedAt      string        `dynamodbav:"kyc_verified_at,omitempty"`
	KYCReviewedAt      string        `dynamodbav:"kyc_reviewed_at,omitempty"`
	KYCReviewedBy      string        `dynamodbav:"kyc_reviewed_by,omitempty"`
	KYCReviewDecision  string        `dynamodbav:"kyc_review_decision,omitempty"`
	KYCRiskScore       int           `dynamodbav:"kyc_risk_score,omitempty"`
	KYCRiskSignals     []string      `dynamodbav:"kyc_risk_signals,omitempty"`
	KYCDocuments       []KYCDocument `dynamodbav:"kyc_documents,omitempty"` // S3 keys; moved to retention/ in Phase 2b
}
```

(add imports `strings` and `gopkg.aoctech.app/account/api/internal/crypto` to `model.go`; `crypto` does not import `user`, so there is no cycle).

`api/internal/domain/user/repository.go`:
- Replace the inline `markerPK := "EMAIL#" + crypto.HashToken(u.Email)` in `Create` with `markerPK := EmailMarkerPK(u.Email)`.
- Add to the `Repository` interface:

```go
	// Purge replaces the user item with tomb (only while it still belongs to
	// tomb.DeletionRequestID), writes the KYC retention item and deletes the
	// uniqueness markers, in one transaction.
	Purge(ctx context.Context, tomb *Tombstone, kyc *KYCRetention, markerPKs []string) error
```

and the implementation:

```go
var ErrPurgeConflict = errors.New("user: purge refused, the deletion block belongs to another request")

func (r *dynamoRepository) Purge(ctx context.Context, tomb *Tombstone, kyc *KYCRetention, markerPKs []string) error {
	tombItem, err := attributevalue.MarshalMap(tomb)
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{{Put: &types.Put{
		TableName:                 aws.String(r.tableName),
		Item:                      tombItem,
		ConditionExpression:       aws.String("deletion_request_id = :rid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":rid": &types.AttributeValueMemberS{Value: tomb.DeletionRequestID}},
	}}}
	if kyc != nil {
		kycItem, err := attributevalue.MarshalMap(kyc)
		if err != nil {
			return err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(r.tableName), Item: kycItem}})
	}
	for _, pk := range markerPKs {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{
			TableName: aws.String(r.tableName),
			Key:       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}},
		}})
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if dynamo.IsConditionFailed(err) {
		return ErrPurgeConflict
	}
	return err
}
```

(imports: `github.com/aws/aws-sdk-go-v2/aws`, `.../feature/dynamodb/attributevalue`, `gopkg.aoctech.app/api-commons/dynamo`. Add any that are missing.)

`api/internal/domain/user/service.go`:

```go
// Purge erases the account for deletion request requestID: the user item
// becomes a tombstone, the KYC record (if any) moves to a retention item kept
// 5 years (D6), and the e-mail and CPF locks are released so the e-mail can
// register again (D10). Idempotent: a tombstone is left alone.
func (s *Service) Purge(ctx context.Context, userID, requestID string, cpfMAC func(string) string, at time.Time) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.DeletionState == DeletionStatePurged {
		return nil
	}
	if u.DeletionRequestID != requestID {
		return ErrPurgeConflict
	}
	purgedAt := at.UTC().Format(time.RFC3339)
	tomb := &Tombstone{
		PK: u.PK, DeletionState: DeletionStatePurged, DeletionRequestID: requestID, PurgedAt: purgedAt,
		TOSVersion: u.TOSVersion, TOSAcceptedAt: u.TOSAcceptedAt,
		PrivacyVersion: u.PrivacyVersion, PrivacyAcceptedAt: u.PrivacyAcceptedAt,
		CreatedAt: u.CreatedAt, UpdatedAt: purgedAt,
	}
	var markers []string
	if u.Email != "" {
		markers = append(markers, EmailMarkerPK(u.Email))
	}
	var kyc *KYCRetention
	if u.CPF != "" {
		tomb.CPFHMAC = cpfMAC(u.CPF)
		markers = append(markers, CPFMarkerPK(u.CPF))
	}
	if u.CPF != "" || u.KYCLevel != "" {
		until := at.UTC().AddDate(5, 0, 0)
		kyc = &KYCRetention{
			PK: KYCRetentionPK(userID), RequestID: requestID,
			RetainUntil: until.Format(time.RFC3339), ExpiresAt: until.Unix(),
			Record: KYCRecord{
				CPF: u.CPF, LegalName: u.LegalName, BirthDate: u.BirthDate, Address: u.Address,
				PhoneNumber: u.PhoneNumber, PhoneVerifiedAt: u.PhoneVerifiedAt,
				KYCLevel: u.KYCLevel, KYCStatus: u.KYCStatus, KYCBasicVerifiedAt: u.KYCBasicVerifiedAt,
				KYCVerifiedAt: u.KYCVerifiedAt, KYCReviewedAt: u.KYCReviewedAt, KYCReviewedBy: u.KYCReviewedBy,
				KYCReviewDecision: u.KYCReviewDecision, KYCRiskScore: u.KYCRiskScore,
				KYCRiskSignals: u.KYCRiskSignals, KYCDocuments: u.KYCDocuments,
			},
		}
	}
	return s.repo.Purge(ctx, tomb, kyc, markers)
}
```

`api/internal/domain/kyc/model.go`: change `BuildCPFPK` to `return user.CPFMarkerPK(cpf)` (the kyc package already imports `user`).

- [ ] **Step 5: Pass** — `go vet ./... && go test ./internal/domain/user/ ./internal/domain/kyc/ ./internal/handler/ ./internal/middleware/ -count=1` → all `ok`.
- [ ] **Step 6: Commit** — `git add internal/domain/user internal/domain/kyc internal/handler/testhelpers_test.go internal/middleware/support_test.go && git commit -m "feat(api): purge a user into a tombstone with a KYC retention item"`

---

### Task 3: Deletion service — blockers, `blocked`, frozen organizations, purge orchestration

**Files:** Modify `api/internal/domain/deletion/model.go`, `api/internal/domain/deletion/service.go`, `api/internal/domain/deletion/model_test.go`; Test `api/internal/domain/deletion/service_test.go`.

**Interfaces:**
- Consumes: `erasure.Blocker` (api-commons).
- Produces:
  - `StateBlocked`, `StatePurged`
  - `Request.Organizations []string`, `Request.Blockers []string`, `Request.BlockedNotified bool`, `Request.PurgedAt string`
  - `type Ownership interface { Assess(ctx context.Context, userID string) (soleOrgs []string, blockers []erasure.Blocker, err error) }`
  - `type Purger interface { Purge(ctx context.Context, r *Request) error }`
  - `type BlockedError struct { Blockers []erasure.Blocker }`
  - `func (s *Service) WithOwnership(o Ownership) *Service`, `func (s *Service) WithPurger(p Purger, awaitParticipants bool) *Service`
  - `Mailer.SendDeletionBlockedEmail(ctx, to, firstName, requestID, cancelToken string, blockers []string) error`

- [ ] **Step 1: Failing tests.**

In `model_test.go`, extend `TestStateOpen`: add `StateBlocked` to the open list and `StatePurged` to the released list.

In `service_test.go`, make these changes:

1. `fakeMailer` gets a `blocked int` field and:

```go
func (f *fakeMailer) SendDeletionBlockedEmail(_ context.Context, _, _, _, token string, _ []string) error {
	f.blocked++
	f.cancelToken = token
	return nil
}
```

2. Add fakes:

```go
type fakeOwnership struct {
	sole     []string
	blockers []erasure.Blocker
}

func (f *fakeOwnership) Assess(context.Context, string) ([]string, []erasure.Blocker, error) {
	return f.sole, f.blockers, nil
}

type fakePurger struct {
	calls int
	err   error
}

func (f *fakePurger) Purge(context.Context, *Request) error {
	f.calls++
	return f.err
}

func (f *fixture) withPurge(t *testing.T, own *fakeOwnership, p *fakePurger, await bool) {
	t.Helper()
	f.svc = f.svc.WithOwnership(own).WithPurger(p, await)
}
```

3. Add tests (import `gopkg.aoctech.app/api-commons/erasure`):

```go
func TestRequest_RefusedWithBlockers(t *testing.T) {
	f := newFixture(t)
	f.withPurge(t, &fakeOwnership{blockers: []erasure.Blocker{{Code: "account.oauth_client_owned"}}}, &fakePurger{}, false)
	_, err := f.svc.Request(ctx, "u1")
	var blocked *BlockedError
	if !errors.As(err, &blocked) || len(blocked.Blockers) != 1 {
		t.Fatalf("err = %v, want BlockedError with 1 blocker", err)
	}
	if _, err := f.svc.Status(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a refused request must not be stored")
	}
}

func TestGraceEnd_BlockedNotifiesAndCanCancel(t *testing.T) {
	f := newFixture(t)
	own := &fakeOwnership{}
	f.withPurge(t, own, &fakePurger{}, false)
	r := f.confirm(t)
	own.blockers = []erasure.Blocker{{Code: "account.organization_shared_owner"}}
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	s := f.stored(t, r.ID)
	if s.State != StateBlocked || !s.BlockedNotified || s.DueState != "" || len(s.Blockers) != 1 || f.mail.blocked != 1 {
		t.Fatalf("blocked request %+v, blocked mails %d", s, f.mail.blocked)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); err != nil {
		t.Fatalf("cancel from blocked: %v", err)
	}
	if f.locker.unlocks != 1 {
		t.Fatalf("unlocks = %d", f.locker.unlocks)
	}
}

func TestGraceEnd_FreezesSoleOrganizationsAndPurges(t *testing.T) {
	f := newFixture(t)
	p := &fakePurger{}
	f.withPurge(t, &fakeOwnership{sole: []string{"org-1"}}, p, false)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx) // pending -> locked -> purging -> purged in one tick
	s := f.stored(t, r.ID)
	if s.State != StatePurged || s.PurgedAt == "" || len(s.Organizations) != 1 || s.Organizations[0] != "org-1" || p.calls != 1 {
		t.Fatalf("purged request %+v, purge calls %d", s, p.calls)
	}
	if _, err := f.svc.Status(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a purged request must release the open marker")
	}
}

func TestPurging_AwaitsParticipants(t *testing.T) {
	f := newFixture(t)
	p := &fakePurger{}
	f.withPurge(t, &fakeOwnership{}, p, true)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePurging || s.DueState != "" || p.calls != 0 {
		t.Fatalf("with participants the account purge waits for acks: %+v calls=%d", s, p.calls)
	}
}

func TestPurging_FailureRetried(t *testing.T) {
	f := newFixture(t)
	p := &fakePurger{err: errors.New("throttled")}
	f.withPurge(t, &fakeOwnership{}, p, false)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePurging || s.DueState != string(StatePurging) {
		t.Fatalf("a failed purge must stay due: %+v", s)
	}
	p.err = nil
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StatePurged || p.calls != 2 {
		t.Fatalf("purge must be retried: calls=%d", p.calls)
	}
}
```

- [ ] **Step 2: Run, verify they fail** — `cd api && go test ./internal/domain/deletion/ -count=1` → build FAIL (`StateBlocked`, `WithOwnership`, `BlockedError` undefined).

- [ ] **Step 3: Implement.**

`model.go` — states and `Open`:

```go
	StateBlocked              State = "blocked" // a local blocker appeared at the end of grace
	StatePurged               State = "purged"
```

```go
// Open reports whether the state holds the user's single open-request marker.
func (s State) Open() bool { return s != StateCancelled && s != StateExpired && s != StatePurged }
```

Fields on `Request`, after `Reminded`:

```go
	BlockedNotified bool     `dynamodbav:"blocked_notified"`
	Organizations   []string `dynamodbav:"organizations,omitempty"` // frozen at LOCKED: single-member orgs erased with the user (D4)
	Blockers        []string `dynamodbav:"blockers,omitempty"`      // blocker codes found at the end of grace
	PurgedAt        string   `dynamodbav:"purged_at,omitempty"`
```

`service.go`:

1. New ports, error, options (after `Mailer`). Add `SendDeletionBlockedEmail(ctx context.Context, to, firstName, requestID, cancelToken string, blockers []string) error` to `Mailer`. Then:

```go
// Ownership finds what stops a user's deletion and which organizations go
// with them (they own it and are its only member).
type Ownership interface {
	Assess(ctx context.Context, userID string) (soleOrgs []string, blockers []erasure.Blocker, err error)
}

// Purger erases ctech-account's own data for a request. Idempotent and
// resumable: every step tolerates already-removed data.
type Purger interface {
	Purge(ctx context.Context, r *Request) error
}

// BlockedError lists what the user must resolve before deletion can start.
type BlockedError struct{ Blockers []erasure.Blocker }

func (e *BlockedError) Error() string { return fmt.Sprintf("deletion: %d blocker(s)", len(e.Blockers)) }

type noOwnership struct{}

func (noOwnership) Assess(context.Context, string) ([]string, []erasure.Blocker, error) {
	return nil, nil, nil
}

// WithOwnership enables blocker checks and organization freezing.
func (s *Service) WithOwnership(o Ownership) *Service { s.ownership = o; return s }

// WithPurger enables the account purge after the erase is published. With
// awaitParticipants the request waits in purging for the participants' acks
// (Phase 3) instead of purging right away.
func (s *Service) WithPurger(p Purger, awaitParticipants bool) *Service {
	s.purger, s.awaitParticipants = p, awaitParticipants
	return s
}
```

Add fields to `Service`: `ownership Ownership`, `purger Purger`, `awaitParticipants bool`. In `NewService` set `ownership: noOwnership{}`. Import `gopkg.aoctech.app/api-commons/erasure`.

2. `Request` — right after the `users.GetByID` error check:

```go
	if _, blockers, err := s.ownership.Assess(ctx, userID); err != nil {
		return nil, err
	} else if len(blockers) > 0 {
		return nil, &BlockedError{Blockers: blockers}
	}
```

3. Generalize `sendCancelLink` to three kinds. Replace its signature and body with:

```go
type linkKind int

const (
	linkScheduled linkKind = iota
	linkReminder
	linkBlocked
)

// sendCancelLink e-mails a fresh cancel link (scheduled notice, reminder, or
// blocked notice) and records it. Under ruling R1 the e-mail is the only way
// to cancel, so the token hash is stored before sending and the "sent" flag
// only after: a failed send stays due and is retried, at worst as a duplicate.
func (s *Service) sendCancelLink(ctx context.Context, r *Request, kind linkKind) error {
	u, err := s.users.GetByID(ctx, r.UserID)
	if err != nil {
		return err
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return err
	}
	from := r.State
	r.CancelTokenHashes = append(r.CancelTokenHashes, hash)
	r.UpdatedAt = ts(s.now())
	if err := s.repo.Save(ctx, r, from); err != nil {
		return err
	}
	switch kind {
	case linkScheduled:
		err = s.mail.SendDeletionScheduledEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil))
	case linkReminder:
		err = s.mail.SendDeletionReminderEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil))
	case linkBlocked:
		err = s.mail.SendDeletionBlockedEmail(ctx, u.Email, u.FirstName, r.ID, raw, r.Blockers)
	}
	if err != nil {
		return err
	}
	switch kind {
	case linkScheduled:
		r.ScheduledSent = true
	case linkReminder:
		r.Reminded = true
	case linkBlocked:
		r.BlockedNotified = true
	}
	r.UpdatedAt = ts(s.now())
	s.scheduleNext(r)
	return s.repo.Save(ctx, r, from)
}

// scheduleNext sets (or clears) the due marker for a pending or blocked request.
func (s *Service) scheduleNext(r *Request) {
	if r.State == StateBlocked {
		if r.BlockedNotified {
			r.clearDue() // nothing to do until the user cancels
		} else {
			r.setDue(s.now())
		}
		return
	}
	r.setDue(s.nextDue(r))
}
```

Update the callers: `Confirm` uses `s.sendCancelLink(ctx, r, linkScheduled)`. In `step`, `sendCancelLink(ctx, r, false)` → `linkScheduled` and `(ctx, r, true)` → `linkReminder`. In `applyLock`, replace `r.setDue(s.nextDue(r))` with `s.scheduleNext(r)`.

4. `Cancel` — allow `blocked` and save from the state read:

```go
	if r.State != StatePending && r.State != StateBlocked {
		return nil, ErrNotCancellable
	}
	from := r.State
	now := s.now()
	r.State, r.CancelledAt, r.UpdatedAt = StateCancelled, ts(now), ts(now)
	r.setDue(now) // unlock not applied yet
	if err := s.repo.Save(ctx, r, from); err != nil {
```

(the rest of `Cancel` unchanged).

5. `ProcessDue` state list:

```go
	for _, st := range []State{StateAwaitingConfirmation, StatePending, StateBlocked, StateCancelled, StateLocked, StatePurging} {
```

6. `step` — replace the grace-end case and the `StateLocked` case, and add `StateBlocked` / `StatePurging`:

```go
		case !now.Before(parseTime(r.GraceUntil)):
			sole, blockers, err := s.ownership.Assess(ctx, r.UserID)
			if err != nil {
				return err
			}
			if len(blockers) > 0 {
				r.State, r.UpdatedAt = StateBlocked, ts(now)
				r.Blockers = make([]string, 0, len(blockers))
				for _, b := range blockers {
					r.Blockers = append(r.Blockers, b.Code)
				}
				r.setDue(now) // the blocked notice is owed
				return s.repo.Save(ctx, r, StatePending)
			}
			r.State, r.LockedAt, r.UpdatedAt, r.Organizations = StateLocked, ts(now), ts(now), sole
			r.setDue(now)
			return s.repo.Save(ctx, r, StatePending)
		}
	case StateBlocked:
		return s.sendCancelLink(ctx, r, linkBlocked)
	case StateLocked:
		// Re-assert the lock: a cancel may have raced the end of grace.
		if err := s.locker.Lock(ctx, r); err != nil {
			return err
		}
		if err := s.locker.Erase(ctx, r); err != nil {
			return err
		}
		r.State, r.UpdatedAt = StatePurging, ts(now)
		if s.purger != nil && !s.awaitParticipants {
			r.setDue(now)
		} else {
			r.clearDue() // no purger yet, or waiting for participant acks (Phase 3)
		}
		return s.repo.Save(ctx, r, StateLocked)
	case StatePurging:
		if err := s.purger.Purge(ctx, r); err != nil {
			return err
		}
		r.State, r.PurgedAt, r.UpdatedAt = StatePurged, ts(now), ts(now)
		r.clearDue()
		return s.repo.Save(ctx, r, StatePurging)
	}
```

- [ ] **Step 4: Pass** — `go vet ./internal/domain/deletion/ && go test ./internal/domain/deletion/ -race -count=1` → `ok` (all Phase 1 tests too).
- [ ] **Step 5: Commit** — `git add internal/domain/deletion && git commit -m "feat(api): deletion blockers, blocked state and purge orchestration"`

(`go build ./...` fails at this point because `email.Client` lacks `SendDeletionBlockedEmail`. Task 5 adds it, so run only the package tests here. If the executor prefers a green build per commit, fold Task 5 Step 3, the e-mail method, into this task.)

---

### Task 4: `AccountOwnership` and `AccountPurger`; erase publishes the frozen organizations

**Files:** Create `api/internal/domain/deletion/ownership.go`, `api/internal/domain/deletion/purger.go`, `api/internal/domain/deletion/purger_test.go`; Modify `api/internal/domain/deletion/locker.go`, `api/internal/domain/deletion/locker_test.go`.

**Interfaces:**
- Consumes:
  - `organization.Service`: `ListForUser(ctx, userID) ([]*organization.Membership, error)`, `ListMembers(ctx, orgID, actorUserID) ([]*organization.Membership, error)`, `Remove(ctx, orgID, actorUserID, targetUserID) error`
  - `client.Repository.ListByOwner(ctx, ownerUserID) ([]*client.OAuthClient, error)`
  - `company.Repository`: `ListForUser(ctx, userID) ([]*company.Actor, error)`, `RemoveActor(ctx, orgID, companyID, userID) error`
  - `consent.Service`: `List(ctx, userID) ([]*consent.Grant, error)`, `Revoke(ctx, userID, clientID) error`
  - `totp.Service.Remove(ctx, userID) error`
  - `passkey.Service`: `List(ctx, userID) ([]*passkey.Credential, error)`, `Delete(ctx, userID, credentialSK) error`
  - Phase 1: `SessionRevoker`, `APIKeys`
- Produces:
  - `BlockerSharedOrganization = "account.organization_shared_owner"`, `BlockerOwnedClient = "account.oauth_client_owned"`
  - `func NewAccountOwnership(orgs OrgMemberships, clients OwnedClients) *AccountOwnership`
  - `type UserPurgeFunc func(ctx context.Context, userID, requestID string, at time.Time) error`
  - `func NewAccountPurger(orgs PurgeMemberships, companies CompanyEdges, consents Consents, sessions SessionRevoker, keys APIKeys, totp TOTPRemover, passkeys Passkeys, purgeUser UserPurgeFunc) *AccountPurger`

- [ ] **Step 1: Failing tests** — `api/internal/domain/deletion/purger_test.go`:

```go
package deletion

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

type fakeOrgs struct {
	memberships map[string][]*organization.Membership // by user
	members     map[string][]*organization.Membership // by org
	removed     []string
	log         *[]string
}

func (f *fakeOrgs) ListForUser(_ context.Context, userID string) ([]*organization.Membership, error) {
	return f.memberships[userID], nil
}
func (f *fakeOrgs) ListMembers(_ context.Context, orgID, _ string) ([]*organization.Membership, error) {
	return f.members[orgID], nil
}
func (f *fakeOrgs) Remove(_ context.Context, orgID, _, _ string) error {
	f.removed = append(f.removed, orgID)
	*f.log = append(*f.log, "membership")
	return nil
}

type fakeClients struct{ owned []*client.OAuthClient }

func (f fakeClients) ListByOwner(context.Context, string) ([]*client.OAuthClient, error) { return f.owned, nil }

func m(org, user, role string) *organization.Membership {
	return &organization.Membership{OrganizationID: org, UserID: user, Role: role}
}

func TestAccountOwnership_Assess(t *testing.T) {
	orgs := &fakeOrgs{
		memberships: map[string][]*organization.Membership{"u1": {
			m("solo", "u1", organization.RoleOwner), m("team", "u1", organization.RoleOwner), m("other", "u1", organization.RoleMember),
		}},
		members: map[string][]*organization.Membership{
			"solo": {m("solo", "u1", organization.RoleOwner)},
			"team": {m("team", "u1", organization.RoleOwner), m("team", "u2", organization.RoleMember)},
		},
		log: &[]string{},
	}
	clients := fakeClients{owned: []*client.OAuthClient{
		{PK: client.BuildPK("dev-app")},
		{PK: client.BuildPK("platform"), FirstParty: true},
	}}
	sole, blockers, err := NewAccountOwnership(orgs, clients).Assess(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(sole) != 1 || sole[0] != "solo" {
		t.Fatalf("sole = %v, want [solo]", sole)
	}
	if len(blockers) != 2 || blockers[0].Code != BlockerSharedOrganization || blockers[0].Detail["organization_id"] != "team" ||
		blockers[1].Code != BlockerOwnedClient || blockers[1].Detail["client_id"] != "dev-app" {
		t.Fatalf("blockers = %+v", blockers)
	}
}

type recCompanies struct{ log *[]string }

func (r recCompanies) ListForUser(context.Context, string) ([]*company.Actor, error) {
	return []*company.Actor{{OrganizationID: "other", CompanyID: "c1", UserID: "u1"}}, nil
}
func (r recCompanies) RemoveActor(context.Context, string, string, string) error {
	*r.log = append(*r.log, "company")
	return nil
}

type recConsents struct{ log *[]string }

func (r recConsents) List(context.Context, string) ([]*consent.Grant, error) {
	return []*consent.Grant{{PK: consent.BuildPK("u1"), SK: consent.BuildSK("dfe")}}, nil
}
func (r recConsents) Revoke(context.Context, string, string) error {
	*r.log = append(*r.log, "consent")
	return nil
}

type recSessions struct{ log *[]string }

func (r recSessions) RevokeAll(context.Context, string, string) error {
	*r.log = append(*r.log, "sessions")
	return nil
}

type recTOTP struct{ log *[]string }

func (r recTOTP) Remove(context.Context, string) error { *r.log = append(*r.log, "totp"); return nil }

type recPasskeys struct{ log *[]string }

func (r recPasskeys) List(context.Context, string) ([]*passkey.Credential, error) {
	return []*passkey.Credential{{SK: "PASSKEY_ab"}}, nil
}
func (r recPasskeys) Delete(context.Context, string, string) error {
	*r.log = append(*r.log, "passkey")
	return nil
}

func TestAccountPurger_RunsEveryStepThenTombstone(t *testing.T) {
	var log []string
	orgs := &fakeOrgs{
		memberships: map[string][]*organization.Membership{"u1": {
			m("solo", "u1", organization.RoleOwner), m("other", "u1", organization.RoleMember),
		}},
		log: &log,
	}
	keys := &fakeKeys{}
	var purgedFor string
	purgeUser := func(_ context.Context, userID, requestID string, _ time.Time) error {
		log = append(log, "tombstone")
		purgedFor = userID + "/" + requestID
		return nil
	}
	p := NewAccountPurger(orgs, recCompanies{&log}, recConsents{&log}, recSessions{&log}, keys, recTOTP{&log}, recPasskeys{&log}, purgeUser)
	r := &Request{ID: "r1", UserID: "u1", Organizations: []string{"solo"}}
	for i := 0; i < 2; i++ { // idempotent: a second run must also succeed
		log = nil
		if err := p.Purge(context.Background(), r); err != nil {
			t.Fatalf("Purge run %d: %v", i+1, err)
		}
	}
	want := []string{"membership", "company", "consent", "sessions", "totp", "passkey", "tombstone"}
	if len(log) != len(want) {
		t.Fatalf("steps = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("steps = %v, want %v (tombstone must be last)", log, want)
		}
	}
	if len(orgs.removed) == 0 || orgs.removed[len(orgs.removed)-1] != "other" || purgedFor != "u1/r1" {
		t.Fatalf("owner membership must be skipped (R6); removed=%v purgedFor=%q", orgs.removed, purgedFor)
	}
}
```

Check `consent.BuildPK`/`BuildSK` and `client.BuildPK` exist with those names (`consent/model.go` has `BuildPK`, `BuildSK`; `client` builds PKs the same way). Adapt the literals to the real constructors if they differ.

Append to `locker_test.go`:

```go
func TestAccountLocker_ErasePublishesFrozenOrganizations(t *testing.T) {
	pub := &fakePub{}
	l := NewAccountLocker(&fakeBlocker{}, &fakeSessions{}, &fakeKeys{}, &fakeTokens{}, pub, []string{"dfe"})
	r := lockedRequest()
	r.Organizations = []string{"org-1"}
	if err := l.Erase(context.Background(), r); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if len(pub.msgs) != 1 || len(pub.msgs[0].Organizations) != 1 || pub.msgs[0].Organizations[0] != "org-1" {
		t.Fatalf("published %+v", pub.msgs)
	}
}
```

- [ ] **Step 2: Run, verify they fail** — `go test ./internal/domain/deletion/ -count=1` → build FAIL (`NewAccountOwnership undefined`, ...).

- [ ] **Step 3: Implement.**

`ownership.go`:

```go
package deletion

import (
	"context"

	"gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/api-commons/erasure"
)

const (
	BlockerSharedOrganization = "account.organization_shared_owner"
	BlockerOwnedClient        = "account.oauth_client_owned"
)

type OrgMemberships interface {
	ListForUser(ctx context.Context, userID string) ([]*organization.Membership, error)
	ListMembers(ctx context.Context, orgID, actorUserID string) ([]*organization.Membership, error)
}

type OwnedClients interface {
	ListByOwner(ctx context.Context, ownerUserID string) ([]*client.OAuthClient, error)
}

// AccountOwnership answers what ctech-account itself blocks: organizations the
// user owns together with other members (ownership must be transferred) and
// OAuth clients the user registered (ruling R4). Organizations the user owns
// alone are returned to be erased with them (D4).
type AccountOwnership struct {
	orgs    OrgMemberships
	clients OwnedClients
}

func NewAccountOwnership(orgs OrgMemberships, clients OwnedClients) *AccountOwnership {
	return &AccountOwnership{orgs: orgs, clients: clients}
}

func (o *AccountOwnership) Assess(ctx context.Context, userID string) ([]string, []erasure.Blocker, error) {
	memberships, err := o.orgs.ListForUser(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	var sole []string
	var blockers []erasure.Blocker
	for _, ms := range memberships {
		if ms.Role != organization.RoleOwner {
			continue
		}
		members, err := o.orgs.ListMembers(ctx, ms.OrganizationID, userID)
		if err != nil {
			return nil, nil, err
		}
		if len(members) <= 1 {
			sole = append(sole, ms.OrganizationID)
			continue
		}
		blockers = append(blockers, erasure.Blocker{Code: BlockerSharedOrganization,
			Detail: map[string]any{"organization_id": ms.OrganizationID}})
	}
	owned, err := o.clients.ListByOwner(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range owned {
		if c.FirstParty {
			continue
		}
		blockers = append(blockers, erasure.Blocker{Code: BlockerOwnedClient, Detail: map[string]any{"client_id": c.ID()}})
	}
	return sole, blockers, nil
}
```

`purger.go`:

```go
package deletion

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

type PurgeMemberships interface {
	ListForUser(ctx context.Context, userID string) ([]*organization.Membership, error)
	Remove(ctx context.Context, orgID, actorUserID, targetUserID string) error
}

type CompanyEdges interface {
	ListForUser(ctx context.Context, userID string) ([]*company.Actor, error)
	RemoveActor(ctx context.Context, orgID, companyID, userID string) error
}

type Consents interface {
	List(ctx context.Context, userID string) ([]*consent.Grant, error)
	Revoke(ctx context.Context, userID, clientID string) error
}

type TOTPRemover interface {
	Remove(ctx context.Context, userID string) error
}

type Passkeys interface {
	List(ctx context.Context, userID string) ([]*passkey.Credential, error)
	Delete(ctx context.Context, userID, credentialSK string) error
}

// UserPurgeFunc turns the user item into a tombstone (user.Service.Purge with
// the CPF keyed hash bound in).
type UserPurgeFunc func(ctx context.Context, userID, requestID string, at time.Time) error

// AccountPurger erases ctech-account's own data for a request, per data
// inventory §3. Each step lists what is left and removes it, so a re-run after
// a partial failure finishes the job. The tombstone is always last: until it
// is written the user item still carries what the earlier steps need.
// Phase 2b adds: erase frozen organizations, anonymize support and audit,
// move KYC documents to retention.
type AccountPurger struct {
	orgs      PurgeMemberships
	companies CompanyEdges
	consents  Consents
	sessions  SessionRevoker
	keys      APIKeys
	totp      TOTPRemover
	passkeys  Passkeys
	purgeUser UserPurgeFunc
	now       func() time.Time
}

func NewAccountPurger(orgs PurgeMemberships, companies CompanyEdges, consents Consents, sessions SessionRevoker,
	keys APIKeys, totp TOTPRemover, passkeys Passkeys, purgeUser UserPurgeFunc) *AccountPurger {
	return &AccountPurger{orgs: orgs, companies: companies, consents: consents, sessions: sessions,
		keys: keys, totp: totp, passkeys: passkeys, purgeUser: purgeUser, now: time.Now}
}

func (p *AccountPurger) Purge(ctx context.Context, r *Request) error {
	u := r.UserID
	memberships, err := p.orgs.ListForUser(ctx, u)
	if err != nil {
		return fmt.Errorf("listing memberships: %w", err)
	}
	for _, ms := range memberships {
		if ms.Role == organization.RoleOwner {
			continue // single-member orgs are erased whole (Phase 2b, ruling R6); shared ones were blockers
		}
		if err := p.orgs.Remove(ctx, ms.OrganizationID, u, u); err != nil {
			return fmt.Errorf("leaving organization: %w", err)
		}
	}
	edges, err := p.companies.ListForUser(ctx, u)
	if err != nil {
		return fmt.Errorf("listing company edges: %w", err)
	}
	for _, e := range edges {
		if err := p.companies.RemoveActor(ctx, e.OrganizationID, e.CompanyID, u); err != nil {
			return fmt.Errorf("removing company edge: %w", err)
		}
	}
	grants, err := p.consents.List(ctx, u)
	if err != nil {
		return fmt.Errorf("listing consents: %w", err)
	}
	for _, g := range grants {
		if err := p.consents.Revoke(ctx, u, g.ClientID()); err != nil {
			return fmt.Errorf("revoking consent: %w", err)
		}
	}
	if err := p.sessions.RevokeAll(ctx, u, ""); err != nil {
		return fmt.Errorf("revoking sessions: %w", err)
	}
	keys, err := p.keys.List(ctx, u)
	if err != nil {
		return fmt.Errorf("listing api keys: %w", err)
	}
	for _, k := range keys {
		if err := p.keys.Revoke(ctx, u, k.ID()); err != nil {
			return fmt.Errorf("revoking api key: %w", err)
		}
	}
	if err := p.totp.Remove(ctx, u); err != nil {
		return fmt.Errorf("removing totp: %w", err)
	}
	creds, err := p.passkeys.List(ctx, u)
	if err != nil {
		return fmt.Errorf("listing passkeys: %w", err)
	}
	for _, c := range creds {
		if err := p.passkeys.Delete(ctx, u, c.SK); err != nil {
			return fmt.Errorf("deleting passkey: %w", err)
		}
	}
	return p.purgeUser(ctx, u, r.ID, p.now())
}
```

Check: removing a TOTP that was never set up must not error (`totp` repository `Remove` is a `DeleteItem`, which succeeds on a missing key). If it returns a not-found error, treat that error as success in `Purge` and ledger it.

`locker.go`:
- `publish` gains an `organizations []string` parameter, passed into `erasure.Message.Organizations`.
- `Lock` and `Unlock` pass `nil`.
- `Erase` passes `r.Organizations`.

- [ ] **Step 4: Pass** — `go vet ./internal/domain/deletion/ && go test ./internal/domain/deletion/ -race -count=1` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/domain/deletion && git commit -m "feat(api): account ownership blockers and own-data purger"`

---

### Task 5: Blocked e-mail, `409 deletion-blocked`, wiring

**Files:**
- Modify `api/internal/apierror/problem.go`, `api/internal/email/ses.go`, `api/internal/handler/deletion.go`, `api/internal/handler/deletion_test.go`, `api/internal/handler/testhelpers_test.go`, `api/cmd/api/main.go`

**Interfaces:**
- Produces:
  - `Problem.Blockers any` (json `blockers,omitempty`)
  - `func apierror.DeletionBlocked(blockers any, instance string) *Problem` (409, slug `deletion-blocked`)
  - `(*email.Client).SendDeletionBlockedEmail`

- [ ] **Step 1: Failing test** — append to `api/internal/handler/deletion_test.go`:

```go
func TestDeletion_RequestBlockedByOwnedOAuthClient(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "dev@example.com", "Sup3rSecret!", "Ana")
	if err := ta.clientRepo.Create(context.Background(), &oauthclientDomain.OAuthClient{
		PK: oauthclientDomain.BuildPK("dev-app"), ClientType: "public", OwnerUserID: u.ID(),
		RedirectURIs: []string{"https://dev.example/cb"}, AllowedScopes: []string{"openid"},
	}); err != nil {
		t.Fatalf("seeding client: %v", err)
	}
	resp := ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase}, ta.issueToken(t, u.ID()))
	body := bodyString(resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "deletion-blocked") || !strings.Contains(body, "account.oauth_client_owned") {
		t.Fatalf("status %d body %s, want 409 deletion-blocked with the blocker", resp.StatusCode, body)
	}
}
```

Imports: `"context"`, `oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"`.

Wire ownership into the test app in `testhelpers_test.go`:
1. Add a stub so the test app needs no organization repository:

```go
// noOrgs is the test app's organization view: nobody owns anything.
type noOrgs struct{}

func (noOrgs) ListForUser(context.Context, string) ([]*orgDomain.Membership, error)          { return nil, nil }
func (noOrgs) ListMembers(context.Context, string, string) ([]*orgDomain.Membership, error) { return nil, nil }
```

2. Replace `deletionSvc := deletion.NewService(...)` with the same call followed by `.WithOwnership(deletion.NewAccountOwnership(noOrgs{}, sharedClientRepo))`.
3. Import `orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"` if not already imported.
4. `sharedClientRepo` is the client repo variable used by the test app; check its name next to `clientRepo:` in the returned struct.

- [ ] **Step 2: Run, verify it fails** — `go test ./internal/handler/ -run RequestBlocked -count=1` → FAIL (202 or 500, not 409), or a build failure on `SendDeletionBlockedEmail`.

- [ ] **Step 3: Implement.**

`apierror/problem.go` — field on `Problem` after `RequiredScope`:

```go
	// Blockers lists what stops an account deletion (deletion-blocked).
	Blockers any `json:"blockers,omitempty"`
```

and:

```go
// DeletionBlocked answers a deletion request the user must unblock first
// (transfer an organization, delete their OAuth apps, ...).
func DeletionBlocked(blockers any, instance string) *Problem {
	p := newProblem("deletion-blocked", "Deletion Blocked", http.StatusConflict,
		"Resolve the listed items before deleting this account.", instance)
	p.Blockers = blockers
	return p
}
```

`email/ses.go` — sender and template, next to the other deletion senders and templates:

```go
func (c *Client) SendDeletionBlockedEmail(ctx context.Context, to, firstName, requestID, cancelToken string, blockers []string) error {
	return c.send(ctx, to, "A exclusão da sua conta está pendente — ctech",
		deletionBlockedEmailHTML(firstName, c.deletionLink("cancel", requestID, cancelToken), blockers))
}
```

```go
var deletionBlockerText = map[string]string{
	"account.organization_shared_owner": "Você é titular de uma organização com outros membros: transfira a titularidade.",
	"account.oauth_client_owned":        "Você tem aplicativos OAuth cadastrados: exclua-os.",
}

func deletionBlockedEmailHTML(firstName, cancelLink string, blockers []string) string {
	items := ""
	for _, b := range blockers {
		text, ok := deletionBlockerText[b]
		if !ok {
			text = html.EscapeString(b)
		}
		items += "<li>" + text + "</li>"
	}
	body := `<p>O prazo para a exclusão da sua conta CTech terminou, mas ela não pôde ser concluída:</p>
  <ul>` + items + `</ul>
  <p>Sua conta continua bloqueada. Cancele a exclusão, resolva os itens acima e faça um novo pedido.</p>
  ` + ctaButton("Cancelar exclusão", cancelLink)
	return emailLayout("Exclusão pendente", firstName, body,
		"Se precisar de ajuda, responda a este e-mail ou abra um chamado no suporte.")
}
```

`handler/deletion.go` — in `request`, map the new error before the `ErrOpenRequest` branch:

```go
	var blocked *deletion.BlockedError
	if errors.As(err, &blocked) {
		return apierror.DeletionBlocked(blocked.Blockers, c.Path()).Send(c)
	}
```

`cmd/api/main.go`:
- Extract the inline company repository: `companyRepo := companyDomain.NewRepository(db, cfg.TablePrefix)`, then `companySvc := companyDomain.NewService(companyRepo, time.Now)`.
- In the `if cfg.AccountDeletionEnabled {` block, right after `deletionSvc := deletion.NewService(...)`:

```go
		purgeUser := func(ctx context.Context, userID, requestID string, at time.Time) error {
			return userSvc.Purge(ctx, userID, requestID, func(cpf string) string { return sealer.MAC("cpf-hmac", cpf) }, at)
		}
		deletionSvc = deletionSvc.
			WithOwnership(deletion.NewAccountOwnership(orgSvc, oauthClientRepo)).
			WithPurger(deletion.NewAccountPurger(orgSvc, companyRepo, consentSvc, sessionSvc, apiKeySvc, totpSvc, passkeySvc, purgeUser),
				len(cfg.ErasureServices) > 0)
```

- [ ] **Step 4: Pass** — `go vet ./... && go build ./... && go test ./... 2>&1 | tail -30` → every package `ok`.
- [ ] **Step 5: Commit** — `git add internal/apierror internal/email internal/handler cmd/api/main.go && git commit -m "feat(api): deletion-blocked problem, blocked e-mail and purge wiring"`

---

### Task 6: Documentation

- [ ] **`api/ENDPOINTS.md`** (Account deletion section):
  - `POST /v1.0/account/deletion` can answer `409 deletion-blocked` with `blockers: [{code, detail}]`;
  - the two codes;
  - the blocked e-mail.
- [ ] **`README.md`** (Account deletion section), add:
  - step 4: at the end of grace, blockers move the request to `blocked`, the user gets a cancel link and cancels;
  - otherwise the request is `locked` with the frozen organizations, then the account purge runs and the account becomes a tombstone (`purged`). The KYC record moves to `KYCRET#{sub}`, kept 5 years.
  - Note that with `ERASURE_SERVICES` set, the purge waits for participant acks (Phase 3).
- [ ] **`PLAN.md`**: split Phase 2 into 2a (checked) and 2b (unchecked), linking this plan.
- [ ] **Specs:**
  - `2026-10-06-account-deletion-ctech-account.md` §7: say the user tombstone is a dedicated item shape without GSI keys and the KYC record goes to `KYCRET#{sub}`; add rulings R4–R6.
  - `2026-10-06-account-deletion-data-inventory.md` §3: OAuth clients row → blocker (R4); invitations sent → `invited_by` kept (R5).
- [ ] **Commit** — `git add README.md PLAN.md api/ENDPOINTS.md docs/specs && git commit -m "docs: account deletion phase 2a"`

## Cross-project impact

- **ctech-account only.** No token, JWKS or session-format change. A purged user's `sub` stops resolving to a person: `GetByID` returns the tombstone, and every token gate refuses `deletion_state != ""`.
- **Participants:** `user.erase` now carries `organizations[]` (already part of the v1 contract).
