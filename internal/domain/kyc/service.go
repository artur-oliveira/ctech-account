package kyc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.aoctech.app/account/internal/domain/user"
)

const birthDateLayout = "2006-01-02"

// Presigner issues time-bounded S3 URLs. The service never touches object
// bytes — the browser uploads straight to the bucket.
type Presigner interface {
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// Size returns the stored object's length; it is how the service proves a
	// presigned upload actually happened before trusting the client's word.
	Size(ctx context.Context, key string) (int64, error)
}

// Service implements the KYC state machine:
//
//	none → awaiting_files (documents uploaded one at a time via
//	  PresignDocument/ConfirmDocument, before any identity data exists)
//	  → pending_review (Submit, once every RequiredDocTypes is present)
//	  → verified | rejected (Review, a human reviewer via cmd/kyc)
//
// A pending submission is frozen: identity data and documents can only change
// once it is rejected (which clears the documents for a fresh attempt) or has
// passed its expiry.
type Service struct {
	repo      Repository
	presigner Presigner
	now       func() time.Time
}

func NewService(repo Repository, presigner Presigner) *Service {
	return &Service{repo: repo, presigner: presigner, now: func() time.Time { return time.Now().UTC() }}
}

// Submit validates the identity data and, provided every RequiredDocTypes
// document is already uploaded (see PresignDocument/ConfirmDocument), queues
// the submission for human review. Nothing is persisted on a validation
// error, and kyc_level stays "" — only Review can set LevelVerified.
func (s *Service) Submit(ctx context.Context, userID string, sub Submission) error {
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
	if !s.DocumentsEnabled() {
		return ErrInvalidMethod
	}
	NormalizeAddress(&sub.Address)
	if err := ValidateAddress(sub.Address); err != nil {
		return err
	}

	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.KYCLevel == LevelVerified {
		return ErrAlreadyVerified
	}
	if s.isLocked(u) {
		return ErrSubmissionLocked
	}
	if !hasRequiredDocuments(u.KYCDocuments) {
		return ErrNoDocuments
	}

	now := s.now()
	return s.repo.SaveSubmission(ctx, userID, Record{
		CPF:         sub.CPF,
		LegalName:   strings.TrimSpace(sub.LegalName),
		BirthDate:   sub.BirthDate,
		Method:      MethodDocument,
		Address:     sub.Address,
		DocStatus:   DocStatusPendingReview,
		SubmittedAt: now.Format(TimeLayout),
		ExpiresAt:   now.Add(SubmissionTTL).Format(TimeLayout),
	}, u.CPF)
}

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

// isLocked reports whether the user has a submission under review that has
// not expired. Documents may not be re-uploaded nor identity data resubmitted
// while locked — only Review (approve/reject) or expiry unlocks it.
func (s *Service) isLocked(u *user.User) bool {
	if u.KYCDocStatus != DocStatusPendingReview {
		return false
	}
	return !s.isExpired(u)
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

// PresignDocument issues an upload URL for one identity document. The object is
// only recorded once ConfirmDocument proves it landed in the bucket.
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

	documentID = uuid.NewString()
	uploadURL, err = s.presigner.PresignPut(ctx, BuildDocumentKey(userID, documentID), contentType, PresignTTL)
	if err != nil {
		return "", "", err
	}
	return documentID, uploadURL, nil
}

// ConfirmDocument records an uploaded document and queues the submission for
// review. The size check is what stops a client from claiming an upload it
// never made, or one that exceeds the cap the presigned URL could not enforce.
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
	return s.repo.AddDocument(ctx, userID, doc, DocStatusAwaitingFiles)
}

// DocumentsEnabled reports whether the document verification path is available
// (it needs a configured bucket — see config.KYCDocumentsBucket).
func (s *Service) DocumentsEnabled() bool { return s.presigner != nil }

