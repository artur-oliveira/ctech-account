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

const (
	RejectionDocumentUnreadable = "document_unreadable"
	RejectionDocumentIncomplete = "document_incomplete"
	RejectionDocumentMismatch   = "document_mismatch"
	RejectionSelfieMismatch     = "selfie_mismatch"
	RejectionDataMismatch       = "data_mismatch"
	RejectionSuspectedFraud     = "suspected_fraud"
	RejectionOther              = "other"
)

var RejectionCodes = []string{
	RejectionDocumentUnreadable, RejectionDocumentIncomplete, RejectionDocumentMismatch,
	RejectionSelfieMismatch, RejectionDataMismatch, RejectionSuspectedFraud, RejectionOther,
}

func IsValidRejectionCode(code string) bool { return slices.Contains(RejectionCodes, code) }

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
	StateNotStarted    = "not_started"
	StateBasicVerified = "basic_verified"
	StateUnderReview   = "under_review"
	StateRejected      = "rejected"
	StateVerified      = "verified"
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
	DecisionApprove      = "approve"
	DecisionReject       = "reject"
	ReviewQueuePending   = "pending"
	ReviewQueueCompleted = "completed"
)

// ReviewActor is the authenticated operator attached to an Enhanced KYC
// decision. Persisting a display-name snapshot keeps completed reviews
// attributable even when the operator later edits their profile.
type ReviewActor struct {
	ID   string
	Name string
}

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
	ErrInvalidAddress   = errors.New("invalid address")
	ErrAlreadyVerified  = errors.New("kyc already verified")
	ErrNotSubmitted     = errors.New("kyc data not submitted")

	// ErrBasicRequired is returned when Enhanced is submitted before Basic is verified.
	ErrBasicRequired = errors.New("basic verification must be completed first")
	// ErrBasicLocked is returned when Basic identity data is resubmitted after
	// it has already been verified — Basic never regresses.
	ErrBasicLocked = errors.New("basic identity data cannot be changed once verified")

	// ErrInvalidMethod is returned when document verification is unavailable —
	// no bucket is configured (see Service.DocumentsEnabled).
	ErrInvalidMethod = errors.New("document verification is not available")

	// ErrSubmissionLocked guards an Enhanced submission under active review:
	// documents and the submit route are frozen until it is rejected or expires.
	ErrSubmissionLocked = errors.New("kyc submission is pending and cannot be changed")

	ErrInvalidDocumentType    = errors.New("invalid document type")
	ErrInvalidContentType     = errors.New("invalid document content type")
	ErrDocumentNotUploaded    = errors.New("document was not uploaded")
	ErrDocumentTooLarge       = errors.New("document exceeds the maximum size")
	ErrTooManyDocuments       = errors.New("too many documents for this submission")
	ErrNoDocuments            = errors.New("no documents uploaded")
	ErrInvalidDecision        = errors.New("invalid review decision")
	ErrInvalidRejectionReason = errors.New("invalid rejection reason")

	// ErrDocumentTypeMismatch is returned when the type a client confirms does
	// not match the type the document was presigned for (SEC-018).
	ErrDocumentTypeMismatch = errors.New("document type does not match the presigned intent")
)

// Address and Document are stored on the user item; their canonical
// definitions live in the user package (kyc imports user, not the reverse).
type (
	Address  = user.Address
	Document = user.KYCDocument
)

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
	Address         *Address   `json:"address,omitempty"`
	BasicVerifiedAt string     `json:"basic_verified_at,omitempty"`
	Documents       []Document `json:"documents,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	RejectionCode   string     `json:"rejection_code,omitempty"`
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
	Address     Address
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
