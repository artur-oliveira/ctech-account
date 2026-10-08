# Account Deletion — Phase 2b (organizations, support, audit, KYC documents) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete ctech-account's purge per data inventory §3:
- erase the frozen single-member organizations (with their memberships, invitations, companies and company actors);
- anonymize the user's support tickets and audit rows;
- move the KYC documents into a retention prefix (D6) and erase every other object under the user's KYC prefix;
- give the users table a TTL so the `KYCRET#` item expires after 5 years.

**Architecture:** One small purge adapter per domain, kept outside the existing `Repository` interfaces, so no current test double changes:
- `organization.Eraser`, `company.Eraser`, `support.Redactor`, `audit.Anonymizer` (DynamoDB);
- `storage.S3` gains `ListKeys` and `CopyObject`.

A shared `database.DeleteAllUnder` deletes everything under a partition key.

`deletion.AccountPurger` gets these through `WithDataErasers(...)` and runs them before the tombstone:
1. organizations first;
2. support and audit next;
3. KYC documents just before the tombstone, because the tombstone drops the document keys.

**Tech Stack:** Go 1.27, aws-sdk-go-v2 (DynamoDB, S3), CDK.

**Spec:** `docs/specs/2026-10-06-account-deletion-data-inventory.md` §3, overview D4/D6, ctech-account spec §7.

**Builds on:** Phase 2a (`docs/plans/2026-10-07-account-deletion-phase2a-purge-core.md`). Execute 2a first.

## Global Constraints

- Branch `feat/account-deletion-phase2` (same as 2a). Conventional Commits, **no `Co-Authored-By` / Claude attribution**.
- Every purge step is **idempotent and resumable**: it lists what remains and removes it, so a re-run after any partial failure finishes the job.
- Redacted support message body: exactly `[mensagem removida a pedido do titular]`.
- Audit rows: remove `ip` and `user_agent` from every row of the user. Also remove `metadata`, except on rows whose `event_type` starts with `account.deletion.`, which keep their `request_id` as proof of compliance.
- KYC retention key: `retention/{user_id}/{document_id}` (`user.KYCRetentionDocKey`). The bucket's existing 5-year expiration counts from the copy, i.e. 5 years after the purge (D6).
- Noncurrent versions of deleted KYC objects expire through the bucket's existing 30-day `noncurrentVersionExpiration` (ruling R7).
- `go vet ./... && go test ./...` green after every task; `cd cdk && npx tsc --noEmit && npx jest` green after the CDK task.

## Rulings

- **R7 Deleted KYC objects are removed with a delete marker** and their old versions expire after 30 days through the existing lifecycle rule, instead of an explicit per-version delete. This avoids `s3:ListBucketVersions`/`DeleteObjectVersion` grants; 30 days is in line with the 35-day PITR window already accepted for DynamoDB. Cost if wrong: a deleted document survives as a noncurrent version for up to 30 days.
- **R8 `KYCPEND_{document_id}` rows** (unconfirmed uploads: `user_id`, `doc_type`, `content_type`) have no index by user and are left in place. Their S3 objects are erased with the prefix. Cost if wrong: an orphan row linking a `sub` to a document id survives.
- **R9 Agent messages, internal notes and NPS scores are kept.** Only the user's own message bodies, NPS comment and ticket ownership are removed. Agent text is the company's record of the interaction. Cost if wrong: an agent reply that quoted the user keeps that quote.

## Review Focus

1. **Purge crashes after the organization rows are gone but before the tombstone.** The re-run must not fail on the missing organization, and must still erase the companies. Test: Task 5 `TestAccountPurger_DataErasersRunFirstAndAreRetried`.
2. **A ticket whose messages were redacted, purge crashes, then re-runs.** The ticket must still be found until its `user_id` is removed, so messages are redacted **before** the ticket loses `user_id`. Test: Task 3 `TestRedactor_RedactsBeforeDroppingOwnership`.
3. **The KYC copy succeeded, the deletes did not, purge re-runs.** It must not copy again from a missing source or fail; it finishes the deletes. Test: Task 4 `TestMoveKYCDocuments_IsIdempotent`.
4. **The tombstone's retained record must point at the retention keys, not the deleted originals.** Test: Task 4 `TestPurge_RetainedDocumentsPointAtRetentionKeys`.
5. **Deletion audit rows keep their `request_id`** while every other row loses its metadata. Test: Task 3 `TestAnonymizer_KeepsDeletionEvidence`.

---

### Task 1: `database.DeleteAllUnder`

**Files:** Modify `api/internal/database/dynamo.go`; Test `api/internal/database/dynamo_test.go` (create if missing).

**Interfaces:** Produces `type ItemDeleter interface { Query(ctx context.Context, opts QueryOpts) (*QueryResult, error); DeleteItem(ctx context.Context, pk string, sk ...string) (bool, error) }` and `func DeleteAllUnder(ctx context.Context, b ItemDeleter, pk, skPrefix string) error` (`*database.Base` satisfies `ItemDeleter`).

- [ ] **Step 1: Failing test** — `api/internal/database/dynamo_test.go`:

