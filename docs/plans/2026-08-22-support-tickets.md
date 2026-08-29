# Support Tickets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a self-hosted support-ticket system (public form + SES notifications + admin reply UI) so the user doesn't need Zendesk.

**Architecture:** New `internal/domain/support` Go domain (model/repository/service) backed by one new DynamoDB table with 4 GSIs; raw-MIME SES sends for e-mail threading; a `support_role` field on the existing user record (checked via a DB lookup in a new middleware, not a JWT claim — see spec §4.3); new public/account/admin routes in `internal/handler`; new Next.js pages under `/support`, `/account/support`, `/admin/support`, each run through the `impeccable` skill for visual design.

**Tech Stack:** Go 1.26, Fiber v3, DynamoDB (aws-sdk-go-v2), SESv2, Next.js 16 (static export), Cloudflare Turnstile.

**Spec:** `docs/specs/2026-08-22-support-tickets-design.md`

## Global Constraints

- Layering is `handler → service → repository`; services take repository **interfaces** (`api/CLAUDE.md`).
- Every HTTP error is an `*apierror.Problem` sent via `.Send(c)` — never raw `c.Status().JSON()`.
- Every request body is validated via `validate.Struct` (use the existing `parseBody[T]` helper in `internal/handler/helpers.go`).
- No production DynamoDB scans; `GetItem` > `Query` > `Scan`.
- Table names always come from `database.TableName(tablePrefix, "...")` / `database.NewBase(...)` — never hardcoded.
- `crypto.JWTService.SignAccessToken` and every one of its call sites are **out of scope** for this feature — `support_role` is never added as a JWT claim (spec §4.3).
- `subject_other`, ticket `body`, and `nps_message` all go through the shared freetext validator (trim, length bounds, junk-pattern rejection) built in Task 5 — no field skips it.
- Every new UI screen (`/support`, `/account/support`, `/admin/support*`) is designed/reviewed via the `impeccable` skill, not freehand markup.
- Every new endpoint/config var/table is added to `README.md` in the same change (`ctech-account` root `CLAUDE.md` mandatory-docs policy).

---

## Task 1: CDK — support tickets table

**Files:**
- Modify: `cdk/lib/dynamodb-stack.ts`

**Interfaces:**
- Produces: DynamoDB table `{environment}_account_support_tickets` with GSIs `status-index` (PK `status`, SK `last_message_at`), `user-index` (PK `user_id`, SK `created_at`), `anon-token-index` (PK `anonymous_token`), `ticket-number-index` (PK `ticket_number`).

- [ ] **Step 1: Add the table definition**

Add this block after the `apiKeysTable` block (same file, same pattern as the other `TableV2` definitions — on-demand billing, `ALL` projection, PITR/removal policy from the existing `pitr`/`removalPolicy` locals already in scope):

```ts
const supportTicketsTable = new dynamodb.TableV2(this, 'SupportTicketsTableV2', {
  tableName: `${environment}_account_support_tickets`,
  partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
  sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
  billing: dynamodb.Billing.onDemand({
    maxReadRequestUnits: 1000,
    maxWriteRequestUnits: 1000,
  }),
  pointInTimeRecoverySpecification: {
    pointInTimeRecoveryEnabled: pitr,
  },
  removalPolicy,
  globalSecondaryIndexes: [
    {
      indexName: 'status-index',
      partitionKey: {name: 'status', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'last_message_at', type: dynamodb.AttributeType.STRING},
      projectionType: dynamodb.ProjectionType.ALL,
      warmThroughput: undefined,
      maxReadRequestUnits: 1000,
      maxWriteRequestUnits: 1000,
    },
    {
      indexName: 'user-index',
      partitionKey: {name: 'user_id', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'created_at', type: dynamodb.AttributeType.STRING},
      projectionType: dynamodb.ProjectionType.ALL,
      warmThroughput: undefined,
      maxReadRequestUnits: 1000,
      maxWriteRequestUnits: 1000,
    },
    {
      indexName: 'anon-token-index',
      partitionKey: {name: 'anonymous_token', type: dynamodb.AttributeType.STRING},
      projectionType: dynamodb.ProjectionType.ALL,
      warmThroughput: undefined,
      maxReadRequestUnits: 1000,
      maxWriteRequestUnits: 1000,
    },
    {
      indexName: 'ticket-number-index',
      partitionKey: {name: 'ticket_number', type: dynamodb.AttributeType.NUMBER},
      projectionType: dynamodb.ProjectionType.ALL,
      warmThroughput: undefined,
      maxReadRequestUnits: 1000,
      maxWriteRequestUnits: 1000,
    },
  ],
});
this.tables.set('account_support_tickets', supportTicketsTable);
```

- [ ] **Step 2: Verify CDK synthesizes**

Run: `cd cdk && npx cdk synth CtechAccount-Dev-DynamoDB > /dev/null`
Expected: exits 0, no diff errors. (Do not deploy — this is a local synth check only.)

- [ ] **Step 3: Commit**

```bash
git add cdk/lib/dynamodb-stack.ts
git commit -m "feat(cdk): add account_support_tickets table with status/user/anon-token/ticket-number GSIs"
```

---

## Task 2: CDK + config — Turnstile secret plumbing

**Files:**
- Modify: `cdk/lib/api-stack.ts:97-108`
- Modify: `api/internal/config/config.go`

**Interfaces:**
- Produces: `config.Config.TurnstileSecretKey string` (env `TURNSTILE_SECRET_KEY`), read from SSM `/ctech-account/{env}/turnstile-secret-key` in deployed environments.

- [ ] **Step 1: Add the SSM env mapping**

In `cdk/lib/api-stack.ts`, add one line to the existing `scripts.run(userData, 'setup-ssm-env.sh', ...)` call (same list as `FROM_EMAIL`/`GOOGLE_CLIENT_ID`):

```ts
      `TURNSTILE_SECRET_KEY=/ctech-account/${environment}/turnstile-secret-key`,
```

- [ ] **Step 2: Add the config field**

In `api/internal/config/config.go`, add to the `Config` struct (near `FromEmail`):

```go
	// TurnstileSecretKey verifies the Cloudflare Turnstile token on the public
	// ticket-creation endpoint (TURNSTILE_SECRET_KEY env var). Empty disables
	// verification — only acceptable in dev.
	TurnstileSecretKey string
```

And in `Load()`'s returned struct literal, add:

```go
		TurnstileSecretKey: os.Getenv("TURNSTILE_SECRET_KEY"),
```

- [ ] **Step 3: Build check**

Run: `cd api && go build ./...`
Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
git add cdk/lib/api-stack.ts api/internal/config/config.go
git commit -m "feat: add TURNSTILE_SECRET_KEY config + SSM wiring"
```

---

## Task 3: Domain model — `support.Ticket` / `support.Message`

**Files:**
- Create: `api/internal/domain/support/model.go`

**Interfaces:**
- Produces: `support.Ticket`, `support.Message` structs; `support.BuildPK`, `support.BuildMessageSK`; category/priority/status/author constants; `support.ValidCategories`, `support.ValidPriorities`, `support.ValidStatuses` (`[]string`, used by both the validator tag and handlers).

- [ ] **Step 1: Write the model**

```go
package support

import "strings"

const (
	StatusOpen     = "open"
	StatusAnswered = "answered"
	StatusClosed   = "closed"

	PriorityLow      = "low"
	PriorityMedium   = "medium"
	PriorityHigh     = "high"
	PriorityUrgent   = "urgent"
	PriorityCritical = "critical"

	CategoryAccount = "account"
	CategoryKYC     = "kyc"
	CategoryWallet  = "wallet"
	CategoryDFe     = "dfe"
	CategoryBilling = "billing"
	CategoryPoker   = "poker"
	CategoryOther   = "other"

	AuthorUser   = "user"
	AuthorAgent  = "agent"
	AuthorSystem = "system"

	metaSK        = "META"
	messageSKPrefix = "MSG_"
)

// ValidCategories, ValidPriorities and ValidStatuses back both the
// go-playground/validator "oneof" tags in the request DTOs and any UI
// picker — the catalog is chosen, never free-typed (mirrors the OAuth scope
// catalog convention).
var ValidCategories = []string{CategoryAccount, CategoryKYC, CategoryWallet, CategoryDFe, CategoryBilling, CategoryPoker, CategoryOther}
var ValidPriorities = []string{PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent, PriorityCritical}
var ValidStatuses = []string{StatusOpen, StatusAnswered, StatusClosed}

type Ticket struct {
	PK               string `dynamodbav:"pk"`
	SK               string `dynamodbav:"sk"`
	TicketNumber     int64  `dynamodbav:"ticket_number"`
	UserID           string `dynamodbav:"user_id,omitempty"`
	AnonymousEmail   string `dynamodbav:"anonymous_email,omitempty"`
	AnonymousToken   string `dynamodbav:"anonymous_token,omitempty"`
	SubjectCategory  string `dynamodbav:"subject_category"`
	SubjectOther     string `dynamodbav:"subject_other,omitempty"`
	Priority         string `dynamodbav:"priority"`
	Status           string `dynamodbav:"status"`
	CreatedAt        string `dynamodbav:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at"`
	ClosedAt         string `dynamodbav:"closed_at,omitempty"`
	LastMessageAt    string `dynamodbav:"last_message_at"`
	RootSESMessageID string `dynamodbav:"root_ses_message_id,omitempty"`
	LastSESMessageID string `dynamodbav:"last_ses_message_id,omitempty"`
	NPSScore         int    `dynamodbav:"nps_score,omitempty"`
	NPSMessage       string `dynamodbav:"nps_message,omitempty"`
	NPSRequestedAt   string `dynamodbav:"nps_requested_at,omitempty"`
}

// BuildPK returns the item-collection partition key for ticket id.
func BuildPK(id string) string { return "TICKET_" + id }

// ID strips the "TICKET_" prefix back off PK.
func (t *Ticket) ID() string { return strings.TrimPrefix(t.PK, "TICKET_") }

// IsAnonymous reports whether the ticket was submitted without a session.
func (t *Ticket) IsAnonymous() bool { return t.UserID == "" }

type Message struct {
	PK           string `dynamodbav:"pk"`
	SK           string `dynamodbav:"sk"`
	AuthorType   string `dynamodbav:"author_type"`
	AuthorID     string `dynamodbav:"author_id,omitempty"`
	Body         string `dynamodbav:"body"`
	CreatedAt    string `dynamodbav:"created_at"`
	SESMessageID string `dynamodbav:"ses_message_id,omitempty"`
}

// BuildMessageSK returns the sort key for a message created at createdAt
// (RFC3339Nano — nanosecond precision keeps same-instant messages ordered).
func BuildMessageSK(createdAt string) string { return messageSKPrefix + createdAt }
```

- [ ] **Step 2: Build check**

Run: `cd api && go build ./internal/domain/support/...`
Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add api/internal/domain/support/model.go
git commit -m "feat: add support ticket/message domain model"
```

---

## Task 4: Freetext validator (trim, length, junk-pattern rejection)

**Files:**
- Create: `api/internal/validate/freetext.go`
- Create: `api/internal/validate/freetext_test.go`

**Interfaces:**
- Produces: `validate.FreetextRule{Min, Max int}`, `validate.Freetext(s string, rule FreetextRule) (cleaned string, err error)` — trims, checks length, rejects junk. This is called explicitly from `support.Service` (not a struct tag) because the min/max bounds differ per field (`body` 15–4000, `subject_other` 3–120, `nps_message` 15–1000) and the required-ness of `nps_message` is conditional (§3.5 of the spec) — a single fixed tag can't express that.

- [x] **Step 1: Write the failing test**

