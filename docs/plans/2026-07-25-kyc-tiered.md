# Tiered KYC (Basic + Enhanced) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:
> executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split KYC into Basic (CPF/name/birthdate/phone, SMS-verified via AWS SNS) and Enhanced (3 documents,
human-reviewed) levels, reusing ctech-wallet's existing `""|"basic"|"verified"` JWT claim contract, and add an
informational-only risk-scoring hook.

**Architecture:** `api/internal/domain/kyc` gets a rewritten state machine (level×status instead of a single doc-status
field) plus Valkey-backed OTP verification; a new `api/internal/sms` package wraps SNS `Publish`; a new
`api/internal/domain/risk` package defines a pluggable `Evaluator` (only a no-op implementation exists). `ui/` gets a
linear Basic→OTP→Enhanced flow replacing the old address+6-document wizard. `cdk/` gains one IAM permission.

**Tech Stack:** Go 1.26 / Fiber v3 / DynamoDB / Valkey (existing), AWS SDK v2 `service/sns` (new dependency), Next.js
16 / React 19 / ShadCN 4 (existing).

## Global Constraints

- Spec: `docs/specs/2026-07-25-kyc-tiered.md` (committed `6f14627`) — every task below implements one of its sections;
  section refs are noted per task.
- `kyc_level` JWT claim values stay exactly `""|"basic"|"verified"` — **never** emit `"enhanced"` (ctech-wallet's
  `middleware.RequireKYC` hard-codes this set; see spec §5). Zero changes to `ctech-wallet` or `ctech-dfe`.
- Every HTTP error is an `*apierror.Problem` via `problem.Send(c)` — never `c.Status().JSON()`.
- `handler → service → repository` layering: no AWS SDK / DynamoDB calls in `kyc.Service`; no business logic in
  `kyc/repository.go` beyond what's needed to persist a decision already made by the service.
- Services take repository **interfaces**; concrete infra clients (`*cache.Client`) are passed directly as fields,
  matching `passkey.Service`'s existing pattern — this is not a repository, so it doesn't need one.
- All new constants (OTP length/TTL/cooldown, doc types, cache key prefixes) go in `kyc/model.go` — no magic
  strings/numbers at call sites.
- No production KYC data exists — every task treats field renames/removals as free schema changes, never as a migration.
- `go build ./...` and `go test ./...` (from `api/`) must pass after every backend task; `npx eslint src --ext .ts,.tsx`
  and `npm run build` (from `ui/`) must pass after every UI task.
- Never add a `Co-Authored-By` trailer to any commit.

---

## Task 1: KYC domain model — levels, status, errors, `ClaimLevel`

Implements spec §4, §5, §6 (constants only), §8 (OTP constants).

**Files:**

- Modify: `api/internal/domain/kyc/model.go`
- Create: `api/internal/domain/kyc/phone.go`
- Delete: `api/internal/domain/kyc/address.go`
- Test: `api/internal/domain/kyc/model_test.go` (extend existing file)

**Interfaces:**

- Produces: `LevelNone/LevelBasic/LevelEnhanced`, `StatusNone/StatusPending/StatusVerified/StatusRejected`,
  `StateNotStarted/StateAwaitingPhoneVerification/StateBasicVerified/StateUnderReview/StateRejected/StateVerified`,
  `DocTypeIDFront/DocTypeIDBack/DocTypeSelfieWithDocument`, `RequiredDocTypes []string`,
  `ClaimLevel(level, status string) string`, `IsValidPhone(phone string) bool`, `MaskPhone(phone string) string`, all
  error vars below. Later tasks (2, 8-14) consume these names verbatim.

- [ ] **Step 1: Delete `api/internal/domain/kyc/address.go`**

This file only defines `Address`-related helpers no longer needed once `address` is dropped from KYC entirely (spec §3,
§6).

- [ ] **Step 2: Rewrite `api/internal/domain/kyc/model.go`**

```go
package kyc

import (
	"errors"
	"slices"
	"strings"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// KYC verification levels stored on the user. ClaimLevel (below) maps these
// onto ctech-wallet's existing kyc_level claim contract ("" | "basic" |
// "verified") — never widen that value set; LevelEnhanced never appears in a
// token as-is.
const (
	LevelNone     = ""
	LevelBasic    = "basic"
	LevelEnhanced = "enhanced"
)

// KYCStatus values. StatusRejected is only reachable from LevelEnhanced —
// Basic never regresses once verified (spec §4).
const (
	StatusNone     = ""
	StatusPending  = "pending"
	StatusVerified = "verified"
	StatusRejected = "rejected"
)

// User-facing state, derived from level+status+expiry (see Service.state) —
// the UI branches on this single value instead of recombining level/status.
const (
	StateNotStarted                = "not_started"
	StateAwaitingPhoneVerification = "awaiting_phone_verification"
	StateBasicVerified             = "basic_verified"
	StateUnderReview               = "under_review"
	StateRejected                  = "rejected"
	StateVerified                  = "verified"
)

// Enhanced document types. A printed photo can't hold itself next to an ID,
// so the human reviewer judges real-vs-photo from one static shot instead of
// the four head-turn video clips the old single-tier scheme required.
const (
	DocTypeIDFront            = "id_front"
	DocTypeIDBack             = "id_back"
	DocTypeSelfieWithDocument = "selfie_with_document"
)

// RequiredDocTypes are the documents SubmitEnhanced requires before it will
// accept a submission — see Service.SubmitEnhanced.
var RequiredDocTypes = []string{DocTypeIDFront, DocTypeIDBack, DocTypeSelfieWithDocument}

// Review decisions accepted by cmd/kyc.
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
)

const (
	// MinAge is the minimum age (years) to submit for KYC.
	MinAge = 18

	// SubmissionTTL is how long a pending Enhanced submission holds the queue
	// slot. Past it the submission is stale (no reviewer acted) and reads back
	// as basic_verified — see Service.state.
	SubmissionTTL = 30 * 24 * time.Hour

	// MaxDocumentBytes caps an uploaded identity document or selfie photo.
	MaxDocumentBytes = 5 << 20

	// PresignTTL bounds how long an upload/download URL stays usable.
	PresignTTL = 10 * time.Minute

	// MaxDocuments caps how many files one Enhanced submission may carry: the
	// 3 required documents plus headroom for re-taking a blurry shot.
	MaxDocuments = 10

	// OTPLength is the digit count of a phone-verification code.
	OTPLength = 6
	// OTPTTL bounds how long a sent code stays valid.
	OTPTTL = 10 * time.Minute
	// OTPResendCooldown is the minimum gap between two sent codes.
	OTPResendCooldown = 60 * time.Second
	// OTPMaxAttempts caps wrong-code guesses against one sent code; exceeding
	// it requires a fresh resend rather than a permanent lockout.
	OTPMaxAttempts = 5
)

// TimeLayout is the wire/storage format for every timestamp in this package.
const TimeLayout = time.RFC3339

// BuildCPFPK keys the uniqueness item enforcing one CPF per account.
func BuildCPFPK(cpf string) string {
	return "CPF_" + cpf
}

// BuildDocumentKey is the S3 object key for an uploaded document. Keys are
// never returned to the user — only presigned URLs are.
func BuildDocumentKey(userID, documentID string) string {
	return "kyc/" + userID + "/" + documentID
}

// ClaimLevel maps (level, status) onto ctech-wallet's existing kyc_level
// claim contract — see ctech-wallet/api/internal/middleware/scope.go. Called
// from handler/token.go and handler/userinfo.go; ctech-wallet requires no
// changes.
func ClaimLevel(level, status string) string {
	switch {
	case level == LevelEnhanced && status == StatusVerified:
		return "verified"
	case level == LevelBasic && status == StatusVerified:
		return "basic"
	case level == LevelEnhanced:
		return "basic" // pending or rejected enhanced still keeps basic access
	default:
		return ""
	}
}

var (
	ErrInvalidCPF       = errors.New("invalid cpf")
	ErrInvalidBirthDate = errors.New("invalid birth date")
	ErrUnderage         = errors.New("user is under the minimum age")
	ErrCPFConflict      = errors.New("cpf already registered to another account")
	ErrInvalidPhone     = errors.New("invalid phone number")
	ErrAlreadyVerified  = errors.New("kyc already verified")
	ErrNotSubmitted     = errors.New("kyc data not submitted")

	// ErrBasicRequired is returned when Enhanced is submitted before Basic has
	// been phone-verified (spec §4: Enhanced requires Basic verified first).
	ErrBasicRequired = errors.New("basic verification must be completed first")
	// ErrBasicLocked is returned when Basic identity data is resubmitted after
	// it has already been phone-verified — Basic never regresses.
	ErrBasicLocked = errors.New("basic identity data cannot be changed once verified")

	// ErrPhoneVerificationUnavailable is returned by every Basic/OTP method
	// while PHONE_VERIFICATION_ENABLED is false (see Service.PhoneVerificationEnabled).
	ErrPhoneVerificationUnavailable = errors.New("phone verification is not available")
	ErrNoOTPPending                 = errors.New("no verification code is pending")
	ErrInvalidCode                  = errors.New("invalid verification code")
	ErrTooManyAttempts              = errors.New("too many invalid attempts, request a new code")
	ErrResendCooldown               = errors.New("a code was already sent recently")

	// ErrInvalidMethod is returned when document verification is unavailable —
	// no bucket is configured (see Service.DocumentsEnabled).
	ErrInvalidMethod = errors.New("document verification is not available")

	// ErrSubmissionLocked guards an Enhanced submission under active review:
	// documents and the submit route are frozen until it is rejected or expires.
	ErrSubmissionLocked = errors.New("kyc submission is pending and cannot be changed")

	ErrInvalidDocumentType = errors.New("invalid document type")
	ErrInvalidContentType  = errors.New("invalid document content type")
	ErrDocumentNotUploaded = errors.New("document was not uploaded")
	ErrDocumentTooLarge    = errors.New("document exceeds the maximum size")
	ErrTooManyDocuments    = errors.New("too many documents for this submission")
	ErrNoDocuments         = errors.New("no documents uploaded")
	ErrInvalidDecision     = errors.New("invalid review decision")

	// ErrDocumentTypeMismatch is returned when the type a client confirms does
	// not match the type the document was presigned for (SEC-018).
	ErrDocumentTypeMismatch = errors.New("document type does not match the presigned intent")
)

// Document is stored on the user item; its canonical definition lives in the
// user package (kyc imports user, not the reverse).
type Document = user.KYCDocument

// PendingDocument is the server-side upload intent recorded when a document is
// presigned (see Service.PresignDocument). ConfirmDocument must match the
// client-supplied Type against PendingDocument.Type (SEC-018). Persisted as
// its own item (KYCPEND_<documentID>) rather than on the user record.
type PendingDocument struct {
	UserID      string `dynamodbav:"user_id"`
	Type        string `dynamodbav:"doc_type"`
	ContentType string `dynamodbav:"content_type"`
}

// Status is the user-facing view of KYC state (CPF/phone always masked, S3
// keys never exposed).
type Status struct {
	State           string     `json:"state"`
	Level           string     `json:"level"`
	CPFMasked       string     `json:"cpf_masked,omitempty"`
	LegalName       string     `json:"legal_name,omitempty"`
	BirthDate       string     `json:"birth_date,omitempty"`
	PhoneMasked     string     `json:"phone_masked,omitempty"`
	BasicVerifiedAt string     `json:"basic_verified_at,omitempty"`
	Documents       []Document `json:"documents,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	SubmittedAt     string     `json:"submitted_at,omitempty"`
	ExpiresAt       string     `json:"expires_at,omitempty"`
	VerifiedAt      string     `json:"verified_at,omitempty"`
}

// BasicSubmission is the validated input of Service.SubmitBasic.
type BasicSubmission struct {
	CPF         string
	LegalName   string
	BirthDate   string
	PhoneNumber string // E.164
}

// IsValidDocumentType reports whether t is an accepted Enhanced document type.
func IsValidDocumentType(t string) bool {
	return slices.Contains(RequiredDocTypes, t)
}

// allowedContentTypes are the MIME types a reviewer can actually open. Video
// types are gone along with the 4-clip selfie flow — every Enhanced document
// is now a static photo or PDF.
var allowedContentTypes = []string{
	"image/jpeg",
	"image/png",
	"image/heic",
	"application/pdf",
}