```go
package database

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeTable struct{ items map[string]bool } // "pk|sk"

func (f *fakeTable) Query(_ context.Context, o QueryOpts) (*QueryResult, error) {
	res := &QueryResult{}
	for k := range f.items {
		pk, sk, _ := strings.Cut(k, "|")
		if pk == o.PK && strings.HasPrefix(sk, o.SKPrefix) && len(res.Items) < 2 { // tiny pages force the loop
			res.Items = append(res.Items, map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk},
			})
		}
	}
	return res, nil
}

func (f *fakeTable) DeleteItem(_ context.Context, pk string, sk ...string) (bool, error) {
	delete(f.items, pk+"|"+sk[0])
	return true, nil
}

func TestDeleteAllUnder(t *testing.T) {
	f := &fakeTable{items: map[string]bool{
		"ORG#1|MEMBER#a": true, "ORG#1|MEMBER#b": true, "ORG#1|MEMBER#c": true,
		"ORG#1|META": true, "ORG#2|MEMBER#z": true,
	}}
	if err := DeleteAllUnder(context.Background(), f, "ORG#1", "MEMBER#"); err != nil {
		t.Fatalf("DeleteAllUnder: %v", err)
	}
	if len(f.items) != 2 || !f.items["ORG#1|META"] || !f.items["ORG#2|MEMBER#z"] {
		t.Fatalf("left %v, want only ORG#1|META and ORG#2|MEMBER#z", f.items)
	}
}
```

- [ ] **Step 2: Fail** — `cd api && go test ./internal/database/ -run TestDeleteAllUnder -count=1` → build FAIL `undefined: DeleteAllUnder`.

- [ ] **Step 3: Implement** — append to `api/internal/database/dynamo.go`:

```go
// ItemDeleter is the slice of *Base that DeleteAllUnder needs.
type ItemDeleter interface {
	Query(ctx context.Context, opts QueryOpts) (*QueryResult, error)
	DeleteItem(ctx context.Context, pk string, sk ...string) (bool, error)
}

// DeleteAllUnder deletes every item under pk whose sort key starts with
// skPrefix ("" for all), page by page until none is left. Idempotent: running
// it again on an emptied partition is a single empty query.
func DeleteAllUnder(ctx context.Context, b ItemDeleter, pk, skPrefix string) error {
	for {
		res, err := b.Query(ctx, QueryOpts{PK: pk, SKPrefix: skPrefix, Limit: 100})
		if err != nil {
			return fmt.Errorf("listing %s: %w", pk, err)
		}
		if len(res.Items) == 0 {
			return nil
		}
		for _, item := range res.Items {
			sk, _ := item["sk"].(*types.AttributeValueMemberS)
			if sk == nil {
				continue
			}
			if _, err := b.DeleteItem(ctx, pk, sk.Value); err != nil {
				return fmt.Errorf("deleting %s/%s: %w", pk, sk.Value, err)
			}
		}
	}
}
```

(imports `fmt`, `github.com/aws/aws-sdk-go-v2/service/dynamodb/types` if missing).

- [ ] **Step 4: Pass** — `go test ./internal/database/ -count=1` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/database && git commit -m "feat(api): database.DeleteAllUnder for partition purges"`

---

### Task 2: `organization.Eraser` and `company.Eraser`

**Files:** Create `api/internal/domain/organization/eraser.go`, `api/internal/domain/company/eraser.go`.

**Interfaces:**
- Produces `func organization.NewEraser(db *dynamodb.Client, tablePrefix string) *organization.Eraser` with `EraseOrganization(ctx, orgID string) error`.
- Produces `func company.NewEraser(db *dynamodb.Client, tablePrefix string) *company.Eraser` with `EraseForOrganization(ctx, orgID string) error`.

These are thin wrappers around `database.DeleteAllUnder` (tested in Task 1) over the existing key layout. Their behaviour is exercised through the purger in Task 5.

- [ ] **Step 1: Implement** — `api/internal/domain/organization/eraser.go`:

```go
package organization

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/account/api/internal/database"
)

// Eraser removes an organization whole: every membership (the owner's
// included), every pending invitation, then the organization row. Used only
// by account deletion for organizations whose sole member is being deleted
// (D4); never reachable from an HTTP route.
type Eraser struct {
	orgs, memberships, invitations database.Base
}

func NewEraser(db *dynamodb.Client, tablePrefix string) *Eraser {
	return &Eraser{
		orgs:        database.NewBase(db, tablePrefix, orgsTable),
		memberships: database.NewBase(db, tablePrefix, membershipsTable),
		invitations: database.NewBase(db, tablePrefix, invitationsTable),
	}
}

func (e *Eraser) EraseOrganization(ctx context.Context, orgID string) error {
	if err := database.DeleteAllUnder(ctx, &e.memberships, orgPK(orgID), memberSKPrefix); err != nil {
		return err
	}
	if err := database.DeleteAllUnder(ctx, &e.invitations, orgPK(orgID), inviteSKPrefix); err != nil {
		return err
	}
	_, err := e.orgs.DeleteItem(ctx, orgPK(orgID), metaSK)
	return err
}
```

`api/internal/domain/company/eraser.go`:

```go
package company

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/account/api/internal/database"
)