```go
package validate

import "testing"

func TestFreetext(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		rule    FreetextRule
		want    string
		wantErr bool
	}{
		{"trims whitespace", "  hello world  ", FreetextRule{Min: 3, Max: 100}, "hello world", false},
		{"too short after trim", "  hi  ", FreetextRule{Min: 15, Max: 100}, "", true},
		{"too long", string(make([]byte, 200)), FreetextRule{Min: 1, Max: 100}, "", true},
		{"repeated character run", "aaaaaaaaaaaaaaaaaaaa", FreetextRule{Min: 3, Max: 100}, "", true},
		{"repeated punctuation run", "..........", FreetextRule{Min: 3, Max: 100}, "", true},
		{"no letters at all", "1234567890123456", FreetextRule{Min: 3, Max: 100}, "", true},
		{"real sentence passes", "O botão de login não funciona no Safari", FreetextRule{Min: 15, Max: 4000}, "O botão de login não funciona no Safari", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Freetext(tc.in, tc.rule)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result %q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/validate/... -run TestFreetext -v`
Expected: FAIL (`Freetext`/`FreetextRule` undefined).

- [ ] **Step 3: Write the implementation**

```go
package validate

import (
	"fmt"
	"strings"
	"unicode"
)

// FreetextRule bounds one free-text field. Min/Max are inclusive, measured
// in runes after trimming.
type FreetextRule struct {
	Min int
	Max int
}

// minLetterRatio is the floor for (letters / non-whitespace runes) after
// collapsing repeated-character runs. Below this, the input reads as
// keyboard-mashing or punctuation spam rather than a sentence — this is a
// bot/low-effort filter, not a content-quality judge.
const minLetterRatio = 0.3

// maxRepeat is how many identical consecutive runes are tolerated before the
// input is rejected as junk (e.g. "aaaaaaaaaaaaaaaa", "..........").
const maxRepeat = 4

// Freetext trims s, checks its length against rule, and rejects low-signal
// junk (long repeated-character runs, or too few letters relative to
// length). Returns the trimmed string on success.
func Freetext(s string, rule FreetextRule) (string, error) {
	trimmed := strings.TrimSpace(s)
	runeLen := len([]rune(trimmed))
	if runeLen < rule.Min {
		return "", fmt.Errorf("must be at least %d characters long", rule.Min)
	}
	if runeLen > rule.Max {
		return "", fmt.Errorf("must be at most %d characters long", rule.Max)
	}
	if hasLongRepeat(trimmed) {
		return "", fmt.Errorf("looks like repeated characters, not a real message")
	}
	if !hasEnoughLetters(trimmed) {
		return "", fmt.Errorf("must contain readable text")
	}
	return trimmed, nil
}

func hasLongRepeat(s string) bool {
	runs := 1
	var prev rune
	for i, r := range s {
		if i == 0 {
			prev = r
			continue
		}
		if r == prev {
			runs++
			if runs > maxRepeat {
				return true
			}
		} else {
			runs = 1
			prev = r
		}
	}
	return false
}

func hasEnoughLetters(s string) bool {
	var letters, nonSpace int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		nonSpace++
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if nonSpace == 0 {
		return false
	}
	return float64(letters)/float64(nonSpace) >= minLetterRatio
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/validate/... -run TestFreetext -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add api/internal/validate/freetext.go api/internal/validate/freetext_test.go
git commit -m "feat: add reusable freetext validator (trim, length, junk-pattern rejection)"
```

---

## Task 5: `support_role` on `account_users`

**Files:**
- Modify: `api/internal/domain/user/model.go`
- Modify: `api/internal/domain/user/service.go`
- Modify: `api/internal/domain/user/service_test.go`

**Interfaces:**
- Consumes: `user.Repository.Update(ctx, userID string, updates map[string]any) error` (already exists).
- Produces: `user.User.SupportRole string` field; `user.Service.SetSupportRole(ctx, userID, role string) error`; constants `user.SupportRoleAgent = "agent"`, `user.SupportRoleManager = "manager"`, `user.SupportRoleAdmin = "admin"`.

- [ ] **Step 1: Add the field and constants**

In `api/internal/domain/user/model.go`, add near the top-level `const` block (create one if none exists) and to the `User` struct (after `PrivacyAcceptedAt`):

```go
const (
	SupportRoleAgent   = "agent"
	SupportRoleManager = "manager"
	SupportRoleAdmin   = "admin"
)
```

```go
	// SupportRole gates the support-ticket admin UI/API. Empty for regular
	// users. Deliberately scoped to this feature, not a general permissions
	// field — see docs/specs/2026-08-22-support-tickets-design.md §2.
	SupportRole string `dynamodbav:"support_role,omitempty"`
```

- [ ] **Step 2: Write the failing test**

In `api/internal/domain/user/service_test.go`, add (following the existing test file's setup — check its top for how `Service`/in-memory repo are constructed and match that style):

```go
func TestSetSupportRole(t *testing.T) {
	svc, repo := newTestService(t) // use whatever constructor this file already defines
	u := createTestUser(t, repo)   // likewise — reuse the file's existing test-user helper

	if err := svc.SetSupportRole(context.Background(), u.ID(), SupportRoleAgent); err != nil {
		t.Fatalf("SetSupportRole: %v", err)
	}

	got, err := svc.GetByID(context.Background(), u.ID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.SupportRole != SupportRoleAgent {
		t.Fatalf("got SupportRole %q, want %q", got.SupportRole, SupportRoleAgent)
	}
}
```

(If `newTestService`/`createTestUser` aren't the real helper names in this file, read the file first and use whatever it actually exports — do not invent names.)

- [ ] **Step 3: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/user/... -run TestSetSupportRole -v`
Expected: FAIL (`SetSupportRole` undefined).

- [ ] **Step 4: Implement**

In `api/internal/domain/user/service.go`, add:

```go
// SetSupportRole grants or changes the caller's support-ticket role. There is
// no self-service path to this — only cmd/supportrole calls it.
func (s *Service) SetSupportRole(ctx context.Context, userID, role string) error {
	return s.repo.Update(ctx, userID, map[string]any{"support_role": role})
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd api && go test ./internal/domain/user/... -run TestSetSupportRole -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/internal/domain/user/model.go api/internal/domain/user/service.go api/internal/domain/user/service_test.go
git commit -m "feat: add support_role field to user + SetSupportRole"
```

---

## Task 6: Domain repository — `support.Repository`

**Files:**
- Create: `api/internal/domain/support/repository.go`

**Interfaces:**
- Consumes: `database.Base` (`GetItem`, `PutItem`, `Query(ctx, database.QueryOpts{PK, SKPrefix})`, `QueryGSI(ctx, index, attrName, value, limit, cursor)`, `UpdateItem(ctx, pk, sk *string, updates map[string]any)`), `database.NewBase`, `database.TableName`, raw `*dynamodb.Client` for the atomic ticket-number counter and cursor-paginated GSI queries (same reasons `audit.dynamoRepository` uses the raw client directly — `ExclusiveStartKey`/`ADD` aren't exposed by `database.Base`).
- Produces: `support.Repository` interface consumed by `support.Service` (Task 8).

- [ ] **Step 1: Write the repository**

```go
package support

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
)

var ErrNotFound = errors.New("ticket not found")

const (
	tableSuffix       = "account_support_tickets"
	counterPK         = "COUNTER"
	counterSK         = "TICKET_NUMBER"
	statusIndex       = "status-index"
	userIndex         = "user-index"
	anonTokenIndex    = "anon-token-index"
	ticketNumberIndex = "ticket-number-index"
)

// Repository is the data-access interface for support tickets and their
// message threads.
type Repository interface {
	NextTicketNumber(ctx context.Context) (int64, error)
	CreateTicket(ctx context.Context, t *Ticket) error
	GetTicket(ctx context.Context, id string) (*Ticket, error)
	GetTicketByAnonToken(ctx context.Context, token string) (*Ticket, error)
	GetTicketByNumber(ctx context.Context, number int64) (*Ticket, error)
	UpdateTicket(ctx context.Context, id string, updates map[string]any) error
	PutMessage(ctx context.Context, m *Message) error
	ListMessages(ctx context.Context, ticketID string) ([]*Message, error)
	ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error)
	ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error)
}

type dynamoRepository struct {
	table     database.Base
	db        *dynamodb.Client
	tableName string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{
		table:     database.NewBase(db, tablePrefix, tableSuffix),
		db:        db,
		tableName: database.TableName(tablePrefix, tableSuffix),
	}
}

// NextTicketNumber atomically increments the single counter item and returns
// the new value. ADD is not exposed by database.Base, so this uses the raw
// client (same reasoning as audit.dynamoRepository's cursor pagination).
func (r *dynamoRepository) NextTicketNumber(ctx context.Context) (int64, error) {
	out, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: counterPK},
			"sk": &types.AttributeValueMemberS{Value: counterSK},
		},
		UpdateExpression:          aws.String("ADD #v :one"),
		ExpressionAttributeNames:  map[string]string{"#v": "value"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}},
		ReturnValues:              types.ReturnValueUpdatedNew,
	})
	if err != nil {
		return 0, fmt.Errorf("incrementing ticket counter: %w", err)
	}
	var result struct {
		Value int64 `dynamodbav:"value"`
	}
	if err := attributevalue.UnmarshalMap(out.Attributes, &result); err != nil {
		return 0, fmt.Errorf("unmarshaling ticket counter: %w", err)
	}
	return result.Value, nil
}

func (r *dynamoRepository) CreateTicket(ctx context.Context, t *Ticket) error {
	t.SK = metaSK
	item, err := attributevalue.MarshalMap(t)
	if err != nil {
		return fmt.Errorf("marshaling ticket: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) GetTicket(ctx context.Context, id string) (*Ticket, error) {
	item, err := r.table.GetItem(ctx, BuildPK(id), metaSK)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}
	var t Ticket
	if err := attributevalue.UnmarshalMap(item, &t); err != nil {
		return nil, fmt.Errorf("unmarshaling ticket: %w", err)
	}
	return &t, nil
}

func (r *dynamoRepository) GetTicketByAnonToken(ctx context.Context, token string) (*Ticket, error) {
	res, err := r.table.QueryGSI(ctx, anonTokenIndex, "anonymous_token", token, 1, nil)
	if err != nil {
		return nil, fmt.Errorf("querying anon token index: %w", err)
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}
	var t Ticket
	if err := attributevalue.UnmarshalMap(res.Items[0], &t); err != nil {
		return nil, fmt.Errorf("unmarshaling ticket: %w", err)
	}
	return &t, nil
}

func (r *dynamoRepository) GetTicketByNumber(ctx context.Context, number int64) (*Ticket, error) {
	res, err := r.table.QueryGSI(ctx, ticketNumberIndex, "ticket_number", number, 1, nil)
	if err != nil {
		return nil, fmt.Errorf("querying ticket number index: %w", err)
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}
	var t Ticket
	if err := attributevalue.UnmarshalMap(res.Items[0], &t); err != nil {
		return nil, fmt.Errorf("unmarshaling ticket: %w", err)
	}
	return &t, nil
}

func (r *dynamoRepository) UpdateTicket(ctx context.Context, id string, updates map[string]any) error {
	sk := metaSK
	_, err := r.table.UpdateItem(ctx, BuildPK(id), &sk, updates)
	return err
}

func (r *dynamoRepository) PutMessage(ctx context.Context, m *Message) error {
	m.PK = BuildPK(m.PK) // callers pass the bare ticket id in PK; normalize here — see note below
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) ListMessages(ctx context.Context, ticketID string) ([]*Message, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(ticketID), SKPrefix: messageSKPrefix})
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	messages := make([]*Message, 0, len(res.Items))
	for _, item := range res.Items {
		var m Message
		if err := attributevalue.UnmarshalMap(item, &m); err != nil {
			return nil, fmt.Errorf("unmarshaling message: %w", err)
		}
		messages = append(messages, &m)
	}
	return messages, nil
}

func (r *dynamoRepository) ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error) {
	return r.queryIndexPage(ctx, userIndex, "user_id", &types.AttributeValueMemberS{Value: userID}, "created_at", cursor, limit)
}

