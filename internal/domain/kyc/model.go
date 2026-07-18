package kyc

import (
	"errors"
	"time"

	"gopkg.aoctech.app/account/internal/domain/user"
)

// KYC verification levels stored on the user and exposed as the kyc_level claim.
// Downstream services (ctech-wallet, ctech-dfe) read this and nothing else —
// never widen the value set. KYC is manual-only: kyc_level stays LevelNone
// throughout the pending phase and only becomes LevelVerified once a human
// reviewer approves it via cmd/kyc — there is no intermediate level.
const (
	LevelNone     = ""
	LevelVerified = "verified" // a human reviewer approved the submitted documents
)

// MethodDocument is the only verification method: the user uploads identity
// documents and a human reviews them. Stored on kyc_method for forward
// compatibility with any future method, but nothing else is ever written here.
const (
	MethodNone     = ""
	MethodDocument = "document"
)

// Document review outcome.
const (
	DocStatusNone          = ""
	DocStatusAwaitingFiles = "awaiting_files" // submitted, required documents not all uploaded yet
	DocStatusPendingReview = "pending_review" // all required documents uploaded, queued for a reviewer
	DocStatusRejected      = "rejected"       // reviewer refused it; the user may re-submit
)

// User-facing state, derived from doc status + submitted_at (never from
// kyc_level, which stays "" until verified). The UI renders off this so it
// never has to recombine the underlying fields.
const (
	StateNotStarted    = "not_started"
	StateAwaitingFiles = "awaiting_files" // submitted, required documents not all uploaded yet
	StateUnderReview   = "under_review"   // all required documents uploaded, waiting on a reviewer
	StateRejected      = "rejected"
	StateVerified      = "verified"
)

// Document types accepted for manual review. The four selfie poses replace a
// single static photo: a printed photo or looping video cannot turn on
// command, so the reviewer gets a lightweight liveness signal without any
// server-side ML.
const (
	DocTypeIDFront     = "id_front"
	DocTypeIDBack      = "id_back"
	DocTypeSelfieUp    = "selfie_up"
	DocTypeSelfieDown  = "selfie_down"
	DocTypeSelfieLeft  = "selfie_left"
	DocTypeSelfieRight = "selfie_right"
)

// RequiredDocTypes are the documents Submit requires before it will accept a
// submission — see Service.Submit.
var RequiredDocTypes = []string{
	DocTypeIDFront, DocTypeIDBack,
	DocTypeSelfieUp, DocTypeSelfieDown, DocTypeSelfieLeft, DocTypeSelfieRight,
}

// Review decisions accepted by cmd/kyc.
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
)

const (
	// MinAge is the minimum age (years) to submit for KYC — real-money games
	// require adults, and the rule lives here so downstream services only read
	// kyc_level.
	MinAge = 18

	// SubmissionTTL is how long a pending submission holds the user's CPF. Past
	// it the submission is stale (no reviewer acted) and the user may submit
	// again — see Service.Submit.
	SubmissionTTL = 30 * 24 * time.Hour

	// MaxDocumentBytes caps an uploaded identity document or selfie clip.
	MaxDocumentBytes = 5 << 20

	// PresignTTL bounds how long an upload/download URL stays usable.
	PresignTTL = 10 * time.Minute

	// MaxDocuments caps how many files one submission may carry: the 6
	// required documents plus headroom for re-taking a blurry shot.
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

var (
	ErrInvalidCPF       = errors.New("invalid cpf")
	ErrInvalidBirthDate = errors.New("invalid birth date")
	ErrUnderage         = errors.New("user is under the minimum age")
	ErrCPFConflict      = errors.New("cpf already registered to another account")
	ErrAlreadyVerified  = errors.New("kyc already verified")
	ErrNotSubmitted     = errors.New("kyc data not submitted")
	ErrInvalidAddress   = errors.New("invalid address")

	// ErrInvalidMethod is returned when document verification is unavailable —
	// no bucket is configured (see Service.DocumentsEnabled).
	ErrInvalidMethod = errors.New("document verification is not available")

	// ErrSubmissionLocked guards a pending submission: identity data is frozen
	// until the submission is rejected or expires.
	ErrSubmissionLocked = errors.New("kyc submission is pending and cannot be changed")

	ErrInvalidDocumentType = errors.New("invalid document type")
	ErrInvalidContentType  = errors.New("invalid document content type")
	ErrDocumentNotUploaded = errors.New("document was not uploaded")
	ErrDocumentTooLarge    = errors.New("document exceeds the maximum size")
	ErrTooManyDocuments    = errors.New("too many documents for this submission")
	ErrNoDocuments         = errors.New("no documents uploaded")
	ErrInvalidDecision     = errors.New("invalid review decision")
)

// Address and Document are stored on the user item, so their canonical
// definitions live in the user package (kyc imports user, not the reverse).
type (
	Address  = user.Address
	Document = user.KYCDocument
)

// Status is the user-facing view of KYC state (CPF always masked, S3 keys
// never exposed).
type Status struct {
	State           string     `json:"state"`
	Level           string     `json:"level"`
	Method          string     `json:"method,omitempty"`
	CPFMasked       string     `json:"cpf_masked,omitempty"`
	LegalName       string     `json:"legal_name,omitempty"`
	BirthDate       string     `json:"birth_date,omitempty"`
	Address         *Address   `json:"address,omitempty"`
	Documents       []Document `json:"documents,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	SubmittedAt     string     `json:"submitted_at,omitempty"`
	ExpiresAt       string     `json:"expires_at,omitempty"`
	VerifiedAt      string     `json:"verified_at,omitempty"`
}

// Submission is the validated input of Service.Submit.
type Submission struct {
	CPF       string
	LegalName string
	BirthDate string
	Address   Address
}

// IsValidDocumentType reports whether t is an accepted document type.
func IsValidDocumentType(t string) bool {
	for _, want := range RequiredDocTypes {
		if t == want {
			return true
		}
	}
	return false
}

// allowedContentTypes are the MIME types a reviewer can actually open. The
// presigned PUT pins the content type, so this is what ends up in the bucket.
var allowedContentTypes = map[string]struct{}{
	"image/jpeg":      {},
	"image/png":       {},
	"image/heic":      {},
	"application/pdf": {},
	"video/webm":      {}, // selfie pose clips recorded via MediaRecorder
	"video/mp4":       {},
}

// IsValidContentType reports whether ct may be uploaded as an identity document.
func IsValidContentType(ct string) bool {
	_, ok := allowedContentTypes[ct]
	return ok
}
