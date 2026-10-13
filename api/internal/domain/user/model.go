package user

import (
	"strings"

	"gopkg.aoctech.app/account/api/internal/crypto"
)

const (
	SupportRoleAgent   = "agent"
	SupportRoleManager = "manager"
	SupportRoleAdmin   = "admin"
)

// DeletionStatePending marks an account locked by a confirmed deletion request.
const DeletionStatePending = "pending"

// DeletionStatePurged marks a tombstone: the account was erased.
const DeletionStatePurged = "purged"

type User struct {
	PK            string `dynamodbav:"pk"`
	Email         string `dynamodbav:"email"`
	GoogleSub     string `dynamodbav:"google_sub,omitempty"` // stable Google OIDC sub — the identity key for social logins
	PasswordHash  string `dynamodbav:"password_hash"`
	FirstName     string `dynamodbav:"first_name"`
	LastName      string `dynamodbav:"last_name"`
	DisplayName   string `dynamodbav:"display_name,omitempty"`
	AvatarURL     string `dynamodbav:"avatar_url,omitempty"`
	EmailVerified bool   `dynamodbav:"email_verified"`
	IsEnabled     bool   `dynamodbav:"is_enabled"`
	CreatedAt     string `dynamodbav:"created_at"`
	UpdatedAt     string `dynamodbav:"updated_at"`

	CPF       string  `dynamodbav:"cpf,omitempty"`        // 11 digits, numbers only — never serialized to clients
	BirthDate string  `dynamodbav:"birth_date,omitempty"` // YYYY-MM-DD
	LegalName string  `dynamodbav:"legal_name,omitempty"` // name as registered with Receita Federal
	Address   Address `dynamodbav:"address,omitempty"`    // collected at Basic — needed for future BaaS integration

	KYCLevel           string        `dynamodbav:"kyc_level,omitempty"`             // kyc.LevelNone | kyc.LevelBasic | kyc.LevelEnhanced
	KYCStatus          string        `dynamodbav:"kyc_status,omitempty"`            // kyc.Status* — renamed from kyc_doc_status
	KYCBasicVerifiedAt string        `dynamodbav:"kyc_basic_verified_at,omitempty"` // RFC3339 — set once, never cleared
	KYCVerifiedAt      string        `dynamodbav:"kyc_verified_at,omitempty"`       // RFC3339 — Enhanced verified timestamp
	KYCRejectionCode   string        `dynamodbav:"kyc_rejection_code,omitempty"`    // fixed review-reason catalog code
	KYCRejectionReason string        `dynamodbav:"kyc_rejection_reason,omitempty"`  // reviewer's note, Enhanced only
	KYCSubmittedAt     string        `dynamodbav:"kyc_submitted_at,omitempty"`      // RFC3339 — whichever level is currently pending
	KYCExpiresAt       string        `dynamodbav:"kyc_expires_at,omitempty"`        // RFC3339 — stale Enhanced pending unlocks re-submission
	KYCDocuments       []KYCDocument `dynamodbav:"kyc_documents,omitempty"`

	PhoneNumber     string `dynamodbav:"phone_number,omitempty"`      // E.164, collected at Basic
	PhoneVerifiedAt string `dynamodbav:"phone_verified_at,omitempty"` // RFC3339

	KYCRiskScore       int      `dynamodbav:"kyc_risk_score,omitempty"`
	KYCRiskSignals     []string `dynamodbav:"kyc_risk_signals,omitempty"`      // "name:detail" pairs, latest snapshot
	KYCRiskEvaluatedAt string   `dynamodbav:"kyc_risk_evaluated_at,omitempty"` // RFC3339
	KYCReviewedAt      string   `dynamodbav:"kyc_reviewed_at,omitempty"`       // RFC3339 — latest Enhanced decision
	KYCReviewedBy      string   `dynamodbav:"kyc_reviewed_by,omitempty"`       // authenticated reviewer user ID
	KYCReviewedByName  string   `dynamodbav:"kyc_reviewed_by_name,omitempty"`  // display snapshot for the admin queue
	KYCReviewDecision  string   `dynamodbav:"kyc_review_decision,omitempty"`   // approve | reject

	TOSVersion        string `dynamodbav:"tos_version,omitempty"`
	TOSAcceptedAt     string `dynamodbav:"tos_accepted_at,omitempty"`
	PrivacyVersion    string `dynamodbav:"privacy_version,omitempty"`
	PrivacyAcceptedAt string `dynamodbav:"privacy_accepted_at,omitempty"`

	// SupportRole gates the support-ticket admin UI/API. Empty for regular
	// users. Deliberately scoped to this feature, not a general permissions
	// field — see docs/specs/2026-08-22-support-tickets-design.md §2.
	SupportRole string `dynamodbav:"support_role,omitempty"`

	// DeletionState is set while a confirmed deletion request is open
	// (docs/specs/2026-10-06-account-deletion-ctech-account.md). Every sign-in
	// path refuses a non-empty value.
	DeletionState     string `dynamodbav:"deletion_state,omitempty"`
	DeletionRequestID string `dynamodbav:"deletion_request_id,omitempty"`
	PurgedAt          string `dynamodbav:"purged_at,omitempty"` // RFC3339, set on the tombstone
	CPFHMAC           string `dynamodbav:"cpf_hmac,omitempty"`  // keyed hash kept for fraud prevention (D10)
}

// Address is the residential address collected during Basic KYC. It lives
// here (not in the kyc package) because kyc imports user, not the other way
// round. Kept post-tiering because the planned BaaS integration needs it.
type Address struct {
	ZipCode    string `dynamodbav:"zip_code" json:"zip_code"`
	Street     string `dynamodbav:"street" json:"street"`
	Number     string `dynamodbav:"number" json:"number"`
	Complement string `dynamodbav:"complement,omitempty" json:"complement,omitempty"`
	District   string `dynamodbav:"district" json:"district"`
	City       string `dynamodbav:"city" json:"city"`
	State      string `dynamodbav:"state" json:"state"` // UF
}

// IsZero reports whether no address was ever stored.
func (a Address) IsZero() bool {
	return a == Address{}
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

// EmailMarkerPK is the uniqueness lock written at registration.
func EmailMarkerPK(email string) string { return "EMAIL#" + crypto.HashToken(email) }

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