// Eraser removes every company row of an organization: companies, their
// tax-id locks and actor edges all live under ORG#{id} in the companies table.
// Used only by account deletion (D4, D12).
type Eraser struct{ companies database.Base }

func NewEraser(db *dynamodb.Client, tablePrefix string) *Eraser {
	return &Eraser{companies: database.NewBase(db, tablePrefix, companiesTable)}
}

func (e *Eraser) EraseForOrganization(ctx context.Context, orgID string) error {
	return database.DeleteAllUnder(ctx, &e.companies, orgPK(orgID), "")
}
```

Check: `database.NewBase` returns a `Base` value (per `organization/repository.go:101`), and `*Base` has `Query`/`DeleteItem` (from api-commons `dynamo.Base`). If `NewBase` returns a pointer, drop the `&`.

- [ ] **Step 2: Build** — `go vet ./internal/domain/organization/ ./internal/domain/company/ && go build ./...` → clean.
- [ ] **Step 3: Commit** — `git add internal/domain/organization/eraser.go internal/domain/company/eraser.go && git commit -m "feat(api): organization and company erasers for account deletion"`

---

### Task 3: `support.Redactor` and `audit.Anonymizer`

**Files:** Create `api/internal/domain/support/redactor.go`, `api/internal/domain/support/redactor_test.go`, `api/internal/domain/audit/anonymizer.go`, `api/internal/domain/audit/anonymizer_test.go`.

**Interfaces:**
- Produces `support.RedactedBody`; `func support.NewRedactor(db *dynamodb.Client, tablePrefix string) *Redactor` with `RedactUser(ctx, userID string) error`; for tests, `func newRedactorWith(t ticketStore) *Redactor`.
- Produces `func audit.NewAnonymizer(db *dynamodb.Client, tablePrefix string) *Anonymizer` with `AnonymizeUser(ctx, userID string) error`; for tests, `func newAnonymizerWith(s eventStore) *Anonymizer`.

The adapters hold a small unexported store interface implemented by the existing `dynamoRepository` plus one new method each, so the logic is unit-tested with fakes.

- [ ] **Step 1: Failing tests.**

`api/internal/domain/support/redactor_test.go`:

```go
package support

import (
	"context"
	"testing"
)

type fakeTickets struct {
	tickets  map[string]*Ticket    // id -> ticket
	messages map[string][]*Message // ticket id -> messages
	log      []string
}

func (f *fakeTickets) ListByUser(_ context.Context, userID, _ string, _ int32) ([]*Ticket, string, error) {
	var out []*Ticket
	for _, t := range f.tickets {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, "", nil
}
func (f *fakeTickets) ListMessages(_ context.Context, ticketID string) ([]*Message, error) {
	return f.messages[ticketID], nil
}
func (f *fakeTickets) RedactMessage(_ context.Context, ticketID, sk, body string) error {
	for _, m := range f.messages[ticketID] {
		if m.SK == sk {
			m.Body = body
		}
	}
	f.log = append(f.log, "redact:"+sk)
	return nil
}
func (f *fakeTickets) UpdateTicket(_ context.Context, id string, updates map[string]any) error {
	if _, ok := updates["user_id"]; ok {
		f.tickets[id].UserID = ""
	}
	f.log = append(f.log, "drop-owner:"+id)
	return nil
}

func TestRedactor_RedactsBeforeDroppingOwnership(t *testing.T) {
	f := &fakeTickets{
		tickets: map[string]*Ticket{"t1": {PK: BuildPK("t1"), UserID: "u1"}},
		messages: map[string][]*Message{"t1": {
			{SK: "MSG#1", AuthorType: AuthorUser, AuthorID: "u1", Body: "meu CPF é 123"},
			{SK: "MSG#2", AuthorType: AuthorAgent, AuthorID: "agent", Body: "Olá"},
		}},
	}
	r := newRedactorWith(f)
	for i := 0; i < 2; i++ {
		if err := r.RedactUser(context.Background(), "u1"); err != nil {
			t.Fatalf("RedactUser: %v", err)
		}
	}
	if f.messages["t1"][0].Body != RedactedBody || f.messages["t1"][1].Body != "Olá" {
		t.Fatalf("bodies %q / %q", f.messages["t1"][0].Body, f.messages["t1"][1].Body)
	}
	if len(f.log) != 2 || f.log[0] != "redact:MSG#1" || f.log[1] != "drop-owner:t1" {
		t.Fatalf("order %v: messages must be redacted before ownership is dropped, once", f.log)
	}
}
```

`api/internal/domain/audit/anonymizer_test.go`:

```go
package audit

import (
	"context"
	"testing"
)

type fakeEvents struct {
	events     []*Event
	anonymized map[string]bool // sk -> dropped metadata
}

func (f *fakeEvents) QueryByUser(_ context.Context, _, cursor string, _ int32) ([]*Event, string, error) {
	if cursor != "" {
		return nil, "", nil
	}
	return f.events, "", nil
}
func (f *fakeEvents) Anonymize(_ context.Context, _ string, sk string, dropMetadata bool) error {
	f.anonymized[sk] = dropMetadata
	return nil
}

func TestAnonymizer_KeepsDeletionEvidence(t *testing.T) {
	f := &fakeEvents{anonymized: map[string]bool{}, events: []*Event{
		{PK: BuildPK("u1"), SK: "EVT_1", EventType: EventLoginSuccess, IP: "1.2.3.4", UserAgent: "UA"},
		{PK: BuildPK("u1"), SK: "EVT_2", EventType: "account.deletion.requested", IP: "1.2.3.4", Metadata: map[string]string{"request_id": "r1"}},
		{PK: BuildPK("u1"), SK: "EVT_3", EventType: EventLoginSuccess}, // already clean
	}}
	if err := newAnonymizerWith(f).AnonymizeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("AnonymizeUser: %v", err)
	}
	if drop, ok := f.anonymized["EVT_1"]; !ok || !drop {
		t.Fatal("a regular event must lose ip/ua and metadata")
	}
	if drop, ok := f.anonymized["EVT_2"]; !ok || drop {
		t.Fatal("a deletion event must lose ip/ua but keep its metadata")
	}
	if _, ok := f.anonymized["EVT_3"]; ok {
		t.Fatal("an already-clean event must not be rewritten")
	}
}
```

- [ ] **Step 2: Fail** — `go test ./internal/domain/support/ ./internal/domain/audit/ -run 'Redactor|Anonymizer' -count=1` → build FAIL.

- [ ] **Step 3: Implement.**

`api/internal/domain/support/redactor.go`:

```go
package support

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// RedactedBody replaces a deleted user's own support messages.
const RedactedBody = "[mensagem removida a pedido do titular]"