func (r *dynamoRepository) ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error) {
	return r.queryIndexPage(ctx, statusIndex, "status", &types.AttributeValueMemberS{Value: status}, "last_message_at", cursor, limit)
}

// queryIndexPage runs a cursor-paginated, newest-first query against a
// {pk, sk} GSI using the raw client — ExclusiveStartKey isn't exposed by
// database.Base (same pattern as audit.dynamoRepository.QueryByUser).
func (r *dynamoRepository) queryIndexPage(ctx context.Context, index, pkAttr string, pkVal types.AttributeValue, skAttr, cursor string, limit int32) ([]*Ticket, string, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(r.tableName),
		IndexName:              aws.String(index),
		KeyConditionExpression: aws.String("#pk = :pk"),
		ExpressionAttributeNames: map[string]string{
			"#pk": pkAttr,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": pkVal},
		ScanIndexForward:          aws.Bool(false),
		Limit:                     aws.Int32(limit),
	}
	if cursor != "" {
		sk, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		in.ExclusiveStartKey = map[string]types.AttributeValue{
			pkAttr:  pkVal,
			skAttr:  &types.AttributeValueMemberS{Value: sk},
		}
	}
	out, err := r.db.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("querying %s: %w", index, err)
	}
	tickets := make([]*Ticket, 0, len(out.Items))
	for _, item := range out.Items {
		var t Ticket
		if err := attributevalue.UnmarshalMap(item, &t); err != nil {
			return nil, "", fmt.Errorf("unmarshaling ticket: %w", err)
		}
		tickets = append(tickets, &t)
	}
	next := ""
	if lek := out.LastEvaluatedKey; lek != nil {
		if sk, ok := lek[skAttr].(*types.AttributeValueMemberS); ok {
			next = encodeCursor(sk.Value)
		}
	}
	return tickets, next, nil
}
```

- [ ] **Step 2: Add the shared cursor helpers**

Append to the same file (these mirror `audit.dynamoRepository`'s inline base64 encode/decode exactly — kept local rather than shared because `audit`'s version is unexported):

```go
func decodeCursor(cursor string) (string, error) {
	sk, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", fmt.Errorf("decoding cursor: %w", err)
	}
	return string(sk), nil
}

func encodeCursor(sk string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sk))
}
```

Add `"encoding/base64"` to the import block.

- [ ] **Step 3: Fix the `PutMessage` PK contract**

The `PutMessage` snippet above double-prefixes PK if the caller already passes `BuildPK(ticketID)`. Standardize: **callers pass the bare ticket ID as `m.PK`**, and `PutMessage` builds both `PK` and `SK`:

Replace the `PutMessage` body with:

```go
func (r *dynamoRepository) PutMessage(ctx context.Context, m *Message) error {
	ticketID := m.PK
	m.PK = BuildPK(ticketID)
	m.SK = BuildMessageSK(m.CreatedAt)
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	return r.table.PutItem(ctx, item)
}
```

(This is the actual, final version — the Step 1 body's `PutMessage` before this correction is superseded by this one.)

- [ ] **Step 4: Build check**

Run: `cd api && go build ./internal/domain/support/...`
Expected: exits 0.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/support/repository.go
git commit -m "feat: add support ticket DynamoDB repository"
```

---

## Task 7: In-memory repository mocks for tests

**Files:**
- Modify: `api/internal/handler/testhelpers_test.go`

**Interfaces:**
- Produces: an in-memory `support.Repository` implementation (`mockSupportRepo` or whatever naming convention the file already uses for other domains — check the file for the existing mock for, e.g., `session.Repository` and match its shape exactly) usable by both `support.Service` unit tests (Task 8) and `support` handler tests (Tasks 12–13).

- [ ] **Step 1: Read the existing mock pattern**

Open `api/internal/handler/testhelpers_test.go` and find the in-memory mock for an existing domain with a similar shape (item-collection + GSI lookups) — `session.Repository`'s mock is the closest analog (item collection + secondary lookup by hash). Match its style: same receiver naming, same way `newTestApp` wires repositories into handlers.

- [ ] **Step 2: Write the mock**

Add a mock implementing `support.Repository` from Task 6, backed by an in-memory `map[string]*support.Ticket` (keyed by ticket ID) and `map[string][]*support.Message` (keyed by ticket ID), plus a counter `int64`. Every method does a direct map operation — no pagination logic needed beyond returning all matching items with `next=""` (tests won't exercise multi-page cursors; if a later test needs that, extend then, not preemptively — YAGNI). Implement all 9 interface methods from Task 6 (`NextTicketNumber`, `CreateTicket`, `GetTicket`, `GetTicketByAnonToken`, `GetTicketByNumber`, `UpdateTicket`, `PutMessage`, `ListMessages`, `ListByUser`, `ListByStatus`), returning `support.ErrNotFound` where the real repository would.

- [ ] **Step 3: Wire it into `newTestApp`**

Find where `newTestApp` constructs and returns its bundle of repos/services (the function at the line found via `grep -n "func newTestApp"` earlier — `testhelpers_test.go:131`). Add the new mock repo, a `support.NewService(mockRepo, ...)` (once Task 8 defines its constructor signature — come back and fill in the real dependencies after Task 8 lands), and expose both on the `testApp` struct the same way existing services are exposed.

- [ ] **Step 4: Build check**

Run: `cd api && go build ./internal/handler/... ./internal/domain/support/...`
Expected: exits 0. (Full wiring in `newTestApp` won't compile until Task 8's `support.Service` constructor exists — if this task runs before Task 8, stop after the mock repo compiles standalone and revisit the `newTestApp` wiring once Task 8 is done.)

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/testhelpers_test.go
git commit -m "test: add in-memory support ticket repository mock"
```

---

## Task 8: Domain service — `support.Service`

**Files:**
- Create: `api/internal/domain/support/service.go`
- Create: `api/internal/domain/support/service_test.go`

**Interfaces:**
- Consumes: `support.Repository` (Task 6), `validate.Freetext`/`validate.FreetextRule` (Task 4).
- Produces:
  - `support.NewService(repo Repository) *Service`
  - `(*Service) CreateTicket(ctx, in CreateTicketInput) (*Ticket, error)`
  - `(*Service) GetTicketForCaller(ctx, id, userID, anonToken string) (*Ticket, []*Message, error)`
  - `(*Service) ReplyAsUser(ctx, id, userID, anonToken, body string) error`
  - `(*Service) ReplyAsAgent(ctx, id, agentUserID, body string) (*Message, *Ticket, error)`
  - `(*Service) SetStatus(ctx, id, status string) error`
  - `(*Service) SubmitNPS(ctx, id, userID, anonToken string, score int, message string) error`
  - `(*Service) ListMine(ctx, userID, cursor string, limit int32) ([]*Ticket, string, error)`
  - `(*Service) ListByStatus(ctx, status, cursor string, limit int32) ([]*Ticket, string, error)`
  - `support.ErrForbidden`, `support.ErrInvalidNPS`
  - `support.CreateTicketInput{UserID, AnonymousEmail, SubjectCategory, SubjectOther, Priority, Body string}`

This task does **not** wire e-mail sending — `CreateTicket`/`ReplyAsAgent`/`SetStatus` return what the handler needs (the ticket, the new message, whether a close-transition happened) and the handler (Tasks 12–13) calls the email client directly, exactly like `AuthHandler` calls `emailCli.SendVerificationEmail` today rather than `user.Service` doing it — keeps `internal/email` (an infra concern) out of `internal/domain` (business logic only, per `api/CLAUDE.md`'s layer table).

- [ ] **Step 1: Write the failing tests**

```go
package support

import (
	"context"
	"strings"
	"testing"
)

// newTestService is local to this file — an in-memory Repository good enough
// for service-level tests without pulling in the handler package's mock.
type memRepo struct {
	tickets  map[string]*Ticket
	messages map[string][]*Message
	counter  int64
}

func newMemRepo() *memRepo {
	return &memRepo{tickets: map[string]*Ticket{}, messages: map[string][]*Message{}}
}

func (m *memRepo) NextTicketNumber(ctx context.Context) (int64, error) {
	m.counter++
	return m.counter, nil
}
func (m *memRepo) CreateTicket(ctx context.Context, t *Ticket) error {
	t.SK = metaSK
	m.tickets[t.ID()] = t
	return nil
}
func (m *memRepo) GetTicket(ctx context.Context, id string) (*Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}
func (m *memRepo) GetTicketByAnonToken(ctx context.Context, token string) (*Ticket, error) {
	for _, t := range m.tickets {
		if t.AnonymousToken == token && token != "" {
			return t, nil
		}
	}
	return nil, ErrNotFound
}
func (m *memRepo) GetTicketByNumber(ctx context.Context, number int64) (*Ticket, error) {
	for _, t := range m.tickets {
		if t.TicketNumber == number {
			return t, nil
		}
	}
	return nil, ErrNotFound
}
func (m *memRepo) UpdateTicket(ctx context.Context, id string, updates map[string]any) error {
	t, ok := m.tickets[id]
	if !ok {
		return ErrNotFound
	}
	for k, v := range updates {
		switch k {
		case "status":
			t.Status = v.(string)
		case "updated_at":
			t.UpdatedAt = v.(string)
		case "closed_at":
			t.ClosedAt = v.(string)
		case "last_message_at":
			t.LastMessageAt = v.(string)
		case "last_ses_message_id":
			t.LastSESMessageID = v.(string)
		case "root_ses_message_id":
			t.RootSESMessageID = v.(string)
		case "nps_score":
			t.NPSScore = v.(int)
		case "nps_message":
			t.NPSMessage = v.(string)
		case "nps_requested_at":
			t.NPSRequestedAt = v.(string)
		}
	}
	return nil
}
func (m *memRepo) PutMessage(ctx context.Context, msg *Message) error {
	ticketID := msg.PK
	msg.PK = BuildPK(ticketID)
	msg.SK = BuildMessageSK(msg.CreatedAt)
	m.messages[ticketID] = append(m.messages[ticketID], msg)
	return nil
}
func (m *memRepo) ListMessages(ctx context.Context, ticketID string) ([]*Message, error) {
	return m.messages[ticketID], nil
}
func (m *memRepo) ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error) {
	var out []*Ticket
	for _, t := range m.tickets {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, "", nil
}
func (m *memRepo) ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error) {
	var out []*Ticket
	for _, t := range m.tickets {
		if t.Status == status {
			out = append(out, t)
		}
	}
	return out, "", nil
}

func TestCreateTicket_Authenticated(t *testing.T) {
	svc := NewService(newMemRepo())
	ticket, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow,
		Body: "Não consigo trocar minha senha pelo app novo.",
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if ticket.TicketNumber != 1 {
		t.Fatalf("got ticket number %d, want 1", ticket.TicketNumber)
	}
	if ticket.Status != StatusOpen {
		t.Fatalf("got status %q, want %q", ticket.Status, StatusOpen)
	}
	if ticket.IsAnonymous() {
		t.Fatalf("ticket should not be anonymous")
	}
}

func TestCreateTicket_AnonymousRequiresEmail(t *testing.T) {
	svc := NewService(newMemRepo())
	_, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Não consigo acessar minha conta de jeito nenhum.",
	})
	if err == nil {
		t.Fatal("expected error for anonymous ticket without email")
	}
}

func TestCreateTicket_RejectsJunkBody(t *testing.T) {
	svc := NewService(newMemRepo())
	_, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "..........",
	})
	if err == nil {
		t.Fatal("expected junk-pattern rejection")
	}
}

func TestCreateTicket_OtherCategoryRequiresSubjectOther(t *testing.T) {
	svc := NewService(newMemRepo())
	_, err := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryOther, Priority: PriorityLow, Body: "Preciso de ajuda com um assunto diferente.",
	})
	if err == nil {
		t.Fatal("expected error when category=other has no subject_other")
	}
}

