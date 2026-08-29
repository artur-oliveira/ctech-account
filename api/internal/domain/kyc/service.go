package kyc

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"uuid"

	"gopkg.aoctech.app/account/api/internal/domain/risk"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/api-commons/observability"
)

const birthDateLayout = "2006-01-02"

// Presigner issues time-bounded S3 URLs. The service never touches object
// bytes — the browser uploads straight to the bucket.
type Presigner interface {
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Size(ctx context.Context, key string) (int64, error)
}

// Service implements the tiered KYC state machine:
//
//	none → basic/verified (SubmitBasic)
//	  → enhanced/pending (SubmitEnhanced, once all RequiredDocTypes uploaded)
//	  → enhanced/verified | enhanced/rejected (Review, a human via cmd/kyc)
//	enhanced/rejected → enhanced/pending (fresh document uploads + SubmitEnhanced)
type Service struct {
	repo      Repository
	presigner Presigner
	risk      risk.Evaluator
	now       func() time.Time
}

func NewService(repo Repository, presigner Presigner, riskEvaluator risk.Evaluator) *Service {
	return &Service{
		repo: repo, presigner: presigner, risk: riskEvaluator,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// ── Basic (CPF/name/birthdate/phone/address) ────────────────────────────────

// SubmitBasic validates identity data, claims the CPF, and grants Basic KYC.
// The phone is collected for downstream onboarding but is not verified by SMS.
// Address is collected here (not Enhanced) for the planned BaaS integration.
func (s *Service) SubmitBasic(ctx context.Context, userID, ip string, sub BasicSubmission) error {
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
	NormalizeAddress(&sub.Address)
	if err := ValidateAddress(sub.Address); err != nil {
		return err
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
		PhoneNumber: sub.PhoneNumber, Address: sub.Address, SubmittedAt: now.Format(TimeLayout),
	}, u.CPF); err != nil {
		return err
	}

	s.evaluateRisk(ctx, userID, ip)
	return nil
}

// isBasicLocked reports whether Basic identity data is immutable: once
// submitted (or once Enhanced has been reached, which implies it), it never
// regresses.
func isBasicLocked(u *user.User) bool {
	return (u.KYCLevel == LevelBasic && u.KYCStatus == StatusVerified) || u.KYCLevel == LevelEnhanced
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
	if err := s.repo.DeletePendingDocument(ctx, documentID); err != nil {
		observability.Warn(ctx, "kyc: failed to delete confirmed pending document", err,
			"user_id", userID, "document_id", documentID)
	}
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
		observability.Error(ctx, "kyc: risk evaluation failed", err, "user_id", userID)
		return
	}
	if err := s.repo.SaveRiskAssessment(ctx, userID, a); err != nil {
		observability.Error(ctx, "kyc: failed to save risk assessment", err, "user_id", userID)
	}
}

// Review applies a human reviewer's decision to an Enhanced submission that
// is currently under review.
func (s *Service) Review(ctx context.Context, userID, decision, reason string) error {
	code := ""
	if decision == DecisionReject {
		code = RejectionOther
	}
	return s.ReviewBy(ctx, userID, decision, code, reason, ReviewActor{ID: "cli", Name: "CLI"})
}

// ReviewBy applies a decision and persistently attributes it to the current
// authenticated reviewer. Repository conditions guarantee only the first
// concurrent decision can transition an Enhanced submission out of pending.
func (s *Service) ReviewBy(ctx context.Context, userID, decision, reasonCode, details string, actor ReviewActor) error {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.KYCLevel != LevelEnhanced || u.KYCStatus != StatusPending {
		return ErrNotSubmitted
	}

	switch decision {
	case DecisionApprove:
		return s.repo.MarkVerified(ctx, userID, s.now().Format(TimeLayout), actor)
	case DecisionReject:
		details = strings.TrimSpace(details)
		if !IsValidRejectionCode(reasonCode) || utf8.RuneCountInString(details) > 255 || (reasonCode == RejectionOther && details == "") {
			return ErrInvalidRejectionReason
		}
		docs := make([]Document, len(u.KYCDocuments))
		copy(docs, u.KYCDocuments)
		if err := s.repo.MarkRejected(ctx, userID, reasonCode, details, s.now().Format(TimeLayout), actor); err != nil {
			return err
		}
		s.purgeRejectedObjects(ctx, docs)
		return nil
	default:
		return ErrInvalidDecision
	}
}

func (s *Service) ListKYCReviews(ctx context.Context, queue string) ([]*user.User, error) {
	if queue != ReviewQueuePending && queue != ReviewQueueCompleted {
		return nil, ErrInvalidDecision
	}
	return s.repo.ListKYCReviews(ctx, queue)
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

	var addr *Address
	if !u.Address.IsZero() {
		addr = &u.Address
	}

	return &Status{
		State:           s.state(u),
		Level:           u.KYCLevel,
		CPFMasked:       MaskCPF(u.CPF),
		LegalName:       u.LegalName,
		BirthDate:       u.BirthDate,
		PhoneMasked:     MaskPhone(u.PhoneNumber),
		Address:         addr,
		BasicVerifiedAt: u.KYCBasicVerifiedAt,
		Documents:       u.KYCDocuments,
		RejectionReason: u.KYCRejectionReason,
		RejectionCode:   u.KYCRejectionCode,
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
func (s *Service) purgeRejectedObjects(ctx context.Context, docs []Document) {
	deleter, ok := s.presigner.(objectDeleter)
	if !ok {
		return
	}
	asyncCtx := context.WithoutCancel(ctx)
	go func() {
		for _, d := range docs {
			key := d.Key
			if err := deleter.DeleteObject(asyncCtx, key); err != nil {
				observability.Error(asyncCtx, "kyc: failed to delete rejected document object", err, "object_key", key)
			}
		}
	}()
}