type ticketStore interface {
	ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error)
	ListMessages(ctx context.Context, ticketID string) ([]*Message, error)
	RedactMessage(ctx context.Context, ticketID, messageSK, body string) error
	UpdateTicket(ctx context.Context, id string, updates map[string]any) error
}

// Redactor anonymizes a deleted user's tickets (inventory §3, ruling R9): their
// own message bodies are replaced, then the ticket loses user_id and the NPS
// comment. Agent replies and internal notes stay. Messages go first because a
// ticket is only found through user_id: dropping it first would strand
// unredacted messages if the purge crashed in between.
type Redactor struct{ store ticketStore }

func NewRedactor(db *dynamodb.Client, tablePrefix string) *Redactor {
	return &Redactor{store: NewRepository(db, tablePrefix).(*dynamoRepository)}
}

func newRedactorWith(s ticketStore) *Redactor { return &Redactor{store: s} }

func (r *Redactor) RedactUser(ctx context.Context, userID string) error {
	for {
		tickets, _, err := r.store.ListByUser(ctx, userID, "", 50)
		if err != nil {
			return fmt.Errorf("listing tickets: %w", err)
		}
		if len(tickets) == 0 {
			return nil
		}
		for _, t := range tickets {
			id := TicketID(t)
			msgs, err := r.store.ListMessages(ctx, id)
			if err != nil {
				return fmt.Errorf("listing messages: %w", err)
			}
			for _, m := range msgs {
				if m.AuthorType == AuthorUser && m.AuthorID == userID && m.Body != RedactedBody {
					if err := r.store.RedactMessage(ctx, id, m.SK, RedactedBody); err != nil {
						return fmt.Errorf("redacting message: %w", err)
					}
				}
			}
			if err := r.store.UpdateTicket(ctx, id, map[string]any{"user_id": nil, "nps_message": nil}); err != nil {
				return fmt.Errorf("dropping ticket owner: %w", err)
			}
		}
	}
}
```

Add to `api/internal/domain/support/repository.go` (the concrete type, **not** the `Repository` interface):

```go
// RedactMessage replaces one message's body (account deletion only).
func (r *dynamoRepository) RedactMessage(ctx context.Context, ticketID, messageSK, body string) error {
	_, err := r.table.UpdateItem(ctx, BuildPK(ticketID), &messageSK, map[string]any{"body": body})
	return err
}
```

`TicketID(t)` must return the id that `ListMessages`/`UpdateTicket` expect: the `TICKET_` prefix stripped from `t.PK`. If `support/model.go` has no such helper, add `func TicketID(t *Ticket) string { return strings.TrimPrefix(t.PK, "TICKET_") }` next to `BuildPK`.

Caution for the loop: it ends only when `ListByUser` returns nothing. `UpdateTicket` with `user_id: nil` REMOVEs the attribute (api-commons `buildUpdateExpr`), which takes the ticket out of `user-index`. `user-index` is eventually consistent, so the same ticket may come back once more. That is harmless: its messages are already redacted and the update is idempotent.

`api/internal/domain/audit/anonymizer.go`:

```go
package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const deletionEventPrefix = "account.deletion."

type eventStore interface {
	QueryByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Event, string, error)
	Anonymize(ctx context.Context, pk, sk string, dropMetadata bool) error
}