// IsValidContentType reports whether ct may be uploaded as an identity document.
func IsValidContentType(ct string) bool {
	for _, doc := range allowedContentTypes {
		if strings.HasPrefix(ct, doc) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Create `api/internal/domain/kyc/phone.go`**

```go
package kyc

import "regexp"

// e164Pattern matches E.164: a leading '+' followed by 8-15 digits, the first
// non-zero (ITU-T E.164 recommendation).
var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)

// IsValidPhone reports whether phone is a plausible E.164 number. The DTO
// layer also validates with go-playground/validator's "e164" tag — this is
// the domain-layer check, same defense-in-depth pattern as IsValidCPF.
func IsValidPhone(phone string) bool {
	return e164Pattern.MatchString(phone)
}

// MaskPhone renders a phone as ***1234 (last 4 digits visible), mirroring
// MaskCPF's style.
func MaskPhone(phone string) string {
	if len(phone) < 4 {
		return ""
	}
	return "***" + phone[len(phone)-4:]
}
```

- [ ] **Step 4: Write failing tests for `ClaimLevel`, `IsValidPhone`, `MaskPhone`**

Append to `api/internal/domain/kyc/model_test.go`:

```go
func TestClaimLevel(t *testing.T) {
	cases := []struct {
		level, status, want string
	}{
		{LevelNone, StatusNone, ""},
		{LevelBasic, StatusPending, ""},
		{LevelBasic, StatusVerified, "basic"},
		{LevelEnhanced, StatusPending, "basic"},
		{LevelEnhanced, StatusRejected, "basic"},
		{LevelEnhanced, StatusVerified, "verified"},
	}
	for _, tc := range cases {
		if got := ClaimLevel(tc.level, tc.status); got != tc.want {
			t.Errorf("ClaimLevel(%q, %q) = %q, want %q", tc.level, tc.status, got, tc.want)
		}
	}
}

func TestIsValidPhone(t *testing.T) {
	cases := []struct {
		phone string
		want  bool
	}{
		{"+5511987654321", true},
		{"+12025550123", true},
		{"5511987654321", false}, // missing +
		{"+0511987654321", false}, // leading zero after +
		{"not-a-phone", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsValidPhone(tc.phone); got != tc.want {
			t.Errorf("IsValidPhone(%q) = %v, want %v", tc.phone, got, tc.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	if got := MaskPhone("+5511987654321"); got != "***4321" {
		t.Errorf("MaskPhone = %q, want ***4321", got)
	}
	if got := MaskPhone(""); got != "" {
		t.Errorf("MaskPhone(empty) = %q, want empty", got)
	}
}
```

- [ ] **Step 5: Run the new tests to verify they fail (compile error — old model.go removed)**

Run: `cd api && go test ./internal/domain/kyc/... -run 'TestClaimLevel|TestIsValidPhone|TestMaskPhone' -v`
Expected: FAIL (undefined: ClaimLevel / IsValidPhone / MaskPhone) until Steps 2-3 are in place — since Steps 2-3 are
written above, running now should PASS. If it fails for any other reason, fix before continuing.

- [ ] **Step 6: Run the full kyc package test suite**

Run: `cd api && go test ./internal/domain/kyc/... -v`
Expected: compile errors in `service.go`, `repository.go`, `service_test.go` (they still reference the old API) — this
is expected until Tasks 8-10 land. Confirm the *new* tests (Step 4) pass in isolation once those are fixed; for now just
confirm `model_test.go` and `cpf_test.go` compile logic is sound by inspection (the package won't build standalone until
Task 8-10 complete). Do not commit yet — commit happens at the end of Task 10 once the whole package builds again.

---

## Task 2: `user.User` model — rename/add/remove KYC fields

Implements spec §6.

**Files:**

- Modify: `api/internal/domain/user/model.go`

**Interfaces:**

- Produces: `User.KYCStatus` (renamed from `KYCDocStatus`), `User.PhoneNumber`, `User.PhoneVerifiedAt`,
  `User.KYCBasicVerifiedAt`, `User.KYCRiskScore`, `User.KYCRiskSignals`, `User.KYCRiskEvaluatedAt`. Removes
  `User.Address`, `User.KYCMethod`. Tasks 3, 8, 9, 11 consume these field names.

- [ ] **Step 1: Edit `api/internal/domain/user/model.go`**

Replace the `User` struct's KYC block and remove the `Address` type entirely:

```go
type User struct {
	PK            string `dynamodbav:"pk"`
	Email         string `dynamodbav:"email"`
	GoogleSub     string `dynamodbav:"google_sub,omitempty"`
	PasswordHash  string `dynamodbav:"password_hash"`
	FirstName     string `dynamodbav:"first_name"`
	LastName      string `dynamodbav:"last_name"`
	DisplayName   string `dynamodbav:"display_name,omitempty"`
	AvatarURL     string `dynamodbav:"avatar_url,omitempty"`
	EmailVerified bool   `dynamodbav:"email_verified"`
	IsEnabled     bool   `dynamodbav:"is_enabled"`
	CreatedAt     string `dynamodbav:"created_at"`
	UpdatedAt     string `dynamodbav:"updated_at"`

	CPF       string `dynamodbav:"cpf,omitempty"`        // 11 digits, numbers only — never serialized to clients
	BirthDate string `dynamodbav:"birth_date,omitempty"`  // YYYY-MM-DD
	LegalName string `dynamodbav:"legal_name,omitempty"`  // name as registered with Receita Federal

	KYCLevel           string        `dynamodbav:"kyc_level,omitempty"`  // kyc.LevelNone | kyc.LevelBasic | kyc.LevelEnhanced
	KYCStatus          string        `dynamodbav:"kyc_status,omitempty"` // kyc.Status* — renamed from kyc_doc_status
	KYCBasicVerifiedAt string        `dynamodbav:"kyc_basic_verified_at,omitempty"` // RFC3339 — set once, never cleared
	KYCVerifiedAt      string        `dynamodbav:"kyc_verified_at,omitempty"`       // RFC3339 — Enhanced verified timestamp
	KYCRejectionReason string        `dynamodbav:"kyc_rejection_reason,omitempty"`  // reviewer's note, Enhanced only
	KYCSubmittedAt     string        `dynamodbav:"kyc_submitted_at,omitempty"`      // RFC3339 — whichever level is currently pending
	KYCExpiresAt       string        `dynamodbav:"kyc_expires_at,omitempty"`        // RFC3339 — stale Enhanced pending unlocks re-submission
	KYCDocuments       []KYCDocument `dynamodbav:"kyc_documents,omitempty"`

	PhoneNumber     string `dynamodbav:"phone_number,omitempty"`      // E.164, collected at Basic
	PhoneVerifiedAt string `dynamodbav:"phone_verified_at,omitempty"` // RFC3339

	KYCRiskScore        int      `dynamodbav:"kyc_risk_score,omitempty"`
	KYCRiskSignals      []string `dynamodbav:"kyc_risk_signals,omitempty"`       // "name:detail" pairs, latest snapshot
	KYCRiskEvaluatedAt  string   `dynamodbav:"kyc_risk_evaluated_at,omitempty"`  // RFC3339

	TOSVersion        string `dynamodbav:"tos_version,omitempty"`
	TOSAcceptedAt     string `dynamodbav:"tos_accepted_at,omitempty"`
	PrivacyVersion    string `dynamodbav:"privacy_version,omitempty"`
	PrivacyAcceptedAt string `dynamodbav:"privacy_accepted_at,omitempty"`
}

// KYCDocument is one identity document uploaded for manual review. Key is the
// S3 object key — internal only, never serialized to clients.
type KYCDocument struct {
	ID         string `dynamodbav:"id" json:"id"`
	Type       string `dynamodbav:"type" json:"type"`
	Key        string `dynamodbav:"key" json:"-"`
	UploadedAt string `dynamodbav:"uploaded_at" json:"uploaded_at"`
}

func BuildPK(userID string) string {
	return "USER_" + userID
}

func (u *User) ID() string {
	return strings.TrimPrefix(u.PK, "USER_")
}

func (u *User) FullName() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

func (u *User) DisplayOrFullName() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.FullName()
}
```

The `Address` struct and its `IsZero()` method are deleted entirely — nothing outside `kyc`/`identity` referenced them
(confirmed by grep during planning).

- [ ] **Step 2: Compile-check (this package alone has no test file needing changes)**

Run: `cd api && go build ./internal/domain/user/...`
Expected: FAIL — every other file still referencing `KYCDocStatus`, `KYCMethod`, `Address` (kyc package, handler
package) won't compile yet. This is expected; those are fixed in Tasks 1 (done), 8, 9, 11. Do not commit this task in
isolation — it lands together with Task 9's commit once the whole module builds. Move directly to Task 3.

---

## Task 3: `risk` domain package — structural hook only

Implements spec §9, §3 (no real detectors).

**Files:**

- Create: `api/internal/domain/risk/model.go`
- Test: `api/internal/domain/risk/model_test.go`

**Interfaces:**

- Produces: `risk.Signal{Name, Score, Detail}`, `risk.Assessment{Score, Signals, EvaluatedAt}`, `risk.Evaluator`
  interface, `risk.NoopEvaluator{}`. Consumed by Task 8 (`kyc.Service`) and Task 8's repository dependency (Task 8's
  `SaveRiskAssessment`).

- [ ] **Step 1: Write the failing test**

`api/internal/domain/risk/model_test.go`:

```go
package risk

import (
	"context"
	"testing"
	"time"
)

func TestNoopEvaluatorReturnsZeroScore(t *testing.T) {
	a, err := NoopEvaluator{}.Evaluate(context.Background(), "user-1", "203.0.113.1")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if a.Score != 0 || len(a.Signals) != 0 {
		t.Fatalf("assessment = %+v, want zero score and no signals", a)
	}
	if _, err := time.Parse(time.RFC3339, a.EvaluatedAt); err != nil {
		t.Fatalf("EvaluatedAt = %q is not RFC3339: %v", a.EvaluatedAt, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd api && go test ./internal/domain/risk/... -v`
Expected: FAIL — package `risk` does not exist yet.

- [ ] **Step 3: Write `api/internal/domain/risk/model.go`**

```go
package risk

import (
	"context"
	"time"
)

// Signal is one contributing factor to an Assessment, named for a category a
// future Evaluator will detect (VPN/Tor use, multiple accounts, suspicious
// activity — spec §9). Score is that signal's contribution; Detail is a
// reviewer-facing note.
type Signal struct {
	Name   string
	Score  int
	Detail string
}

// Assessment is the latest risk snapshot for one KYC submission. It is
// informational only — nothing in kyc.Service gates on Score; a human
// reviewer sees it via cmd/kyc show.
type Assessment struct {
	Score       int
	Signals     []Signal
	EvaluatedAt string // RFC3339
}

// Evaluator scores a submission's fraud risk from the acting user and IP.
// userID+ip already thread through every call site, so a real IP-reputation
// lookup (VPN/Tor) or multi-account correlation plugs in without a signature
// change.
type Evaluator interface {
	Evaluate(ctx context.Context, userID, ip string) (Assessment, error)
}

// NoopEvaluator is the only Evaluator implementation until real detectors are
// defined.
//
// ponytail: always scores zero — swap in a concrete Evaluator once VPN/Tor,
// multi-account, or suspicious-activity detection criteria exist; Evaluate's
// signature already carries what those need (userID, ip).
type NoopEvaluator struct{}

func (NoopEvaluator) Evaluate(_ context.Context, _, _ string) (Assessment, error) {
	return Assessment{EvaluatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd api && go test ./internal/domain/risk/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd api && git add internal/domain/risk
git commit -m "feat: add risk domain package (structural hook, no-op evaluator)"
```

---

## Task 4: `sms` package — AWS SNS OTP delivery

Implements spec §8 (delivery mechanism only — config gating is Task 5).

**Files:**

- Create: `api/internal/sms/client.go`
- Modify: `api/go.mod` (new dependency)

**Interfaces:**

- Produces: `sms.Client`, `sms.New(ctx, region string) (*Client, error)`,
  `(*Client) SendOTP(ctx, phoneE164, code string) error`. Consumed by Task 17 (main.go wiring) via the `kyc.OTPSender`
  interface defined in Task 8.

- [ ] **Step 1: Add the AWS SNS SDK dependency**

Run: `cd api && go get github.com/aws/aws-sdk-go-v2/service/sns@latest`
Expected: `go.mod`/`go.sum` gain the new module (mirrors how `sesv2` was added for email).

- [ ] **Step 2: Write `api/internal/sms/client.go`**

```go
// Package sms sends one-time verification codes via AWS SNS direct-to-phone
// Publish — no SNS topic is created or needed for this pattern. Mirrors
// internal/email's SES wrapper shape.
package sms

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"gopkg.aoctech.app/api-commons/awsconfig"
)

type Client struct {
	sns *sns.Client
}

func New(ctx context.Context, region string) (*Client, error) {
	cfg, err := awsconfig.Load(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Client{sns: sns.NewFromConfig(cfg)}, nil
}

// SendOTP publishes a phone-verification code directly to phoneE164.
func (c *Client) SendOTP(ctx context.Context, phoneE164, code string) error {
	_, err := c.sns.Publish(ctx, &sns.PublishInput{
		PhoneNumber: aws.String(phoneE164),
		Message:     aws.String(fmt.Sprintf("Seu código de verificação CTech é %s. Válido por 10 minutos.", code)),
	})
	return err
}
```

- [ ] **Step 3: Build-check**

Run: `cd api && go build ./internal/sms/...`
Expected: builds cleanly (no unit test here — it's a thin AWS SDK wrapper with no branching logic, same testing posture
as `internal/email`, which also has none).

- [ ] **Step 4: Commit**

```bash
cd api && git add internal/sms go.mod go.sum
git commit -m "feat: add sms package wrapping SNS direct-to-phone Publish"
```

---

## Task 5: Config — `PHONE_VERIFICATION_ENABLED`

Implements spec §8 (gating flag).

**Files:**

- Modify: `api/internal/config/config.go`

**Interfaces:**

- Produces: `Config.PhoneVerificationEnabled bool`. Consumed by Task 17 (main.go wiring).

- [ ] **Step 1: Add the field and parsing**

In the `Config` struct, add (near `KYCDocumentsBucket`):

```go
	// PhoneVerificationEnabled gates AWS SNS phone verification (PHONE_VERIFICATION_ENABLED
	// env var, default false). While false, every Basic/OTP route hard-blocks
	// with 503 — mirrors how an absent KYCDocumentsBucket disables document
	// verification. Flip to true once production SNS SMS access is granted.
	PhoneVerificationEnabled bool
```

In `Load()`, add before the returned struct literal:

```go
	phoneVerificationEnabled, _ := strconv.ParseBool(getEnv("PHONE_VERIFICATION_ENABLED", "false"))
```

And add `PhoneVerificationEnabled: phoneVerificationEnabled,` to the returned `&Config{...}` literal.

- [ ] **Step 2: Build-check**

Run: `cd api && go build ./internal/config/...`
Expected: PASS (`strconv` is already imported in this file).

- [ ] **Step 3: Commit**

```bash
cd api && git add internal/config/config.go
git commit -m "feat: add PHONE_VERIFICATION_ENABLED config flag"
```

---

## Task 6: `apierror` — new/removed Problem constructors

Implements spec §7 (error constructors), §8 (503 gate).

**Files:**

- Modify: `api/internal/apierror/problem.go`

**Interfaces:**

- Produces: `apierror.KYCBasicRequired(instance string) *Problem`, `apierror.KYCInvalidCode(instance string) *Problem`,
  `apierror.KYCResendCooldown(retryAfter time.Duration, instance string) *Problem`,
  `apierror.KYCPhoneVerificationUnavailable(instance string) *Problem`. Removes `KYCCPFMismatch`, `KYCWrongMethod`
  (confirmed unused outside their own definitions during planning — dead code from the removed PIX-method design).
  Consumed by Task 11 (`handler/kyc.go`).

- [ ] **Step 1: Add `RetryAfterSeconds` to the `Problem` struct**

```go
type Problem struct {
	Type             string `json:"type"`
	Title            string `json:"title"`
	Status           int    `json:"status"`
	Detail           string `json:"detail,omitempty"`
	Instance         string `json:"instance,omitempty"`
	OAuthError       string `json:"error,omitempty"`
	OAuthDescription string `json:"error_description,omitempty"`
	MaxAgeSeconds     int64 `json:"max_age_seconds,omitempty"`
	// RetryAfterSeconds tells the client how long to wait before retrying a
	// rate-limited request (see KYCResendCooldown).
	RetryAfterSeconds int64 `json:"retry_after_seconds,omitempty"`
}
```

- [ ] **Step 2: Remove `KYCCPFMismatch` and `KYCWrongMethod`**

Delete both functions (lines ~189-213 in the current file) — the PIX verification method they served was already removed
per `docs/specs/2026-07-15-kyc-manual.md`, and no call site remains.

- [ ] **Step 3: Add the four new constructors**

Append after `KYCDocumentTooLarge`:

```go
// KYCBasicRequired → 409: Enhanced verification requires Basic to be
// phone-verified first.
func KYCBasicRequired(instance string) *Problem {
	return newProblem("kyc-basic-required", "Basic Verification Required", http.StatusConflict,
		"Complete phone-verified Basic identity verification before submitting Enhanced documents.", instance)
}

// KYCInvalidCode → 422: the submitted phone verification code is wrong,
// expired, or its attempt budget was exhausted.
func KYCInvalidCode(instance string) *Problem {
	return newProblem("kyc-invalid-code", "Invalid Verification Code", http.StatusUnprocessableEntity,
		"The verification code is invalid or has expired.", instance)
}

// KYCResendCooldown → 429: a verification code was already sent recently.
func KYCResendCooldown(retryAfter time.Duration, instance string) *Problem {
	p := newProblem("kyc-resend-cooldown", "Resend Cooldown", http.StatusTooManyRequests,
		"A verification code was already sent. Wait before requesting another.", instance)
	p.RetryAfterSeconds = int64(retryAfter.Seconds())
	return p
}

// KYCPhoneVerificationUnavailable → 503: SMS phone verification is not
// configured (PHONE_VERIFICATION_ENABLED=false).
func KYCPhoneVerificationUnavailable(instance string) *Problem {
	return newProblem("kyc-phone-verification-unavailable", "Phone Verification Unavailable", http.StatusServiceUnavailable,
		"Phone verification is not available right now. Try again later.", instance)
}
```

- [ ] **Step 4: Build-check**

Run: `cd api && go build ./internal/apierror/...`
Expected: PASS (`time` and `http` already imported).

- [ ] **Step 5: Commit**

```bash
cd api && git add internal/apierror/problem.go
git commit -m "feat: add KYC basic/OTP problem constructors, drop unused PIX-era ones"
```

---

## Task 7: `audit` — new phone-verified event

**Files:**

- Modify: `api/internal/domain/audit/events.go`

**Interfaces:**

- Produces: `audit.EventKYCPhoneVerified`. Consumed by Task 11 (`handler/kyc.go`).

- [ ] **Step 1: Add the constant next to the other KYC events**

```go
	EventKYCSubmitted        = "kyc.submitted"
	EventKYCPhoneVerified    = "kyc.phone_verified"
	EventKYCVerified         = "kyc.verified"
	EventKYCDocumentUploaded = "kyc.document_uploaded"
	EventKYCRejected         = "kyc.rejected"
```

- [ ] **Step 2: Build-check**

Run: `cd api && go build ./internal/domain/audit/...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
cd api && git add internal/domain/audit/events.go
git commit -m "feat: add kyc.phone_verified audit event"
```

---

## Task 8: `kyc` repository — rewrite for Basic/Enhanced split

Implements spec §6, §9 (risk persistence).

**Files:**

- Modify: `api/internal/domain/kyc/repository.go`

**Interfaces:**

- Consumes: `user.User` fields from Task 2, `risk.Assessment` from Task 3,
  `kyc.LevelBasic/LevelEnhanced/StatusPending/StatusVerified/StatusRejected/BuildCPFPK` from Task 1.
- Produces: `Repository` interface with `GetUser`,
  `SaveBasicSubmission(ctx, userID string, rec BasicRecord, oldCPF string) error`,
  `MarkPhoneVerified(ctx, userID, verifiedAt string) error`, `AddDocument(ctx, userID string, doc Document) error`
  (docStatus param removed), `SavePendingDocument`/`GetPendingDocument`/`DeletePendingDocument` (unchanged),
  `SaveEnhancedSubmission(ctx, userID, submittedAt, expiresAt string) error`,
  `MarkVerified(ctx, userID, verifiedAt string) error`, `MarkRejected(ctx, userID, reason string) error`,
  `SaveRiskAssessment(ctx, userID string, a risk.Assessment) error`, `ListPendingKYC(ctx) ([]*user.User, error)` (now
  scoped to `kyc_level=enhanced AND kyc_status=pending`).
  `BasicRecord{CPF, LegalName, BirthDate, PhoneNumber, SubmittedAt string}`. Consumed by Task 9 (`kyc.Service`) and Task
  12 (`handler` test helpers' in-memory mock).

- [ ] **Step 1: Rewrite `api/internal/domain/kyc/repository.go`**

```go
package kyc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/account/api/internal/domain/risk"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// usersTable matches the table used by the user repository — the CPF
// uniqueness item lives next to the user items (single-table pattern).
const usersTable = "account_users"

const conditionalCheckFailed = "ConditionalCheckFailed"

// BasicRecord is a validated Basic submission as persisted on the user item.
type BasicRecord struct {
	CPF         string
	LegalName   string
	BirthDate   string
	PhoneNumber string
	SubmittedAt string
}

// Repository persists KYC state on the user item plus a CPF_{cpf}
// uniqueness item, transactionally.
type Repository interface {
	GetUser(ctx context.Context, userID string) (*user.User, error)

	// SaveBasicSubmission writes Basic identity data, sets kyc_level=basic and
	// kyc_status=pending, and claims CPF_{cpf} transactionally (failing with
	// ErrCPFConflict if another account owns it, releasing CPF_{oldCPF} when
	// re-submitting with a different CPF). Only reachable while Basic is not
	// yet phone-verified — see Service.SubmitBasic.
	SaveBasicSubmission(ctx context.Context, userID string, rec BasicRecord, oldCPF string) error
	// MarkPhoneVerified sets kyc_status=verified, phone_verified_at, and
	// kyc_basic_verified_at (only ever called once per Basic cycle — see
	// Service.VerifyPhone).
	MarkPhoneVerified(ctx context.Context, userID, verifiedAt string) error

	// AddDocument appends an uploaded Enhanced document. Unlike the old
	// single-tier scheme there is no separate "awaiting files" doc status:
	// documents may accumulate any time Service.assertAcceptsDocuments allows
	// it, while the derived state stays basic_verified until SubmitEnhanced.
	AddDocument(ctx context.Context, userID string, doc Document) error
	// SavePendingDocument records the presigned upload intent (documentID →
	// type, content_type) so ConfirmDocument can reject a mismatched type
	// (SEC-018).
	SavePendingDocument(ctx context.Context, userID, documentID, docType, contentType string) error
	// GetPendingDocument returns the recorded intent for documentID, or nil
	// when none was presigned.
	GetPendingDocument(ctx context.Context, documentID string) (*PendingDocument, error)
	// DeletePendingDocument drops the intent once the upload is confirmed.
	DeletePendingDocument(ctx context.Context, documentID string) error

	// SaveEnhancedSubmission moves a basic/verified (or enhanced/rejected)
	// user to enhanced/pending. Documents were already uploaded and validated
	// by Service.SubmitEnhanced; no CPF transaction is needed since it was
	// already claimed at Basic time.
	SaveEnhancedSubmission(ctx context.Context, userID, submittedAt, expiresAt string) error
	MarkVerified(ctx context.Context, userID, verifiedAt string) error
	// MarkRejected records the rejection and clears kyc_documents: a rejected
	// submission's documents were judged insufficient, so a resubmission must
	// upload fresh ones.
	MarkRejected(ctx context.Context, userID, reason string) error

	// SaveRiskAssessment overwrites the latest risk snapshot — no history is
	// kept (spec §9).
	SaveRiskAssessment(ctx context.Context, userID string, a risk.Assessment) error

	// ListPendingKYC returns every user whose Enhanced submission is queued
	// for review, for cmd/kyc list. Operator-tool Scan, not a request path.
	ListPendingKYC(ctx context.Context) ([]*user.User, error)
}

type dynamoRepository struct {
	db       *dynamodb.Client
	table    string
	userRepo user.Repository
}

// NewRepository returns a DynamoDB-backed Repository reusing the user
// repository for reads.
func NewRepository(db *dynamodb.Client, tablePrefix string, userRepo user.Repository) Repository {
	return &dynamoRepository{db: db, table: database.TableName(tablePrefix, usersTable), userRepo: userRepo}
}

func (r *dynamoRepository) GetUser(ctx context.Context, userID string) (*user.User, error) {
	return r.userRepo.GetByID(ctx, userID)
}

func (r *dynamoRepository) SaveBasicSubmission(ctx context.Context, userID string, rec BasicRecord, oldCPF string) error {
	table := r.table
	now := time.Now().UTC().Format(time.RFC3339)

	cpfItem, err := attributevalue.MarshalMap(map[string]string{
		"pk":         BuildCPFPK(rec.CPF),
		"user_id":    userID,
		"created_at": now,
	})
	if err != nil {
		return fmt.Errorf("marshaling cpf item: %w", err)
	}

	items := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName: aws.String(table),
				Item:      cpfItem,
				// New claims require an unclaimed pk; a re-submission with the same
				// CPF finds the user's own item — not a conflict.
				ConditionExpression: aws.String("attribute_not_exists(pk) OR user_id = :uid"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":uid": &types.AttributeValueMemberS{Value: userID},
				},
			},
		},
		{
			Update: &types.Update{
				TableName: aws.String(table),
				Key: map[string]types.AttributeValue{
					"pk": &types.AttributeValueMemberS{Value: user.BuildPK(userID)},
				},
				UpdateExpression: aws.String(
					"SET cpf = :cpf, legal_name = :ln, birth_date = :bd, phone_number = :phone, " +
						"kyc_level = :lvl, kyc_status = :st, kyc_submitted_at = :sub, updated_at = :now " +
						"REMOVE kyc_rejection_reason, phone_verified_at",
				),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":cpf":   &types.AttributeValueMemberS{Value: rec.CPF},
					":ln":    &types.AttributeValueMemberS{Value: rec.LegalName},
					":bd":    &types.AttributeValueMemberS{Value: rec.BirthDate},
					":phone": &types.AttributeValueMemberS{Value: rec.PhoneNumber},
					":lvl":   &types.AttributeValueMemberS{Value: LevelBasic},
					":st":    &types.AttributeValueMemberS{Value: StatusPending},
					":sub":   &types.AttributeValueMemberS{Value: rec.SubmittedAt},
					":now":   &types.AttributeValueMemberS{Value: now},
				},
			},
		},
	}

	if oldCPF != "" && oldCPF != rec.CPF {
		items = append(items, types.TransactWriteItem{
			Delete: &types.Delete{
				TableName: aws.String(table),
				Key: map[string]types.AttributeValue{
					"pk": &types.AttributeValueMemberS{Value: BuildCPFPK(oldCPF)},
				},
			},
		})
	}

	if _, err := r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items}); err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			for _, reason := range canceled.CancellationReasons {
				if reason.Code != nil && *reason.Code == conditionalCheckFailed {
					return ErrCPFConflict
				}
			}
		}
		return err
	}
	return nil
}

func (r *dynamoRepository) MarkPhoneVerified(ctx context.Context, userID, verifiedAt string) error {
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_status":             StatusVerified,
		"phone_verified_at":      verifiedAt,
		"kyc_basic_verified_at":  verifiedAt,
	})
}

func (r *dynamoRepository) AddDocument(ctx context.Context, userID string, doc Document) error {
	table := r.table
	now := time.Now().UTC().Format(time.RFC3339)

	docAV, err := attributevalue.Marshal([]Document{doc})
	if err != nil {
		return fmt.Errorf("marshaling document: %w", err)
	}

	key := map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: user.BuildPK(userID)},
	}
	// list_append on a missing attribute errors, so seed it with an empty list.
	update := types.Update{
		TableName: aws.String(table),
		Key:       key,
		UpdateExpression: aws.String(
			"SET kyc_documents = list_append(if_not_exists(kyc_documents, :empty), :doc), updated_at = :now",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":empty": &types.AttributeValueMemberL{Value: []types.AttributeValue{}},
			":doc":   docAV,
			":now":   &types.AttributeValueMemberS{Value: now},
		},
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Update: &update}}})
	return err
}

// pendingPKPrefix keys the standalone item holding a presigned upload intent.
const pendingPKPrefix = "KYCPEND_"

func buildPendingPK(documentID string) string { return pendingPKPrefix + documentID }

func (r *dynamoRepository) SavePendingDocument(ctx context.Context, userID, documentID, docType, contentType string) error {
	item, err := attributevalue.MarshalMap(map[string]string{
		"pk":           buildPendingPK(documentID),
		"user_id":      userID,
		"doc_type":     docType,
		"content_type": contentType,
	})
	if err != nil {
		return fmt.Errorf("marshaling pending document: %w", err)
	}
	if _, err := r.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(r.table),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	}); err != nil {
		return fmt.Errorf("saving pending document %s: %w", documentID, err)
	}
	return nil
}

func (r *dynamoRepository) GetPendingDocument(ctx context.Context, documentID string) (*PendingDocument, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: buildPendingPK(documentID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting pending document %s: %w", documentID, err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var p PendingDocument
	if err := attributevalue.UnmarshalMap(out.Item, &p); err != nil {
		return nil, fmt.Errorf("unmarshaling pending document %s: %w", documentID, err)
	}
	return &p, nil
}

func (r *dynamoRepository) DeletePendingDocument(ctx context.Context, documentID string) error {
	if _, err := r.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: buildPendingPK(documentID)},
		},
	}); err != nil {
		return fmt.Errorf("deleting pending document %s: %w", documentID, err)
	}
	return nil
}

func (r *dynamoRepository) SaveEnhancedSubmission(ctx context.Context, userID, submittedAt, expiresAt string) error {
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_level":            LevelEnhanced,
		"kyc_status":           StatusPending,
		"kyc_submitted_at":     submittedAt,
		"kyc_expires_at":       expiresAt,
		"kyc_rejection_reason": "",
	})
}

func (r *dynamoRepository) MarkVerified(ctx context.Context, userID, verifiedAt string) error {
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_status":           StatusVerified,
		"kyc_verified_at":      verifiedAt,
		"kyc_rejection_reason": "",
	})
}

func (r *dynamoRepository) MarkRejected(ctx context.Context, userID, reason string) error {
	if err := r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_status":           StatusRejected,
		"kyc_rejection_reason": reason,
	}); err != nil {
		return err
	}
	// Documents were judged insufficient — clear them so re-submission requires
	// a fresh upload instead of silently reusing the rejected ones.
	update := types.Update{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: user.BuildPK(userID)},
		},
		UpdateExpression: aws.String("REMOVE kyc_documents"),
	}
	_, err := r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Update: &update}}})
	return err
}

func (r *dynamoRepository) SaveRiskAssessment(ctx context.Context, userID string, a risk.Assessment) error {
	signals := make([]string, len(a.Signals))
	for i, s := range a.Signals {
		signals[i] = s.Name + ":" + s.Detail
	}
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_risk_score":        a.Score,
		"kyc_risk_signals":      signals,
		"kyc_risk_evaluated_at": a.EvaluatedAt,
	})
}

// ListPendingKYC scans for users whose Enhanced submission is queued for
// review.
// ponytail: offline operator tool (cmd/kyc list), not a request path — a GSI
// on kyc_status is the scale upgrade if this table grows large.
func (r *dynamoRepository) ListPendingKYC(ctx context.Context) ([]*user.User, error) {
	table := r.table
	var users []*user.User
	var startKey map[string]types.AttributeValue
	for {
		out, err := r.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String(table),
			FilterExpression: aws.String("kyc_level = :lvl AND kyc_status = :st"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":lvl": &types.AttributeValueMemberS{Value: LevelEnhanced},
				":st":  &types.AttributeValueMemberS{Value: StatusPending},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scanning for pending kyc: %w", err)
		}
		for _, item := range out.Items {
			var u user.User
			if err := attributevalue.UnmarshalMap(item, &u); err != nil {
				return nil, fmt.Errorf("unmarshaling user: %w", err)
			}
			users = append(users, &u)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return users, nil
}
```

- [ ] **Step 2: Build-check (package still won't compile standalone — `service.go` not yet updated)**

Run: `cd api && go vet ./internal/domain/kyc/... 2>&1 | head -50`
Expected: errors only from `service.go`/`service_test.go` referencing the old API — confirm no errors originate from
`repository.go` itself (i.e., every error message references `service.go` or `service_test.go`, not `repository.go`).
Proceed to Task 9 without committing yet — Task 8+9+10 land together in Task 10's final commit once the package builds
and tests pass.

---

## Task 9: `kyc.Service` — rewrite for Basic/OTP/Enhanced

Implements spec §4, §5 (via `ClaimLevel`, already in `model.go`), §8, §9.

**Files:**

- Modify: `api/internal/domain/kyc/service.go`

**Interfaces:**

- Consumes: `Repository` from Task 8, `risk.Evaluator`/`risk.Assessment` from Task 3, `*cache.Client` (existing,
  `api/internal/cache`), `crypto.HashToken` (existing, `api/internal/crypto`).
- Produces: `Service{repo, presigner, cache, sms, risk, now}`,
  `NewService(repo Repository, presigner Presigner, cache *cache.Client, sms OTPSender, riskEvaluator risk.Evaluator) *Service`,
  `OTPSender interface { SendOTP(ctx, phoneE164, code string) error }`, `(*Service) PhoneVerificationEnabled() bool`,
  `(*Service) SubmitBasic(ctx, userID, ip string, sub BasicSubmission) error`,
  `(*Service) VerifyPhone(ctx, userID, code string) error`, `(*Service) ResendCode(ctx, userID string) error`,
  `(*Service) SubmitEnhanced(ctx, userID, ip string) error`, `(*Service) PresignDocument`/`ConfirmDocument` (signatures
  unchanged), `(*Service) DocumentsEnabled() bool`, `(*Service) Review(ctx, userID, decision, reason string) error`,
  `(*Service) ListPendingKYC`/`DocumentURLs`/`Get`/`GetUser` (signatures unchanged). Consumed by Task 11
  (`handler/kyc.go`), Task 16 (`cmd/kyc/main.go`), Task 17 (`cmd/api/main.go`).

- [ ] **Step 1: Rewrite `api/internal/domain/kyc/service.go`**

```go
package kyc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"uuid"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/domain/risk"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

const birthDateLayout = "2006-01-02"

// Presigner issues time-bounded S3 URLs. The service never touches object
// bytes — the browser uploads straight to the bucket.
type Presigner interface {
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Size(ctx context.Context, key string) (int64, error)
}

// OTPSender delivers a phone-verification code. sms.Client satisfies this.
type OTPSender interface {
	SendOTP(ctx context.Context, phoneE164, code string) error
}

// Service implements the tiered KYC state machine:
//
//	none → basic/pending (SubmitBasic, sends an OTP)
//	  → basic/verified (VerifyPhone) — Basic never regresses past here
//	  → enhanced/pending (SubmitEnhanced, once all RequiredDocTypes uploaded)
//	  → enhanced/verified | enhanced/rejected (Review, a human via cmd/kyc)
//	enhanced/rejected → enhanced/pending (fresh document uploads + SubmitEnhanced)
type Service struct {
	repo      Repository
	presigner Presigner
	cache     *cache.Client
	sms       OTPSender
	risk      risk.Evaluator
	now       func() time.Time
}

func NewService(repo Repository, presigner Presigner, cache *cache.Client, sms OTPSender, riskEvaluator risk.Evaluator) *Service {
	return &Service{
		repo: repo, presigner: presigner, cache: cache, sms: sms, risk: riskEvaluator,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// PhoneVerificationEnabled reports whether SMS delivery is configured
// (PHONE_VERIFICATION_ENABLED=true — see config.Config).
func (s *Service) PhoneVerificationEnabled() bool { return s.sms != nil }

// ── Basic (CPF/name/birthdate/phone + SMS OTP) ──────────────────────────────

// SubmitBasic validates identity data, claims the CPF, and sends a fresh OTP.
// Reachable while Basic is unset or still pending (not yet phone-verified) —
// see isBasicLocked.
func (s *Service) SubmitBasic(ctx context.Context, userID, ip string, sub BasicSubmission) error {
	if !s.PhoneVerificationEnabled() {
		return ErrPhoneVerificationUnavailable
	}
	if !IsValidCPF(sub.CPF) {
		return ErrInvalidCPF
	}
	born, err := time.Parse(birthDateLayout, sub.BirthDate)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidBirthDate, sub.BirthDate)
	}
	if !isAtLeast(born, MinAge, s.now()) {
		return ErrUnderage
	}
	if !IsValidPhone(sub.PhoneNumber) {
		return ErrInvalidPhone
	}

	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if isBasicLocked(u) {
		return ErrBasicLocked
	}

	now := s.now()
	if err := s.repo.SaveBasicSubmission(ctx, userID, BasicRecord{
		CPF: sub.CPF, LegalName: strings.TrimSpace(sub.LegalName), BirthDate: sub.BirthDate,
		PhoneNumber: sub.PhoneNumber, SubmittedAt: now.Format(TimeLayout),
	}, u.CPF); err != nil {
		return err
	}

	if err := s.sendOTP(ctx, userID, sub.PhoneNumber); err != nil {
		return err
	}

	s.evaluateRisk(ctx, userID, ip)
	return nil
}

// isBasicLocked reports whether Basic identity data is immutable: once
// phone-verified (or once Enhanced has been reached, which implies it), it
// never regresses.
func isBasicLocked(u *user.User) bool {
	return (u.KYCLevel == LevelBasic && u.KYCStatus == StatusVerified) || u.KYCLevel == LevelEnhanced
}

// ResendCode sends a fresh OTP for a Basic submission still awaiting phone
// verification.
func (s *Service) ResendCode(ctx context.Context, userID string) error {
	if !s.PhoneVerificationEnabled() {
		return ErrPhoneVerificationUnavailable
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.KYCLevel != LevelBasic || u.KYCStatus != StatusPending {
		return ErrNoOTPPending
	}
	return s.sendOTP(ctx, userID, u.PhoneNumber)
}

// VerifyPhone checks code against the last sent OTP. On success it marks
// Basic verified — this is the only path that ever sets kyc_basic_verified_at.
func (s *Service) VerifyPhone(ctx context.Context, userID, code string) error {
	if !s.PhoneVerificationEnabled() {
		return ErrPhoneVerificationUnavailable
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.KYCLevel != LevelBasic || u.KYCStatus != StatusPending {
		return ErrNoOTPPending
	}

	attemptsKey := otpAttemptsKey(userID)
	attempts, _ := s.cache.Incr(ctx, attemptsKey, OTPTTL)
	if attempts > OTPMaxAttempts {
		return ErrTooManyAttempts
	}

	var storedHash string
	if err := s.cache.Get(ctx, otpKey(userID), &storedHash); err != nil {
		return ErrNoOTPPending
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(crypto.HashToken(code))) != 1 {
		return ErrInvalidCode
	}

	_ = s.cache.Delete(ctx, otpKey(userID))
	_ = s.cache.Delete(ctx, attemptsKey)
	_ = s.cache.Delete(ctx, otpResendKey(userID))

	return s.repo.MarkPhoneVerified(ctx, userID, s.now().Format(TimeLayout))
}

// sendOTP is the shared send path for SubmitBasic and ResendCode: it enforces
// the resend cooldown, generates+hashes a fresh code, resets the attempt
// counter, and dispatches via s.sms.
func (s *Service) sendOTP(ctx context.Context, userID, phone string) error {
	cooldownKey := otpResendKey(userID)
	onCooldown, err := s.cache.Exists(ctx, cooldownKey)
	if err != nil {
		return err
	}
	if onCooldown {
		return ErrResendCooldown
	}

	code, err := generateOTP()
	if err != nil {
		return err
	}
	if err := s.cache.Set(ctx, otpKey(userID), crypto.HashToken(code), OTPTTL); err != nil {
		return err
	}
	_ = s.cache.Delete(ctx, otpAttemptsKey(userID))
	if err := s.cache.Set(ctx, cooldownKey, "1", OTPResendCooldown); err != nil {
		return err
	}
	return s.sms.SendOTP(ctx, phone, code)
}

func otpKey(userID string) string        { return "kyc_otp:" + userID }
func otpAttemptsKey(userID string) string { return "kyc_otp_attempts:" + userID }
func otpResendKey(userID string) string   { return "kyc_otp_resend:" + userID }

// generateOTP returns a random OTPLength-digit numeric code, zero-padded.
func generateOTP() (string, error) {
	max := big.NewInt(1)
	for i := 0; i < OTPLength; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", OTPLength, n.Int64()), nil
}

// ── Enhanced (documents + human review) ─────────────────────────────────────

// hasRequiredDocuments reports whether docs contains at least one document of
// every type in RequiredDocTypes.
func hasRequiredDocuments(docs []Document) bool {
	seen := make(map[string]bool, len(docs))
	for _, d := range docs {
		seen[d.Type] = true
	}
	for _, want := range RequiredDocTypes {
		if !seen[want] {
			return false
		}
	}
	return true
}

func (s *Service) isExpired(u *user.User) bool {
	if u.KYCExpiresAt == "" {
		return false
	}
	exp, err := time.Parse(TimeLayout, u.KYCExpiresAt)
	if err != nil {
		return false
	}
	return s.now().After(exp)
}

// PresignDocument issues an upload URL for one Enhanced document. The object
// is only recorded once ConfirmDocument proves it landed in the bucket.
func (s *Service) PresignDocument(ctx context.Context, userID, docType, contentType string) (documentID, uploadURL string, err error) {
	if !IsValidDocumentType(docType) {
		return "", "", ErrInvalidDocumentType
	}
	if !IsValidContentType(contentType) {
		return "", "", ErrInvalidContentType
	}

	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if err := s.assertAcceptsDocuments(u); err != nil {
		return "", "", err
	}
	if len(u.KYCDocuments) >= MaxDocuments {
		return "", "", ErrTooManyDocuments
	}

	documentID = uuid.New().String()
	if err := s.repo.SavePendingDocument(ctx, userID, documentID, docType, contentType); err != nil {
		return "", "", err
	}
	uploadURL, err = s.presigner.PresignPut(ctx, BuildDocumentKey(userID, documentID), contentType, PresignTTL)
	if err != nil {
		return "", "", err
	}
	return documentID, uploadURL, nil
}

// ConfirmDocument records an uploaded document. The size check is what stops
// a client from claiming an upload it never made, or one that exceeds the cap
// the presigned URL could not enforce.
func (s *Service) ConfirmDocument(ctx context.Context, userID, documentID, docType string) error {
	if !IsValidDocumentType(docType) {
		return ErrInvalidDocumentType
	}

	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.assertAcceptsDocuments(u); err != nil {
		return err
	}
	if len(u.KYCDocuments) >= MaxDocuments {
		return ErrTooManyDocuments
	}

	key := BuildDocumentKey(userID, documentID)
	pending, err := s.repo.GetPendingDocument(ctx, documentID)
	if err != nil {
		return err
	}
	if pending == nil || pending.Type != docType || pending.UserID != userID {
		return ErrDocumentTypeMismatch
	}

	size, err := s.presigner.Size(ctx, key)
	if err != nil {
		return ErrDocumentNotUploaded
	}
	if size == 0 {
		return ErrDocumentNotUploaded
	}
	if size > MaxDocumentBytes {
		return ErrDocumentTooLarge
	}

	doc := Document{
		ID:         documentID,
		Type:       docType,
		Key:        key,
		UploadedAt: s.now().Format(TimeLayout),
	}
	if err := s.repo.AddDocument(ctx, userID, doc); err != nil {
		return err
	}
	_ = s.repo.DeletePendingDocument(ctx, documentID)
	return nil
}

// DocumentsEnabled reports whether the document verification path is
// available (it needs a configured bucket — see config.KYCDocumentsBucket).
func (s *Service) DocumentsEnabled() bool { return s.presigner != nil }

// assertAcceptsDocuments guards both document endpoints: uploads are allowed
// once Basic is phone-verified, or while a rejected Enhanced submission is
// being redone — never while none/basic-pending (ErrBasicRequired) nor while
// an Enhanced submission is pending review or already verified (ErrSubmissionLocked).
func (s *Service) assertAcceptsDocuments(u *user.User) error {
	if !s.DocumentsEnabled() {
		return ErrInvalidMethod
	}
	basicVerified := u.KYCLevel == LevelBasic && u.KYCStatus == StatusVerified
	enhancedRejected := u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusRejected
	if basicVerified || enhancedRejected {
		return nil
	}
	if u.KYCLevel == LevelEnhanced {
		return ErrSubmissionLocked
	}
	return ErrBasicRequired
}

// SubmitEnhanced finalizes an Enhanced submission once every RequiredDocTypes
// document is uploaded, queuing it for human review.
func (s *Service) SubmitEnhanced(ctx context.Context, userID, ip string) error {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}

	basicVerified := u.KYCLevel == LevelBasic && u.KYCStatus == StatusVerified
	enhancedRejected := u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusRejected
	switch {
	case u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusVerified:
		return ErrAlreadyVerified
	case u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusPending:
		return ErrSubmissionLocked
	case !basicVerified && !enhancedRejected:
		return ErrBasicRequired
	}

	if !s.DocumentsEnabled() {
		return ErrInvalidMethod
	}
	if !hasRequiredDocuments(u.KYCDocuments) {
		return ErrNoDocuments
	}

	now := s.now()
	if err := s.repo.SaveEnhancedSubmission(ctx, userID, now.Format(TimeLayout), now.Add(SubmissionTTL).Format(TimeLayout)); err != nil {
		return err
	}
	s.evaluateRisk(ctx, userID, ip)
	return nil
}

// evaluateRisk scores the submission and persists the snapshot best-effort —
// informational only, never blocks or fails the submission it's attached to.
func (s *Service) evaluateRisk(ctx context.Context, userID, ip string) {
	a, err := s.risk.Evaluate(ctx, userID, ip)
	if err != nil {
		log.Printf("kyc: risk evaluation failed for user %s: %v", userID, err)
		return
	}
	if err := s.repo.SaveRiskAssessment(ctx, userID, a); err != nil {
		log.Printf("kyc: saving risk assessment failed for user %s: %v", userID, err)
	}
}

// Review applies a human reviewer's decision to an Enhanced submission that
// is currently under review.
func (s *Service) Review(ctx context.Context, userID, decision, reason string) error {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.KYCLevel != LevelEnhanced || u.KYCStatus != StatusPending {
		return ErrNotSubmitted
	}

	switch decision {
	case DecisionApprove:
		return s.repo.MarkVerified(ctx, userID, s.now().Format(TimeLayout))
	case DecisionReject:
		docs := make([]Document, len(u.KYCDocuments))
		copy(docs, u.KYCDocuments)
		if err := s.repo.MarkRejected(ctx, userID, strings.TrimSpace(reason)); err != nil {
			return err
		}
		s.purgeRejectedObjects(docs)
		return nil
	default:
		return ErrInvalidDecision
	}
}

// ListPendingKYC returns every user whose Enhanced submission is currently
// queued for review — used by cmd/kyc list.
func (s *Service) ListPendingKYC(ctx context.Context) ([]*user.User, error) {
	return s.repo.ListPendingKYC(ctx)
}

// DocumentURLs returns presigned GET URLs so a reviewer can open the uploaded
// files. Internal callers only.
func (s *Service) DocumentURLs(ctx context.Context, userID string) ([]DocumentURL, error) {
	if !s.DocumentsEnabled() {
		return nil, ErrInvalidMethod
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentURL, 0, len(u.KYCDocuments))
	for _, d := range u.KYCDocuments {
		url, err := s.presigner.PresignGet(ctx, d.Key, PresignTTL)
		if err != nil {
			return nil, err
		}
		out = append(out, DocumentURL{ID: d.ID, Type: d.Type, UploadedAt: d.UploadedAt, URL: url})
	}
	return out, nil
}

type DocumentURL struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	UploadedAt string `json:"uploaded_at"`
	URL        string `json:"url"`
}

// Get returns the user-facing KYC status (CPF/phone masked).
func (s *Service) Get(ctx context.Context, userID string) (*Status, error) {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &Status{
		State:           s.state(u),
		Level:           u.KYCLevel,
		CPFMasked:       MaskCPF(u.CPF),
		LegalName:       u.LegalName,
		BirthDate:       u.BirthDate,
		PhoneMasked:     MaskPhone(u.PhoneNumber),
		BasicVerifiedAt: u.KYCBasicVerifiedAt,
		Documents:       u.KYCDocuments,
		RejectionReason: u.KYCRejectionReason,
		SubmittedAt:     u.KYCSubmittedAt,
		ExpiresAt:       u.KYCExpiresAt,
		VerifiedAt:      u.KYCVerifiedAt,
	}, nil
}

// state derives the single value the UI branches on from level+status+expiry.
func (s *Service) state(u *user.User) string {
	switch {
	case u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusVerified:
		return StateVerified
	case u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusRejected:
		return StateRejected
	case u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusPending && s.isExpired(u):
		// Stale — no reviewer acted. Basic access remains: Basic never regresses.
		return StateBasicVerified
	case u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusPending:
		return StateUnderReview
	case u.KYCLevel == LevelBasic && u.KYCStatus == StatusVerified:
		return StateBasicVerified
	case u.KYCLevel == LevelBasic && u.KYCStatus == StatusPending:
		return StateAwaitingPhoneVerification
	default:
		return StateNotStarted
	}
}

// GetUser exposes the raw user record for internal (service-to-service)
// consumers that need the unmasked CPF/phone.
func (s *Service) GetUser(ctx context.Context, userID string) (*user.User, error) {
	return s.repo.GetUser(ctx, userID)
}

// isAtLeast reports whether someone born on born is at least years old at now.
func isAtLeast(born time.Time, years int, now time.Time) bool {
	return !now.Before(born.AddDate(years, 0, 0))
}

// objectDeleter is the optional S3-delete capability some Presigner
// implementations expose.
type objectDeleter interface {
	DeleteObject(ctx context.Context, key string) error
}

// purgeRejectedObjects best-effort deletes the rejected documents' S3 objects
// in the background (SEC-038) — failures are logged, never returned.
func (s *Service) purgeRejectedObjects(docs []Document) {
	deleter, ok := s.presigner.(objectDeleter)
	if !ok {
		return
	}
	for _, d := range docs {
		go func(key string) {
			if err := deleter.DeleteObject(context.Background(), key); err != nil {
				log.Printf("kyc: failed to delete rejected document object %s: %v", key, err)
			}
		}(d.Key)
	}
}
```

- [ ] **Step 2: Build-check**

Run: `cd api && go build ./internal/domain/kyc/...`
Expected: FAIL only on `service_test.go` (still references the old `Submit`/`Address`/`Record` API) — confirm no errors
from `service.go` or `repository.go` themselves. Proceed to Task 10.

---

## Task 10: `kyc.Service` tests — full rewrite

Implements spec §13 (kyc/service_test.go bullet).

**Files:**

- Modify: `api/internal/domain/kyc/service_test.go` (full rewrite)

**Interfaces:**

- Consumes everything from Tasks 1, 3, 8, 9.

- [ ] **Step 1: Rewrite the in-memory test fixtures at the top of `service_test.go`**

```go
package kyc

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/domain/risk"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type memRepo struct {
	users   map[string]*user.User
	cpfs    map[string]string
	pending map[string]PendingDocument
}

func newMemRepo() *memRepo {
	return &memRepo{users: map[string]*user.User{}, cpfs: map[string]string{}, pending: map[string]PendingDocument{}}
}

func (m *memRepo) GetUser(_ context.Context, userID string) (*user.User, error) {
	u, ok := m.users[userID]
	if !ok {
		return nil, user.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *memRepo) SaveBasicSubmission(_ context.Context, userID string, rec BasicRecord, oldCPF string) error {
	if owner, taken := m.cpfs[rec.CPF]; taken && owner != userID {
		return ErrCPFConflict
	}
	if oldCPF != "" && oldCPF != rec.CPF {
		delete(m.cpfs, oldCPF)
	}
	m.cpfs[rec.CPF] = userID

	u := m.users[userID]
	u.CPF, u.LegalName, u.BirthDate, u.PhoneNumber = rec.CPF, rec.LegalName, rec.BirthDate, rec.PhoneNumber
	u.KYCLevel, u.KYCStatus = LevelBasic, StatusPending
	u.KYCSubmittedAt = rec.SubmittedAt
	u.KYCRejectionReason, u.PhoneVerifiedAt = "", ""
	return nil
}

func (m *memRepo) MarkPhoneVerified(_ context.Context, userID, verifiedAt string) error {
	u := m.users[userID]
	u.KYCStatus, u.PhoneVerifiedAt, u.KYCBasicVerifiedAt = StatusVerified, verifiedAt, verifiedAt
	return nil
}

func (m *memRepo) AddDocument(_ context.Context, userID string, doc Document) error {
	u := m.users[userID]
	u.KYCDocuments = append(u.KYCDocuments, doc)
	return nil
}

func (m *memRepo) SaveEnhancedSubmission(_ context.Context, userID, submittedAt, expiresAt string) error {
	u := m.users[userID]
	u.KYCLevel, u.KYCStatus = LevelEnhanced, StatusPending
	u.KYCSubmittedAt, u.KYCExpiresAt, u.KYCRejectionReason = submittedAt, expiresAt, ""
	return nil
}

func (m *memRepo) MarkVerified(_ context.Context, userID, verifiedAt string) error {
	u := m.users[userID]
	u.KYCStatus, u.KYCVerifiedAt, u.KYCRejectionReason = StatusVerified, verifiedAt, ""
	return nil
}

func (m *memRepo) MarkRejected(_ context.Context, userID, reason string) error {
	u := m.users[userID]
	u.KYCStatus, u.KYCRejectionReason = StatusRejected, reason
	u.KYCDocuments = nil
	return nil
}

func (m *memRepo) SaveRiskAssessment(_ context.Context, userID string, a risk.Assessment) error {
	u := m.users[userID]
	u.KYCRiskScore, u.KYCRiskEvaluatedAt = a.Score, a.EvaluatedAt
	for _, sig := range a.Signals {
		u.KYCRiskSignals = append(u.KYCRiskSignals, sig.Name+":"+sig.Detail)
	}
	return nil
}

func (m *memRepo) SavePendingDocument(_ context.Context, userID, documentID, docType, contentType string) error {
	m.pending[documentID] = PendingDocument{UserID: userID, Type: docType, ContentType: contentType}
	return nil
}

func (m *memRepo) GetPendingDocument(_ context.Context, documentID string) (*PendingDocument, error) {
	p, ok := m.pending[documentID]
	if !ok {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *memRepo) DeletePendingDocument(_ context.Context, documentID string) error {
	delete(m.pending, documentID)
	return nil
}

func (m *memRepo) ListPendingKYC(_ context.Context) ([]*user.User, error) {
	var out []*user.User
	for _, u := range m.users {
		if u.KYCLevel == LevelEnhanced && u.KYCStatus == StatusPending {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out, nil
}

// memPresigner is an in-memory stand-in for S3.
type memPresigner struct {
	objects map[string]int64
}

func newMemPresigner() *memPresigner {
	return &memPresigner{objects: map[string]int64{}}
}

func (p *memPresigner) PresignPut(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://s3.test/" + key + "?sig=put", nil
}
func (p *memPresigner) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://s3.test/" + key + "?sig=get", nil
}
func (p *memPresigner) Size(_ context.Context, key string) (int64, error) {
	size, ok := p.objects[key]
	if !ok {
		return 0, errors.New("not found")
	}
	return size, nil
}
func (p *memPresigner) put(key string, size int64) { p.objects[key] = size }

type testPresigner interface {
	Presigner
	put(key string, size int64)
}

type memDeleterPresigner struct {
	*memPresigner
	deleted []string
}

func newMemDeleterPresigner() *memDeleterPresigner {
	return &memDeleterPresigner{memPresigner: newMemPresigner()}
}

func (p *memDeleterPresigner) DeleteObject(_ context.Context, key string) error {
	p.deleted = append(p.deleted, key)
	return nil
}

// fakeSMS records every sent code instead of calling AWS SNS.
type fakeSMS struct {
	sent []struct{ phone, code string }
	fail bool
}

func (f *fakeSMS) SendOTP(_ context.Context, phone, code string) error {
	if f.fail {
		return errors.New("sms send failed")
	}
	f.sent = append(f.sent, struct{ phone, code string }{phone, code})
	return nil
}
func (f *fakeSMS) lastCode() string { return f.sent[len(f.sent)-1].code }

func adultBirthDate() string {
	return time.Now().UTC().AddDate(-30, 0, 0).Format("2006-01-02")
}

func basicSub(cpf string) BasicSubmission {
	return BasicSubmission{CPF: cpf, LegalName: "Fulano da Silva", BirthDate: adultBirthDate(), PhoneNumber: "+5511987654321"}
}

// setup returns a Service with phone verification enabled (fakeSMS non-nil).
func setup() (*Service, *memRepo, *memPresigner, *fakeSMS) {
	repo := newMemRepo()
	repo.users["u1"] = &user.User{PK: user.BuildPK("u1")}
	repo.users["u2"] = &user.User{PK: user.BuildPK("u2")}
	presigner := newMemPresigner()
	sms := &fakeSMS{}
	svc := NewService(repo, presigner, cache.NewInMemory(), sms, risk.NoopEvaluator{})
	return svc, repo, presigner, sms
}

func advance(svc *Service, d time.Duration) {
	svc.now = func() time.Time { return time.Now().UTC().Add(d) }
}

// verifyBasic drives SubmitBasic → VerifyPhone using the code fakeSMS captured.
func verifyBasic(t *testing.T, svc *Service, sms *fakeSMS, userID string, sub BasicSubmission) error {
	t.Helper()
	if err := svc.SubmitBasic(context.Background(), userID, "203.0.113.1", sub); err != nil {
		return err
	}
	return svc.VerifyPhone(context.Background(), userID, sms.lastCode())
}

// uploadAllRequiredDocs uploads one document per RequiredDocTypes entry.
func uploadAllRequiredDocs(t *testing.T, svc *Service, presigner testPresigner, userID string) {
	t.Helper()
	for _, docType := range RequiredDocTypes {
		docID, _, err := svc.PresignDocument(context.Background(), userID, docType, "image/jpeg")
		if err != nil {
			t.Fatalf("PresignDocument(%s): %v", docType, err)
		}
		presigner.put(BuildDocumentKey(userID, docID), 1024)
		if err := svc.ConfirmDocument(context.Background(), userID, docID, docType); err != nil {
			t.Fatalf("ConfirmDocument(%s): %v", docType, err)
		}
	}
}

// basicVerifiedWithDocs drives a user all the way to basic/verified with every
// Enhanced document already uploaded, ready for SubmitEnhanced.
func basicVerifiedWithDocs(t *testing.T, svc *Service, presigner testPresigner, sms *fakeSMS, userID, cpf string) {
	t.Helper()
	if err := verifyBasic(t, svc, sms, userID, basicSub(cpf)); err != nil {
		t.Fatalf("verifyBasic: %v", err)
	}
	uploadAllRequiredDocs(t, svc, presigner, userID)
}

// enhancedReviewed drives a user to enhanced/pending then applies decision.
func enhancedReviewed(t *testing.T, svc *Service, presigner testPresigner, sms *fakeSMS, userID, cpf, decision, reason string) {
	t.Helper()
	basicVerifiedWithDocs(t, svc, presigner, sms, userID, cpf)
	if err := svc.SubmitEnhanced(context.Background(), userID, "203.0.113.1"); err != nil {
		t.Fatalf("SubmitEnhanced: %v", err)
	}
	if err := svc.Review(context.Background(), userID, decision, reason); err != nil {
		t.Fatalf("Review: %v", err)
	}
}
```

- [ ] **Step 2: Write the Basic + OTP tests**

Append:

```go
func TestSubmitBasicRejectsWhenPhoneVerificationDisabled(t *testing.T) {
	repo := newMemRepo()
	repo.users["u1"] = &user.User{PK: user.BuildPK("u1")}
	svc := NewService(repo, newMemPresigner(), cache.NewInMemory(), nil, risk.NoopEvaluator{})

	err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725"))
	if !errors.Is(err, ErrPhoneVerificationUnavailable) {
		t.Fatalf("err = %v, want ErrPhoneVerificationUnavailable", err)
	}
}

func TestSubmitBasicRejectsInvalidCPF(t *testing.T) {
	svc, _, _, _ := setup()
	sub := basicSub("11111111111")
	err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", sub)
	if !errors.Is(err, ErrInvalidCPF) {
		t.Fatalf("err = %v, want ErrInvalidCPF", err)
	}
}

func TestSubmitBasicRejectsUnderage(t *testing.T) {
	svc, repo, _, _ := setup()
	sub := basicSub("52998224725")
	sub.BirthDate = time.Now().UTC().AddDate(-18, 0, 1).Format("2006-01-02")
	err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", sub)
	if !errors.Is(err, ErrUnderage) {
		t.Fatalf("err = %v, want ErrUnderage", err)
	}
	if repo.users["u1"].KYCLevel != LevelNone {
		t.Fatal("underage submission must not persist anything")
	}
}

func TestSubmitBasicRejectsInvalidPhone(t *testing.T) {
	svc, _, _, _ := setup()
	sub := basicSub("52998224725")
	sub.PhoneNumber = "not-a-phone"
	err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", sub)
	if !errors.Is(err, ErrInvalidPhone) {
		t.Fatalf("err = %v, want ErrInvalidPhone", err)
	}
}

func TestSubmitBasicSendsOTPAndSetsPending(t *testing.T) {
	svc, repo, _, sms := setup()
	if err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725")); err != nil {
		t.Fatalf("SubmitBasic: %v", err)
	}
	u := repo.users["u1"]
	if u.KYCLevel != LevelBasic || u.KYCStatus != StatusPending {
		t.Fatalf("level/status = %q/%q", u.KYCLevel, u.KYCStatus)
	}
	if len(sms.sent) != 1 || sms.sent[0].phone != "+5511987654321" {
		t.Fatalf("sms.sent = %+v", sms.sent)
	}
	if len(sms.lastCode()) != OTPLength {
		t.Fatalf("code length = %d, want %d", len(sms.lastCode()), OTPLength)
	}
}

func TestSubmitBasicRejectsDuplicateCPF(t *testing.T) {
	svc, _, _, sms := setup()
	if err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725")); err != nil {
		t.Fatalf("u1 submit: %v", err)
	}
	_ = sms // u1's code isn't needed for this test
	err := svc.SubmitBasic(context.Background(), "u2", "1.2.3.4", basicSub("52998224725"))
	if !errors.Is(err, ErrCPFConflict) {
		t.Fatalf("err = %v, want ErrCPFConflict", err)
	}
}

func TestSubmitBasicLockedOnceVerified(t *testing.T) {
	svc, _, _, sms := setup()
	if err := verifyBasic(t, svc, sms, "u1", basicSub("52998224725")); err != nil {
		t.Fatalf("verifyBasic: %v", err)
	}
	err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("11144477735"))
	if !errors.Is(err, ErrBasicLocked) {
		t.Fatalf("err = %v, want ErrBasicLocked", err)
	}
}

func TestSubmitBasicAllowsResubmitWhilePending(t *testing.T) {
	svc, repo, _, sms := setup()
	if err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725")); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("11144477735")); err != nil {
		t.Fatalf("resubmit while pending: %v", err)
	}
	if repo.users["u1"].CPF != "11144477735" {
		t.Fatalf("cpf = %q, want the corrected one", repo.users["u1"].CPF)
	}
	if len(sms.sent) != 2 {
		t.Fatalf("expected 2 sends (one per submit), got %d", len(sms.sent))
	}
}

func TestVerifyPhoneRejectsWrongCode(t *testing.T) {
	svc, _, _, _ := setup()
	if err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725")); err != nil {
		t.Fatalf("SubmitBasic: %v", err)
	}
	err := svc.VerifyPhone(context.Background(), "u1", "000000")
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("err = %v, want ErrInvalidCode", err)
	}
}

func TestVerifyPhoneLocksOutAfterMaxAttempts(t *testing.T) {
	svc, _, _, _ := setup()
	if err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725")); err != nil {
		t.Fatalf("SubmitBasic: %v", err)
	}
	for i := 0; i < OTPMaxAttempts; i++ {
		_ = svc.VerifyPhone(context.Background(), "u1", "000000")
	}
	err := svc.VerifyPhone(context.Background(), "u1", "000000")
	if !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("err = %v, want ErrTooManyAttempts", err)
	}
}

func TestVerifyPhoneSucceedsAndSetsBasicVerified(t *testing.T) {
	svc, repo, _, sms := setup()
	if err := verifyBasic(t, svc, sms, "u1", basicSub("52998224725")); err != nil {
		t.Fatalf("verifyBasic: %v", err)
	}
	u := repo.users["u1"]
	if u.KYCLevel != LevelBasic || u.KYCStatus != StatusVerified {
		t.Fatalf("level/status = %q/%q", u.KYCLevel, u.KYCStatus)
	}
	if u.KYCBasicVerifiedAt == "" || u.PhoneVerifiedAt == "" {
		t.Fatal("basic_verified_at and phone_verified_at must both be set")
	}
}

func TestVerifyPhoneRejectsAfterAlreadyVerified(t *testing.T) {
	svc, _, _, sms := setup()
	if err := verifyBasic(t, svc, sms, "u1", basicSub("52998224725")); err != nil {
		t.Fatalf("verifyBasic: %v", err)
	}
	err := svc.VerifyPhone(context.Background(), "u1", sms.lastCode())
	if !errors.Is(err, ErrNoOTPPending) {
		t.Fatalf("err = %v, want ErrNoOTPPending", err)
	}
}

func TestResendCodeEnforcesCooldown(t *testing.T) {
	svc, _, _, sms := setup()
	if err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725")); err != nil {
		t.Fatalf("SubmitBasic: %v", err)
	}
	err := svc.ResendCode(context.Background(), "u1")
	if !errors.Is(err, ErrResendCooldown) {
		t.Fatalf("err = %v, want ErrResendCooldown", err)
	}

	advance(svc, OTPResendCooldown+time.Second)
	if err := svc.ResendCode(context.Background(), "u1"); err != nil {
		t.Fatalf("resend after cooldown: %v", err)
	}
	if len(sms.sent) != 2 {
		t.Fatalf("sent = %d, want 2", len(sms.sent))
	}
}

func TestResendCodeRejectsWithoutPendingBasic(t *testing.T) {
	svc, _, _, _ := setup()
	err := svc.ResendCode(context.Background(), "u1")
	if !errors.Is(err, ErrNoOTPPending) {
		t.Fatalf("err = %v, want ErrNoOTPPending", err)
	}
}
```

- [ ] **Step 3: Write the Enhanced + Review + state tests**

Append:

```go
func TestPresignDocumentRequiresBasicVerified(t *testing.T) {
	svc, _, _, _ := setup()
	_, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/jpeg")
	if !errors.Is(err, ErrBasicRequired) {
		t.Fatalf("err = %v, want ErrBasicRequired", err)
	}
}

func TestSubmitEnhancedRequiresBasicVerified(t *testing.T) {
	svc, _, _, _ := setup()
	err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4")
	if !errors.Is(err, ErrBasicRequired) {
		t.Fatalf("err = %v, want ErrBasicRequired", err)
	}
}

func TestSubmitEnhancedRequiresAllDocuments(t *testing.T) {
	svc, _, presigner, sms := setup()
	if err := verifyBasic(t, svc, sms, "u1", basicSub("52998224725")); err != nil {
		t.Fatalf("verifyBasic: %v", err)
	}
	_ = presigner
	err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4")
	if !errors.Is(err, ErrNoDocuments) {
		t.Fatalf("err = %v, want ErrNoDocuments", err)
	}
}

func TestSubmitEnhancedQueuesForReview(t *testing.T) {
	svc, repo, presigner, sms := setup()
	basicVerifiedWithDocs(t, svc, presigner, sms, "u1", "52998224725")

	if err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4"); err != nil {
		t.Fatalf("SubmitEnhanced: %v", err)
	}
	u := repo.users["u1"]
	if u.KYCLevel != LevelEnhanced || u.KYCStatus != StatusPending {
		t.Fatalf("level/status = %q/%q", u.KYCLevel, u.KYCStatus)
	}
	if u.KYCExpiresAt == "" || u.KYCSubmittedAt == "" {
		t.Fatal("submission must carry submitted_at and expires_at")
	}
	if len(u.KYCDocuments) != len(RequiredDocTypes) {
		t.Fatalf("documents = %d, want %d", len(u.KYCDocuments), len(RequiredDocTypes))
	}
}

func TestSubmitEnhancedLockedWhilePending(t *testing.T) {
	svc, _, presigner, sms := setup()
	basicVerifiedWithDocs(t, svc, presigner, sms, "u1", "52998224725")
	if err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4"); err != nil {
		t.Fatalf("SubmitEnhanced: %v", err)
	}
	err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4")
	if !errors.Is(err, ErrSubmissionLocked) {
		t.Fatalf("err = %v, want ErrSubmissionLocked", err)
	}
}

func TestDocumentUploadRejectedWhileEnhancedPending(t *testing.T) {
	svc, _, presigner, sms := setup()
	basicVerifiedWithDocs(t, svc, presigner, sms, "u1", "52998224725")
	if err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4"); err != nil {
		t.Fatalf("SubmitEnhanced: %v", err)
	}
	_, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/jpeg")
	if !errors.Is(err, ErrSubmissionLocked) {
		t.Fatalf("err = %v, want ErrSubmissionLocked", err)
	}
}

func TestReviewApproveVerifies(t *testing.T) {
	svc, repo, presigner, sms := setup()
	enhancedReviewed(t, svc, presigner, sms, "u1", "52998224725", DecisionApprove, "")

	u := repo.users["u1"]
	if u.KYCLevel != LevelEnhanced || u.KYCStatus != StatusVerified || u.KYCVerifiedAt == "" {
		t.Fatalf("user = %+v", u)
	}
}

func TestReviewRejectClearsDocumentsAndAllowsResubmit(t *testing.T) {
	svc, repo, presigner, sms := setup()
	enhancedReviewed(t, svc, presigner, sms, "u1", "52998224725", DecisionReject, "blurry photo")

	u := repo.users["u1"]
	if u.KYCLevel != LevelEnhanced || u.KYCStatus != StatusRejected || u.KYCRejectionReason != "blurry photo" {
		t.Fatalf("user = %+v", u)
	}
	if len(u.KYCDocuments) != 0 {
		t.Fatal("rejection must clear uploaded documents")
	}

	// Resubmitting without fresh documents fails; fresh uploads unlock it.
	err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4")
	if !errors.Is(err, ErrNoDocuments) {
		t.Fatalf("err = %v, want ErrNoDocuments", err)
	}
	uploadAllRequiredDocs(t, svc, presigner, "u1")
	if err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4"); err != nil {
		t.Fatalf("resubmit after fresh uploads: %v", err)
	}
}

func TestReviewRejectPurgesS3Objects(t *testing.T) {
	repo := newMemRepo()
	repo.users["u1"] = &user.User{PK: user.BuildPK("u1")}
	presigner := newMemDeleterPresigner()
	sms := &fakeSMS{}
	svc := NewService(repo, presigner, cache.NewInMemory(), sms, risk.NoopEvaluator{})

	enhancedReviewed(t, svc, presigner, sms, "u1", "52998224725", DecisionReject, "unreadable")

	want := len(RequiredDocTypes)
	for i := 0; i < 200; i++ {
		if len(presigner.deleted) == want {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(presigner.deleted) != want {
		t.Fatalf("deleted = %d, want %d", len(presigner.deleted), want)
	}
}

func TestReviewRequiresPendingSubmission(t *testing.T) {
	svc, _, _, _ := setup()
	err := svc.Review(context.Background(), "u1", DecisionApprove, "")
	if !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("err = %v, want ErrNotSubmitted", err)
	}
}

func TestListPendingKYCScopedToEnhancedPending(t *testing.T) {
	svc, _, presigner, sms := setup()
	basicVerifiedWithDocs(t, svc, presigner, sms, "u1", "52998224725")
	if err := svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4"); err != nil {
		t.Fatalf("SubmitEnhanced: %v", err)
	}
	// u2 only reaches basic/verified — must not show up.
	if err := verifyBasic(t, svc, sms, "u2", basicSub("11144477735")); err != nil {
		t.Fatalf("verifyBasic u2: %v", err)
	}

	pending, err := svc.ListPendingKYC(context.Background())
	if err != nil {
		t.Fatalf("ListPendingKYC: %v", err)
	}
	if len(pending) != 1 || pending[0].ID() != "u1" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestGetStates(t *testing.T) {
	t.Run("not started", func(t *testing.T) {
		svc, _, _, _ := setup()
		assertState(t, svc, "u1", StateNotStarted)
	})
	t.Run("awaiting phone verification", func(t *testing.T) {
		svc, _, _, _ := setup()
		_ = svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", basicSub("52998224725"))
		assertState(t, svc, "u1", StateAwaitingPhoneVerification)
	})
	t.Run("basic verified", func(t *testing.T) {
		svc, _, _, sms := setup()
		_ = verifyBasic(t, svc, sms, "u1", basicSub("52998224725"))
		assertState(t, svc, "u1", StateBasicVerified)
	})
	t.Run("under review", func(t *testing.T) {
		svc, _, presigner, sms := setup()
		basicVerifiedWithDocs(t, svc, presigner, sms, "u1", "52998224725")
		_ = svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4")
		assertState(t, svc, "u1", StateUnderReview)
	})
	t.Run("rejected", func(t *testing.T) {
		svc, _, presigner, sms := setup()
		enhancedReviewed(t, svc, presigner, sms, "u1", "52998224725", DecisionReject, "blurry")
		assertState(t, svc, "u1", StateRejected)
	})
	t.Run("verified", func(t *testing.T) {
		svc, _, presigner, sms := setup()
		enhancedReviewed(t, svc, presigner, sms, "u1", "52998224725", DecisionApprove, "")
		assertState(t, svc, "u1", StateVerified)
	})
	t.Run("expired enhanced pending reads as basic verified", func(t *testing.T) {
		svc, _, presigner, sms := setup()
		basicVerifiedWithDocs(t, svc, presigner, sms, "u1", "52998224725")
		_ = svc.SubmitEnhanced(context.Background(), "u1", "1.2.3.4")
		advance(svc, SubmissionTTL+time.Hour)
		assertState(t, svc, "u1", StateBasicVerified)
	})
}

func TestGetMasksCPFAndPhone(t *testing.T) {
	svc, _, presigner, sms := setup()
	basicVerifiedWithDocs(t, svc, presigner, sms, "u1", "52998224725")

	st, err := svc.Get(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.CPFMasked != "***.***.***-25" || st.PhoneMasked != "***4321" {
		t.Fatalf("status = %+v", st)
	}
	if len(st.Documents) != len(RequiredDocTypes) {
		t.Fatalf("documents = %+v", st.Documents)
	}
}

func assertState(t *testing.T, svc *Service, userID, want string) {
	t.Helper()
	st, err := svc.Get(context.Background(), userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.State != want {
		t.Fatalf("state = %q, want %q", st.State, want)
	}
}
```

- [ ] **Step 4: Run the full package test suite**

Run: `cd api && go test ./internal/domain/kyc/... -v`
Expected: PASS — every test in `model_test.go`, `cpf_test.go`, and the rewritten `service_test.go` (this also confirms
Task 1's Step 6 and Task 8's Step 2 deferred checks).

- [ ] **Step 5: Commit Tasks 1-2, 8-10 together**

```bash
cd api && git add internal/domain/kyc internal/domain/user internal/domain/risk internal/domain/audit/events.go
git commit -m "feat: split KYC into Basic (phone-verified) and Enhanced (reviewed) levels"
```

---

## Task 11: `handler/kyc.go` — routes and DTOs rewrite

Implements spec §7.

**Files:**

- Modify: `api/internal/handler/kyc.go`

**Interfaces:**

- Consumes: `kyc.BasicSubmission`, `kyc.Err*` from Task 1,
  `kyc.Service.SubmitBasic/VerifyPhone/ResendCode/SubmitEnhanced` from Task 9, `apierror.KYC*` from Task 6,
  `audit.EventKYCPhoneVerified` from Task 7, `clientIP(c)` (existing, `handler/helpers.go`).
- Produces: routes `POST /account/kyc/basic`, `POST /account/kyc/basic/verify-phone`,
  `POST /account/kyc/basic/resend-code`, `POST /account/kyc/enhanced` (new); `GET /account/kyc`,
  `POST /account/kyc/documents`, `POST /account/kyc/documents/confirm` (updated). Consumed by Task 12 (tests), Task 19
  (README).

- [ ] **Step 1: Rewrite `api/internal/handler/kyc.go`**

```go
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/kyc"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

// KYCHandler serves the user-facing identity verification routes and the
// slim internal (service-to-service) read used by ctech-wallet. The human
// approve/reject decision is not an HTTP route — see cmd/kyc.
type KYCHandler struct {
	kycSvc *kyc.Service
	audit  *audit.Service
}

func NewKYCHandler(kycSvc *kyc.Service, auditSvc *audit.Service) *KYCHandler {
	return &KYCHandler{kycSvc: kycSvc, audit: auditSvc}
}

// Register mounts the user-facing routes on the account group. Everything
// that writes identity data sits behind step-up.
func (h *KYCHandler) Register(account fiber.Router, stepUp fiber.Handler) {
	account.Get("/kyc", h.get)
	account.Post("/kyc/basic", stepUp, h.submitBasic)
	account.Post("/kyc/basic/verify-phone", stepUp, h.verifyPhone)
	account.Post("/kyc/basic/resend-code", stepUp, h.resendCode)
	account.Post("/kyc/documents", stepUp, h.presignDocument)
	account.Post("/kyc/documents/confirm", stepUp, h.confirmDocument)
	account.Post("/kyc/enhanced", stepUp, h.submitEnhanced)
}

// RegisterInternalGet mounts the one service-to-service route ctech-wallet
// still needs: the raw (unmasked) identity record for withdrawal-key
// validation.
func (h *KYCHandler) RegisterInternalGet(v1 fiber.Router, internalAuth ...fiber.Handler) {
	handlers := make([]any, len(internalAuth))
	for i, m := range internalAuth {
		handlers[i] = m
	}
	grp := v1.Group("/internal/kyc", handlers...)
	grp.Get("/:user_id", h.internalGet)
}

type submitBasicRequest struct {
	CPF         string `json:"cpf" validate:"required,len=11,numeric"`
	LegalName   string `json:"legal_name" validate:"required,min=3,max=200"`
	BirthDate   string `json:"birth_date" validate:"required,datetime=2006-01-02"`
	PhoneNumber string `json:"phone_number" validate:"required,e164"`
}

// submitBasic validates and stores CPF/name/birthdate/phone, then sends an
// SMS OTP. Replaces the old single-tier POST /account/kyc.
func (h *KYCHandler) submitBasic(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req submitBasicRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	sub := kyc.BasicSubmission{
		CPF: req.CPF, LegalName: req.LegalName, BirthDate: req.BirthDate, PhoneNumber: req.PhoneNumber,
	}
	if err := h.kycSvc.SubmitBasic(c.Context(), userID, clientIP(c), sub); err != nil {
		return h.sendKYCError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventKYCSubmitted, map[string]string{"level": kyc.LevelBasic})
	return h.sendStatus(c, userID)
}

type verifyPhoneRequest struct {
	Code string `json:"code" validate:"required,len=6,numeric"`
}

func (h *KYCHandler) verifyPhone(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req verifyPhoneRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if err := h.kycSvc.VerifyPhone(c.Context(), userID, req.Code); err != nil {
		return h.sendKYCError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventKYCPhoneVerified, nil)
	return h.sendStatus(c, userID)
}

func (h *KYCHandler) resendCode(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	if err := h.kycSvc.ResendCode(c.Context(), userID); err != nil {
		return h.sendKYCError(c, err)
	}
	return h.sendStatus(c, userID)
}

func (h *KYCHandler) get(c fiber.Ctx) error {
	return h.sendStatus(c, middleware.GetUserID(c))
}

type presignDocumentRequest struct {
	Type        string `json:"type" validate:"required,oneof=id_front id_back selfie_with_document"`
	ContentType string `json:"content_type" validate:"required"`
}

// presignDocument hands the browser a short-lived S3 upload URL. The API
// never receives the file itself.
func (h *KYCHandler) presignDocument(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req presignDocumentRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	documentID, uploadURL, err := h.kycSvc.PresignDocument(c.Context(), userID, req.Type, req.ContentType)
	if err != nil {
		return h.sendKYCError(c, err)
	}

	return c.JSON(fiber.Map{
		"document_id":  documentID,
		"upload_url":   uploadURL,
		"expires_in":   int(kyc.PresignTTL.Seconds()),
		"max_bytes":    int64(kyc.MaxDocumentBytes),
		"content_type": req.ContentType,
	})
}

type confirmDocumentRequest struct {
	DocumentID string `json:"document_id" validate:"required,uuid4"`
	Type       string `json:"type" validate:"required,oneof=id_front id_back selfie_with_document"`
}

func (h *KYCHandler) confirmDocument(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req confirmDocumentRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if err := h.kycSvc.ConfirmDocument(c.Context(), userID, req.DocumentID, req.Type); err != nil {
		return h.sendKYCError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventKYCDocumentUploaded, map[string]string{"type": req.Type})
	return h.sendStatus(c, userID)
}

// submitEnhanced finalizes an Enhanced submission that already has every
// required document uploaded and queues it for human review.
func (h *KYCHandler) submitEnhanced(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	if err := h.kycSvc.SubmitEnhanced(c.Context(), userID, clientIP(c)); err != nil {
		return h.sendKYCError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventKYCSubmitted, map[string]string{"level": kyc.LevelEnhanced})
	return h.sendStatus(c, userID)
}

// internalGet returns the full (unmasked) identity record — service-to-service
// only; the wallet needs the raw CPF for withdrawal key validation.
func (h *KYCHandler) internalGet(c fiber.Ctx) error {
	u, err := h.kycSvc.GetUser(c.Context(), c.Params("user_id"))
	if err != nil {
		return h.sendKYCError(c, err)
	}
	return c.JSON(fiber.Map{
		"level":        u.KYCLevel,
		"status":       u.KYCStatus,
		"cpf":          u.CPF,
		"legal_name":   u.LegalName,
		"birth_date":   u.BirthDate,
		"phone_number": u.PhoneNumber,
	})
}

// sendStatus is the single response shape of every user-facing KYC write, so
// the client never has to re-fetch to learn the new state.
func (h *KYCHandler) sendStatus(c fiber.Ctx, userID string) error {
	st, err := h.kycSvc.Get(c.Context(), userID)
	if err != nil {
		return h.sendKYCError(c, err)
	}
	return c.JSON(st)
}

// sendKYCError maps every domain error of this package to its RFC 7807 problem.
func (h *KYCHandler) sendKYCError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, kyc.ErrInvalidCPF):
		return apierror.ValidationFailed("cpf: invalid CPF.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidBirthDate):
		return apierror.ValidationFailed("birth_date: invalid date.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidPhone):
		return apierror.ValidationFailed("phone_number: invalid phone number.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidMethod):
		return apierror.ValidationFailed("method: document verification is not available.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidDocumentType):
		return apierror.ValidationFailed("type: unsupported document type.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidContentType):
		return apierror.ValidationFailed("content_type: unsupported document content type.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidDecision):
		return apierror.ValidationFailed("decision: must be approve or reject.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrUnderage):
		return apierror.AgeRequirementNotMet(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrAlreadyVerified):
		return apierror.KYCAlreadyVerified(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrBasicLocked):
		return apierror.KYCAlreadyVerified(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrBasicRequired):
		return apierror.KYCBasicRequired(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrCPFConflict):
		return apierror.CPFAlreadyRegistered(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrSubmissionLocked):
		return apierror.KYCSubmissionLocked(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrNotSubmitted), errors.Is(err, kyc.ErrNoDocuments), errors.Is(err, kyc.ErrNoOTPPending):
		return apierror.KYCNotSubmitted(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidCode), errors.Is(err, kyc.ErrTooManyAttempts):
		return apierror.KYCInvalidCode(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrResendCooldown):
		return apierror.KYCResendCooldown(kyc.OTPResendCooldown, c.Path()).Send(c)
	case errors.Is(err, kyc.ErrPhoneVerificationUnavailable):
		return apierror.KYCPhoneVerificationUnavailable(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrDocumentNotUploaded):
		return apierror.KYCDocumentNotUploaded(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrDocumentTooLarge):
		return apierror.KYCDocumentTooLarge(kyc.MaxDocumentBytes, c.Path()).Send(c)
	case errors.Is(err, kyc.ErrTooManyDocuments):
		return apierror.ValidationFailed("documents: too many documents for this submission.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrDocumentTypeMismatch):
		return apierror.KYCDocumentNotUploaded(c.Path()).Send(c)
	case errors.Is(err, user.ErrNotFound):
		return apierror.NotFound("User", c.Path()).Send(c)
	}
	return apierror.ServerError(c.Path()).Send(c)
}
```

- [ ] **Step 2: Build-check**

Run: `cd api && go build ./internal/handler/...`
Expected: FAIL only in `kyc_test.go` and `testhelpers_test.go` (Task 12 rewrites both) — confirm no error originates
from `kyc.go` itself.

- [ ] **Step 3: Commit deferred to Task 12** (the package must build+test green first)

---

## Task 12: `handler` KYC integration tests + shared test fixtures

Implements spec §13 (`handler/kyc_test.go` bullet).

**Files:**

- Modify: `api/internal/handler/testhelpers_test.go` (the `memKYCRepo` fixture + `newTestApp` wiring)
- Modify: `api/internal/handler/kyc_test.go` (full rewrite of the KYC-specific tests; the client-credentials tests at
  the top of the file are unrelated to this spec and must be left untouched)

**Interfaces:**

- Consumes: everything from Tasks 1, 3, 8, 9, 11.

- [ ] **Step 1: Rewrite `memKYCRepo` in `testhelpers_test.go` to satisfy the new `kyc.Repository`**

Locate the existing `memKYCRepo` type and its methods (`GetUser`, `SavePendingDocument`, `GetPendingDocument`,
`DeletePendingDocument`, `SaveSubmission`, `AddDocument`, `MarkVerified`, `MarkRejected`, `ListPendingKYC`) and replace
them with:

```go
// memKYCRepo implements kyc.Repository over the shared memUserRepo store with
// small helper fields for the CPF-uniqueness index and pending upload intents.
type memKYCRepo struct {
	users   *memUserRepo
	cpfs    map[string]string
	pending map[string]*kycDomain.PendingDocument
}

func newMemKYCRepo(users *memUserRepo) *memKYCRepo {
	return &memKYCRepo{users: users, cpfs: map[string]string{}, pending: map[string]*kycDomain.PendingDocument{}}
}

func (m *memKYCRepo) GetUser(ctx context.Context, userID string) (*userDomain.User, error) {
	return m.users.GetByID(ctx, userID)
}

func (m *memKYCRepo) SaveBasicSubmission(ctx context.Context, userID string, rec kycDomain.BasicRecord, oldCPF string) error {
	if owner, taken := m.cpfs[rec.CPF]; taken && owner != userID {
		return kycDomain.ErrCPFConflict
	}
	if oldCPF != "" && oldCPF != rec.CPF {
		delete(m.cpfs, oldCPF)
	}
	m.cpfs[rec.CPF] = userID

	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.CPF, u.LegalName, u.BirthDate, u.PhoneNumber = rec.CPF, rec.LegalName, rec.BirthDate, rec.PhoneNumber
	u.KYCLevel, u.KYCStatus = kycDomain.LevelBasic, kycDomain.StatusPending
	u.KYCSubmittedAt = rec.SubmittedAt
	u.KYCRejectionReason, u.PhoneVerifiedAt = "", ""
	return m.users.replace(u)
}

func (m *memKYCRepo) MarkPhoneVerified(ctx context.Context, userID, verifiedAt string) error {
	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.KYCStatus, u.PhoneVerifiedAt, u.KYCBasicVerifiedAt = kycDomain.StatusVerified, verifiedAt, verifiedAt
	return m.users.replace(u)
}

func (m *memKYCRepo) SavePendingDocument(_ context.Context, userID, documentID, docType, contentType string) error {
	m.pending[documentID] = &kycDomain.PendingDocument{UserID: userID, Type: docType, ContentType: contentType}
	return nil
}

func (m *memKYCRepo) GetPendingDocument(_ context.Context, documentID string) (*kycDomain.PendingDocument, error) {
	return m.pending[documentID], nil
}

func (m *memKYCRepo) DeletePendingDocument(_ context.Context, documentID string) error {
	delete(m.pending, documentID)
	return nil
}

func (m *memKYCRepo) AddDocument(ctx context.Context, userID string, doc kycDomain.Document) error {
	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.KYCDocuments = append(u.KYCDocuments, doc)
	return m.users.replace(u)
}

func (m *memKYCRepo) SaveEnhancedSubmission(ctx context.Context, userID, submittedAt, expiresAt string) error {
	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.KYCLevel, u.KYCStatus = kycDomain.LevelEnhanced, kycDomain.StatusPending
	u.KYCSubmittedAt, u.KYCExpiresAt, u.KYCRejectionReason = submittedAt, expiresAt, ""
	return m.users.replace(u)
}

func (m *memKYCRepo) MarkVerified(ctx context.Context, userID, verifiedAt string) error {
	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.KYCStatus, u.KYCVerifiedAt, u.KYCRejectionReason = kycDomain.StatusVerified, verifiedAt, ""
	return m.users.replace(u)
}

func (m *memKYCRepo) MarkRejected(ctx context.Context, userID, reason string) error {
	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.KYCStatus, u.KYCRejectionReason = kycDomain.StatusRejected, reason
	u.KYCDocuments = nil
	return m.users.replace(u)
}

func (m *memKYCRepo) SaveRiskAssessment(ctx context.Context, userID string, a riskDomain.Assessment) error {
	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.KYCRiskScore, u.KYCRiskEvaluatedAt = a.Score, a.EvaluatedAt
	return m.users.replace(u)
}

func (m *memKYCRepo) ListPendingKYC(ctx context.Context) ([]*userDomain.User, error) {
	all, err := m.users.all(ctx)
	if err != nil {
		return nil, err
	}
	var out []*userDomain.User
	for _, u := range all {
		if u.KYCLevel == kycDomain.LevelEnhanced && u.KYCStatus == kycDomain.StatusPending {
			out = append(out, u)
		}
	}
	return out, nil
}
```

If `memUserRepo` does not already expose a `replace(u *userDomain.User) error` helper or an
`all(ctx) ([]*userDomain.User, error)` helper, add them (small additions — `replace` is a keyed map write, `all` returns
every stored value; follow whatever the existing `memUserRepo` internal storage field is named). Add
`riskDomain "gopkg.aoctech.app/account/api/internal/domain/risk"` to the file's imports.

Locate the `newOAuthTestApp`/`newTestApp` constructor's
`kycSvc := kycDomain.NewService(newMemKYCRepo(userRepo), kycPresigner)` line and update it to:

```go
	kycSvc := kycDomain.NewService(newMemKYCRepo(userRepo), kycPresigner, cache.NewInMemory(), &fakeOTPSender{}, riskDomain.NoopEvaluator{})
```

Add a `fakeOTPSender` type (records sent codes, same shape as `fakeSMS` in `kyc/service_test.go` but exported within the
`handler_test` package) near the other test doubles in `testhelpers_test.go`:

```go
// fakeOTPSender captures the last code sent per phone number so tests can
// read it back without a real SNS call.
type fakeOTPSender struct {
	sent map[string]string // phone -> last code
}

func (f *fakeOTPSender) SendOTP(_ context.Context, phone, code string) error {
	if f.sent == nil {
		f.sent = map[string]string{}
	}
	f.sent[phone] = code
	return nil
}
```

Add `f := &fakeOTPSender{}` to the constructor's local variables and store it on `testApp` (new field
`otpSender *fakeOTPSender`) so tests can read `ta.otpSender.sent[phone]`.

- [ ] **Step 2: Build-check the test helpers alone**

Run: `cd api && go vet ./internal/handler/... 2>&1 | head -60`
Expected: remaining errors all originate from `kyc_test.go` (Step 3 rewrites it).

- [ ] **Step 3: Rewrite the KYC section of `kyc_test.go`**

Keep the file's client-credentials tests (`TestClientCredentialsIssuesToken` through
`TestClientCredentialsClampsScopes`) and the imports/helpers they use (`seedM2MClient`, `clientCredentialsForm`,
`decodeJWTPayload`) exactly as-is. Replace everything from the `// ── KYC route tests` comment onward with:

```go
// ── KYC route tests (user-facing + internal) ────────────────────────────────

const validCPF = "52998224725"
const otherValidCPF = "11144477735"
const validPhone = "+5511987654321"

func submitBasicBody(cpf string) map[string]any {
	return map[string]any{
		"cpf": cpf, "legal_name": "Fulano da Silva", "birth_date": "1990-01-01", "phone_number": validPhone,
	}
}

// verifyBasicPhone drives POST /kyc/basic → reads the OTP the fake sender
// captured → POST /kyc/basic/verify-phone. Returns the final status body.
func verifyBasicPhone(t *testing.T, ta *testApp, token, cpf string) map[string]any {
	t.Helper()
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(cpf), token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit basic: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	code, ok := ta.otpSender.sent[validPhone]
	if !ok {
		t.Fatal("no OTP was sent to validPhone")
	}
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic/verify-phone", map[string]string{"code": code}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify phone: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var st map[string]any
	readJSON(t, resp, &st)
	return st
}

func uploadKYCDocument(t *testing.T, ta *testApp, userID, token, docType string) map[string]any {
	t.Helper()

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": docType, "content_type": "image/png"}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presign(%s): expected 200, got %d: %s", docType, resp.StatusCode, bodyString(resp))
	}
	var presigned map[string]any
	readJSON(t, resp, &presigned)

	documentID, _ := presigned["document_id"].(string)
	if documentID == "" || presigned["upload_url"] == "" {
		t.Fatalf("presign response = %v", presigned)
	}

	ta.kycPresigner.putObject(kycDomain.BuildDocumentKey(userID, documentID), 2048)

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents/confirm",
		map[string]string{"document_id": documentID, "type": docType}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm document(%s): expected 200, got %d: %s", docType, resp.StatusCode, bodyString(resp))
	}
	var st map[string]any
	readJSON(t, resp, &st)
	return st
}

func uploadAllRequiredKYCDocuments(t *testing.T, ta *testApp, userID, token string) map[string]any {
	t.Helper()
	var st map[string]any
	for _, docType := range kycDomain.RequiredDocTypes {
		st = uploadKYCDocument(t, ta, userID, token, docType)
	}
	return st
}

func TestSubmitBasicRequiresStepUp(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-stepup@example.com", "Password!123", "Fulano")
	stale := ta.issueStaleToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), stale)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if !strings.HasSuffix(body["type"].(string), "step-up-required") {
		t.Fatalf("body = %v", body)
	}
}

func TestKYCFullFlow(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-flow@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	// 1. Basic: submit → OTP sent → verify → basic_verified.
	st := verifyBasicPhone(t, ta, token, validCPF)
	if st["state"] != "basic_verified" || st["level"] != "basic" {
		t.Fatalf("status after phone verify = %v", st)
	}

	// 2. Enhanced: upload every required document, then submit → under review.
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit enhanced: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	readJSON(t, resp, &st)
	if st["state"] != "under_review" {
		t.Fatalf("status after enhanced submit = %v", st)
	}

	// 3. get → masked CPF, masked phone.
	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	readJSON(t, resp, &st)
	if st["cpf_masked"] != "***.***.***-25" || st["state"] != "under_review" {
		t.Fatalf("status = %v", st)
	}

	// 4. A human reviewer approves via cmd/kyc (Service.Review directly).
	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionApprove, ""); err != nil {
		t.Fatalf("Review: %v", err)
	}

	// 5. get → verified, kyc_level claim reachable as "verified".
	resp = ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	readJSON(t, resp, &st)
	if st["state"] != "verified" || st["verified_at"] == "" {
		t.Fatalf("status after approval = %v", st)
	}

	// 6. internal get → full CPF + phone, for ctech-wallet withdrawal-key validation.
	m2m := ta.issueMachineToken(t, "wallet", []string{"internal:wallet:confirm-deposit"})
	resp = ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+u.ID(), nil, m2m)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("internal get: expected 200, got %d", resp.StatusCode)
	}
	var full map[string]any
	readJSON(t, resp, &full)
	if full["cpf"] != validCPF || full["phone_number"] != validPhone || full["level"] != "enhanced" {
		t.Fatalf("internal record = %v", full)
	}
}

func TestSubmitBasicValidation(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-val@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody("52998224724"), token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad cpf: expected 422, got %d", resp.StatusCode)
	}

	body := submitBasicBody(validCPF)
	body["birth_date"] = time.Now().UTC().AddDate(-17, 0, 0).Format("2006-01-02")
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", body, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("underage: expected 422, got %d", resp.StatusCode)
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "age-requirement-not-met") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestSubmitBasicDuplicateCPFConflict(t *testing.T) {
	ta := newTestApp(t)
	u1 := ta.registerUser(t, "kyc-dup1@example.com", "Password!123", "Fulano")
	u2 := ta.registerUser(t, "kyc-dup2@example.com", "Password!123", "Beltrano")
	token1, token2 := ta.issueToken(t, u1.ID()), ta.issueToken(t, u2.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), token1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first submit: %d", resp.StatusCode)
	}
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), token2)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: expected 409, got %d", resp.StatusCode)
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "cpf-already-registered") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestVerifyPhoneRejectsWrongCode(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-wrongcode@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), token)
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic/verify-phone", map[string]string{"code": "000000"}, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-invalid-code") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestResendCodeCooldown(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-resend@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic", submitBasicBody(validCPF), token)
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/basic/resend-code", nil, token)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-resend-cooldown") {
		t.Fatalf("problem = %v", problem)
	}
	if problem["retry_after_seconds"] == nil {
		t.Fatal("retry_after_seconds must be present")
	}
}

func TestSubmitEnhancedRequiresBasicVerified(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-nobasic@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-basic-required") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestDocumentUploadRequiresBasicVerified(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-nobasic@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-basic-required") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestSubmitEnhancedRejectsWithoutDocuments(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-nodocs@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-not-submitted") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestResubmitEnhancedWhilePendingConflicts(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-locked@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first submit: %d: %s", resp.StatusCode, bodyString(resp))
	}
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resubmit: expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var problem map[string]any
	readJSON(t, resp, &problem)
	if !strings.HasSuffix(problem["type"].(string), "kyc-submission-locked") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestKYCDocumentFlowRejectedRequiresFreshUploads(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-reject@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)
	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)

	if err := ta.kycSvc.Review(context.Background(), u.ID(), kycDomain.DecisionReject, "document unreadable"); err != nil {
		t.Fatalf("Review: %v", err)
	}

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/kyc", nil, token)
	var st map[string]any
	readJSON(t, resp, &st)
	if st["state"] != "rejected" || st["rejection_reason"] != "document unreadable" {
		t.Fatalf("status after rejection = %v", st)
	}

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resubmit without fresh docs: expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	uploadAllRequiredKYCDocuments(t, ta, u.ID(), token)
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/enhanced", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resubmit after fresh uploads: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestConfirmDocumentWithoutUploadRejected(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-noupload@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())
	verifyBasicPhone(t, ta, token, validCPF)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, token)
	var presigned map[string]any
	readJSON(t, resp, &presigned)

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents/confirm",
		map[string]string{"document_id": presigned["document_id"].(string), "type": "id_front"}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestPresignDocumentRequiresStepUp(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-doc-stepup@example.com", "Password!123", "Fulano")
	stale := ta.issueStaleToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/kyc/documents",
		map[string]string{"type": "id_front", "content_type": "image/png"}, stale)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestInternalKYCRejectsUserToken(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "kyc-usertoken@example.com", "Password!123", "Fulano")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/"+u.ID(), nil, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestInternalKYCRejectsMissingScope(t *testing.T) {
	ta := newTestApp(t)
	m2m := ta.issueMachineToken(t, "wallet", []string{"openid"})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/internal/kyc/u1", nil, m2m)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAccessTokenCarriesKYCLevelAfterRefresh(t *testing.T) {
	ta := newOAuthTestApp(t)
	secretHash, _ := crypto.HashPassword("web-secret")
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("web"), ClientType: "confidential",
		ClientSecretHash: secretHash,
		AllowedScopes:    []string{"openid", "profile", "email", "kyc"},
		FirstParty:       true,
	})
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-kyc", Email: "kyc@example.com", EmailVerified: true,
		CPF: validCPF, KYCLevel: kycDomain.LevelEnhanced, KYCStatus: kycDomain.StatusVerified,
	})
	_, _, err := ta.sessionSvc.Create(context.Background(), "user-kyc", "Chrome", "1.2.3.4", "UA", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessions, _ := ta.sessionSvc.List(context.Background(), "user-kyc")
	refreshToken, err := ta.sessionSvc.IssueClientToken(context.Background(), "user-kyc", sessions[0].ID(), "web", []string{"openid", "profile", "email", "kyc"})
	if err != nil {
		t.Fatalf("issue client token: %v", err)
	}

	resp := ta.postForm("/v1.0/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {"web"},
		"client_secret": {"web-secret"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	claims := decodeJWTPayload(t, body["access_token"].(string))
	if claims["kyc_level"] != "verified" {
		t.Fatalf("kyc_level = %v", claims["kyc_level"])
	}
}
```

Note: this rewrite drops the old `TestSubmitKYCRequiresAddress`/`TestSubmitKYCRejectsUnknownState` tests (address no
longer exists) and the old `TestKYCStatusExposesAwaitingFilesState` test (that intermediate doc status no longer
exists — documents may now accumulate freely while `basic_verified`, with no distinct state).
`TestSubmitAfterVerifiedConflict`'s intent is preserved by `TestSubmitBasicLockedOnceVerified` already covered at the
`kyc.Service` unit-test layer (Task 10); it is not duplicated here to avoid redundant coverage across layers for the
same rule.

- [ ] **Step 4: Run the full handler test suite**

Run: `cd api && go test ./internal/handler/... -v -run TestSubmit -v; cd api && go test ./internal/handler/... -v`
Expected: PASS

- [ ] **Step 5: Run the whole module's tests**

Run: `cd api && go build ./... && go test ./...`
Expected: PASS across every package.

- [ ] **Step 6: Commit**

```bash
cd api && git add internal/handler
git commit -m "feat: add Basic/OTP/Enhanced KYC routes, rewrite KYC integration tests"
```

---

## Task 13: `handler/token.go` — use `kyc.ClaimLevel` at both choke points

Implements spec §5.

**Files:**

- Modify: `api/internal/handler/token.go`

**Interfaces:**

- Consumes: `kyc.ClaimLevel(level, status string) string` from Task 1.

- [ ] **Step 1: Update `kycClaimFor` (around line 452-459)**

```go
// kycClaimFor returns the token-facing kyc_level claim when the kyc scope was
// granted; empty otherwise so the claim is omitted from the token.
func kycClaimFor(u *user.User, scp []string) string {
	if u == nil || !slices.Contains(scp, scopes.KYC) {
		return ""
	}
	return kyc.ClaimLevel(u.KYCLevel, u.KYCStatus)
}
```

Add `"gopkg.aoctech.app/account/api/internal/domain/kyc"` to the file's imports.

- [ ] **Step 2: Update the inline refresh-grant block (around line 379-386)**

Replace:

```go
	kycLevel := ""
	if slices.Contains(scp, scopes.KYC) {
		u, err := h.userSvc.GetByID(c.Context(), sess.UserID())
		if err != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}
		kycLevel = u.KYCLevel
	}
```

with:

```go
	kycLevel := ""
	if slices.Contains(scp, scopes.KYC) {
		u, err := h.userSvc.GetByID(c.Context(), sess.UserID())
		if err != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}
		kycLevel = kyc.ClaimLevel(u.KYCLevel, u.KYCStatus)
	}
```

- [ ] **Step 3: Build-check**

Run: `cd api && go build ./internal/handler/...`
Expected: PASS

- [ ] **Step 4: Run the existing claim regression test**

Run: `cd api && go test ./internal/handler/... -run TestAccessTokenCarriesKYCLevelAfterRefresh -v`
Expected: PASS (this test was already updated in Task 12 to seed
`KYCLevel: kyc.LevelEnhanced, KYCStatus: kyc.StatusVerified` and assert `claims["kyc_level"] == "verified"` — it now
exercises `ClaimLevel` specifically).

- [ ] **Step 5: Commit**

```bash
cd api && git add internal/handler/token.go
git commit -m "feat: derive kyc_level claim via kyc.ClaimLevel at token issuance"
```

---

## Task 14: `handler/userinfo.go` — use `kyc.ClaimLevel`

Implements spec §5.

**Files:**

- Modify: `api/internal/handler/userinfo.go`
- Test: `api/internal/handler/userinfo_test.go` (new file — none existed before)

**Interfaces:**

- Consumes: `kyc.ClaimLevel` from Task 1.

- [ ] **Step 1: Update `UserInfo`**

Replace:

```go
	if middleware.HasScope(c, scopes.KYC) && u.KYCLevel != "" {
		resp["kyc_level"] = u.KYCLevel
	}
```

with:

```go
	if middleware.HasScope(c, scopes.KYC) {
		if level := kyc.ClaimLevel(u.KYCLevel, u.KYCStatus); level != "" {
			resp["kyc_level"] = level
		}
	}
```

Add `"gopkg.aoctech.app/account/api/internal/domain/kyc"` to the file's imports.

- [ ] **Step 2: Write the failing test**

Create `api/internal/handler/userinfo_test.go`:

```go
package handler_test

import (
	"context"
	"net/http"
	"testing"

	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

func TestUserInfoOmitsKYCLevelWhenEnhancedPending(t *testing.T) {
	ta := newOAuthTestApp(t)
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_userinfo-pending", Email: "userinfo-pending@example.com", EmailVerified: true,
		KYCLevel: kycDomain.LevelEnhanced, KYCStatus: kycDomain.StatusPending,
	})
	token := ta.issueTokenWithScopes(t, "userinfo-pending", []string{"openid", "kyc"})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/userinfo", nil, token)
	var body map[string]any
	readJSON(t, resp, &body)
	if level, present := body["kyc_level"]; present && level != "" {
		t.Fatalf("kyc_level = %v, want basic (enhanced/pending still keeps basic access)", level)
	}
}

func TestUserInfoReportsVerifiedKYCLevel(t *testing.T) {
	ta := newOAuthTestApp(t)
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_userinfo-verified", Email: "userinfo-verified@example.com", EmailVerified: true,
		KYCLevel: kycDomain.LevelEnhanced, KYCStatus: kycDomain.StatusVerified,
	})
	token := ta.issueTokenWithScopes(t, "userinfo-verified", []string{"openid", "kyc"})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/userinfo", nil, token)
	var body map[string]any
	readJSON(t, resp, &body)
	if body["kyc_level"] != "verified" {
		t.Fatalf("kyc_level = %v, want verified", body["kyc_level"])
	}
}
```

If `testApp` has no `issueTokenWithScopes(userID string, scopes []string) string` helper yet, check
`testhelpers_test.go` for the closest existing token-issuing helper (`issueToken`, `issueMachineToken`) and add a small
`issueTokenWithScopes` that mirrors `issueToken`'s signing call but accepts an explicit scope slice instead of a
hardcoded default set — this is a small, focused addition, not a new pattern.

- [ ] **Step 3: Run it to verify the enhanced/pending case fails against the old code**

(This step is about verifying you're testing the right thing — since Step 1 and Step 2 are written together above, run
after both are in place.)

Run: `cd api && go test ./internal/handler/... -run TestUserInfo -v`
Expected: PASS (both tests). If `TestUserInfoOmitsKYCLevelWhenEnhancedPending` fails, it means Step 1's `kyc.ClaimLevel`
substitution is missing or wrong — the pre-fix code (`u.KYCLevel != ""`) would have leaked the raw `"enhanced"` string,
so this test is the one that would have caught that regression.

- [ ] **Step 4: Commit**

```bash
cd api && git add internal/handler/userinfo.go internal/handler/userinfo_test.go internal/handler/testhelpers_test.go
git commit -m "feat: derive kyc_level claim via kyc.ClaimLevel in /userinfo"
```

---

## Task 15: `cmd/kyc` — scope to Enhanced, print phone/risk

Implements spec §7 (cmd/kyc row).

**Files:**

- Modify: `api/cmd/kyc/main.go`

**Interfaces:**

- Consumes: `kyc.Service.ListPendingKYC/Review/DocumentURLs/GetUser` (unchanged signatures from Task 9),
  `kyc.NewService` (new signature from Task 9).

- [ ] **Step 1: Update the `kyc.NewService` call**

```go
	kycSvc := kycDomain.NewService(kycRepo, presigner, cache.NewInMemory(), nil, riskDomain.NoopEvaluator{})
```

Add imports `"gopkg.aoctech.app/account/api/internal/cache"` and
`riskDomain "gopkg.aoctech.app/account/api/internal/domain/risk"`. `cache.NewInMemory()` and a `nil` `OTPSender` are
safe here: `cmd/kyc`'s four subcommands (`list`/`show`/`approve`/`reject`) never call `SubmitBasic`/`VerifyPhone`/
`ResendCode`, so the OTP/cache path is never exercised.

- [ ] **Step 2: Update `runShow` to print phone and risk fields**

Replace the `fmt.Printf` block in `runShow` with:

```go
	fmt.Printf("user_id:      %s\n", u.ID())
	fmt.Printf("legal_name:   %s\n", u.LegalName)
	fmt.Printf("cpf:          %s\n", u.CPF)
	fmt.Printf("birth_date:   %s\n", u.BirthDate)
	fmt.Printf("phone_number: %s\n", u.PhoneNumber)
	fmt.Printf("level:        %s\n", u.KYCLevel)
	fmt.Printf("status:       %s\n", u.KYCStatus)
	fmt.Printf("submitted_at: %s\n", u.KYCSubmittedAt)
	fmt.Printf("risk_score:   %d\n", u.KYCRiskScore)
	fmt.Printf("risk_signals: %v\n", u.KYCRiskSignals)
```

(`runList`, `runReview`, and the `usage`/`main` dispatch need no changes — `ListPendingKYC` is already scoped to
`enhanced`+`pending` by Task 8's repository rewrite, and `Review` already gates on the same pair per Task 9.)

- [ ] **Step 3: Build-check**

Run: `cd api && go build ./cmd/kyc/...`
Expected: PASS

- [ ] **Step 4: Manual smoke test (no automated test — this is an operator CLI, matching the existing testing posture
  for `cmd/kyc`)**

Run: `cd api && AWS_REGION=us-east-1 TABLE_PREFIX=dev go run ./cmd/kyc list` against a local/dev table if one is
available; otherwise skip and rely on Task 12's `handler` integration tests, which already exercise `Service.Review`/
`ListPendingKYC`/`DocumentURLs` end-to-end.

- [ ] **Step 5: Commit**

```bash
cd api && git add cmd/kyc/main.go
git commit -m "feat: cmd/kyc show prints phone number and risk assessment"
```

---

## Task 16: `cmd/api/main.go` — wire sms/risk/config into `kyc.Service`

Implements spec §8, §9, §11 (deploy note is Task 19).

**Files:**

- Modify: `api/cmd/api/main.go`

**Interfaces:**

- Consumes: `sms.New`/`sms.Client` from Task 4, `risk.NoopEvaluator` from Task 3, `cfg.PhoneVerificationEnabled` from
  Task 5, `kyc.NewService`'s new signature from Task 9.

- [ ] **Step 1: Add the SMS client and risk evaluator, update the `kyc.NewService` call**

Locate the existing `kycSvc := kycDomain.NewService(kycRepo, kycPresigner)` line (right after the `kycPresigner`/
`KYC_DOCUMENTS_BUCKET` block) and replace the surrounding block with:

```go
	// KYC document uploads need a bucket; without one, Enhanced document
	// verification is unavailable entirely (see kyc.Service.DocumentsEnabled).
	var kycPresigner kycDomain.Presigner
	if cfg.KYCDocumentsBucket != "" {
		s3Cli, err := storage.NewS3(context.Background(), cfg.AWSRegion, cfg.KYCDocumentsBucket)
		if err != nil {
			log.Fatalf("initializing KYC document storage: %v", err)
		}
		kycPresigner = s3Cli
	} else {
		log.Println("KYC_DOCUMENTS_BUCKET not set — Enhanced document verification disabled")
	}

	// SMS OTP delivery needs PHONE_VERIFICATION_ENABLED=true; without it, every
	// Basic/OTP route hard-blocks with 503 (see kyc.Service.PhoneVerificationEnabled).
	var smsClient kycDomain.OTPSender
	if cfg.PhoneVerificationEnabled {
		cli, err := sms.New(context.Background(), cfg.AWSRegion)
		if err != nil {
			log.Fatalf("initializing SMS client: %v", err)
		}
		smsClient = cli
	} else {
		log.Println("PHONE_VERIFICATION_ENABLED=false — phone verification disabled")
	}

	kycSvc := kycDomain.NewService(kycRepo, kycPresigner, valkeyClient, smsClient, risk.NoopEvaluator{})
```

Add imports `"gopkg.aoctech.app/account/api/internal/domain/risk"` and `"gopkg.aoctech.app/account/api/internal/sms"`.

- [ ] **Step 2: Build-check**

Run: `cd api && go build ./...`
Expected: PASS — this is the point where the whole module compiles cleanly end-to-end.

- [ ] **Step 3: Run the complete test suite**

Run: `cd api && go test ./...`
Expected: PASS across every package.

- [ ] **Step 4: Commit**

```bash
cd api && git add cmd/api/main.go
git commit -m "feat: wire sms client and risk evaluator into kyc.Service"
```

---

## Task 17: `legal` — bump ToS/Privacy version

Implements spec §10 (phone number is now collected, so acceptance scope changed).

**Files:**

- Modify: `api/internal/legal/version.go`

**Interfaces:** none new — `legal.CurrentToSVersion`/`CurrentPrivacyVersion` are read by existing callers unchanged.

- [ ] **Step 1: Bump both constants**

```go
const (
	CurrentToSVersion     = "3.1"
	CurrentPrivacyVersion = "3.1"
)
```

- [ ] **Step 2: Run the existing version test**

Run: `cd api && go test ./internal/legal/... -v`
Expected: PASS (the test in `version_test.go` uses `legal.CurrentToSVersion`/`CurrentPrivacyVersion` symbolically, not a
hardcoded string, so the bump doesn't require a test change).

- [ ] **Step 3: Commit**

```bash
cd api && git add internal/legal/version.go
git commit -m "chore: bump ToS/Privacy version for phone number collection at Basic KYC"
```

---

## Task 18: `cdk` — `sns:Publish` IAM permission

Implements spec §11.

**Files:**

- Modify: `cdk/lib/iam-stack.ts`

**Interfaces:** none — this only widens the existing `appRole`'s policy.

- [ ] **Step 1: Add the SNS policy statement**

In `cdk/lib/iam-stack.ts`, after the existing SES policy statement (`ses:SendEmail`/`ses:SendRawEmail`), add:

```typescript
    // SNS — phone-verification OTP SMS (direct-to-phone-number Publish).
    // Resource must be * — a direct Publish to a PhoneNumber has no topic or
    // other ARN to scope to; this is the documented AWS pattern, not a
    // least-privilege gap.
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['sns:Publish'],
      resources: ['*'],
    }));
```

- [ ] **Step 2: Synth-check**

Run: `cd cdk && npm install && cdk synth`
Expected: synthesizes cleanly; inspect the generated `IAMStack` template to confirm the new `sns:Publish` statement with
`Resource: "*"` is present on `AppRole`.

- [ ] **Step 3: Commit**

```bash
cd cdk && git add lib/iam-stack.ts
git commit -m "feat: grant sns:Publish for phone-verification OTP delivery"
```

---

## Task 19: `README.md` — document the new routes/config/deploy note

Implements spec §7, §8, §11 (documentation obligations from `api/CLAUDE.md` and `cdk/CLAUDE.md`).

**Files:**

- Modify: `README.md` (root)

**Interfaces:** none — documentation only.

- [ ] **Step 1: Replace the KYC route table rows (around lines 107-111)**

```markdown
| `GET`    | `/v1.0/account/kyc`                                 | Bearer   | KYC status: `{state, level, cpf_masked, legal_name, birth_date, phone_masked, basic_verified_at, documents, rejection_reason, submitted_at, expires_at, verified_at}` |
| `POST`   | `/v1.0/account/kyc/basic`                           | Bearer + step-up | Submit Basic identity data `{cpf, legal_name, birth_date, phone_number}` → validates CPF/age/phone, sends an SMS OTP, `basic/pending` |
| `POST`   | `/v1.0/account/kyc/basic/verify-phone`              | Bearer + step-up | `{code}` → `basic/verified` on a correct 6-digit code |
| `POST`   | `/v1.0/account/kyc/basic/resend-code`               | Bearer + step-up | Resends the OTP; 60s cooldown (`429` + `Retry-After`-equivalent `retry_after_seconds`) |
| `POST`   | `/v1.0/account/kyc/documents`                       | Bearer + step-up | `{type, content_type}` → `{document_id, upload_url}` — presigned S3 PUT; `type` one of `id_front`, `id_back`, `selfie_with_document`; requires `basic/verified` first |
| `POST`   | `/v1.0/account/kyc/documents/confirm`               | Bearer + step-up | `{document_id, type}` → records the upload (verified via HeadObject) |
| `POST`   | `/v1.0/account/kyc/enhanced`                        | Bearer + step-up | Finalizes an Enhanced submission once all 3 documents are uploaded → `enhanced/pending` |
| `GET`    | `/v1.0/internal/kyc/:user_id`                       | Service token (`internal:account:kyc`) | Full unmasked identity record incl. `phone_number` (ctech-wallet withdrawal-key validation) |
```

- [ ] **Step 2: Rewrite the "KYC (identity verification)" narrative section (around lines 332-388)**

Replace the section with a description of the two-level Basic/Enhanced flow, the SMS OTP mechanics (Valkey keys, TTLs,
cooldown, max attempts), the `PHONE_VERIFICATION_ENABLED` hard-block behavior, the risk-score informational hook, and
the updated `cmd/kyc` usage (unchanged commands, now scoped to `enhanced`/`pending`). Base the wording on spec §4, §8,
§9 — keep it at the same level of detail as the section it replaces (a few paragraphs, not exhaustive).

- [ ] **Step 3: Update the config vars table (around line 450)**

Add a row after `KYC_DOCUMENTS_BUCKET`:

```markdown
| `PHONE_VERIFICATION_ENABLED` | No | `false` unless set to `true`. Gates AWS SNS phone verification — while false, every `/kyc/basic*` route returns `503`. Flip once production SNS SMS access is granted; no redeploy needed beyond the env var |
```

- [ ] **Step 4: Update the `kyc` OIDC scope description (around lines 173-174)**

Change "`kyc` adds the `kyc_level` claim (`""` \| `verified`) to access tokens, id_tokens" to "`kyc` adds the
`kyc_level` claim (`""` \| `"basic"` \| `"verified"`) to access tokens, id_tokens".

- [ ] **Step 5: Add the SNS first-deploy note**

In the "First Deploy" checklist, add a step (after the KYC documents bucket note, if one exists, otherwise near the
end): "Once AWS grants production SNS SMS access, run `aws sns set-sms-attributes` once per account/region to set the
monthly spend limit, then set `PHONE_VERIFICATION_ENABLED=true` in SSM — this is outside CDK's scope (no such resource
exists to provision)."

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs: document Basic/Enhanced KYC routes, PHONE_VERIFICATION_ENABLED, SNS deploy note"
```

---

## Task 20: `ui/src/lib/types.ts` — Basic/Enhanced KYC types

Implements spec §6, §7, §10.

**Files:**

- Modify: `ui/src/lib/types.ts`

**Interfaces:**

- Produces: `KYCLevel = '' | 'basic' | 'enhanced'`, `KYCState` (6 values),
  `KYCDocumentType = 'id_front' | 'id_back' | 'selfie_with_document'`, `KYCStatus` (no `address`/`method`, adds
  `phone_masked`/`basic_verified_at`), `KYCBasicSubmission`. Removes `Address`, `KYCMethod`. Consumed by every UI task
  below.

- [ ] **Step 1: Replace the KYC-related types**

Remove the `KYCMethod` type and the `Address` interface entirely. Replace `KYCLevel`, `KYCState`, `KYCDocumentType`,
`KYCStatus`, `KYCSubmission` with:

```ts
export type KYCLevel = '' | 'basic' | 'enhanced'

/** Derived by the API from level+status+expiry — branch on this. */
export type KYCState =
  | 'not_started'
  | 'awaiting_phone_verification'
  | 'basic_verified'
  | 'under_review'
  | 'rejected'
  | 'verified'

/** A static photo holding the document replaces the old four-clip video liveness check. */
export type KYCDocumentType = 'id_front' | 'id_back' | 'selfie_with_document'

export interface KYCDocument {
  id: string
  type: KYCDocumentType
  uploaded_at: string
}

export interface KYCStatus {
  state: KYCState
  level: KYCLevel
  cpf_masked?: string
  legal_name?: string
  birth_date?: string
  phone_masked?: string
  basic_verified_at?: string
  documents?: KYCDocument[]
  rejection_reason?: string
  submitted_at?: string
  expires_at?: string
  verified_at?: string
}

export interface KYCBasicSubmission {
  cpf: string
  legal_name: string
  birth_date: string
  phone_number: string
}
```

`PresignedUpload` is unchanged — leave it as-is.

- [ ] **Step 2: Build-check (this alone won't compile the package — consumers still reference the old shapes)**

Run: `cd ui && npx tsc --noEmit 2>&1 | head -40`
Expected: errors from every file Tasks 21-29 update — confirm no error originates from `types.ts` itself. Do not commit
yet; this lands with Task 29's final commit once the whole `ui/` package builds.

---

## Task 21: `ui/src/lib/constants.ts` — drop address/video consts, add OTP consts

Implements spec §6, §8, §10.

**Files:**

- Modify: `ui/src/lib/constants.ts`

**Interfaces:**

- Produces: `REQUIRED_DOC_TYPES` (3 entries), `OTP_CODE_LENGTH`, `OTP_RESEND_COOLDOWN_SECONDS`. Removes
  `BRAZILIAN_STATES`, `ZIP_CODE_DIGITS`, `SELFIE_CLIP_CONTENT_TYPE`, `SELFIE_CLIP_MIME_CANDIDATES`.

- [ ] **Step 1: Rewrite the KYC block of `constants.ts`**

```ts
// KYC — must stay in step with api/internal/domain/kyc/model.go.
export const CPF_DIGITS = 11

/** Mirrors kyc.MinAge — client-side pre-check only, server remains authoritative. */
export const KYC_MIN_AGE_YEARS = 18

/** Mirrors kyc.MaxDocumentBytes (5 MiB) so the UI rejects oversized files early. */
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024

/** Mirrors kyc.allowedContentTypes — every Enhanced document is now a static photo or PDF, no video. */
export const ID_DOCUMENT_ACCEPTED_TYPES = ['image/jpeg', 'image/png', 'image/heic', 'application/pdf'] as const

/** Content types ID_DOCUMENT_ACCEPTED_TYPES allows that a browser <img>/next/image can actually decode inline. */
export const ID_DOCUMENT_PREVIEWABLE_TYPES = ['image/jpeg', 'image/png'] as const

/** Mirrors kyc.RequiredDocTypes — SubmitEnhanced is rejected until every one is uploaded. */
export const REQUIRED_DOC_TYPES = ['id_front', 'id_back', 'selfie_with_document'] as const

/** Mirrors kyc.OTPLength. */
export const OTP_CODE_LENGTH = 6

/** Mirrors kyc.OTPResendCooldown (seconds) — drives the resend button's countdown. */
export const OTP_RESEND_COOLDOWN_SECONDS = 60

/** Same contact used on /privacy and /terms — one address for user-facing support asks. */
export const SUPPORT_EMAIL = 'dpo@aoctech.app'
```

Remove `BRAZILIAN_STATES`, `ZIP_CODE_DIGITS`, `SELFIE_CLIP_CONTENT_TYPE`, `SELFIE_CLIP_MIME_CANDIDATES` — confirmed
during planning that nothing outside the identity page/its components/`lib/viacep.ts` (deleted in Task 22) referenced
them.

- [ ] **Step 2: Build-check deferred to Task 29** (consumers still reference removed constants until then)

---

## Task 22: Delete `ui/src/lib/viacep.ts`

Implements spec §3 (address dropped entirely).

**Files:**

- Delete: `ui/src/lib/viacep.ts`

- [ ] **Step 1: Delete the file**

`lookupZipCode`/`normalizeZipCode`/`formatZipCodeInput` served the old address form only; confirmed via grep during
planning that no other file imports `lib/viacep`.

- [ ] **Step 2: Build-check deferred to Task 29** (`identity/page.tsx` and its test still import this until Tasks 28-29
  land)

---

## Task 23: `ui/src/lib/mutations.ts` — Basic/OTP/Enhanced mutations

Implements spec §7.

**Files:**

- Modify: `ui/src/lib/mutations.ts`

**Interfaces:**

- Produces: `submitBasicKYCAPI(payload: KYCBasicSubmission): Promise<KYCStatus>`,
  `verifyPhoneKYCAPI(code: string): Promise<KYCStatus>`, `resendKYCCodeAPI(): Promise<KYCStatus>`,
  `submitEnhancedKYCAPI(): Promise<KYCStatus>`. `uploadKYCDocumentAPI`'s `type` parameter now accepts the narrowed
  `KYCDocumentType`. Removes `submitKYCAPI`.

- [ ] **Step 1: Replace `submitKYCAPI` with the four new mutations**

Replace:

```ts
export async function submitKYCAPI(payload: KYCSubmission): Promise<KYCStatus> {
  const { data } = await api.post<KYCStatus>('/v1.0/account/kyc', payload)
  return data
}
```

with:

```ts
export async function submitBasicKYCAPI(payload: KYCBasicSubmission): Promise<KYCStatus> {
  const { data } = await api.post<KYCStatus>('/v1.0/account/kyc/basic', payload)
  return data
}

export async function verifyPhoneKYCAPI(code: string): Promise<KYCStatus> {
  const { data } = await api.post<KYCStatus>('/v1.0/account/kyc/basic/verify-phone', { code })
  return data
}

export async function resendKYCCodeAPI(): Promise<KYCStatus> {
  const { data } = await api.post<KYCStatus>('/v1.0/account/kyc/basic/resend-code')
  return data
}

export async function submitEnhancedKYCAPI(): Promise<KYCStatus> {
  const { data } = await api.post<KYCStatus>('/v1.0/account/kyc/enhanced')
  return data
}
```

Update the file's type-only import line to
`import type { KYCBasicSubmission, KYCDocumentType, KYCStatus, OAuthClient, PresignedUpload, TermsPending } from './types'`
(drop `KYCSubmission`). `presignKYCDocumentAPI`/`confirmKYCDocumentAPI`/`uploadKYCDocumentAPI` need no body changes —
their `type: KYCDocumentType` parameter narrows automatically from Task 20's type change.

- [ ] **Step 2: Build-check deferred to Task 29**

---

## Task 24: `ui/src/components/kyc-basic-form.tsx` — new component

Implements spec §7, §10.

**Files:**

- Create: `ui/src/components/kyc-basic-form.tsx`

**Interfaces:**

- Consumes: `submitBasicKYCAPI` (Task 23), `KYCStatus`/`KYCBasicSubmission` (Task 20), `CPF_DIGITS`/`KYC_MIN_AGE_YEARS`
  (Task 21).
- Produces: `<KYCBasicForm status={KYCStatus} />`. Consumed by Task 28 (`identity/page.tsx`).

- [ ] **Step 1: Write `ui/src/components/kyc-basic-form.tsx`**

```tsx
'use client'

import { SyntheticEvent, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { submitBasicKYCAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { CPF_DIGITS, KYC_MIN_AGE_YEARS } from '@/lib/constants'
import type { KYCStatus } from '@/lib/types'

function formatCPFInput(value: string): string {
  const digits = value.replace(/\D/g, '').slice(0, CPF_DIGITS)
  return digits
    .replace(/(\d{3})(\d)/, '$1.$2')
    .replace(/(\d{3})\.(\d{3})(\d)/, '$1.$2.$3')
    .replace(/(\d{3})\.(\d{3})\.(\d{3})(\d)/, '$1.$2.$3-$4')
}

function formatPhoneInput(value: string): string {
  const digits = value.replace(/\D/g, '').slice(0, 13)
  if (digits.length <= 2) return digits ? `+${digits}` : ''
  return `+${digits.slice(0, 2)} ${digits.slice(2)}`
}

function phoneDigitsToE164(value: string): string {
  return '+' + value.replace(/\D/g, '')
}

function problemSlug(err: unknown): string {
  if (isAxiosError(err)) {
    const type = err.response?.data?.type
    if (typeof type === 'string') return type.slice(type.lastIndexOf('/') + 1)
  }
  return ''
}

function isValidCPF(cpf: string): boolean {
  if (!/^\d{11}$/.test(cpf) || /^(\d)\1{10}$/.test(cpf)) return false
  const checkDigit = (pos: number) => {
    let sum = 0
    for (let i = 0; i < pos; i++) sum += Number(cpf[i]) * (pos + 1 - i)
    const d = 11 - (sum % 11)
    return d >= 10 ? 0 : d
  }
  return checkDigit(9) === Number(cpf[9]) && checkDigit(10) === Number(cpf[10])
}

function isEligibleAge(birthDate: string, years: number): boolean {
  const born = new Date(`${birthDate}T00:00:00Z`)
  if (Number.isNaN(born.getTime())) return false
  const eligibleFrom = new Date(born)
  eligibleFrom.setUTCFullYear(eligibleFrom.getUTCFullYear() + years)
  return new Date() >= eligibleFrom
}

/**
 * Basic KYC step: CPF/name/birthdate/phone. Resubmittable while pending phone
 * verification (a mistyped field doesn't need a separate "edit" mode), locked
 * once phone-verified (server returns kyc-already-verified past that point).
 */
export function KYCBasicForm({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [cpf, setCPF] = useState('')
  const [legalName, setLegalName] = useState(status.legal_name ?? '')
  const [birthDate, setBirthDate] = useState(status.birth_date ?? '')
  const [phone, setPhone] = useState('')
  const [cpfError, setCpfError] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<'legal_name' | 'birth_date' | 'phone', string>>>({})

  const { mutate: submit, isPending, error } = useMutation({
    mutationFn: submitBasicKYCAPI,
    onSuccess: (st) => queryClient.setQueryData(['kyc'], st),
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const digits = cpf.replace(/\D/g, '')
    const cpfValid = isValidCPF(digits)
    setCpfError(!cpfValid)

    const errors: Partial<Record<'legal_name' | 'birth_date' | 'phone', string>> = {}
    if (legalName.trim().length < 3) errors.legal_name = t('identity.fieldRequired')
    if (!birthDate) errors.birth_date = t('identity.fieldRequired')
    else if (!isEligibleAge(birthDate, KYC_MIN_AGE_YEARS)) errors.birth_date = t('identity.underage')
    if (phone.replace(/\D/g, '').length < 10) errors.phone = t('identity.fieldRequired')
    setFieldErrors(errors)

    if (!cpfValid || Object.keys(errors).length > 0) return
    submit({ cpf: digits, legal_name: legalName, birth_date: birthDate, phone_number: phoneDigitsToE164(phone) })
  }

  const slugMessages: Record<string, string> = {
    'age-requirement-not-met': t('identity.underage'),
    'cpf-already-registered': t('identity.cpfTaken'),
    'kyc-already-verified': t('identity.alreadyVerified'),
    'kyc-phone-verification-unavailable': t('identity.phoneVerificationUnavailable'),
    'validation-failed': t('identity.invalidData'),
  }
  const errorMsg = error ? (slugMessages[problemSlug(error)] ?? t('identity.submitFailed')) : null
  const maxBirthDate = new Date().toISOString().slice(0, 10)

  return (
    <form onSubmit={handleSubmit} className="space-y-4" noValidate>
      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="cpf">{t('identity.cpf')}</Label>
        <Input
          id="cpf"
          required
          inputMode="numeric"
          placeholder="000.000.000-00"
          value={cpf}
          onChange={(e) => {
            setCPF(formatCPFInput(e.target.value))
            setCpfError(false)
          }}
          aria-invalid={cpfError}
          aria-describedby={cpfError ? 'cpf-error' : undefined}
          className="min-h-11"
        />
        {cpfError && (
          <p id="cpf-error" className="text-destructive text-sm">
            {t('identity.cpfInvalid')}
          </p>
        )}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="legal_name">{t('identity.legalName')}</Label>
        <Input
          id="legal_name"
          required
          minLength={3}
          value={legalName}
          onChange={(e) => setLegalName(e.target.value)}
          aria-invalid={!!fieldErrors.legal_name}
          className="min-h-11"
        />
        {fieldErrors.legal_name && <p className="text-destructive text-sm">{fieldErrors.legal_name}</p>}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="birth_date">{t('identity.birthDate')}</Label>
        <Input
          id="birth_date"
          type="date"
          required
          max={maxBirthDate}
          value={birthDate}
          onChange={(e) => setBirthDate(e.target.value)}
          aria-invalid={!!fieldErrors.birth_date}
          className="min-h-11"
        />
        {fieldErrors.birth_date && <p className="text-destructive text-sm">{fieldErrors.birth_date}</p>}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="phone">{t('identity.phoneNumber')}</Label>
        <Input
          id="phone"
          type="tel"
          required
          placeholder="+55 11987654321"
          value={phone}
          onChange={(e) => setPhone(formatPhoneInput(e.target.value))}
          aria-invalid={!!fieldErrors.phone}
          className="min-h-11"
        />
        {fieldErrors.phone && <p className="text-destructive text-sm">{fieldErrors.phone}</p>}
        <p className="text-muted-foreground text-xs">{t('identity.phoneNumberHint')}</p>
      </div>

      <Button type="submit" className="min-h-11" disabled={isPending}>
        {isPending ? t('identity.submitting') : t('identity.submit')}
      </Button>
    </form>
  )
}
```

- [ ] **Step 2: Build-check deferred to Task 29**

---

## Task 25: `ui/src/components/kyc-otp-form.tsx` — new component

Implements spec §7, §8, §10.

**Files:**

- Create: `ui/src/components/kyc-otp-form.tsx`

**Interfaces:**

- Consumes: `verifyPhoneKYCAPI`/`resendKYCCodeAPI` (Task 23), `OTP_CODE_LENGTH`/`OTP_RESEND_COOLDOWN_SECONDS` (Task 21).
- Produces: `<KYCOtpForm status={KYCStatus} />`. Consumed by Task 28.

- [ ] **Step 1: Write `ui/src/components/kyc-otp-form.tsx`**

```tsx
'use client'

import { SyntheticEvent, useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { resendKYCCodeAPI, verifyPhoneKYCAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { OTP_CODE_LENGTH, OTP_RESEND_COOLDOWN_SECONDS } from '@/lib/constants'
import type { KYCStatus } from '@/lib/types'

function problemSlug(err: unknown): string {
  if (isAxiosError(err)) {
    const type = err.response?.data?.type
    if (typeof type === 'string') return type.slice(type.lastIndexOf('/') + 1)
  }
  return ''
}

/** Phone verification step: enter the SMS code, or request a fresh one after a cooldown. */
export function KYCOtpForm({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  const [cooldown, setCooldown] = useState(OTP_RESEND_COOLDOWN_SECONDS)

  useEffect(() => {
    if (cooldown <= 0) return
    const id = setInterval(() => setCooldown((n) => Math.max(0, n - 1)), 1000)
    return () => clearInterval(id)
  }, [cooldown])

  const { mutate: verify, isPending: verifying, error: verifyError } = useMutation({
    mutationFn: verifyPhoneKYCAPI,
    onSuccess: (st) => queryClient.setQueryData(['kyc'], st),
  })

  const { mutate: resend, isPending: resending, error: resendError } = useMutation({
    mutationFn: resendKYCCodeAPI,
    onSuccess: (st) => {
      queryClient.setQueryData(['kyc'], st)
      setCooldown(OTP_RESEND_COOLDOWN_SECONDS)
      setCode('')
    },
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    verify(code)
  }

  const slugMessages: Record<string, string> = {
    'kyc-invalid-code': t('identity.otpInvalidCode'),
    'kyc-resend-cooldown': t('identity.otpResendCooldownError'),
    'kyc-phone-verification-unavailable': t('identity.phoneVerificationUnavailable'),
  }
  const activeError = verifyError ?? resendError
  const errorMsg = activeError ? (slugMessages[problemSlug(activeError)] ?? t('identity.submitFailed')) : null

  return (
    <div className="space-y-4">
      <p className="text-muted-foreground text-sm">{t('identity.otpDescription', { phone: status.phone_masked ?? '' })}</p>

      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <form onSubmit={handleSubmit} className="space-y-3" noValidate>
        <div className="space-y-1.5">
          <Label htmlFor="otp_code">{t('identity.otpCodeLabel')}</Label>
          <Input
            id="otp_code"
            inputMode="numeric"
            required
            maxLength={OTP_CODE_LENGTH}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, OTP_CODE_LENGTH))}
            className="min-h-11 tracking-widest"
          />
        </div>
        <div className="flex items-center gap-2">
          <Button type="submit" className="min-h-11" disabled={verifying || code.length !== OTP_CODE_LENGTH}>
            {verifying ? t('identity.submitting') : t('identity.otpSubmit')}
          </Button>
          <Button
            type="button"
            variant="outline"
            className="min-h-11"
            disabled={resending || cooldown > 0}
            onClick={() => resend()}
          >
            {cooldown > 0 ? t('identity.otpResendCooldown', { seconds: cooldown }) : t('identity.otpResend')}
          </Button>
        </div>
      </form>
    </div>
  )
}
```

- [ ] **Step 2: Build-check deferred to Task 29**

---

## Task 26: `ui/src/components/kyc-selfie-photo.tsx` — replaces `selfie-capture.tsx`

Implements spec §3, §10.

**Files:**

- Delete: `ui/src/components/selfie-capture.tsx`
- Create: `ui/src/components/kyc-selfie-photo.tsx`

**Interfaces:**

- Consumes: `uploadKYCDocumentAPI` (existing, `lib/mutations.ts`), `KYCDocumentType` (Task 20).
- Produces: `<KYCSelfiePhoto uploaded={boolean} />`. Consumed by Task 28.

- [ ] **Step 1: Delete `ui/src/components/selfie-capture.tsx`**

The 4-clip `MediaRecorder` video flow is replaced entirely by a single static photo (spec §3).

- [ ] **Step 2: Write `ui/src/components/kyc-selfie-photo.tsx`**

```tsx
'use client'

import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { uploadKYCDocumentAPI } from '@/lib/mutations'
import { Camera, ShieldCheck, XCircle } from 'lucide-react'

type CameraErrorReason = 'permission' | 'not-found' | 'in-use' | 'insecure' | 'unsupported' | null

function classifyCameraError(err: unknown): CameraErrorReason {
  const name = err instanceof DOMException ? err.name : ''
  if (name === 'NotFoundError' || name === 'OverconstrainedError') return 'not-found'
  if (name === 'NotReadableError' || name === 'TrackStartError') return 'in-use'
  if (name === 'SecurityError') return 'insecure'
  return 'permission'
}

/**
 * Captures a single static photo of the user holding their identity document
 * (selfie_with_document) — replaces the old four-clip head-turn video flow.
 * The human reviewer still judges real-vs-photo; there is no server-side ML.
 */
export function KYCSelfiePhoto({ uploaded }: { uploaded: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const videoRef = useRef<HTMLVideoElement>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const [cameraError, setCameraError] = useState<CameraErrorReason>(null)
  const [consented, setConsented] = useState(false)
  const [retake, setRetake] = useState(false)
  const [preview, setPreview] = useState<{ url: string; blob: Blob } | null>(null)

  const active = retake || !uploaded

  const { mutate: upload, isPending } = useMutation({
    mutationFn: (blob: Blob) => uploadKYCDocumentAPI(new File([blob], 'selfie_with_document.jpg', { type: 'image/jpeg' }), 'selfie_with_document'),
    onSuccess: (status) => {
      queryClient.setQueryData(['kyc'], status)
      toast.success(t('identity.uploadSuccess'))
      setPreview(null)
      setRetake(false)
    },
    onError: () => toast.error(t('identity.uploadFailed')),
  })

  useEffect(() => {
    if (!preview) return
    return () => URL.revokeObjectURL(preview.url)
  }, [preview])

  useEffect(() => {
    if (!active || !consented || preview) return
    if (!navigator.mediaDevices?.getUserMedia) {
      setCameraError('unsupported')
      return
    }
    let alive = true
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: 'user' }, audio: false })
      .then((stream) => {
        if (!alive) {
          stream.getTracks().forEach((track) => track.stop())
          return
        }
        streamRef.current = stream
        if (videoRef.current) videoRef.current.srcObject = stream
        setCameraError(null)
      })
      .catch((err) => setCameraError(classifyCameraError(err)))

    return () => {
      alive = false
      streamRef.current?.getTracks().forEach((track) => track.stop())
      streamRef.current = null
    }
  }, [active, consented, preview])

  function capture() {
    const video = videoRef.current
    if (!video) return
    const canvas = document.createElement('canvas')
    canvas.width = video.videoWidth
    canvas.height = video.videoHeight
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.drawImage(video, 0, 0)
    canvas.toBlob((blob) => {
      if (blob) setPreview({ url: URL.createObjectURL(blob), blob })
    }, 'image/jpeg', 0.92)
  }

  function confirm() {
    if (!preview) return
    upload(preview.blob)
  }

  if (!active) {
    return (
      <div className="flex items-center gap-2">
        <p className="text-sm font-medium">{t('identity.selfieWithDocumentDone')}</p>
        <Button type="button" variant="ghost" size="sm" className="min-h-11" onClick={() => setRetake(true)}>
          {t('identity.retakePhoto')}
        </Button>
      </div>
    )
  }

  if (!consented) {
    return (
      <Alert>
        <ShieldCheck className="size-4" />
        <AlertDescription className="space-y-3">
          <p className="text-foreground font-medium">{t('identity.selfieConsentTitle')}</p>
          <p>{t('identity.selfieWithDocumentConsentBody')}</p>
          <Button type="button" className="min-h-11" onClick={() => setConsented(true)}>
            <Camera className="size-4" />
            {t('identity.selfieConsentCta')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  if (preview) {
    return (
      <div className="space-y-3">
        <p className="text-sm font-medium">{t('identity.documentPreviewTitle')}</p>
        {/* eslint-disable-next-line @next/next/no-img-element -- object URL, next/image can't optimize it */}
        <img src={preview.url} alt={t('identity.selfieWithDocument')} className="aspect-3/4 w-full max-w-xs sm:max-w-sm rounded-lg bg-muted object-cover" />
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" className="min-h-11" onClick={() => setPreview(null)} disabled={isPending}>
            {t('identity.retakePhoto')}
          </Button>
          <Button type="button" className="min-h-11" onClick={confirm} disabled={isPending}>
            {isPending ? t('identity.uploading') : t('identity.documentPreviewConfirm')}
          </Button>
        </div>
      </div>
    )
  }

  const cameraErrorMessage =
    cameraError === 'not-found'
      ? t('identity.cameraNotFound')
      : cameraError === 'in-use'
        ? t('identity.cameraInUse')
        : cameraError === 'insecure'
          ? t('identity.cameraInsecure')
          : cameraError === 'unsupported'
            ? t('identity.cameraUnsupported')
            : t('identity.cameraDenied')

  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">{t('identity.selfieWithDocumentInstruction')}</p>

      {cameraError ? (
        <Alert variant="destructive">
          <XCircle className="size-4" />
          <AlertDescription>{cameraErrorMessage}</AlertDescription>
        </Alert>
      ) : (
        <video ref={videoRef} autoPlay muted playsInline className="aspect-3/4 w-full max-w-xs sm:max-w-sm -scale-x-100 rounded-lg bg-muted object-cover" />
      )}

      <Button type="button" className="min-h-11" onClick={capture} disabled={!!cameraError}>
        <Camera className="size-4" />
        {t('identity.capturePhoto')}
      </Button>
    </div>
  )
}
```

- [ ] **Step 3: Build-check deferred to Task 29**

---

## Task 27: `ui/src/components/kyc-document-upload.tsx` — narrow to 2 file-picker types

Implements spec §3, §10.

**Files:**

- Modify: `ui/src/components/kyc-document-upload.tsx`

**Interfaces:**

- Consumes: `KYCDocumentType` (Task 20).

- [ ] **Step 1: Update `TYPE_LABEL_KEY` — drop the four selfie-pose entries, add `selfie_with_document`**

```ts
const TYPE_LABEL_KEY: Record<KYCDocumentType, string> = {
  id_front: 'identity.documentIdFront',
  id_back: 'identity.documentIdBack',
  selfie_with_document: 'identity.selfieWithDocument',
}
```

`DOCUMENT_TYPES` (the file-picker's own selectable list) stays `['id_front', 'id_back']` unchanged —
`selfie_with_document` is captured via `<KYCSelfiePhoto/>` (Task 26), not this file picker, mirroring how the old four
selfie poses went through `<SelfieCapture/>` instead of this component.

- [ ] **Step 2: Build-check deferred to Task 29**

---

## Task 28: `ui/src/app/account/identity/page.tsx` — rewrite around the 6-state machine

Implements spec §10.

**Files:**

- Modify: `ui/src/app/account/identity/page.tsx` (full rewrite)

**Interfaces:**

- Consumes: `KYCBasicForm` (Task 24), `KYCOtpForm` (Task 25), `KYCSelfiePhoto` (Task 26), `KYCDocumentUpload` (Task 27),
  `submitEnhancedKYCAPI` (Task 23), `KYCStatus`/`KYCState` (Task 20), `REQUIRED_DOC_TYPES`/`SUPPORT_EMAIL` (Task 21).

- [ ] **Step 1: Rewrite `ui/src/app/account/identity/page.tsx`**

```tsx
'use client'

import Link from 'next/link'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Separator } from '@/components/ui/separator'
import { cn } from '@/lib/utils'
import { KYCBasicForm } from '@/components/kyc-basic-form'
import { KYCOtpForm } from '@/components/kyc-otp-form'
import { KYCSelfiePhoto } from '@/components/kyc-selfie-photo'
import { KYCDocumentUpload } from '@/components/kyc-document-upload'
import { CheckCircle2, Clock, FileSearch, Info, XCircle } from 'lucide-react'
import { fetchKYC, fetchPasskeys, fetchTOTPStatus } from '@/lib/queries'
import { submitEnhancedKYCAPI } from '@/lib/mutations'
import { formatDate } from '@/lib/format'
import { REQUIRED_DOC_TYPES, SUPPORT_EMAIL } from '@/lib/constants'
import type { KYCDocumentType, KYCStatus } from '@/lib/types'
import { toast } from 'sonner'
import { QueryError } from '@/components/query-error'

const READ_ONLY_LOCK_CLASS = 'read-only:bg-muted read-only:cursor-default'

const DOCUMENT_LABEL_KEY: Record<KYCDocumentType, string> = {
  id_front: 'identity.documentIdFront',
  id_back: 'identity.documentIdBack',
  selfie_with_document: 'identity.selfieWithDocument',
}

function StateBadge({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  switch (status.state) {
    case 'verified':
      return <Badge variant="default"><CheckCircle2 className="size-3.5" />{t('identity.levelVerified')}</Badge>
    case 'basic_verified':
      return <Badge variant="secondary"><CheckCircle2 className="size-3.5" />{t('identity.levelBasicVerified')}</Badge>
    case 'awaiting_phone_verification':
      return <Badge variant="secondary"><Clock className="size-3.5" />{t('identity.levelAwaitingPhoneVerification')}</Badge>
    case 'under_review':
      return <Badge variant="secondary"><FileSearch className="size-3.5" />{t('identity.levelUnderReview')}</Badge>
    case 'rejected':
      return <Badge variant="destructive"><XCircle className="size-3.5" />{t('identity.levelRejected')}</Badge>
    default:
      return <Badge variant="outline">{t('identity.levelNone')}</Badge>
  }
}

function ReadOnlyField({ id, label, value }: { id: string; label: string; value: string }) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} readOnly className={cn('min-h-11', READ_ONLY_LOCK_CLASS)} />
    </div>
  )
}

/** Read-only view shown once Basic data can no longer change (basic_verified onward). */
function SubmittedDetails({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <ReadOnlyField id="submitted-cpf" label={t('identity.cpf')} value={status.cpf_masked ?? ''} />
      <ReadOnlyField id="submitted-legal-name" label={t('identity.legalName')} value={status.legal_name ?? ''} />
      <ReadOnlyField id="submitted-birth-date" label={t('identity.birthDate')} value={status.birth_date ?? ''} />
      <ReadOnlyField id="submitted-phone" label={t('identity.phoneNumber')} value={status.phone_masked ?? ''} />
    </div>
  )
}

function DocumentList({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  if (!status.documents?.length) return null
  return (
    <ul className="text-muted-foreground space-y-1 text-sm">
      {status.documents.map((doc) => (
        <li key={doc.id}>
          {t(DOCUMENT_LABEL_KEY[doc.type])} — {t('identity.uploadedOn', { date: formatDate(doc.uploaded_at) })}
        </li>
      ))}
    </ul>
  )
}

/** Every KYC write route sits behind step-up, which a user with no enrolled MFA method can never satisfy. */
function MFARequired() {
  const { t } = useTranslation()
  return (
    <Alert>
      <Info className="size-4" />
      <AlertTitle>{t('identity.mfaRequiredTitle')}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t('identity.mfaRequired')}</p>
        <Button render={<Link href="/account/security" />} className="min-h-11">{t('identity.mfaRequiredCta')}</Button>
      </AlertDescription>
    </Alert>
  )
}

/** Basic verified onward: upload the 3 Enhanced documents, then submit for review. */
function EnhancedSection({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const uploadedTypes = status.documents?.map((d) => d.type) ?? []
  const docsComplete = REQUIRED_DOC_TYPES.every((docType) => uploadedTypes.includes(docType))

  const { mutate: submit, isPending, error } = useMutation({
    mutationFn: submitEnhancedKYCAPI,
    onSuccess: (st) => {
      queryClient.setQueryData(['kyc'], st)
      toast.success(t('identity.submittedOn', { date: formatDate(new Date().toISOString()) }))
    },
  })

  return (
    <div className="space-y-4">
      {status.state === 'rejected' && (
        <Alert variant="destructive">
          <XCircle className="size-4" />
          <AlertDescription className="space-y-2">
            {status.rejection_reason && <p>{t('identity.rejectionReason', { reason: status.rejection_reason })}</p>}
            <p>{t('identity.rejectionGuidance')}</p>
            <a href={`mailto:${SUPPORT_EMAIL}`} className="underline underline-offset-4">{t('identity.contactSupport')}</a>
          </AlertDescription>
        </Alert>
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{t('identity.submitFailed')}</AlertDescription>
        </Alert>
      )}

      <Alert>
        <Info className="size-4" />
        <AlertDescription>{t('identity.documentsPrivacyNote')}</AlertDescription>
      </Alert>

      <DocumentList status={status} />

      <div className="space-y-3">
        <h2 className="text-base font-semibold">{t('identity.documentIdFront')} / {t('identity.documentIdBack')}</h2>
        <KYCDocumentUpload uploadedTypes={uploadedTypes} />
      </div>

      <Separator />

      <div className="space-y-3">
        <h2 className="text-base font-semibold">{t('identity.selfieWithDocument')}</h2>
        <KYCSelfiePhoto uploaded={uploadedTypes.includes('selfie_with_document')} />
      </div>

      <Button type="button" className="min-h-11" disabled={!docsComplete || isPending} onClick={() => submit()}>
        {isPending ? t('identity.submitting') : t('identity.submitEnhancedCta')}
      </Button>
    </div>
  )
}

function cardDescriptionKey(state: KYCStatus['state']): string {
  switch (state) {
    case 'awaiting_phone_verification':
      return 'identity.awaitingPhoneVerification'
    case 'basic_verified':
      return 'identity.basicVerifiedCta'
    case 'under_review':
      return 'identity.underReview'
    case 'rejected':
      return 'identity.rejected'
    case 'verified':
      return 'identity.levelVerified'
    default:
      return 'identity.notVerifiedCta'
  }
}

export default function IdentityPage() {
  const { t } = useTranslation()
  const { data: status, isError: kycError, error: kycErr, refetch: refetchKYC } = useQuery({ queryKey: ['kyc'], queryFn: fetchKYC })
  const { data: totp, isError: totpError, error: totpErr, refetch: refetchTOTP } = useQuery({ queryKey: ['totp'], queryFn: fetchTOTPStatus })
  const { data: passkeys, isError: passkeysError, error: passkeysErr, refetch: refetchPasskeys } = useQuery({ queryKey: ['passkeys'], queryFn: fetchPasskeys })

  if (kycError || totpError || passkeysError) {
    return (
      <QueryError
        error={kycErr ?? totpErr ?? passkeysErr}
        onRetry={() => { void refetchKYC(); void refetchTOTP(); void refetchPasskeys() }}
      />
    )
  }

  const mfaLoaded = totp !== undefined && passkeys !== undefined
  const hasMFA = (totp?.enabled ?? false) || (passkeys?.length ?? 0) > 0

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('identity.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('identity.subtitle')}</p>
        </div>
        {status && <StateBadge status={status} />}
      </div>

      <Card>
        <CardHeader>
          <CardDescription>{status ? t(cardDescriptionKey(status.state)) : null}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {status?.state === 'verified' && (
            <>
              <Alert>
                <CheckCircle2 className="size-4" />
                <AlertDescription>
                  {t('identity.verifiedOn', { date: formatDate(status.verified_at ?? null) })} — {t('identity.lockedNote')}
                </AlertDescription>
              </Alert>
              <SubmittedDetails status={status} />
            </>
          )}

          {status?.state === 'under_review' && (
            <>
              <SubmittedDetails status={status} />
              <DocumentList status={status} />
              <p className="text-muted-foreground text-sm">{t('identity.underReviewNote')}</p>
              {status.expires_at && (
                <p className="text-muted-foreground text-sm">{t('identity.expiresOn', { date: formatDate(status.expires_at) })}</p>
              )}
            </>
          )}

          {status && (status.state === 'not_started' || status.state === 'awaiting_phone_verification' || status.state === 'basic_verified' || status.state === 'rejected') && (
            <>
              {mfaLoaded && !hasMFA ? (
                <MFARequired />
              ) : status.state === 'not_started' ? (
                <KYCBasicForm status={status} />
              ) : status.state === 'awaiting_phone_verification' ? (
                <KYCOtpForm status={status} />
              ) : (
                <>
                  <SubmittedDetails status={status} />
                  <Separator />
                  <EnhancedSection status={status} />
                </>
              )}
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
```

- [ ] **Step 2: Build-check deferred to Task 29**

---

## Task 29: `ui/src/app/account/identity/page.test.tsx` — rewrite for the new flow

Implements spec §13 (UI regression coverage parity with the old suite).

**Files:**

- Modify: `ui/src/app/account/identity/page.test.tsx` (full rewrite)

**Interfaces:** consumes everything from Tasks 20-28.

- [ ] **Step 1: Rewrite the test file**

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import IdentityPage from './page'
import { fetchKYC, fetchPasskeys, fetchTOTPStatus } from '@/lib/queries'
import { submitBasicKYCAPI, verifyPhoneKYCAPI } from '@/lib/mutations'
import type { KYCStatus } from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchKYC: vi.fn(),
  fetchTOTPStatus: vi.fn(),
  fetchPasskeys: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  submitBasicKYCAPI: vi.fn(),
  verifyPhoneKYCAPI: vi.fn(),
  resendKYCCodeAPI: vi.fn(),
  submitEnhancedKYCAPI: vi.fn(),
  uploadKYCDocumentAPI: vi.fn(),
}))

const NOT_STARTED: KYCStatus = { state: 'not_started', level: '' }
const AWAITING_PHONE: KYCStatus = { state: 'awaiting_phone_verification', level: 'basic', phone_masked: '***4321' }

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <IdentityPage />
    </QueryClientProvider>,
  )
}

describe('IdentityPage — Basic step', () => {
  beforeEach(() => {
    vi.mocked(fetchKYC).mockResolvedValue(NOT_STARTED)
    vi.mocked(fetchTOTPStatus).mockResolvedValue({ enabled: true })
    vi.mocked(fetchPasskeys).mockResolvedValue([])
  })

  it('blocks submit with an underage error and never calls the API', async () => {
    const user = userEvent.setup()
    renderPage()

    await screen.findByLabelText('CPF')
    await user.type(screen.getByLabelText('CPF'), '11144477735')
    await user.type(screen.getByLabelText('Full legal name'), 'Jane Doe')
    const seventeenYearsAgo = new Date()
    seventeenYearsAgo.setUTCFullYear(seventeenYearsAgo.getUTCFullYear() - 17)
    await user.type(screen.getByLabelText('Date of birth'), seventeenYearsAgo.toISOString().slice(0, 10))
    await user.type(screen.getByLabelText('Phone number'), '+5511987654321')

    await user.click(screen.getByRole('button', { name: 'Submit' }))

    expect(await screen.findByText('You must be at least 18 years old.')).toBeInTheDocument()
    expect(submitBasicKYCAPI).not.toHaveBeenCalled()
  })

  it('shows a generic failure message instead of "invalid data" for an unmapped submit error', async () => {
    const user = userEvent.setup()
    vi.mocked(submitBasicKYCAPI).mockRejectedValue(new Error('network down'))
    renderPage()

    await screen.findByLabelText('CPF')
    await user.type(screen.getByLabelText('CPF'), '11144477735')
    await user.type(screen.getByLabelText('Full legal name'), 'Jane Doe')
    const eighteenYearsAgo = new Date()
    eighteenYearsAgo.setUTCFullYear(eighteenYearsAgo.getUTCFullYear() - 18)
    await user.type(screen.getByLabelText('Date of birth'), eighteenYearsAgo.toISOString().slice(0, 10))
    await user.type(screen.getByLabelText('Phone number'), '+5511987654321')

    await user.click(screen.getByRole('button', { name: 'Submit' }))

    expect(await screen.findByText('Something went wrong. Try again.')).toBeInTheDocument()
    expect(screen.queryByText('Check the data and try again.')).not.toBeInTheDocument()
  })
})

describe('IdentityPage — phone verification step', () => {
  beforeEach(() => {
    vi.mocked(fetchKYC).mockResolvedValue(AWAITING_PHONE)
    vi.mocked(fetchTOTPStatus).mockResolvedValue({ enabled: true })
    vi.mocked(fetchPasskeys).mockResolvedValue([])
  })

  it('maps kyc-invalid-code to a readable message', async () => {
    const user = userEvent.setup()
    vi.mocked(verifyPhoneKYCAPI).mockRejectedValue({
      isAxiosError: true,
      response: { data: { type: 'https://accounts.aoctech.app/problems/kyc-invalid-code' } },
    })
    renderPage()

    await screen.findByLabelText('Verification code')
    await user.type(screen.getByLabelText('Verification code'), '000000')
    await user.click(screen.getByRole('button', { name: 'Verify' }))

    await waitFor(() => expect(screen.getByText('The code you entered is invalid or has expired.')).toBeInTheDocument())
  })
})
```

Adjust the exact English copy strings above (`'Submit'`, `'Verify'`,
`'The code you entered is invalid or has expired.'`, etc.) to whatever `en.json` ends up containing after Task 30 — the
test must assert against the real translated strings, not invented ones.

- [ ] **Step 2: Run the UI test suite**

Run: `cd ui && npm test`
Expected: PASS

- [ ] **Step 3: Run the full UI verification suite**

Run: `cd ui && npx eslint src --ext .ts,.tsx && npm run build`
Expected: both succeed cleanly — this is the point where `ui/` compiles end-to-end (confirms Tasks 20-23's deferred
build-checks).

- [ ] **Step 4: Manual smoke test in a browser**

Run: `cd ui && npm run dev` (with `NEXT_PUBLIC_MOCK_API=true` in `.env.local` if a local API isn't running), then visit
`/account/identity` and click through: Basic form submit → OTP screen appears → (mock) enter any code → Enhanced
document step appears → upload id_front/id_back → capture the selfie-with-document photo → "submit for review" button
enables only once all 3 are present. This is required per `ui/CLAUDE.md`'s "test the golden path in a browser before
reporting complete" rule — automated tests alone don't verify the camera flow renders correctly.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/lib/types.ts src/lib/constants.ts src/lib/mutations.ts src/components/kyc-basic-form.tsx src/components/kyc-otp-form.tsx src/components/kyc-selfie-photo.tsx src/components/kyc-document-upload.tsx src/app/account/identity/page.tsx src/app/account/identity/page.test.tsx
git rm src/lib/viacep.ts src/components/selfie-capture.tsx
git commit -m "feat: rewrite identity page around Basic/OTP/Enhanced KYC flow"
```

---

## Task 30: Locale files — `en.json` / `pt-BR.json` `identity` section

Implements spec §10 (UI copy needed to make Task 28-29 actually render/pass — content itself is otherwise out of spec
scope).

**Files:**

- Modify: `ui/src/locales/en.json`
- Modify: `ui/src/locales/pt-BR.json`

- [ ] **Step 1: Replace the `identity` object in `pt-BR.json`**

Remove every address/selfie-video-specific key (`stepAddressHeading`, `zipCode`, `street`, `number`, `complement`,
`district`, `city`, `state`, `selectState`, `zipNotFound`, `zipFound`, `address`, `livenessUp/Down/Left/Right`,
`selfieUp/Down/Left/Right`, `selfieHint`, `selfieAllDone`, `retake`, `poseUp/Down/Left/Right`, `selfieConsentBody`
(replaced), `selfiePreviewTitle/Retake/Keep`, `selfieClipReady`, `stepDocuments`, `stepSelfie`, `stepNavLabel`,
`stepLocked`, `finishChecklistNote`, `progressLabel`, `documentType`, `reviewCta`, `reviewNote`, `confirmSubmit*`,
`editDetails`, `awaitingFiles`, `levelAwaitingFiles`, `cameraRetry`, `cameraBlocked`). Add the new Basic/OTP/Enhanced
keys. Replace the `"identity": { ... }` block with:

```json
  "identity": {
    "title": "Verificação de identidade",
    "subtitle": "Verifique sua identidade para desbloquear o acesso completo à sua conta.",
    "cpf": "CPF",
    "legalName": "Nome legal completo",
    "birthDate": "Data de nascimento",
    "phoneNumber": "Telefone",
    "phoneNumberHint": "Enviaremos um código de verificação por SMS para este número.",
    "submit": "Enviar",
    "submitting": "Enviando…",
    "levelNone": "Não verificado",
    "levelAwaitingPhoneVerification": "Aguardando verificação",
    "levelBasicVerified": "Verificado (básico)",
    "levelUnderReview": "Em análise",
    "levelRejected": "Rejeitado",
    "levelVerified": "Verificado",
    "notVerifiedCta": "Preencha seus dados para iniciar a verificação de identidade.",
    "awaitingPhoneVerification": "Enviamos um código de verificação por SMS. Digite-o abaixo para continuar.",
    "basicVerifiedCta": "Envie seus documentos de identidade para concluir a verificação completa.",
    "underReview": "Seus documentos estão na fila de análise. Avisaremos quando houver uma decisão.",
    "rejected": "Sua verificação foi rejeitada. Veja os detalhes abaixo para saber o que fazer.",
    "verifiedOn": "Verificado em {{date}}",
    "lockedNote": "Os dados de identidade ficam travados após a verificação.",
    "underage": "Você precisa ter pelo menos 18 anos.",
    "cpfTaken": "Este CPF já está cadastrado em outra conta.",
    "alreadyVerified": "Você não pode alterar os dados de identidade depois da verificação.",
    "phoneVerificationUnavailable": "A verificação por telefone não está disponível no momento. Tente novamente mais tarde.",
    "invalidData": "Confira os dados e tente novamente.",
    "submitFailed": "Algo deu errado. Tente novamente.",
    "cpfInvalid": "Esse CPF não parece correto. Confira os 11 dígitos.",
    "fieldRequired": "Este campo é obrigatório.",
    "submissionLocked": "Sua verificação já está em andamento. Você não pode fazer alterações até que a análise termine ou ela expire.",
    "mfaRequiredTitle": "Autenticação em dois fatores obrigatória",
    "mfaRequired": "A verificação de identidade exige autenticação em dois fatores. Configure um aplicativo autenticador ou uma chave de acesso para continuar.",
    "mfaRequiredCta": "Configurar autenticação em dois fatores",
    "otpDescription": "Digite o código de 6 dígitos enviado para {{phone}}.",
    "otpCodeLabel": "Código de verificação",
    "otpSubmit": "Verificar",
    "otpResend": "Reenviar código",
    "otpResendCooldown": "Reenviar em {{seconds}}s",
    "otpInvalidCode": "O código digitado é inválido ou expirou.",
    "otpResendCooldownError": "Um código já foi enviado recentemente. Aguarde antes de solicitar outro.",
    "submitEnhancedCta": "Enviar para análise",
    "submittedOn": "Enviado em {{date}}",
    "documentIdFront": "Documento — frente",
    "documentIdBack": "Documento — verso",
    "selfieWithDocument": "Selfie segurando o documento",
    "selfieWithDocumentInstruction": "Segure seu documento ao lado do rosto e tire a foto.",
    "selfieWithDocumentConsentBody": "Vamos pedir acesso à câmera para tirar uma foto sua segurando o documento de identidade. A foto é usada apenas para verificação de identidade e revisada pela nossa equipe.",
    "selfieWithDocumentDone": "Foto enviada.",
    "capturePhoto": "Tirar foto",
    "retakePhoto": "Tirar outra foto",
    "selfieConsentTitle": "Antes de ativar sua câmera",
    "selfieConsentCta": "Permitir câmera e começar",
    "cameraDenied": "Acesso à câmera negado. Ative-o no navegador para continuar.",
    "cameraNotFound": "Nenhuma câmera foi encontrada neste dispositivo. Conecte uma câmera e tente novamente.",
    "cameraInUse": "Sua câmera está sendo usada por outro app ou aba do navegador. Feche-o e tente novamente.",
    "cameraInsecure": "O acesso à câmera exige uma conexão segura. Recarregue a página em HTTPS e tente novamente.",
    "cameraUnsupported": "Seu navegador não é compatível com acesso à câmera. Tente um navegador diferente.",
    "uploadDocument": "Enviar documento",
    "replaceDocument": "Substituir documento",
    "documentPreviewTitle": "Revise sua foto antes de enviar",
    "documentPreviewChangeFile": "Escolher outro arquivo",
    "documentPreviewConfirm": "Enviar esta foto",
    "documentPreviewUnavailable": "A pré-visualização não está disponível para este tipo de arquivo, mas o envio funcionará normalmente.",
    "documentsPrivacyNote": "Suas fotos de documento são usadas apenas para verificar sua identidade e são armazenadas com segurança. Nunca são compartilhadas além disso.",
    "documentCaptureTips": "Verifique se os quatro cantos estão visíveis, a foto está nítida e não há reflexos sobre o documento.",
    "uploading": "Enviando…",
    "uploadSuccess": "Documento enviado.",
    "documentReplaced": "Documento substituído.",
    "uploadFailed": "Falha no envio. Tente novamente.",
    "uploadedOn": "Enviado em {{date}}",
    "fileTooLarge": "O arquivo é muito grande (máximo 5 MB).",
    "fileTypeUnsupported": "Tipo de arquivo não suportado. Use JPG, PNG, HEIC ou PDF.",
    "rejectionReason": "Motivo: {{reason}}",
    "rejectionGuidance": "Reenvie seus documentos de identidade para reenviar — reenvios parciais não são aceitos.",
    "contactSupport": "Falar com o suporte",
    "expiresOn": "Este envio expira em {{date}}.",
    "underReviewNote": "Analisamos os envios na ordem em que chegam e enviaremos um e-mail assim que houver uma decisão."
  }
```

- [ ] **Step 2: Replace the `identity` object in `en.json`** with the same key set, English copy (mirror the tone of the
  existing English strings elsewhere in that file — e.g. `"submit": "Submit"`, `"cpf": "CPF"`,
  `"legalName": "Full legal name"`, `"birthDate": "Date of birth"`, `"phoneNumber": "Phone number"`,
  `"otpCodeLabel": "Verification code"`, `"otpSubmit": "Verify"`,
  `"otpInvalidCode": "The code you entered is invalid or has expired."`, etc. — translate every key from Step 1 1:1).

- [ ] **Step 3: Confirm Task 29's test strings match**

Run: `cd ui && npm test -- identity`
Expected: PASS — if any assertion string doesn't match what's actually in `en.json`, fix the test (Task 29) to match the
shipped copy, not the other way around.

- [ ] **Step 4: Full verification**

Run: `cd ui && npx eslint src --ext .ts,.tsx && npm run build && npm test`
Expected: all three succeed — this is the final gate for the entire `ui/` scope of this plan.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/locales/en.json src/locales/pt-BR.json
git commit -m "feat: add Basic/OTP/Enhanced KYC copy to en/pt-BR locales"
```

---

## Self-Review

**Spec coverage:**

- §1-3 (background/goals/non-goals): reflected throughout — no risk gate, no migration, no PIX, no address, no video
  selfie.
- §4 (levels/status/state machine): Task 1 (constants), Task 9 (`state()` derivation), Task 10 (state tests).
- §5 (JWT claim mapping): Task 1 (`ClaimLevel`), Tasks 13-14 (both choke points).
- §6 (data model): Task 2 (user fields), Task 8 (repository persistence).
- §7 (API contract): Task 11 (routes/DTOs), Task 19 (README).
- §8 (phone verification/SNS): Tasks 4-5 (sms package + config), Task 9 (OTP logic), Task 18 (IAM).
- §9 (risk scoring): Task 3 (package), Task 9 (`evaluateRisk` call sites), Task 8 (persistence).
- §10 (UI): Tasks 20-30.
- §11 (cdk): Task 18.
- §12 (cross-project impact): no task touches `ctech-wallet`/`ctech-dfe` — confirmed by construction (every change is
  additive to `ClaimLevel`'s already-matching output set).
- §13 (testing): Task 10 (`kyc/service_test.go`), Task 3 (`risk` test), Task 12 (`handler/kyc_test.go`), Tasks 13-14
  (`token`/`userinfo` claim tests), Task 29 (UI test).

**Placeholder scan:** no `TBD`/`TODO` remain; every step has literal code or an exact command.

**Type consistency:** `kyc.BasicSubmission`/`kyc.BasicRecord`/`kyc.Status` field names match across Tasks 1, 8, 9, 11;
`KYCBasicSubmission`/`KYCStatus` match across UI Tasks 20, 23, 24, 28; `OTPSender` interface name matches between Task
9's definition and Tasks 4, 16's callers.

**Ambiguity check:** the one genuine judgment call — whether Basic's form needs a two-step review/confirm like the old
Enhanced form — is resolved explicitly in Task 24's step 1 comment (no confirm step, since Basic remains freely
resubmittable until phone-verified, unlike the old scheme where any submit was immediately locked).

---

## Execution Handoff

Plan complete and saved to `docs/plans/2026-07-25-kyc-tiered.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