func TestReplyAsAgent_SetsAnsweredAndThreadsMessageID(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Meu problema de login persiste desde ontem.",
	})

	msg, updated, err := svc.ReplyAsAgent(context.Background(), ticket.ID(), "agent-1", "Já verificamos, pode tentar novamente agora.")
	if err != nil {
		t.Fatalf("ReplyAsAgent: %v", err)
	}
	if msg.AuthorType != AuthorAgent {
		t.Fatalf("got author_type %q, want %q", msg.AuthorType, AuthorAgent)
	}
	if updated.Status != StatusAnswered {
		t.Fatalf("got status %q, want %q", updated.Status, StatusAnswered)
	}
}

func TestReplyAsUser_ReopensClosedTicket(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Preciso de suporte com minha conta principal.",
	})
	if err := svc.SetStatus(context.Background(), ticket.ID(), StatusClosed); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if err := svc.ReplyAsUser(context.Background(), ticket.ID(), "user-1", "", "Voltou a acontecer o mesmo problema de antes."); err != nil {
		t.Fatalf("ReplyAsUser: %v", err)
	}

	got, err := repo.GetTicket(context.Background(), ticket.ID())
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Status != StatusOpen {
		t.Fatalf("got status %q after reply on closed ticket, want %q (reopen)", got.Status, StatusOpen)
	}
}

func TestReplyAsUser_ForbiddenForWrongOwner(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Ticket original do usuário correto aqui.",
	})
	err := svc.ReplyAsUser(context.Background(), ticket.ID(), "user-2", "", "Tentando responder um ticket que não é meu.")
	if err != ErrForbidden {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

func TestSubmitNPS_RequiresMessageWhenScoreLow(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Preciso de ajuda urgente com pagamento.",
	})
	svc.SetStatus(context.Background(), ticket.ID(), StatusClosed)

	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 2, ""); err != ErrInvalidNPS {
		t.Fatalf("got %v, want ErrInvalidNPS for low score with no message", err)
	}
	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 2, "Demorou muito e não resolveu meu problema real."); err != nil {
		t.Fatalf("SubmitNPS with message: %v", err)
	}
}

func TestSubmitNPS_HighScoreMessageOptional(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Preciso de ajuda com meu cadastro atual.",
	})
	svc.SetStatus(context.Background(), ticket.ID(), StatusClosed)

	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 5, ""); err != nil {
		t.Fatalf("SubmitNPS: %v", err)
	}
}

func TestSubmitNPS_RejectsBeforeClosed(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Ticket ainda aberto, sem resposta do agente.",
	})
	if err := svc.SubmitNPS(context.Background(), ticket.ID(), "user-1", "", 5, ""); err != ErrInvalidNPS {
		t.Fatalf("got %v, want ErrInvalidNPS before ticket is closed", err)
	}
}

func TestReplyAsAgent_ThreadIDChaining(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	ticket, _ := svc.CreateTicket(context.Background(), CreateTicketInput{
		UserID: "user-1", SubjectCategory: CategoryAccount, Priority: PriorityLow, Body: "Problema recorrente com meu extrato de conta.",
	})
	if err := repo.UpdateTicket(context.Background(), ticket.ID(), map[string]any{"root_ses_message_id": "<root@ses>", "last_ses_message_id": "<root@ses>"}); err != nil {
		t.Fatalf("seeding root message id: %v", err)
	}

	_, updated1, err := svc.ReplyAsAgent(context.Background(), ticket.ID(), "agent-1", "Primeira resposta do agente.")
	if err != nil {
		t.Fatalf("first reply: %v", err)
	}
	if updated1.RootSESMessageID != "<root@ses>" {
		t.Fatalf("root message id should not change: got %q", updated1.RootSESMessageID)
	}

	_, updated2, err := svc.ReplyAsAgent(context.Background(), ticket.ID(), "agent-1", "Segunda resposta do agente.")
	if err != nil {
		t.Fatalf("second reply: %v", err)
	}
	if updated2.LastSESMessageID == updated1.RootSESMessageID {
		t.Fatalf("last_ses_message_id should advance past the root once a reply email is sent — handler sets it after SES returns a Message-ID; the service alone can't chain further without that")
	}
}
```

Note on the last test: `ReplyAsAgent` itself does not send e-mail (Task 13's handler does, then calls `UpdateTicket` with the new `last_ses_message_id`) — so `TestReplyAsAgent_ThreadIDChaining` only asserts the service doesn't clobber `root_ses_message_id`; delete the second assertion block (`updated2.LastSESMessageID == ...`) since the service has no SES message ID to advance to. Keep just the first assertion.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/domain/support/... -v`
Expected: FAIL (`Service`, `NewService`, `CreateTicketInput`, etc. undefined).

- [ ] **Step 3: Implement the service**

```go
package support

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"
	"gopkg.aoctech.app/account/api/internal/validate"
)

var (
	ErrForbidden   = errors.New("not authorized for this ticket")
	ErrInvalidNPS  = errors.New("invalid NPS submission")
	ErrInvalidInput = errors.New("invalid ticket input")
)

// Freetext bounds, per docs/specs/2026-08-22-support-tickets-design.md §3.5.
var (
	bodyRule         = validate.FreetextRule{Min: 15, Max: 4000}
	subjectOtherRule = validate.FreetextRule{Min: 3, Max: 120}
	npsMessageRule   = validate.FreetextRule{Min: 15, Max: 1000}
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

type CreateTicketInput struct {
	UserID          string // empty for anonymous submissions
	AnonymousEmail  string // required when UserID is empty
	SubjectCategory string
	SubjectOther    string
	Priority        string // defaults to PriorityLow when empty
	Body            string
}

func (s *Service) CreateTicket(ctx context.Context, in CreateTicketInput) (*Ticket, error) {
	if !contains(ValidCategories, in.SubjectCategory) {
		return nil, fmt.Errorf("%w: unknown subject_category %q", ErrInvalidInput, in.SubjectCategory)
	}
	if in.SubjectCategory == CategoryOther {
		cleaned, err := validate.Freetext(in.SubjectOther, subjectOtherRule)
		if err != nil {
			return nil, fmt.Errorf("%w: subject_other %v", ErrInvalidInput, err)
		}
		in.SubjectOther = cleaned
	} else {
		in.SubjectOther = ""
	}
	if in.Priority == "" {
		in.Priority = PriorityLow
	}
	if !contains(ValidPriorities, in.Priority) {
		return nil, fmt.Errorf("%w: unknown priority %q", ErrInvalidInput, in.Priority)
	}
	body, err := validate.Freetext(in.Body, bodyRule)
	if err != nil {
		return nil, fmt.Errorf("%w: body %v", ErrInvalidInput, err)
	}
	if in.UserID == "" && in.AnonymousEmail == "" {
		return nil, fmt.Errorf("%w: anonymous_email is required for anonymous tickets", ErrInvalidInput)
	}

	number, err := s.repo.NextTicketNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("allocating ticket number: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ticket := &Ticket{
		PK:              BuildPK(uuid.New().String()),
		TicketNumber:    number,
		UserID:          in.UserID,
		AnonymousEmail:  in.AnonymousEmail,
		SubjectCategory: in.SubjectCategory,
		SubjectOther:    in.SubjectOther,
		Priority:        in.Priority,
		Status:          StatusOpen,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastMessageAt:   now,
	}
	if ticket.IsAnonymous() {
		ticket.AnonymousToken = uuid.New().String()
	}
	if err := s.repo.CreateTicket(ctx, ticket); err != nil {
		return nil, fmt.Errorf("creating ticket: %w", err)
	}
	if err := s.putSystemMessage(ctx, ticket.ID(), "Ticket criado."); err != nil {
		return nil, err
	}
	if err := s.putMessage(ctx, ticket.ID(), AuthorUser, in.UserID, body); err != nil {
		return nil, err
	}
	return ticket, nil
}

// resolveAccess loads the ticket and checks the caller can see it: either
// userID matches Ticket.UserID, or anonToken matches Ticket.AnonymousToken.
// An empty userID with a matching anonToken is the anonymous-link path.
func (s *Service) resolveAccess(ctx context.Context, id, userID, anonToken string) (*Ticket, error) {
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	if userID != "" && ticket.UserID == userID {
		return ticket, nil
	}
	if anonToken != "" && ticket.AnonymousToken == anonToken {
		return ticket, nil
	}
	return nil, ErrForbidden
}

func (s *Service) GetTicketForCaller(ctx context.Context, id, userID, anonToken string) (*Ticket, []*Message, error) {
	ticket, err := s.resolveAccess(ctx, id, userID, anonToken)
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.repo.ListMessages(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("listing messages: %w", err)
	}
	return ticket, messages, nil
}

func (s *Service) ReplyAsUser(ctx context.Context, id, userID, anonToken, body string) error {
	ticket, err := s.resolveAccess(ctx, id, userID, anonToken)
	if err != nil {
		return err
	}
	cleaned, err := validate.Freetext(body, bodyRule)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.putMessage(ctx, id, AuthorUser, userID, cleaned); err != nil {
		return err
	}
	if ticket.Status == StatusClosed {
		if err := s.putSystemMessage(ctx, id, "Ticket reaberto pelo usuário."); err != nil {
			return err
		}
		return s.repo.UpdateTicket(ctx, id, map[string]any{
			"status":     StatusOpen,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
	return nil
}

// ReplyAsAgent appends the agent's message and sets status=answered. It does
// not send e-mail — the handler does that with the returned Message/Ticket
// and then persists the resulting SES Message-ID via UpdateTicket.
func (s *Service) ReplyAsAgent(ctx context.Context, id, agentUserID, body string) (*Message, *Ticket, error) {
	cleaned, err := validate.Freetext(body, bodyRule)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if _, err := s.repo.GetTicket(ctx, id); err != nil {
		return nil, nil, err
	}
	msg, err := s.putMessageReturning(ctx, id, AuthorAgent, agentUserID, cleaned)
	if err != nil {
		return nil, nil, err
	}
	if err := s.repo.UpdateTicket(ctx, id, map[string]any{
		"status":     StatusAnswered,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return nil, nil, fmt.Errorf("updating ticket status: %w", err)
	}
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return msg, ticket, nil
}

func (s *Service) SetStatus(ctx context.Context, id, status string) error {
	if !contains(ValidStatuses, status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidInput, status)
	}
	if _, err := s.repo.GetTicket(ctx, id); err != nil {
		return err
	}
	updates := map[string]any{"status": status, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	if status == StatusClosed {
		updates["closed_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.repo.UpdateTicket(ctx, id, updates); err != nil {
		return err
	}
	return s.putSystemMessage(ctx, id, fmt.Sprintf("Status alterado para %q.", status))
}

func (s *Service) SubmitNPS(ctx context.Context, id, userID, anonToken string, score int, message string) error {
	ticket, err := s.resolveAccess(ctx, id, userID, anonToken)
	if err != nil {
		return err
	}
	if ticket.Status != StatusClosed || ticket.NPSScore != 0 {
		return ErrInvalidNPS
	}
	if score < 1 || score > 5 {
		return ErrInvalidNPS
	}
	cleanMessage := ""
	if score <= 3 {
		cleaned, err := validate.Freetext(message, npsMessageRule)
		if err != nil {
			return ErrInvalidNPS
		}
		cleanMessage = cleaned
	} else if message != "" {
		cleaned, err := validate.Freetext(message, npsMessageRule)
		if err != nil {
			return ErrInvalidNPS
		}
		cleanMessage = cleaned
	}
	if err := s.repo.UpdateTicket(ctx, id, map[string]any{
		"nps_score":   score,
		"nps_message": cleanMessage,
	}); err != nil {
		return err
	}
	return s.putSystemMessage(ctx, id, "Avaliação NPS registrada.")
}

func (s *Service) ListMine(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error) {
	return s.repo.ListByUser(ctx, userID, cursor, limit)
}

func (s *Service) ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error) {
	return s.repo.ListByStatus(ctx, status, cursor, limit)
}

func (s *Service) putMessage(ctx context.Context, ticketID, authorType, authorID, body string) error {
	_, err := s.putMessageReturning(ctx, ticketID, authorType, authorID, body)
	return err
}

func (s *Service) putMessageReturning(ctx context.Context, ticketID, authorType, authorID, body string) (*Message, error) {
	msg := &Message{
		PK:         ticketID, // PutMessage builds the real PK/SK — see repository.go
		AuthorType: authorType,
		AuthorID:   authorID,
		Body:       body,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.repo.PutMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("putting message: %w", err)
	}
	if err := s.repo.UpdateTicket(ctx, ticketID, map[string]any{"last_message_at": msg.CreatedAt}); err != nil {
		return nil, fmt.Errorf("updating last_message_at: %w", err)
	}
	return msg, nil
}

func (s *Service) putSystemMessage(ctx context.Context, ticketID, body string) error {
	return s.putMessage(ctx, ticketID, AuthorSystem, "", body)
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/domain/support/... -v`
Expected: PASS for all tests (after deleting the invalid second assertion in `TestReplyAsAgent_ThreadIDChaining` per the note in Step 1).

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/support/service.go api/internal/domain/support/service_test.go
git commit -m "feat: add support ticket service (create/reply/status/NPS)"
```

---

## Task 9: Finish `newTestApp` wiring for support

**Files:**
- Modify: `api/internal/handler/testhelpers_test.go`

**Interfaces:**
- Consumes: `support.NewService(repo support.Repository) *support.Service` (Task 8), the mock repo from Task 7.

- [ ] **Step 1: Wire the service into `newTestApp`**

Return to the mock repo added in Task 7 and construct `support.NewService(mockRepo)`, storing it on the `testApp` struct (e.g. `supportSvc *support.Service`) the same way other services (`userSvc`, `sessionSvc`) are already exposed there.

- [ ] **Step 2: Build check**

Run: `cd api && go build ./... && go vet ./...`
Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add api/internal/handler/testhelpers_test.go
git commit -m "test: wire support service into handler test harness"
```