// Anonymizer strips a deleted user's audit rows of IP, user agent and metadata
// (inventory §3). Deletion events keep their metadata (request_id) as proof
// the request was honoured.
type Anonymizer struct{ store eventStore }

func NewAnonymizer(db *dynamodb.Client, tablePrefix string) *Anonymizer {
	return &Anonymizer{store: NewRepository(db, tablePrefix).(*dynamoRepository)}
}

func newAnonymizerWith(s eventStore) *Anonymizer { return &Anonymizer{store: s} }

func (a *Anonymizer) AnonymizeUser(ctx context.Context, userID string) error {
	cursor := ""
	for {
		events, next, err := a.store.QueryByUser(ctx, userID, cursor, 100)
		if err != nil {
			return fmt.Errorf("listing audit events: %w", err)
		}
		for _, e := range events {
			keepMeta := strings.HasPrefix(e.EventType, deletionEventPrefix)
			if e.IP == "" && e.UserAgent == "" && (keepMeta || len(e.Metadata) == 0) {
				continue // already clean
			}
			if err := a.store.Anonymize(ctx, e.PK, e.SK, !keepMeta); err != nil {
				return fmt.Errorf("anonymizing audit event: %w", err)
			}
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

func (r *dynamoRepository) Anonymize(ctx context.Context, pk, sk string, dropMetadata bool) error {
	expr := "REMOVE ip, user_agent"
	if dropMetadata {
		expr += ", metadata"
	}
	_, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression:    aws.String(expr),
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	return err
}
```

(`ip`, `user_agent` and `metadata` are not DynamoDB reserved words.) Check that the concrete repository types are named `dynamoRepository` in both packages and that `NewRepository` returns them; adapt the type assertion to the real names.

- [ ] **Step 4: Pass** — `go vet ./... && go test ./internal/domain/support/ ./internal/domain/audit/ -count=1` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/domain/support internal/domain/audit && git commit -m "feat(api): support redactor and audit anonymizer for account deletion"`

---

### Task 4: KYC documents to retention

**Files:**
- Modify `api/internal/storage/s3.go` (`ListKeys`, `CopyObject`)
- Modify `api/internal/domain/user/model.go` (`KYCRetentionDocKey`) and `api/internal/domain/user/service.go` (`Purge` maps document keys)
- Create `api/internal/domain/deletion/kycdocs.go`, `api/internal/domain/deletion/kycdocs_test.go`
- Test `api/internal/domain/user/service_test.go`

**Interfaces:**
- Produces `func (s *S3) ListKeys(ctx, prefix string) ([]string, error)` and `func (s *S3) CopyObject(ctx, srcKey, dstKey string) error`.
- Produces `func user.KYCRetentionDocKey(userID, documentID string) string`.
- Produces `type deletion.KYCStore interface { ListKeys; CopyObject; DeleteObject(ctx, key string) error }` and `func deletion.moveKYCDocuments(ctx, store KYCStore, u *user.User) error`.

- [ ] **Step 1: Failing tests.**

`api/internal/domain/deletion/kycdocs_test.go`:

```go
package deletion

import (
	"context"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type fakeBucket struct{ objects map[string]bool }

func (f *fakeBucket) ListKeys(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *fakeBucket) CopyObject(_ context.Context, src, dst string) error {
	if !f.objects[src] {
		return errNoSuchKey
	}
	f.objects[dst] = true
	return nil
}
func (f *fakeBucket) DeleteObject(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func TestMoveKYCDocuments_IsIdempotent(t *testing.T) {
	b := &fakeBucket{objects: map[string]bool{
		"kyc/u1/d1": true, "kyc/u1/orphan": true, "kyc/u2/x": true,
	}}
	u := &user.User{PK: user.BuildPK("u1"), KYCDocuments: []user.KYCDocument{{ID: "d1", Key: "kyc/u1/d1"}}}
	for i := 0; i < 2; i++ {
		if err := moveKYCDocuments(context.Background(), b, u); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if !b.objects[user.KYCRetentionDocKey("u1", "d1")] || len(b.objects) != 2 || !b.objects["kyc/u2/x"] {
		t.Fatalf("objects %v: d1 retained, u1's prefix emptied, u2 untouched", b.objects)
	}
}
```

with `var errNoSuchKey = errors.New("no such key")` declared in the test file (import `errors`).

Append to `api/internal/domain/user/service_test.go`:

```go
func TestPurge_RetainedDocumentsPointAtRetentionKeys(t *testing.T) {
	ctx := context.Background()
	svc, repo, u := purgedFixture(t)
	repo.byID[u.ID()].KYCDocuments = []user.KYCDocument{{ID: "d1", Type: "id_front", Key: "kyc/" + u.ID() + "/d1"}}
	if err := svc.Purge(ctx, u.ID(), "req-1", fakeMAC, time.Now()); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	docs := repo.kycRet[0].Record.KYCDocuments
	if len(docs) != 1 || docs[0].Key != user.KYCRetentionDocKey(u.ID(), "d1") {
		t.Fatalf("retained documents %+v", docs)
	}
}
```

- [ ] **Step 2: Fail** — `go test ./internal/domain/deletion/ ./internal/domain/user/ -run 'KYC|RetentionKeys' -count=1` → build FAIL.

- [ ] **Step 3: Implement.**

`user/model.go`:

```go
// KYCRetentionDocKey is where a deleted account's KYC document is kept for
// 5 years (D6); the bucket's expiration rule counts from the copy.
func KYCRetentionDocKey(userID, documentID string) string {
	return "retention/" + userID + "/" + documentID
}
```

`user/service.go` — in `Purge`, build the retained document list with the retention keys:

```go
	retainedDocs := make([]KYCDocument, 0, len(u.KYCDocuments))
	for _, d := range u.KYCDocuments {
		d.Key = KYCRetentionDocKey(userID, d.ID)
		retainedDocs = append(retainedDocs, d)
	}
```

and use `KYCDocuments: retainedDocs` in the `KYCRecord` literal.

`storage/s3.go`:

```go
// ListKeys returns every object key under prefix (current versions).
func (s *S3) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	return keys, nil
}

// CopyObject copies srcKey to dstKey inside the bucket.
func (s *S3) CopyObject(ctx context.Context, srcKey, dstKey string) error {
	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.bucket),
		CopySource: aws.String(s.bucket + "/" + srcKey),
		Key:        aws.String(dstKey),
	})
	if err != nil {
		return fmt.Errorf("copying %s: %w", srcKey, err)
	}
	return nil
}
```

`deletion/kycdocs.go`:

```go
package deletion

import (
	"context"
	"fmt"
	"slices"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type KYCStore interface {
	ListKeys(ctx context.Context, prefix string) ([]string, error)
	CopyObject(ctx context.Context, srcKey, dstKey string) error
	DeleteObject(ctx context.Context, key string) error
}

// moveKYCDocuments copies the user's confirmed KYC documents to the retention
// prefix (D6), then deletes every object under kyc/{user}/ (confirmed ones and
// abandoned uploads). Listing first makes it idempotent: a document already
// copied and deleted is no longer listed, so it is neither copied again nor
// looked for. Old versions expire through the bucket lifecycle (ruling R7).
func moveKYCDocuments(ctx context.Context, store KYCStore, u *user.User) error {
	userID := u.ID()
	keys, err := store.ListKeys(ctx, "kyc/"+userID+"/")
	if err != nil {
		return err
	}
	for _, d := range u.KYCDocuments {
		if !slices.Contains(keys, d.Key) {
			continue
		}
		if err := store.CopyObject(ctx, d.Key, user.KYCRetentionDocKey(userID, d.ID)); err != nil {
			return fmt.Errorf("retaining kyc document: %w", err)
		}
	}
	for _, k := range keys {
		if err := store.DeleteObject(ctx, k); err != nil {
			return fmt.Errorf("deleting kyc object: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Pass** — `go vet ./... && go test ./internal/domain/deletion/ ./internal/domain/user/ ./internal/storage/ -count=1` → `ok`.
- [ ] **Step 5: Commit** — `git add internal/storage internal/domain/user internal/domain/deletion && git commit -m "feat(api): move KYC documents to retention on account purge"`

---

### Task 5: Wire the erasers into `AccountPurger`

**Files:** Modify `api/internal/domain/deletion/purger.go`, `api/internal/domain/deletion/purger_test.go`, `api/cmd/api/main.go`.

**Interfaces:**
- Consumes Tasks 2–4.
- Produces `func (p *AccountPurger) WithDataErasers(orgs OrgEraser, companies CompanyEraser, support SupportRedactor, audit AuditAnonymizer, kyc KYCStore, users Users) *AccountPurger`, with:
  - `type OrgEraser interface { EraseOrganization(ctx, orgID string) error }`
  - `type CompanyEraser interface { EraseForOrganization(ctx, orgID string) error }`
  - `type SupportRedactor interface { RedactUser(ctx, userID string) error }`
  - `type AuditAnonymizer interface { AnonymizeUser(ctx, userID string) error }`

- [ ] **Step 1: Failing test** — append to `purger_test.go`:

```go
type recEraser struct {
	log  *[]string
	fail int // fail this many calls first
}

func (r *recEraser) step(name string) error {
	*r.log = append(*r.log, name)
	if r.fail > 0 {
		r.fail--
		return errors.New("throttled")
	}
	return nil
}
func (r *recEraser) EraseOrganization(context.Context, string) error    { return r.step("org") }
func (r *recEraser) EraseForOrganization(context.Context, string) error { return r.step("companies") }
func (r *recEraser) RedactUser(context.Context, string) error           { return r.step("support") }
func (r *recEraser) AnonymizeUser(context.Context, string) error        { return r.step("audit") }

func TestAccountPurger_DataErasersRunFirstAndAreRetried(t *testing.T) {
	var log []string
	orgs := &fakeOrgs{memberships: map[string][]*organization.Membership{}, log: &log}
	purgeUser := func(context.Context, string, string, time.Time) error { log = append(log, "tombstone"); return nil }
	companies := &recEraser{log: &log, fail: 1} // first attempt fails after the org row is gone
	er := &recEraser{log: &log}
	p := NewAccountPurger(orgs, recCompanies{&log}, recConsents{&log}, recSessions{&log}, &fakeKeys{}, recTOTP{&log}, recPasskeys{&log}, purgeUser).
		WithDataErasers(er, companies, er, er, &fakeBucket{objects: map[string]bool{}}, fakeUsers{u: &user.User{PK: user.BuildPK("u1")}})
	r := &Request{ID: "r1", UserID: "u1", Organizations: []string{"solo"}}
	if err := p.Purge(context.Background(), r); err == nil {
		t.Fatal("the first run must surface the companies failure")
	}
	log = nil
	if err := p.Purge(context.Background(), r); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if log[0] != "companies" || log[1] != "org" || log[len(log)-1] != "tombstone" {
		t.Fatalf("order %v: companies, org first; tombstone last", log)
	}
	for _, want := range []string{"support", "audit"} {
		if !slices.Contains(log, want) {
			t.Fatalf("missing step %q in %v", want, log)
		}
	}
}
```

(imports `errors`, `slices`, `gopkg.aoctech.app/account/api/internal/domain/user`; `fakeUsers` comes from `service_test.go`; `recCompanies` is the CompanyEdges fake from 2a, a different type from the `companies` eraser here).

- [ ] **Step 2: Fail** — `go test ./internal/domain/deletion/ -run DataErasers -count=1` → build FAIL `WithDataErasers undefined`.

- [ ] **Step 3: Implement** — `purger.go`:

```go
type OrgEraser interface {
	EraseOrganization(ctx context.Context, orgID string) error
}

type CompanyEraser interface {
	EraseForOrganization(ctx context.Context, orgID string) error
}

type SupportRedactor interface {
	RedactUser(ctx context.Context, userID string) error
}

type AuditAnonymizer interface {
	AnonymizeUser(ctx context.Context, userID string) error
}

// WithDataErasers adds the Phase 2b steps. kyc may be nil when no KYC bucket
// is configured (dev).
func (p *AccountPurger) WithDataErasers(orgs OrgEraser, companies CompanyEraser, support SupportRedactor,
	audit AuditAnonymizer, kyc KYCStore, users Users) *AccountPurger {
	p.orgEraser, p.companyEraser, p.support, p.audit, p.kyc, p.users = orgs, companies, support, audit, kyc, users
	return p
}
```

Add the fields `orgEraser OrgEraser`, `companyEraser CompanyEraser`, `support SupportRedactor`, `audit AuditAnonymizer`, `kyc KYCStore` and `users Users` to `AccountPurger`. Update its doc comment: drop the "Phase 2b adds" sentence and state the order.

In `Purge`, insert at the very top (before memberships):

```go
	if p.orgEraser != nil {
		for _, orgID := range r.Organizations {
			// Companies first: their rows live under the org id, which stays
			// known from the request even after the org row is gone.
			if err := p.companyEraser.EraseForOrganization(ctx, orgID); err != nil {
				return fmt.Errorf("erasing companies of %s: %w", orgID, err)
			}
			if err := p.orgEraser.EraseOrganization(ctx, orgID); err != nil {
				return fmt.Errorf("erasing organization %s: %w", orgID, err)
			}
		}
		if err := p.support.RedactUser(ctx, u); err != nil {
			return fmt.Errorf("redacting support: %w", err)
		}
		if err := p.audit.AnonymizeUser(ctx, u); err != nil {
			return fmt.Errorf("anonymizing audit: %w", err)
		}
	}
```

and just before `return p.purgeUser(...)`:

```go
	if p.kyc != nil {
		usr, err := p.users.GetByID(ctx, u)
		if err != nil {
			return fmt.Errorf("loading user for kyc documents: %w", err)
		}
		if usr.DeletionState != user.DeletionStatePurged { // the tombstone has no document keys
			if err := moveKYCDocuments(ctx, p.kyc, usr); err != nil {
				return err
			}
		}
	}
```

(import `gopkg.aoctech.app/account/api/internal/domain/user`). The 2a test `TestAccountPurger_RunsEveryStepThenTombstone` does not call `WithDataErasers`, so its expected order is unchanged.

`cmd/api/main.go` — append `.WithDataErasers(...)` to the `deletion.NewAccountPurger(...)` call:

```go
				WithDataErasers(orgDomain.NewEraser(db, cfg.TablePrefix), companyDomain.NewEraser(db, cfg.TablePrefix),
					supportDomain.NewRedactor(db, cfg.TablePrefix), auditDomain.NewAnonymizer(db, cfg.TablePrefix),
					kycDocs, userSvc)
```

with `kycDocs` declared before it:

```go
		var kycDocs deletion.KYCStore
		if s3Cli, ok := kycPresigner.(*storage.S3); ok {
			kycDocs = s3Cli
		}
```

(`kycPresigner` is the `*storage.S3` built for KYC when `KYC_DOCUMENTS_BUCKET` is set; nil otherwise.)

- [ ] **Step 4: Pass** — `go vet ./... && go build ./... && go test ./... 2>&1 | tail -30` → all `ok`.
- [ ] **Step 5: Commit** — `git add internal/domain/deletion cmd/api/main.go && git commit -m "feat(api): purge organizations, support, audit and KYC documents"`

---

### Task 6: Infrastructure — users-table TTL, KYC bucket grants

**Files:** Modify `cdk/lib/dynamodb-stack.ts`, `cdk/lib/iam-stack.ts`; Test `cdk/test/dynamodb-stack.test.ts`, `cdk/test/iam-stack.test.ts`.

- [ ] **Step 1: Failing tests** — append to `cdk/test/dynamodb-stack.test.ts`:

```ts
test('users table expires KYC retention items via TTL', () => {
  const app = new cdk.App()
  const stack = new DynamoDBStack(app, 'TestUsersTTLStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
  })
  Template.fromStack(stack).hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_account_users',
    TimeToLiveSpecification: {AttributeName: 'expires_at', Enabled: true},
  })
})
```

and to `cdk/test/iam-stack.test.ts` (inside a new test that reuses the setup of the existing one):

```ts
test('app role may list KYC objects and move them to retention', () => {
  const app = new cdk.App()
  const env = {account: '868899309401', region: 'us-east-1'}
  const tables = new DynamoDBStack(app, 'TestIAMTables2', {env, environment: 'prod'}).tables
  const stack = new IAMStack(app, 'TestIAMStack2', {
    env, environment: 'prod', dynamoDBTables: tables,
    deploymentsBucketArn: 'arn:aws:s3:::deployments', logsBucketArn: 'arn:aws:s3:::logs',
    kycDocumentsBucketArn: 'arn:aws:s3:::kyc',
  })
  Template.fromStack(stack).hasResourceProperties('AWS::IAM::Policy', {
    PolicyDocument: {Statement: Match.arrayWith([
      Match.objectLike({Action: 's3:ListBucket', Resource: 'arn:aws:s3:::kyc',
        Condition: {StringLike: {'s3:prefix': 'kyc/*'}}}),
      Match.objectLike({Action: Match.arrayWith(['s3:PutObject']), Resource: 'arn:aws:s3:::kyc/retention/*'}),
      Match.objectLike({Action: 's3:DeleteObject', Resource: 'arn:aws:s3:::kyc/kyc/*'}),
    ])},
  })
})
```

- [ ] **Step 2: Fail** — `cd cdk && npx jest` → the two new tests fail.

- [ ] **Step 3: Implement.**

`dynamodb-stack.ts`, on the users `TableV2`:

```ts
      // KYCRET#{user_id} retention items of deleted accounts expire 5 years after
      // the purge (account-deletion D6). No other users-table item sets expires_at.
      timeToLiveAttribute: 'expires_at',
