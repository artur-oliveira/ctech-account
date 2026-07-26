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

	"github.com/google/uuid"
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

// ── Basic (CPF/name/birthdate/phone/address + SMS OTP) ──────────────────────

// SubmitBasic validates identity data, claims the CPF, and sends a fresh OTP.
// Reachable while Basic is unset or still pending (not yet phone-verified) —
// see isBasicLocked. Address is collected here (not Enhanced) for the planned
// BaaS integration.
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

	// A resubmission carries corrected data (e.g. a mistyped phone number) and
	// always needs a fresh code — bypass the resend cooldown here; ResendCode
	// still enforces it below via sendOTP.
	if err := s.sendFreshOTP(ctx, userID, sub.PhoneNumber); err != nil {
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

// sendOTP is ResendCode's send path: it enforces the resend cooldown before
// dispatching a fresh code via sendFreshOTP.
func (s *Service) sendOTP(ctx context.Context, userID, phone string) error {
	onCooldown, err := s.cache.Exists(ctx, otpResendKey(userID))
	if err != nil {
		return err
	}
	if onCooldown {
		return ErrResendCooldown
	}
	return s.sendFreshOTP(ctx, userID, phone)
}

// sendFreshOTP generates+hashes a fresh code, resets the attempt counter,
// (re)starts the resend cooldown, and dispatches via s.sms — regardless of
// any cooldown already in effect. SubmitBasic calls this directly (a
// resubmission with corrected data must not be blocked by a cooldown from
// the previous attempt); ResendCode goes through sendOTP, which checks the
// cooldown first.
func (s *Service) sendFreshOTP(ctx context.Context, userID, phone string) error {
	code, err := generateOTP()
	if err != nil {
		return err
	}
	if err := s.cache.Set(ctx, otpKey(userID), crypto.HashToken(code), OTPTTL); err != nil {
		return err
	}
	_ = s.cache.Delete(ctx, otpAttemptsKey(userID))
	if err := s.cache.Set(ctx, otpResendKey(userID), "1", OTPResendCooldown); err != nil {
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

	documentID = uuid.NewString()
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