// assertAcceptsDocuments guards both document endpoints: uploads are allowed
// any time the user isn't already verified or locked by a submission under
// active review.
func (s *Service) assertAcceptsDocuments(u *user.User) error {
	if !s.DocumentsEnabled() {
		return ErrInvalidMethod
	}
	if u.KYCLevel == LevelVerified {
		return ErrAlreadyVerified
	}
	if s.isLocked(u) {
		return ErrSubmissionLocked
	}
	return nil
}

// Review applies a human reviewer's decision to a document submission that is
// currently under review. Approving verifies the user; rejecting clears the
// uploaded documents (they were judged insufficient) and unlocks a fresh
// upload-then-submit cycle.
func (s *Service) Review(ctx context.Context, userID, decision, reason string) error {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.KYCLevel == LevelVerified {
		return ErrAlreadyVerified
	}
	if u.KYCDocStatus != DocStatusPendingReview {
		return ErrNotSubmitted
	}

	switch decision {
	case DecisionApprove:
		return s.repo.MarkVerified(ctx, userID, s.now().Format(TimeLayout))
	case DecisionReject:
		return s.repo.MarkRejected(ctx, userID, strings.TrimSpace(reason))
	default:
		return ErrInvalidDecision
	}
}

// ListPendingKYC returns every user whose submission is currently queued for
// review — used by cmd/kyc list.
func (s *Service) ListPendingKYC(ctx context.Context) ([]*user.User, error) {
	return s.repo.ListPendingKYC(ctx)
}

// DocumentURLs returns presigned GET URLs so a reviewer can open the uploaded
// files. Internal callers only — the keys themselves never leave the service.
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

// DocumentURL is one reviewable document plus a short-lived download link.
type DocumentURL struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	UploadedAt string `json:"uploaded_at"`
	URL        string `json:"url"`
}

// Get returns the user-facing KYC status (CPF masked).
func (s *Service) Get(ctx context.Context, userID string) (*Status, error) {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	st := &Status{
		State:           s.state(u),
		Level:           u.KYCLevel,
		Method:          u.KYCMethod,
		CPFMasked:       MaskCPF(u.CPF),
		LegalName:       u.LegalName,
		BirthDate:       u.BirthDate,
		Documents:       u.KYCDocuments,
		RejectionReason: u.KYCRejectionReason,
		SubmittedAt:     u.KYCSubmittedAt,
		ExpiresAt:       u.KYCExpiresAt,
		VerifiedAt:      u.KYCVerifiedAt,
	}
	if !u.Address.IsZero() {
		addr := u.Address
		st.Address = &addr
	}
	return st, nil
}

// state collapses doc status into the single value the UI branches on. It
// never reads kyc_level except to detect LevelVerified — the rest of the
// machine lives entirely in kyc_doc_status.
func (s *Service) state(u *user.User) string {
	switch {
	case u.KYCLevel == LevelVerified:
		return StateVerified
	case u.KYCDocStatus == DocStatusRejected:
		return StateRejected
	case u.KYCDocStatus == DocStatusPendingReview && s.isExpired(u):
		// A stale pending submission is indistinguishable from none: the user
		// must submit again, and Submit will let them (their documents are
		// still on file, so this just re-queues them).
		return StateNotStarted
	case u.KYCDocStatus == DocStatusPendingReview:
		return StateUnderReview
	case u.KYCDocStatus == DocStatusAwaitingFiles:
		return StateAwaitingFiles
	default:
		return StateNotStarted
	}
}

// GetUser exposes the raw user record for internal (service-to-service)
// consumers that need the unmasked CPF.
func (s *Service) GetUser(ctx context.Context, userID string) (*user.User, error) {
	return s.repo.GetUser(ctx, userID)
}

// isAtLeast reports whether someone born on born is at least years old at now.
// The comparison uses date arithmetic so the birthday itself counts.
func isAtLeast(born time.Time, years int, now time.Time) bool {
	return !now.Before(born.AddDate(years, 0, 0))
}