```

`iam-stack.ts`, after the existing KYC `s3:PutObject/GetObject` statement:

```ts
    // Account deletion: list a user's KYC prefix, copy documents to retention/
    // (kept 5 years by the bucket lifecycle) and delete the originals.
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:ListBucket'],
      resources: [kycDocumentsBucketArn],
      conditions: {StringLike: {'s3:prefix': 'kyc/*'}},
    }));
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject'],
      resources: [`${kycDocumentsBucketArn}/retention/*`],
    }));
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:DeleteObject'],
      resources: [`${kycDocumentsBucketArn}/kyc/*`],
    }));
```

(`CopyObject` needs `s3:GetObject` on the source, `kyc/*`, which is already granted, and `s3:PutObject` on the destination.)

- [ ] **Step 4: Pass** — `npx tsc --noEmit && npx jest` → all pass.
- [ ] **Step 5: Commit** — `git add cdk && git commit -m "feat(cdk): users-table TTL and KYC retention grants for account deletion"`

---

### Task 7: Documentation

- [ ] **`README.md`** Account deletion: the purge now also does the following:
  - erases single-member organizations (with companies and actors);
  - redacts the user's support messages;
  - strips IP/UA/metadata from audit rows (deletion rows keep `request_id`);
  - moves KYC documents to `retention/{sub}/` (5 years).
- [ ] **`PLAN.md`**: check Phase 2b.
- [ ] **Data inventory §3:** support row → R9; KYC documents row → retention prefix + R7; add a note on `KYCPEND_` (R8).
- [ ] **ctech-account spec §7:** steps 1, 3, 4 and 5 now match the implementation order (organizations → memberships/credentials → support/audit → KYC documents → tombstone).
- [ ] **Commit** — `git add README.md PLAN.md docs/specs && git commit -m "docs: account deletion phase 2b"`

## Cross-project impact

- ctech-account only. IAM: the app role gains `ListBucket` on the KYC bucket with a `kyc/*` prefix condition, `PutObject` on `retention/*` and `DeleteObject` on `kyc/*`. The users table gains TTL on `expires_at`.
- `ctech-dfe` erases the same frozen organizations' DF-e data when it receives `user.erase` (its own participant plan).