---

## Task 10: E-mail — raw MIME threading + ticket templates

**Files:**
- Modify: `api/internal/email/ses.go`
- Modify: `api/internal/email/ses_test.go`

**Interfaces:**
- Consumes: existing `emailLayout`, `ctaButton` helpers (`ses.go`).
- Produces: `(*Client) SendTicketConfirmationEmail(ctx, to string, ticketNumber int64, subjectLine, portalLink string) (string, error)`, `(*Client) SendTicketReplyEmail(ctx, to string, ticketNumber int64, subjectLine, agentBody, inReplyTo, references, portalLink string) (string, error)`, `(*Client) SendTicketNPSEmail(ctx, to string, ticketNumber int64, npsLink, inReplyTo, references string) (string, error)`. Each returns the new `Message-ID` (without angle brackets) for the caller to persist.

- [ ] **Step 1: Write the failing test**

```go
func TestSendTicketConfirmationEmail_ContainsPortalLink(t *testing.T) {
	cli, sent := newTestClientWithCapture(t) // see note below
	_, err := cli.SendTicketConfirmationEmail(context.Background(), "user@example.com", 42, "Conta / Login", "https://accounts.aoctech.app/support/ticket/abc?token=xyz")
	if err != nil {
		t.Fatalf("SendTicketConfirmationEmail: %v", err)
	}
	raw := sent.lastRaw()
	if !strings.Contains(raw, "https://accounts.aoctech.app/support/ticket/abc?token=xyz") {
		t.Fatal("expected portal link in raw message")
	}
	if !strings.Contains(raw, "Ticket #42") {
		t.Fatal("expected ticket number in subject/body")
	}
	if strings.Contains(raw, "In-Reply-To:") {
		t.Fatal("confirmation email must not have In-Reply-To — it's the thread root")
	}
}

func TestSendTicketReplyEmail_SetsThreadingHeaders(t *testing.T) {
	cli, sent := newTestClientWithCapture(t)
	_, err := cli.SendTicketReplyEmail(context.Background(), "user@example.com", 42, "Conta / Login", "Resolvido, pode conferir.", "<root@ses.amazonaws.com>", "<root@ses.amazonaws.com>", "https://accounts.aoctech.app/support/ticket/abc")
	if err != nil {
		t.Fatalf("SendTicketReplyEmail: %v", err)
	}
	raw := sent.lastRaw()
	if !strings.Contains(raw, "In-Reply-To: <root@ses.amazonaws.com>") {
		t.Fatal("expected In-Reply-To header")
	}
	if !strings.Contains(raw, "References: <root@ses.amazonaws.com>") {
		t.Fatal("expected References header")
	}
	if !strings.Contains(raw, "Subject: Re: [Ticket #42] Conta / Login") {
		t.Fatal("expected Re: subject with ticket number")
	}
}
```

Before writing these, **read `ses_test.go` first** to find how the existing tests fake the SES client (there must already be a seam — a `sesAPI` interface or similar — since `TestSend*` tests exist for the four current methods without hitting real AWS). Reuse that exact seam; `newTestClientWithCapture` above is illustrative naming, not a mandate — match whatever the file already calls it, and add a way to capture the raw MIME bytes passed to `SendEmail` if the existing fake doesn't already expose that (add a `lastInput *sesv2.SendEmailInput` capture field to the existing fake).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/email/... -v`
Expected: FAIL (`SendTicketConfirmationEmail` etc. undefined).

- [x] **Step 3: Implement**

Add to `ses.go`:

```go
// sendRaw builds a full RFC 5322 message and sends it via SESv2's raw
// content mode, which is the only way to set custom headers (In-Reply-To,
// References) — the existing send() helper uses Simple content and can't
// express threading. Returns the assigned Message-ID (no angle brackets).
func (c *Client) sendRaw(ctx context.Context, to, subject, htmlBody, inReplyTo, references string) (string, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", c.from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	if inReplyTo != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", inReplyTo)
	}
	if references != "" {
		fmt.Fprintf(&buf, "References: %s\r\n", references)
	}
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	buf.WriteString(htmlBody)

	in := sesv2.SendEmailInput{
		Content: &sestypes.EmailContent{
			Raw: &sestypes.RawMessage{Data: buf.Bytes()},
		},
	}
	out, err := c.ses.SendEmail(ctx, &in)
	if err != nil {
		return "", err
	}
	return aws.ToString(out.MessageId), nil
}

// SendTicketConfirmationEmail is the first e-mail for a new support ticket —
// no In-Reply-To, since it establishes the thread root. Returns the
// Message-ID the caller persists as both root_ses_message_id and
// last_ses_message_id.
func (c *Client) SendTicketConfirmationEmail(ctx context.Context, to string, ticketNumber int64, subjectLine, portalLink string) (string, error) {
	subject := fmt.Sprintf("[Ticket #%d] %s", ticketNumber, subjectLine)
	body := fmt.Sprintf(`<h2>Seu ticket #%d foi criado</h2>
  <p>Um agente em breve entrará em contato para responder sua dúvida.</p>
  <p><strong>Assunto:</strong> %s</p>
  %s`, ticketNumber, html.EscapeString(subjectLine), ctaButton("Acompanhar ticket", portalLink))
	return c.sendRaw(ctx, to, subject, body, "", "")
}

// SendTicketReplyEmail sends an agent's reply, threaded via inReplyTo/references
// onto the prior message in the ticket (root or previous reply).
func (c *Client) SendTicketReplyEmail(ctx context.Context, to string, ticketNumber int64, subjectLine, agentBody, inReplyTo, references, portalLink string) (string, error) {
	subject := fmt.Sprintf("Re: [Ticket #%d] %s", ticketNumber, subjectLine)
	body := fmt.Sprintf(`<p>%s</p>
  %s`, html.EscapeString(agentBody), ctaButton("Acompanhar ticket", portalLink))
	return c.sendRaw(ctx, to, subject, body, inReplyTo, references)
}

// SendTicketNPSEmail is sent once when a ticket closes, threaded the same way.
func (c *Client) SendTicketNPSEmail(ctx context.Context, to string, ticketNumber int64, npsLink, inReplyTo, references string) (string, error) {
	subject := fmt.Sprintf("Re: [Ticket #%d] Como foi seu atendimento?", ticketNumber)
	body := fmt.Sprintf(`<p>Seu ticket foi encerrado. Conta pra gente como foi o atendimento:</p>
  %s`, ctaButton("Avaliar atendimento", npsLink))
	return c.sendRaw(ctx, to, subject, body, inReplyTo, references)
}
```

Add imports: `"bytes"`, `sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"` (if not already imported — it is, per the existing `send()` method), and confirm `"github.com/aws/aws-sdk-go-v2/aws"` is imported (also already used by `send()`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/email/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/email/ses.go api/internal/email/ses_test.go
git commit -m "feat: add threaded ticket e-mails (confirmation/reply/NPS) via SES raw MIME"
```

---

## Task 11: Turnstile verification

**Files:** determined by the skill invocation below.

- [x] **Step 1: Invoke the `turnstile-spin` skill**

This is exactly the scenario the skill exists for. Invoke it with: "add Cloudflare Turnstile verification to the `POST /v1.0/support/tickets` endpoint in `ctech-account/api`. The widget goes on the new `/support` ticket-creation page in `ctech-account/ui`. Secret key is already wired as `config.TurnstileSecretKey` (env `TURNSTILE_SECRET_KEY`, SSM `/ctech-account/{env}/turnstile-secret-key` — see Task 2 of `docs/plans/2026-08-22-support-tickets.md`). Verification must produce an `apierror.ValidationFailed` (422) on failure, per this repo's RFC 7807 convention — not a generic 400."

Let the skill create/place the Cloudflare-side widget, the server-side `siteverify` package, and wire the site key into the UI's build-time env var list (`.github/workflows/frontend.yml`'s `build-env-*`, per README §5 — every external origin the SPA talks to must be a literal there for CSP `connect-src`).

> Implementation note (2026-08-22): the `turnstile-spin` skill was not installed in the workspace, so this scope was implemented directly with the same contract: `api/internal/turnstile`, a reusable `ui/src/components/turnstile-widget.tsx`, the shared Poker site-key variables, and Cloudflare's required `script-src`/`frame-src` CSP origins. Task 14 wires the verifier into the handler when that route is created.

- [ ] **Step 2: Build check**

Run: `cd api && go build ./... && cd ../ui && npm run build`
Expected: both exit 0.

- [ ] **Step 3: Commit**

Follow the skill's own commit (it manages its own commit boundary); if it doesn't commit, commit its output with:

```bash
git add -A
git commit -m "feat: add Turnstile verification to support ticket creation"
```

---

## Task 12: Middleware — `RequireSupportRole`

**Files:**
- Create: `api/internal/middleware/support.go`
- Create: `api/internal/middleware/support_test.go`

**Interfaces:**
- Consumes: `middleware.GetUserID(c fiber.Ctx) string` (existing), `*user.Service.GetByID`.
- Produces: `middleware.RequireSupportRole(userSvc *user.Service, minRole string) fiber.Handler`.

- [ ] **Step 1: Write the failing test**

Read the file adjacent to the middleware you're modeling this on (`internal/middleware/auth.go` or the account-scope middleware) for how existing middleware tests construct a minimal Fiber app + fake JWT/user service, then write:

```go
package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

func TestRequireSupportRole_RejectsBelowMinimum(t *testing.T) {
	app := fiber.New()
	repo := newFakeUserRepoWithRole(t, "user-1", user.SupportRoleAgent) // match whatever fake exists for user.Repository in this package's other tests
	svc := user.NewService(repo)
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals(LocalUserID, "user-1")
		return c.Next()
	}, RequireSupportRole(svc, user.SupportRoleAdmin), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("got status %d, want 403", resp.StatusCode)
	}
}

