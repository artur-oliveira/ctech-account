package user

import "strings"

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
	CPF           string `dynamodbav:"cpf,omitempty"`             // 11 digits, numbers only — never serialized to clients
	BirthDate     string `dynamodbav:"birth_date,omitempty"`      // YYYY-MM-DD
	LegalName     string `dynamodbav:"legal_name,omitempty"`      // name as registered with Receita Federal
	KYCLevel      string `dynamodbav:"kyc_level,omitempty"`       // kyc.LevelNone | kyc.LevelBasic | kyc.LevelVerified
	KYCVerifiedAt string `dynamodbav:"kyc_verified_at,omitempty"` // RFC3339

	KYCMethod          string        `dynamodbav:"kyc_method,omitempty"`           // kyc.MethodPIX | kyc.MethodDocument
	KYCDocStatus       string        `dynamodbav:"kyc_doc_status,omitempty"`       // kyc.DocStatus*
	KYCRejectionReason string        `dynamodbav:"kyc_rejection_reason,omitempty"` // reviewer's note, shown to the user
	KYCSubmittedAt     string        `dynamodbav:"kyc_submitted_at,omitempty"`     // RFC3339
	KYCExpiresAt       string        `dynamodbav:"kyc_expires_at,omitempty"`       // RFC3339 — a stale pending submission unlocks re-submission
	KYCDocuments       []KYCDocument `dynamodbav:"kyc_documents,omitempty"`

	Address Address `dynamodbav:"address,omitempty"`

	TOSVersion        string `dynamodbav:"tos_version,omitempty"`         // legal.CurrentToSVersion at acceptance time
	TOSAcceptedAt     string `dynamodbav:"tos_accepted_at,omitempty"`     // RFC3339
	PrivacyVersion    string `dynamodbav:"privacy_version,omitempty"`     // legal.CurrentPrivacyVersion at acceptance time
	PrivacyAcceptedAt string `dynamodbav:"privacy_accepted_at,omitempty"` // RFC3339
}

// Address is the residential address collected during KYC. It lives here (not
// in the kyc package) because kyc imports user, not the other way round.
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
