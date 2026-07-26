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
	u.Address = rec.Address
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

func testAddress() Address {
	return Address{
		ZipCode: "01310100", Street: "Av. Paulista", Number: "1000",
		District: "Bela Vista", City: "São Paulo", State: "SP",
	}
}

func basicSub(cpf string) BasicSubmission {
	return BasicSubmission{
		CPF: cpf, LegalName: "Fulano da Silva", BirthDate: adultBirthDate(),
		PhoneNumber: "+5511987654321", Address: testAddress(),
	}
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
	svc.cache.Now = func() time.Time { return time.Now().Add(d) }
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

func TestSubmitBasicRejectsInvalidAddress(t *testing.T) {
	svc, _, _, _ := setup()
	sub := basicSub("52998224725")
	sub.Address = Address{}
	err := svc.SubmitBasic(context.Background(), "u1", "1.2.3.4", sub)
	if !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("err = %v, want ErrInvalidAddress", err)
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
	if u.Address.IsZero() {
		t.Fatal("address must be persisted on submit")
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
	if st.Address == nil {
		t.Fatal("address must be returned once submitted")
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