func TestRequireSupportRole_AllowsAtOrAboveMinimum(t *testing.T) {
	app := fiber.New()
	repo := newFakeUserRepoWithRole(t, "user-1", user.SupportRoleManager)
	svc := user.NewService(repo)
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals(LocalUserID, "user-1")
		return c.Next()
	}, RequireSupportRole(svc, user.SupportRoleAgent), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("GET", "/x", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
}
```

If no `user.Repository` fake already exists in `internal/middleware`'s test files, write a minimal one (single-user in-memory map is enough — same shape as the `memRepo` pattern from Task 8, just for `user.Repository`'s smaller interface).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/middleware/... -run TestRequireSupportRole -v`
Expected: FAIL (`RequireSupportRole` undefined).

- [ ] **Step 3: Implement**

```go
package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// supportRoleRank orders roles for a minimum-role check. Higher is more
// privileged. Unknown/empty roles rank below every named role.
var supportRoleRank = map[string]int{
	user.SupportRoleAgent:   1,
	user.SupportRoleManager: 2,
	user.SupportRoleAdmin:   3,
}

// RequireSupportRole runs after RequireAuth. It loads the caller's user
// record and checks support_role against minRole. This intentionally reads
// the DB rather than a JWT claim — see docs/specs/2026-08-22-support-tickets-design.md
// §4.3 for why: SignAccessToken's call sites are a Critical Area and plumbing
// a new claim through all of them isn't worth it for a handful of admin routes.
func RequireSupportRole(userSvc *user.Service, minRole string) fiber.Handler {
	minRank := supportRoleRank[minRole]
	return func(c fiber.Ctx) error {
		userID := GetUserID(c)
		u, err := userSvc.GetByID(c.Context(), userID)
		if err != nil {
			if errors.Is(err, user.ErrNotFound) {
				return apierror.Forbidden("Support role required.", c.Path()).Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		}
		if supportRoleRank[u.SupportRole] < minRank {
			return apierror.Forbidden("Support role required.", c.Path()).Send(c)
		}
		return c.Next()
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/middleware/... -run TestRequireSupportRole -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/middleware/support.go api/internal/middleware/support_test.go
git commit -m "feat: add RequireSupportRole middleware"
```

---

## Task 13: CLI — `cmd/supportrole`

**Files:**
- Create: `api/cmd/supportrole/main.go`

**Interfaces:**
- Consumes: `user.NewRepository`, `user.NewService`, `user.Service.SetSupportRole` (Task 5), `user.Service.GetByEmail`/`GetByID` (existing).

- [ ] **Step 1: Write the CLI**

Model this directly on `api/cmd/kyc/main.go` (same `TABLE_PREFIX`/`ENVIRONMENT` fallback, same `database.New` + repository wiring, same `switch os.Args[1]` dispatch):

```go
// Command supportrole grants or revokes the support-ticket role on a user
// account. There is no self-service or HTTP path to this — support_role is
// operator-provisioned only.
//
//	AWS_REGION=... TABLE_PREFIX=production go run ./cmd/supportrole set <user_id> -role agent|manager|admin
//	... go run ./cmd/supportrole revoke <user_id>
//	... go run ./cmd/supportrole list
//
// TABLE_PREFIX falls back to ENVIRONMENT (same rule as the API config).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.aoctech.app/account/api/internal/database"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	ctx := context.Background()
	region := os.Getenv("AWS_REGION")
	tablePrefix := os.Getenv("TABLE_PREFIX")
	if tablePrefix == "" {
		tablePrefix = os.Getenv("ENVIRONMENT")
	}
	tablePrefix = strings.TrimSuffix(tablePrefix, "_")
	if tablePrefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}

	db, err := database.New(ctx, region)
	if err != nil {
		log.Fatalf("dynamodb client: %v", err)
	}
	userRepo := userDomain.NewRepository(db, tablePrefix)
	userSvc := userDomain.NewService(userRepo)

	switch os.Args[1] {
	case "set":
		runSet(ctx, userSvc, os.Args[2:])
	case "revoke":
		runRevoke(ctx, userSvc, os.Args[2:])
	case "list":
		log.Fatal("list requires a table scan — not implemented; look users up individually via `set`/`show` by ID")
	default:
		usage()
		os.Exit(1)
	}
}

func runSet(ctx context.Context, svc *userDomain.Service, args []string) {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	role := fs.String("role", "", "agent|manager|admin")
	fs.Parse(args)
	if fs.NArg() < 1 || *role == "" {
		log.Fatal("usage: supportrole set <user_id> -role agent|manager|admin")
	}
	userID := fs.Arg(0)
	switch *role {
	case userDomain.SupportRoleAgent, userDomain.SupportRoleManager, userDomain.SupportRoleAdmin:
	default:
		log.Fatalf("invalid role %q", *role)
	}
	if _, err := svc.GetByID(ctx, userID); err != nil {
		if errors.Is(err, userDomain.ErrNotFound) {
			log.Fatalf("user %s not found", userID)
		}
		log.Fatalf("looking up user: %v", err)
	}
	if err := svc.SetSupportRole(ctx, userID, *role); err != nil {
		log.Fatalf("setting support role: %v", err)
	}
	fmt.Printf("user %s support_role=%s\n", userID, *role)
}

func runRevoke(ctx context.Context, svc *userDomain.Service, args []string) {
	if len(args) < 1 {
		log.Fatal("usage: supportrole revoke <user_id>")
	}
	userID := args[0]
	if err := svc.SetSupportRole(ctx, userID, ""); err != nil {
		log.Fatalf("revoking support role: %v", err)
	}
	fmt.Printf("user %s support_role revoked\n", userID)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: supportrole <set|revoke> <user_id> [flags]")
}
```

- [ ] **Step 2: Build check**

Run: `cd api && go build ./cmd/supportrole/...`
Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add api/cmd/supportrole/main.go
git commit -m "feat: add cmd/supportrole CLI for support_role provisioning"
```

---

## Task 14: Public + account handlers

**Files:**
- Create: `api/internal/handler/support.go`
- Create: `api/internal/handler/support_test.go`

**Interfaces:**
- Consumes: `support.Service` (Task 8), `email.Client` (Task 10), `parseBody[T]`/`middleware.GetUserID`/`middleware.OptionalAuth` (existing).
- Produces: `handler.NewSupportHandler(svc *support.Service, emailCli *email.Client, appURL string) *SupportHandler`, `(*SupportHandler) Register(public fiber.Router)`, `(*SupportHandler) RegisterAccount(account fiber.Router)`.

- [ ] **Step 1: Write the failing tests**

```go
package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateTicket_Authenticated(t *testing.T) {
	app := newTestApp(t)
	token := app.loginAndGetToken(t) // reuse whatever helper other _test.go files use to get a bearer token

	body, _ := json.Marshal(map[string]any{
		"subject_category": "account",
		"body":             "Não consigo acessar minha conta desde ontem à noite.",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1.0/support/tickets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.fiber.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("got status %d", resp.StatusCode)
	}
}

func TestCreateTicket_AnonymousRequiresTurnstileAndEmail(t *testing.T) {
	app := newTestApp(t)
	body, _ := json.Marshal(map[string]any{
		"subject_category": "account",
		"body":             "Não consigo acessar minha conta desde ontem à noite.",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1.0/support/tickets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.fiber.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422 (missing email)", resp.StatusCode)
	}
}
```

(Match whatever existing `_test.go` file's exact helper names for `app.fiber`/`loginAndGetToken` — read one existing handler test file first, e.g. `profile_test.go`, and mirror its request-building style exactly rather than guessing.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/handler/... -run TestCreateTicket -v`
Expected: FAIL (route not registered / 404).

- [ ] **Step 3: Implement the handler**

```go
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/support"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

type SupportHandler struct {
	svc      *support.Service
	emailCli *email.Client
	appURL   string
}

func NewSupportHandler(svc *support.Service, emailCli *email.Client, appURL string) *SupportHandler {
	return &SupportHandler{svc: svc, emailCli: emailCli, appURL: appURL}
}

// Register mounts the public (OptionalAuth) routes.
func (h *SupportHandler) Register(public fiber.Router) {
	public.Post("/support/tickets", h.create)
	public.Get("/support/tickets/:id", h.get)
	public.Post("/support/tickets/:id/reply", h.reply)
	public.Post("/support/tickets/:id/nps", h.nps)
}

// RegisterAccount mounts the authenticated-only "my tickets" route onto an
// already-`RequireAuth`-gated group (the same `account` group every other
// account/* handler uses).
func (h *SupportHandler) RegisterAccount(account fiber.Router) {
	account.Get("/support/tickets", h.listMine)
}

type createTicketRequest struct {
	SubjectCategory string `json:"subject_category" validate:"required,oneof=account kyc wallet dfe billing poker other"`
	SubjectOther    string `json:"subject_other"     validate:"omitempty,max=200"`
	Priority        string `json:"priority"          validate:"omitempty,oneof=low medium high urgent critical"`
	Body            string `json:"body"              validate:"required,max=4200"`
	Email           string `json:"email"             validate:"omitempty,email"`
	TurnstileToken  string `json:"turnstile_token"   validate:"required"`
}

func (h *SupportHandler) create(c fiber.Ctx) error {
	var req createTicketRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	// Turnstile verification is wired by Task 11 (turnstile-spin skill) —
	// call whatever verify function that task produces here, e.g.:
	//   if err := turnstile.Verify(c.Context(), h.turnstileSecret, req.TurnstileToken, clientIP(c)); err != nil {
	//       return apierror.ValidationFailed("Captcha verification failed.", c.Path()).Send(c)
	//   }

	userID := middleware.GetUserID(c)
	if userID == "" && req.Email == "" {
		return apierror.ValidationFailed("email is required for anonymous submissions", c.Path()).Send(c)
	}

	ticket, err := h.svc.CreateTicket(c.Context(), support.CreateTicketInput{
		UserID:          userID,
		AnonymousEmail:  req.Email,
		SubjectCategory: req.SubjectCategory,
		SubjectOther:    req.SubjectOther,
		Priority:        req.Priority,
		Body:            req.Body,
	})
	if err != nil {
		if errors.Is(err, support.ErrInvalidInput) {
			return apierror.ValidationFailed(err.Error(), c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	if h.emailCli != nil {
		to := req.Email
		if userID != "" {
			// TODO(wiring): the handler needs the caller's account e-mail here —
			// thread a user.Service lookup through NewSupportHandler (same
			// dependency ProfileHandler already takes) and use u.Email when
			// userID != "".
		}
		subjectLine := req.SubjectCategory
		if req.SubjectCategory == support.CategoryOther {
			subjectLine = req.SubjectOther
		}
		portalLink := h.portalLink(ticket)
		messageID, sendErr := h.emailCli.SendTicketConfirmationEmail(c.Context(), to, ticket.TicketNumber, subjectLine, portalLink)
		if sendErr == nil {
			_ = h.svc // ticket update below reuses the service's repo indirectly via SetStatus-style call —
			// simplest is a small Service method; add `(*Service) RecordRootMessageID(ctx, id, messageID string) error`
			// back in Task 8 if this inline TODO isn't resolved before implementing — do not ship this TODO as-is.
		}
	}

	resp := fiber.Map{"ticket_id": ticket.ID(), "ticket_number": ticket.TicketNumber}
	if ticket.IsAnonymous() {
		resp["anonymous_token"] = ticket.AnonymousToken
	}
	return c.JSON(resp)
}
```

**Stop here and resolve the two inline TODOs before treating this task as done** — they are flagged, not placeholders left in: (1) fetch the account e-mail for authenticated submitters (thread a `*user.Service` into `NewSupportHandler`, same as `ProfileHandler` already does, and call `userSvc.GetByID` when `userID != ""`), (2) add a `(*support.Service) RecordRootMessageID(ctx, id, messageID string) error` to Task 8's service (one-line `UpdateTicket` call setting both `root_ses_message_id` and `last_ses_message_id`) and call it here after a successful send. Update Task 8's file and its tests when you do.

Continue the handler with `get`, `reply`, `nps`, `listMine`:

```go
func (h *SupportHandler) resolveCaller(c fiber.Ctx) (userID, anonToken string) {
	return middleware.GetUserID(c), c.Query("token")
}

func (h *SupportHandler) get(c fiber.Ctx) error {
	userID, anonToken := h.resolveCaller(c)
	ticket, messages, err := h.svc.GetTicketForCaller(c.Context(), c.Params("id"), userID, anonToken)
	if err != nil {
		if errors.Is(err, support.ErrNotFound) {
			return apierror.NotFound("Ticket", c.Path()).Send(c)
		}
		if errors.Is(err, support.ErrForbidden) {
			return apierror.Forbidden("Not authorized for this ticket.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}
	return c.JSON(fiber.Map{"ticket": ticket, "messages": messages})
}

type replyRequest struct {
	Body string `json:"body" validate:"required,max=4200"`
}

func (h *SupportHandler) reply(c fiber.Ctx) error {
	var req replyRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	userID, anonToken := h.resolveCaller(c)
	err := h.svc.ReplyAsUser(c.Context(), c.Params("id"), userID, anonToken, req.Body)
	if err != nil {
		switch {
		case errors.Is(err, support.ErrNotFound):
			return apierror.NotFound("Ticket", c.Path()).Send(c)
		case errors.Is(err, support.ErrForbidden):
			return apierror.Forbidden("Not authorized for this ticket.", c.Path()).Send(c)
		case errors.Is(err, support.ErrInvalidInput):
			return apierror.ValidationFailed(err.Error(), c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}
	return c.Status(fiber.StatusNoContent).Send(nil)
}

type npsRequest struct {
	Score   int    `json:"score"   validate:"required,gte=1,lte=5"`
	Message string `json:"message" validate:"omitempty,max=1200"`
}

func (h *SupportHandler) nps(c fiber.Ctx) error {
	var req npsRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	userID, anonToken := h.resolveCaller(c)
	err := h.svc.SubmitNPS(c.Context(), c.Params("id"), userID, anonToken, req.Score, req.Message)
	if err != nil {
		switch {
		case errors.Is(err, support.ErrNotFound):
			return apierror.NotFound("Ticket", c.Path()).Send(c)
		case errors.Is(err, support.ErrForbidden):
			return apierror.Forbidden("Not authorized for this ticket.", c.Path()).Send(c)
		case errors.Is(err, support.ErrInvalidNPS):
			return apierror.ValidationFailed("Invalid NPS submission — a message is required for scores of 3 or below.", c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}
	return c.Status(fiber.StatusNoContent).Send(nil)
}

func (h *SupportHandler) listMine(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	limit := int32(20)
	tickets, next, err := h.svc.ListMine(c.Context(), userID, c.Query("cursor"), limit)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	return c.JSON(fiber.Map{"tickets": tickets, "next_cursor": next})
}

// portalLink builds the user-facing thread URL, including the anonymous
// token when the ticket has no owning user.
func (h *SupportHandler) portalLink(t *support.Ticket) string {
	link := h.appURL + "/support/ticket/" + t.ID()
	if t.IsAnonymous() {
		link += "?token=" + t.AnonymousToken
	}
	return link
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/handler/... -run TestCreateTicket -v`
Expected: PASS once wired into `main.go`/`newTestApp` routing (Task 16) — if routes aren't registered yet in the test app, this will still 404; finish Task 16's test-app route registration before calling this task done.

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/support.go api/internal/handler/support_test.go
git commit -m "feat: add public/account support ticket handlers"
```

---

## Task 15: Admin handlers

**Files:**
- Create: `api/internal/handler/support_admin.go`
- Modify: `api/internal/handler/support_test.go`

**Interfaces:**
- Consumes: `support.Service`, `email.Client`, `middleware.RequireSupportRole` (mounted at registration time, not inside the handler).
- Produces: `handler.NewSupportAdminHandler(svc *support.Service, emailCli *email.Client, appURL string) *SupportAdminHandler`, `(*SupportAdminHandler) Register(admin fiber.Router)`.

- [ ] **Step 1: Write the failing test**

```go
func TestAdminReply_RequiresSupportRole(t *testing.T) {
	app := newTestApp(t)
	regularUserToken := app.loginAndGetToken(t)

	body, _ := json.Marshal(map[string]any{"body": "Já verificamos e o problema foi resolvido."})
	req := httptest.NewRequest(http.MethodPost, "/v1.0/admin/support/tickets/does-not-matter/reply", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+regularUserToken)
	resp, err := app.fiber.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/handler/... -run TestAdminReply -v`
Expected: FAIL (404 — route not registered).

- [ ] **Step 3: Implement**

```go
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/support"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

type SupportAdminHandler struct {
	svc      *support.Service
	emailCli *email.Client
	appURL   string
}

func NewSupportAdminHandler(svc *support.Service, emailCli *email.Client, appURL string) *SupportAdminHandler {
	return &SupportAdminHandler{svc: svc, emailCli: emailCli, appURL: appURL}
}

// Register mounts admin routes. Callers pass a group already carrying
// RequireAuth + RequireSupportRole(user.SupportRoleAgent) — same convention
// as ProfileHandler.Register taking a pre-gated `account` group.
func (h *SupportAdminHandler) Register(admin fiber.Router) {
	admin.Get("/support/tickets", h.list)
	admin.Get("/support/tickets/:id", h.get)
	admin.Post("/support/tickets/:id/reply", h.reply)
	admin.Put("/support/tickets/:id/status", h.setStatus)
}

func (h *SupportAdminHandler) list(c fiber.Ctx) error {
	status := c.Query("status", support.StatusOpen)
	tickets, next, err := h.svc.ListByStatus(c.Context(), status, c.Query("cursor"), 50)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	return c.JSON(fiber.Map{"tickets": tickets, "next_cursor": next})
}

func (h *SupportAdminHandler) get(c fiber.Ctx) error {
	ticket, messages, err := h.svc.GetTicketForCaller(c.Context(), c.Params("id"), "", "")
	// Admins bypass the owner/anon-token check entirely: GetTicketForCaller
	// as written in Task 8 requires one of userID/anonToken to match. Add a
	// dedicated `(*Service) GetTicketAdmin(ctx, id string) (*Ticket, []*Message, error)`
	// to Task 8 (skips resolveAccess) and call that here instead of the
	// caller-scoped method — do this before shipping this handler.
	if err != nil {
		if errors.Is(err, support.ErrNotFound) {
			return apierror.NotFound("Ticket", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}
	return c.JSON(fiber.Map{"ticket": ticket, "messages": messages})
}

type adminReplyRequest struct {
	Body string `json:"body" validate:"required,max=4200"`
}

func (h *SupportAdminHandler) reply(c fiber.Ctx) error {
	var req adminReplyRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	agentUserID := middleware.GetUserID(c)
	msg, ticket, err := h.svc.ReplyAsAgent(c.Context(), c.Params("id"), agentUserID, req.Body)
	if err != nil {
		switch {
		case errors.Is(err, support.ErrNotFound):
			return apierror.NotFound("Ticket", c.Path()).Send(c)
		case errors.Is(err, support.ErrInvalidInput):
			return apierror.ValidationFailed(err.Error(), c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}

	if h.emailCli != nil {
		to := ticket.AnonymousEmail
		// TODO(wiring): same as Task 14's create() — resolve the account
		// e-mail via user.Service when ticket.UserID != "".
		subjectLine := ticket.SubjectCategory
		if ticket.SubjectCategory == support.CategoryOther {
			subjectLine = ticket.SubjectOther
		}
		portalLink := h.appURL + "/support/ticket/" + ticket.ID()
		if ticket.IsAnonymous() {
			portalLink += "?token=" + ticket.AnonymousToken
		}
		references := ticket.RootSESMessageID
		if ticket.LastSESMessageID != "" && ticket.LastSESMessageID != ticket.RootSESMessageID {
			references = ticket.RootSESMessageID + " " + ticket.LastSESMessageID
		}
		messageID, sendErr := h.emailCli.SendTicketReplyEmail(c.Context(), to, ticket.TicketNumber, subjectLine, msg.Body, ticket.LastSESMessageID, references, portalLink)
		if sendErr == nil {
			// Same missing service method as Task 14's TODO —
			// (*support.Service).RecordRootMessageID doesn't fit here (that's
			// root-only); add a sibling `(*Service) RecordReplyMessageID(ctx, id, messageID string) error`
			// that sets only last_ses_message_id, and call it here.
			_ = messageID
		}
	}

	return c.JSON(fiber.Map{"message": msg, "ticket": ticket})
}

type setStatusRequest struct {
	Status string `json:"status" validate:"required,oneof=open answered closed"`
}

func (h *SupportAdminHandler) setStatus(c fiber.Ctx) error {
	var req setStatusRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if err := h.svc.SetStatus(c.Context(), c.Params("id"), req.Status); err != nil {
		if errors.Is(err, support.ErrNotFound) {
			return apierror.NotFound("Ticket", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}
	// TODO(wiring): when req.Status == support.StatusClosed, send the NPS
	// e-mail via h.emailCli.SendTicketNPSEmail (Task 10), guarded by
	// ticket.NPSRequestedAt being empty (fetch the ticket first via
	// h.svc.GetTicketAdmin, check the guard, then call SetStatus, then send
	// and persist nps_requested_at through a small service method) — do this
	// before shipping this handler.
	return c.Status(fiber.StatusNoContent).Send(nil)
}
```

**Before treating this task as done**, resolve all three inline TODOs (admin `get` needs an unscoped service method, reply needs `RecordReplyMessageID`, close needs the NPS-send-once wiring) by extending Task 8's `support.Service` with:

```go
// GetTicketAdmin skips the owner/anon-token check resolveAccess enforces —
// only RequireSupportRole gates admin routes.
func (s *Service) GetTicketAdmin(ctx context.Context, id string) (*Ticket, []*Message, error) {
	ticket, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.repo.ListMessages(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("listing messages: %w", err)
	}
	return ticket, messages, nil
}

func (s *Service) RecordRootMessageID(ctx context.Context, id, messageID string) error {
	wrapped := "<" + messageID + ">"
	return s.repo.UpdateTicket(ctx, id, map[string]any{
		"root_ses_message_id": wrapped,
		"last_ses_message_id": wrapped,
	})
}

func (s *Service) RecordReplyMessageID(ctx context.Context, id, messageID string) error {
	return s.repo.UpdateTicket(ctx, id, map[string]any{"last_ses_message_id": "<" + messageID + ">"})
}

// MarkNPSRequested guards SendTicketNPSEmail against double-sends.
func (s *Service) MarkNPSRequested(ctx context.Context, id string) error {
	return s.repo.UpdateTicket(ctx, id, map[string]any{"nps_requested_at": time.Now().UTC().Format(time.RFC3339)})
}
```

Add corresponding unit tests to `service_test.go` (one per new method, following the existing table/style in that file), then update `support.go`'s `create()` (Task 14) and `support_admin.go`'s `reply()`/`setStatus()` above to call them instead of the TODOs. Use `admin.get` → `h.svc.GetTicketAdmin(...)` instead of `GetTicketForCaller(...,"","")`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/domain/support/... ./internal/handler/... -v`
Expected: PASS, including `TestAdminReply_RequiresSupportRole` once routes are registered in `main.go`/`newTestApp` (Task 16).

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/support_admin.go api/internal/handler/support_test.go api/internal/domain/support/service.go api/internal/domain/support/service_test.go
git commit -m "feat: add admin support ticket handlers (list/get/reply/status)"
```

---

## Task 16: Wire everything into `cmd/api/main.go` and the test harness routes

**Files:**
- Modify: `api/cmd/api/main.go`
- Modify: `api/internal/handler/testhelpers_test.go`

**Interfaces:**
- Consumes: everything from Tasks 6, 8, 10, 12, 14, 15.

- [ ] **Step 1: Wire production `main.go`**

Add, near the existing repository/service/handler construction blocks (matching the existing style exactly — see the excerpt in this plan's research, `cmd/api/main.go`):

```go
supportRepo := supportDomain.NewRepository(db, cfg.TablePrefix)
supportSvc := supportDomain.NewService(supportRepo)
```

(import `supportDomain "gopkg.aoctech.app/account/api/internal/domain/support"`)

```go
supportH := handler.NewSupportHandler(supportSvc, emailCli, cfg.AppURL)
supportAdminH := handler.NewSupportAdminHandler(supportSvc, emailCli, cfg.AppURL)
```

Route registration, after the existing `v1.Get("/userinfo", ...)` line and before the `account := v1.Group(...)` block (public + `OptionalAuth`):

```go
supportH.Register(v1.Group("/support", middleware.OptionalAuth(jwtSvc)))
```

Inside the existing `account := v1.Group("/account", middleware.RequireAuth(jwtSvc), perUserLimiter)` block, alongside the other `.Register(account, ...)` calls:

```go
supportH.RegisterAccount(account)
```

After the `account` block, add a new admin group:

```go
supportAdmin := v1.Group("/admin", middleware.RequireAuth(jwtSvc), middleware.RequireSupportRole(userSvc, userDomain.SupportRoleAgent))
supportAdminH.Register(supportAdmin)
```

- [ ] **Step 2: Wire the test harness routes**

In `testhelpers_test.go`, register the same routes on the test Fiber app that `main.go` registers, using the mock-backed `supportSvc` from Task 9 and whatever fake/nil `email.Client` the other handler tests already use for handlers that send e-mail (check how `authH`/`socialH` are registered in the test app for the convention — likely a `nil` `*email.Client`, which every `Send*` call already guards against with `if h.emailCli != nil` in this plan's handlers).

- [ ] **Step 3: Full build + test**

Run: `cd api && go build ./... && go vet ./... && go test ./...`
Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add api/cmd/api/main.go api/internal/handler/testhelpers_test.go
git commit -m "feat: wire support ticket routes into the API"
```

---

## Task 17: README + PLAN.md updates

**Files:**
- Modify: `README.md`
- Modify: `PLAN.md`

- [ ] **Step 1: Update `README.md`**

Add rows to the API table (§API) for every route from Tasks 14–15 (`POST /v1.0/support/tickets`, `GET/POST .../reply`, `POST .../nps`, `GET /v1.0/account/support/tickets`, `GET/POST/PUT /v1.0/admin/support/tickets*`), following the exact column format of the existing table. Add `TURNSTILE_SECRET_KEY` to the Configuration table. Add `account_support_tickets` to the DynamoDB table count/description (currently "Eight tables" → "Nine tables"). Add a new subsection under §API (mirroring the existing "GeoIP + new-device login notification" subsection style) documenting: the ticket lifecycle (open/answered/closed + reopen-on-reply), the `support_role` field and `cmd/supportrole`, and the e-mail threading behavior + its known limitation (inbound replies aren't ingested — see spec §2).

- [ ] **Step 2: Update `PLAN.md`**

Add a new `## Sprint 5 — Support Tickets` section listing every file created/modified across Tasks 1–16 as checked-off `[x]` items, following the exact style of the existing Sprint sections.

- [ ] **Step 3: Commit**

```bash
git add README.md PLAN.md
git commit -m "docs: document support ticket endpoints, config, and lifecycle"
```

---

## Task 18: Frontend — public ticket form (`/support`)

**Files:**
- Create: `ui/src/app/support/page.tsx`
- Create: whatever BFF Route Handler or Server Action the form submit uses (follow the existing pattern from `ui/src/app/forgot-password/page.tsx` + its submit handler — read that file first for the exact convention: Server Action vs Route Handler, and how it calls the API with `NEXT_PUBLIC_API_URL`).
- Modify: `ui/src/locales/en.json`, `ui/src/locales/pt-BR.json` (new `support` namespace).

**Interfaces:**
- Consumes: `POST /v1.0/support/tickets` (Task 14), the Turnstile widget from Task 11.
- Produces: a working `/support` page.

- [ ] **Step 1: Invoke `impeccable` for this screen**

Per the Global Constraints, this screen's visual design goes through the `impeccable` skill rather than freehand markup. Invoke it with the functional contract this page must satisfy (this is the "real content an engineer needs" for this task — the skill fills in layout/visual treatment):

- Fields: `subject_category` (select: Conta/Login, KYC/Verificação, Wallet, DF-e, Billing, Poker, Outros — values `account|kyc|wallet|dfe|billing|poker|other`), `subject_other` (text input, shown only when category is `other`, 3–120 chars), `body` (textarea, 15–4000 chars, live character counter), `priority` (select: Baixa/Média/Alta/Urgente/Crítica, default Baixa, values `low|medium|high|urgent|critical`), Turnstile widget, submit button.
- Behavior: if a session exists (reuse the same session-check the account layout already uses), hide the `email` field and omit it from the payload; otherwise show a required `email` input.
- On submit: `POST /v1.0/support/tickets` with `{subject_category, subject_other?, body, priority, email?, turnstile_token}`. On success, redirect to `/support/ticket/[id]` (with `?token=` appended when the response includes `anonymous_token`). On 422, show the server's validation detail inline near the offending field where possible, generic otherwise.
- Empty/error states: a clear inline error banner on network failure or non-422 error responses.

- [ ] **Step 2: Add i18n strings**

Add a `support` namespace to both locale files following the existing `forgotPassword` namespace's key structure (labels, placeholders, button text, validation messages) in en + pt-BR.

- [ ] **Step 3: Manual verification**

Run: `cd ui && npm run dev`, open `/support` in a browser, submit once logged out (with a valid Turnstile pass token in dev mode, or Cloudflare's test sitekey) and once logged in; confirm both create a ticket via the running API and redirect correctly.

- [ ] **Step 4: Build check**

Run: `cd ui && npm run build`
Expected: exits 0 (static export succeeds).

- [ ] **Step 5: Commit**

```bash
git add ui/src/app/support ui/src/locales/en.json ui/src/locales/pt-BR.json
git commit -m "feat: add public support ticket form"
```

---

## Task 19: Frontend — ticket thread page

**Files:**
- Create: `ui/src/app/support/ticket/[id]/page.tsx`

**Interfaces:**
- Consumes: `GET /v1.0/support/tickets/:id[?token=]`, `POST /v1.0/support/tickets/:id/reply`, `POST /v1.0/support/tickets/:id/nps` (Task 14).

- [ ] **Step 1: Invoke `impeccable` for this screen**

Functional contract: renders the ticket header (number, category, priority, status badge) and the message timeline in chronological order, visually distinguishing `user`/`agent`/`system` authors. A reply textarea + submit button at the bottom, hidden when `status=closed` **and** the ticket already has an `nps_score` (fully closed-out); when `status=closed` and no NPS yet, replace the reply box with the NPS prompt (1–5 score picker + conditional message textarea, required and validated client-side as non-empty when the picked score is ≤3, mirroring the server's rule). Reads `?token=` from the URL for anonymous access (kept only in memory/URL for the page's lifetime, never persisted to a cookie or localStorage) or relies on the session when logged in.

- [ ] **Step 2: Manual verification**

Run: `cd ui && npm run dev`; walk through: create a ticket anonymously → open the returned link → reply → have an admin (Task 21) reply back → close it (Task 21) → submit NPS.

- [ ] **Step 3: Build check**

Run: `cd ui && npm run build`

- [ ] **Step 4: Commit**

```bash
git add ui/src/app/support/ticket
git commit -m "feat: add support ticket thread page"
```

---

## Task 20: Frontend — "Meus tickets"

**Files:**
- Create: `ui/src/app/account/support/page.tsx`

**Interfaces:**
- Consumes: `GET /v1.0/account/support/tickets` (Task 14), auth-gated the same way every other `ui/src/app/account/*` page already is (reuse `ui/src/app/account/layout.tsx`'s guard — no new auth code).

- [ ] **Step 1: Invoke `impeccable` for this screen**

Functional contract: a list of the caller's tickets (number, category label, priority badge, status badge, last activity timestamp), each linking to `/support/ticket/[id]`, following the existing `/account/sessions` or `/account/activity` list page's structural conventions (read one of those first) for empty state ("Nenhum ticket ainda" + a CTA to `/support`) and pagination (cursor "load more", matching whatever `/account/activity` already does for its own cursor).

- [ ] **Step 2: Build check**

Run: `cd ui && npm run build`

- [ ] **Step 3: Commit**

```bash
git add ui/src/app/account/support
git commit -m "feat: add \"meus tickets\" account page"
```

---

## Task 21: Frontend — admin queue + thread

**Files:**
- Create: `ui/src/app/admin/support/page.tsx`
- Create: `ui/src/app/admin/support/[id]/page.tsx`

**Interfaces:**
- Consumes: `GET /v1.0/admin/support/tickets[?status=&priority=&category=&cursor=]`, `GET .../:id`, `POST .../:id/reply`, `PUT .../:id/status` (Task 15). Gate: `GET /v1.0/account/profile`'s `support_role` field (add this field to the profile response first — see Step 0 below).

- [ ] **Step 0: Add `support_role` to the profile response**

In `api/internal/handler/profile.go`'s `get` handler (the `fiber.Map{...}` this plan read earlier), add:

```go
		"support_role": u.SupportRole,
```

Update `api/internal/handler/profile_test.go` if it asserts on the full response shape (add the new key to any exact-match assertions). This is a small, self-contained addition — do it as the first step of this task rather than a separate task, since the admin UI can't gate without it and nothing else in this plan needed it yet.

Run: `cd api && go test ./internal/handler/... -run TestProfile -v` to confirm nothing broke, then:

```bash
git add api/internal/handler/profile.go api/internal/handler/profile_test.go
git commit -m "feat: expose support_role on GET /account/profile"
```

- [ ] **Step 1: Invoke `impeccable` for both screens**

Functional contract for `/admin/support` (queue): gated — if `support_role` is empty, redirect to `/account` (same redirect-on-missing-auth convention `account/layout.tsx` uses for missing sessions). Filters for status/priority/category, a table/list of tickets sorted by `last_message_at` descending, each row linking to `/admin/support/[id]`. No claim/assignment UI (v1 is an open pool per spec §2).

Functional contract for `/admin/support/[id]`: same gate. Full thread view (reuse the same timeline rendering approach as Task 19's `/support/ticket/[id]`, since the data shape is identical) plus a reply textarea (posts to the admin reply endpoint) and a status `<select>` (open/answered/closed) that calls the status endpoint on change.

- [ ] **Step 2: Manual verification**

Run: `cd api && go run ./cmd/supportrole set <your-dev-user-id> -role admin` against a local/dev table, log in as that user, confirm `/admin/support` is reachable and a regular user is redirected away.

- [ ] **Step 3: Build check**

Run: `cd ui && npm run build`

- [ ] **Step 4: Commit**

```bash
git add ui/src/app/admin
git commit -m "feat: add admin support ticket queue and thread pages"
```

---

## Task 22: Final full-repo verification

**Files:** none (verification only).

- [ ] **Step 1: Backend**

Run: `cd api && go build ./... && go vet ./... && go test ./...`
Expected: all pass.

- [ ] **Step 2: Frontend**

Run: `cd ui && npm run build`
Expected: exits 0.

- [ ] **Step 3: CDK**

Run: `cd cdk && npx cdk synth --all > /dev/null`
Expected: exits 0.

- [ ] **Step 4: Manual end-to-end smoke test**

Walk the full path once against a local/dev stack: submit `/support` anonymously → confirmation e-mail (check SES sandbox/dev logs) → reply as the anonymous user via the portal link → reply as an admin via `/admin/support/[id]` → confirm the outbound e-mail's `In-Reply-To`/`References` headers thread correctly in a real mail client → close the ticket → submit NPS with a low score and confirm the message-required rule enforces client- and server-side.

- [ ] **Step 5: Final commit (if anything was fixed during verification)**

```bash
git add -A
git commit -m "fix: address issues found in end-to-end support ticket verification"
```
