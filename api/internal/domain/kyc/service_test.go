package kyc

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type memRepo struct {
	users map[string]*user.User
	cpfs  map[string]string // cpf -> userID
}

func newMemRepo() *memRepo {
	return &memRepo{users: map[string]*user.User{}, cpfs: map[string]string{}}
}

func (m *memRepo) GetUser(_ context.Context, userID string) (*user.User, error) {
	u, ok := m.users[userID]
	if !ok {
		return nil, user.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *memRepo) SaveSubmission(_ context.Context, userID string, rec Record, oldCPF string) error {
	if owner, taken := m.cpfs[rec.CPF]; taken && owner != userID {
		return ErrCPFConflict
	}
	if oldCPF != "" && oldCPF != rec.CPF {
		delete(m.cpfs, oldCPF)
	}
	m.cpfs[rec.CPF] = userID

	u := m.users[userID]
	u.CPF, u.LegalName, u.BirthDate = rec.CPF, rec.LegalName, rec.BirthDate
	u.KYCMethod, u.KYCDocStatus = rec.Method, rec.DocStatus
	u.KYCSubmittedAt, u.KYCExpiresAt = rec.SubmittedAt, rec.ExpiresAt
	u.Address = rec.Address
	// Documents are left untouched: they were already uploaded and validated
	// before Submit was called. Only the rejection reason is stale.
	u.KYCRejectionReason = ""
	return nil
}

func (m *memRepo) AddDocument(_ context.Context, userID string, doc Document, docStatus string) error {
	u := m.users[userID]
	u.KYCDocuments = append(u.KYCDocuments, doc)
	u.KYCDocStatus = docStatus
	return nil
}

func (m *memRepo) MarkVerified(_ context.Context, userID, verifiedAt string) error {
	u := m.users[userID]
	u.KYCLevel, u.KYCVerifiedAt = LevelVerified, verifiedAt
	u.KYCDocStatus, u.KYCRejectionReason = DocStatusNone, ""
	return nil
}

func (m *memRepo) MarkRejected(_ context.Context, userID, reason string) error {
	u := m.users[userID]
	u.KYCDocStatus, u.KYCRejectionReason = DocStatusRejected, reason
	u.KYCDocuments = nil
	return nil
}

func (m *memRepo) ListPendingKYC(_ context.Context) ([]*user.User, error) {
	var out []*user.User
	for _, u := range m.users {
		if u.KYCDocStatus == DocStatusPendingReview {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out, nil
}

// memPresigner is an in-memory stand-in for S3: PresignPut only records that a
// key was offered; tests call put() to simulate the browser actually uploading.
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

// put simulates the browser uploading to the presigned URL.
func (p *memPresigner) put(key string, size int64) { p.objects[key] = size }

func adultBirthDate() string {
	return time.Now().UTC().AddDate(-30, 0, 0).Format("2006-01-02")
}

func validAddress() Address {
	return Address{
		ZipCode:  "01001000",
		Street:   "Praça da Sé",
		Number:   "100",
		District: "Sé",
		City:     "São Paulo",
		State:    "SP",
	}
}

func submission(cpf, name string) Submission {
	return Submission{
		CPF:       cpf,
		LegalName: name,
		BirthDate: adultBirthDate(),
		Address:   validAddress(),
	}
}

func setup() (*Service, *memRepo, *memPresigner) {
	repo := newMemRepo()
	repo.users["u1"] = &user.User{PK: user.BuildPK("u1")}
	repo.users["u2"] = &user.User{PK: user.BuildPK("u2")}
	presigner := newMemPresigner()
	return NewService(repo, presigner), repo, presigner
}

// advance moves the service clock forward, which is how tests reach expiry.
func advance(svc *Service, d time.Duration) {
	svc.now = func() time.Time { return time.Now().UTC().Add(d) }
}

// uploadAllRequiredDocs uploads one document per RequiredDocTypes entry.
func uploadAllRequiredDocs(t *testing.T, svc *Service, presigner *memPresigner, userID string) {
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

// submitWithDocuments uploads every required document then submits.
func submitWithDocuments(t *testing.T, svc *Service, presigner *memPresigner, userID string, sub Submission) error {
	t.Helper()
	uploadAllRequiredDocs(t, svc, presigner, userID)
	return svc.Submit(context.Background(), userID, sub)
}

func TestSubmitRequiresAllDocuments(t *testing.T) {
	svc, repo, _ := setup()
	err := svc.Submit(context.Background(), "u1", submission("52998224725", "Fulano da Silva"))
	if !errors.Is(err, ErrNoDocuments) {
		t.Fatalf("err = %v, want ErrNoDocuments", err)
	}
	if repo.users["u1"].KYCDocStatus != DocStatusNone {
		t.Fatal("a rejected submission must not persist anything")
	}
}

func TestSubmitRequiresEveryPose(t *testing.T) {
	svc, _, presigner := setup()
	// Upload everything except the last required type (selfie_right).
	for _, docType := range RequiredDocTypes[:len(RequiredDocTypes)-1] {
		docID, _, _ := svc.PresignDocument(context.Background(), "u1", docType, "video/webm")
		presigner.put(BuildDocumentKey("u1", docID), 1024)
		_ = svc.ConfirmDocument(context.Background(), "u1", docID, docType)
	}
	err := svc.Submit(context.Background(), "u1", submission("52998224725", "Fulano"))
	if !errors.Is(err, ErrNoDocuments) {
		t.Fatalf("err = %v, want ErrNoDocuments (missing one pose)", err)
	}
}

func TestSubmitQueuesForReview(t *testing.T) {
	svc, repo, presigner := setup()
	if err := submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano da Silva")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	u := repo.users["u1"]
	if u.KYCLevel != LevelNone {
		t.Fatalf("kyc_level must stay empty until Review approves, got %q", u.KYCLevel)
	}
	if u.KYCMethod != MethodDocument || u.KYCDocStatus != DocStatusPendingReview {
		t.Fatalf("method = %q, doc_status = %q", u.KYCMethod, u.KYCDocStatus)
	}
	if u.KYCExpiresAt == "" || u.KYCSubmittedAt == "" {
		t.Fatal("submission must carry submitted_at and expires_at")
	}
	if u.Address.City != "São Paulo" || u.Address.State != "SP" {
		t.Fatalf("address = %+v", u.Address)
	}
	if len(u.KYCDocuments) != len(RequiredDocTypes) {
		t.Fatalf("documents = %d, want %d", len(u.KYCDocuments), len(RequiredDocTypes))
	}
}

func TestSubmitRejectsInvalidCPF(t *testing.T) {
	svc, _, _ := setup()
	err := svc.Submit(context.Background(), "u1", submission("11111111111", "Fulano"))
	if !errors.Is(err, ErrInvalidCPF) {
		t.Fatalf("err = %v, want ErrInvalidCPF", err)
	}
}

func TestSubmitRejectsBadBirthDate(t *testing.T) {
	svc, _, _ := setup()
	sub := submission("52998224725", "Fulano")
	sub.BirthDate = "31/12/1990"
	err := svc.Submit(context.Background(), "u1", sub)
	if !errors.Is(err, ErrInvalidBirthDate) {
		t.Fatalf("err = %v, want ErrInvalidBirthDate", err)
	}
}

func TestSubmitRejectsUnderage(t *testing.T) {
	svc, repo, _ := setup()
	sub := submission("52998224725", "Fulano")
	sub.BirthDate = time.Now().UTC().AddDate(-18, 0, 1).Format("2006-01-02") // 18 tomorrow
	err := svc.Submit(context.Background(), "u1", sub)
	if !errors.Is(err, ErrUnderage) {
		t.Fatalf("err = %v, want ErrUnderage", err)
	}
	if repo.users["u1"].KYCDocStatus != DocStatusNone {
		t.Fatal("underage submission must not persist anything")
	}
}

func TestSubmitAcceptsExactly18Today(t *testing.T) {
	svc, _, presigner := setup()
	sub := submission("52998224725", "Fulano")
	sub.BirthDate = time.Now().UTC().AddDate(-18, 0, 0).Format("2006-01-02")
	if err := submitWithDocuments(t, svc, presigner, "u1", sub); err != nil {
		t.Fatalf("18th birthday today must pass: %v", err)
	}
}

func TestSubmitRejectsDocumentMethodWhenStorageDisabled(t *testing.T) {
	repo := newMemRepo()
	repo.users["u1"] = &user.User{PK: user.BuildPK("u1")}
	svc := NewService(repo, nil) // no bucket configured

	err := svc.Submit(context.Background(), "u1", submission("52998224725", "Fulano"))
	if !errors.Is(err, ErrInvalidMethod) {
		t.Fatalf("err = %v, want ErrInvalidMethod", err)
	}
}

func TestSubmitRejectsBadAddress(t *testing.T) {
	tests := map[string]func(*Address){
		"short zip":     func(a *Address) { a.ZipCode = "0100" },
		"unknown state": func(a *Address) { a.State = "XX" },
		"no street":     func(a *Address) { a.Street = "" },
		"no number":     func(a *Address) { a.Number = "  " },
		"no city":       func(a *Address) { a.City = "" },
		"no district":   func(a *Address) { a.District = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo, _ := setup()
			sub := submission("52998224725", "Fulano")
			mutate(&sub.Address)

			err := svc.Submit(context.Background(), "u1", sub)
			if !errors.Is(err, ErrInvalidAddress) {
				t.Fatalf("err = %v, want ErrInvalidAddress", err)
			}
			if repo.users["u1"].KYCDocStatus != DocStatusNone {
				t.Fatal("invalid address must not persist anything")
			}
		})
	}
}

func TestSubmitNormalizesAddress(t *testing.T) {
	svc, repo, presigner := setup()
	sub := submission("52998224725", "Fulano")
	sub.Address.State = " sp "
	sub.Address.City = " São Paulo "

	if err := submitWithDocuments(t, svc, presigner, "u1", sub); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := repo.users["u1"].Address; got.State != "SP" || got.City != "São Paulo" {
		t.Fatalf("address = %+v", got)
	}
}

func TestSubmitRejectsDuplicateCPF(t *testing.T) {
	svc, _, presigner := setup()
	if err := submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano")); err != nil {
		t.Fatalf("Submit u1: %v", err)
	}
	err := submitWithDocuments(t, svc, presigner, "u2", submission("52998224725", "Beltrano"))
	if !errors.Is(err, ErrCPFConflict) {
		t.Fatalf("err = %v, want ErrCPFConflict", err)
	}
}

func TestResubmitWhilePendingIsLocked(t *testing.T) {
	svc, repo, presigner := setup()
	if err := submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano")); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	err := svc.Submit(context.Background(), "u1", submission("11144477735", "Fulano"))
	if !errors.Is(err, ErrSubmissionLocked) {
		t.Fatalf("err = %v, want ErrSubmissionLocked", err)
	}
	if repo.users["u1"].CPF != "52998224725" {
		t.Fatal("a locked submission must not have its CPF swapped")
	}
}

// Documents may not be re-uploaded either while a submission is locked.
func TestUploadRejectedWhilePending(t *testing.T) {
	svc, _, presigner := setup()
	_ = submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano"))

	_, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/jpeg")
	if !errors.Is(err, ErrSubmissionLocked) {
		t.Fatalf("err = %v, want ErrSubmissionLocked", err)
	}
}

func TestResubmitAfterExpiryRequeues(t *testing.T) {
	svc, repo, presigner := setup()
	_ = submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano"))

	advance(svc, SubmissionTTL+time.Hour)

	// The documents are still on file — an expired pending submission just
	// re-queues with the same evidence.
	if err := svc.Submit(context.Background(), "u1", submission("52998224725", "Fulano")); err != nil {
		t.Fatalf("expired submission must unlock: %v", err)
	}
	if repo.users["u1"].KYCDocStatus != DocStatusPendingReview {
		t.Fatalf("doc_status = %q, want pending_review", repo.users["u1"].KYCDocStatus)
	}
}

func TestResubmitAfterRejectionRequiresFreshDocuments(t *testing.T) {
	svc, repo, presigner := setup()
	uploadAndReview(t, svc, presigner, "u1", DecisionReject, "blurry photo")

	// Old documents were cleared by the rejection — resubmitting without
	// re-uploading must fail.
	err := svc.Submit(context.Background(), "u1", submission("11144477735", "Fulano"))
	if !errors.Is(err, ErrNoDocuments) {
		t.Fatalf("err = %v, want ErrNoDocuments (documents were cleared on rejection)", err)
	}

	if err := submitWithDocuments(t, svc, presigner, "u1", submission("11144477735", "Fulano")); err != nil {
		t.Fatalf("resubmit after fresh uploads: %v", err)
	}
	u := repo.users["u1"]
	if u.KYCRejectionReason != "" || u.KYCDocStatus != DocStatusPendingReview {
		t.Fatalf("a new submission must clear the previous rejection: %+v", u)
	}
}

func TestSubmitAfterVerifiedRejected(t *testing.T) {
	svc, _, presigner := setup()
	uploadAndReview(t, svc, presigner, "u1", DecisionApprove, "")

	err := svc.Submit(context.Background(), "u1", submission("11144477735", "Fulano"))
	if !errors.Is(err, ErrAlreadyVerified) {
		t.Fatalf("err = %v, want ErrAlreadyVerified", err)
	}
}

func TestPresignRejectsBadTypes(t *testing.T) {
	svc, _, _ := setup()

	if _, _, err := svc.PresignDocument(context.Background(), "u1", "passport", "image/png"); !errors.Is(err, ErrInvalidDocumentType) {
		t.Fatalf("err = %v, want ErrInvalidDocumentType", err)
	}
	if _, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "application/zip"); !errors.Is(err, ErrInvalidContentType) {
		t.Fatalf("err = %v, want ErrInvalidContentType", err)
	}
}

func TestPresignAcceptsVideoContentType(t *testing.T) {
	svc, _, _ := setup()
	if _, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeSelfieUp, "video/webm"); err != nil {
		t.Fatalf("selfie pose clips must accept video/webm: %v", err)
	}
}

func TestConfirmDocumentRequiresRealUpload(t *testing.T) {
	svc, _, _ := setup()

	docID, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/png")
	if err != nil {
		t.Fatalf("PresignDocument: %v", err)
	}

	// The client claims the upload happened, but the bucket is empty.
	err = svc.ConfirmDocument(context.Background(), "u1", docID, DocTypeIDFront)
	if !errors.Is(err, ErrDocumentNotUploaded) {
		t.Fatalf("err = %v, want ErrDocumentNotUploaded", err)
	}
}

func TestConfirmDocumentRejectsOversizedFile(t *testing.T) {
	svc, _, presigner := setup()

	docID, _, _ := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/png")
	presigner.put(BuildDocumentKey("u1", docID), MaxDocumentBytes+1)

	err := svc.ConfirmDocument(context.Background(), "u1", docID, DocTypeIDFront)
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("err = %v, want ErrDocumentTooLarge", err)
	}
}

func TestConfirmDocumentAccumulatesAwaitingFiles(t *testing.T) {
	svc, repo, presigner := setup()

	docID, _, _ := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/png")
	presigner.put(BuildDocumentKey("u1", docID), 1024)

	if err := svc.ConfirmDocument(context.Background(), "u1", docID, DocTypeIDFront); err != nil {
		t.Fatalf("ConfirmDocument: %v", err)
	}
	u := repo.users["u1"]
	if u.KYCDocStatus != DocStatusAwaitingFiles || len(u.KYCDocuments) != 1 {
		t.Fatalf("user = %+v", u)
	}
	if u.KYCDocuments[0].Key != BuildDocumentKey("u1", docID) {
		t.Fatalf("document key = %q", u.KYCDocuments[0].Key)
	}
}

func TestUploadRejectsTooManyDocuments(t *testing.T) {
	svc, _, presigner := setup()
	for i := 0; i < MaxDocuments; i++ {
		docID, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/png")
		if err != nil {
			t.Fatalf("PresignDocument #%d: %v", i, err)
		}
		presigner.put(BuildDocumentKey("u1", docID), 1024)
		if err := svc.ConfirmDocument(context.Background(), "u1", docID, DocTypeIDFront); err != nil {
			t.Fatalf("ConfirmDocument #%d: %v", i, err)
		}
	}
	if _, _, err := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/png"); !errors.Is(err, ErrTooManyDocuments) {
		t.Fatalf("err = %v, want ErrTooManyDocuments", err)
	}
}

func TestReviewApproveVerifies(t *testing.T) {
	svc, repo, presigner := setup()
	uploadAndReview(t, svc, presigner, "u1", DecisionApprove, "")

	u := repo.users["u1"]
	if u.KYCLevel != LevelVerified || u.KYCVerifiedAt == "" {
		t.Fatalf("user = %+v", u)
	}
}

func TestReviewRejectClearsDocumentsAndRecordsReason(t *testing.T) {
	svc, repo, presigner := setup()
	uploadAndReview(t, svc, presigner, "u1", DecisionReject, "document unreadable")

	u := repo.users["u1"]
	if u.KYCDocStatus != DocStatusRejected || u.KYCRejectionReason != "document unreadable" {
		t.Fatalf("user = %+v", u)
	}
	if u.KYCLevel != LevelNone {
		t.Fatal("a rejection must not change the level")
	}
	if len(u.KYCDocuments) != 0 {
		t.Fatal("a rejection must clear the uploaded documents")
	}
}

func TestReviewRequiresPendingSubmission(t *testing.T) {
	svc, _, _ := setup()
	err := svc.Review(context.Background(), "u1", DecisionApprove, "")
	if !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("err = %v, want ErrNotSubmitted", err)
	}
}

func TestReviewRejectsAlreadyVerified(t *testing.T) {
	svc, _, presigner := setup()
	uploadAndReview(t, svc, presigner, "u1", DecisionApprove, "")

	err := svc.Review(context.Background(), "u1", DecisionApprove, "")
	if !errors.Is(err, ErrAlreadyVerified) {
		t.Fatalf("err = %v, want ErrAlreadyVerified", err)
	}
}

func TestReviewRejectsUnknownDecision(t *testing.T) {
	svc, _, presigner := setup()
	_ = submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano"))

	err := svc.Review(context.Background(), "u1", "maybe", "")
	if !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("err = %v, want ErrInvalidDecision", err)
	}
}

func TestDocumentURLsForReviewer(t *testing.T) {
	svc, _, presigner := setup()
	uploadAllRequiredDocs(t, svc, presigner, "u1")

	urls, err := svc.DocumentURLs(context.Background(), "u1")
	if err != nil {
		t.Fatalf("DocumentURLs: %v", err)
	}
	if len(urls) != len(RequiredDocTypes) {
		t.Fatalf("urls = %+v", urls)
	}
}

func TestListPendingKYC(t *testing.T) {
	svc, _, presigner := setup()
	if err := submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano")); err != nil {
		t.Fatalf("Submit u1: %v", err)
	}
	// u2 never submits — must not show up.

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
		svc, _, _ := setup()
		assertState(t, svc, "u1", StateNotStarted)
	})

	t.Run("awaiting files", func(t *testing.T) {
		svc, _, presigner := setup()
		docID, _, _ := svc.PresignDocument(context.Background(), "u1", DocTypeIDFront, "image/png")
		presigner.put(BuildDocumentKey("u1", docID), 1024)
		_ = svc.ConfirmDocument(context.Background(), "u1", docID, DocTypeIDFront)
		assertState(t, svc, "u1", StateAwaitingFiles)
	})

	t.Run("under review", func(t *testing.T) {
		svc, _, presigner := setup()
		_ = submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano"))
		assertState(t, svc, "u1", StateUnderReview)
	})

	t.Run("rejected", func(t *testing.T) {
		svc, _, presigner := setup()
		uploadAndReview(t, svc, presigner, "u1", DecisionReject, "blurry")
		assertState(t, svc, "u1", StateRejected)
	})

	t.Run("verified", func(t *testing.T) {
		svc, _, presigner := setup()
		uploadAndReview(t, svc, presigner, "u1", DecisionApprove, "")
		assertState(t, svc, "u1", StateVerified)
	})

	// A stale pending submission is indistinguishable from none — the user
	// must be able to start over (Submit will let them, documents intact).
	t.Run("expired pending reads as not started", func(t *testing.T) {
		svc, _, presigner := setup()
		_ = submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano"))
		advance(svc, SubmissionTTL+time.Hour)
		assertState(t, svc, "u1", StateNotStarted)
	})
}

func TestGetMasksCPFAndHidesKeys(t *testing.T) {
	svc, _, presigner := setup()
	_ = submitWithDocuments(t, svc, presigner, "u1", submission("52998224725", "Fulano"))

	st, err := svc.Get(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.CPFMasked != "***.***.***-25" || st.State != StateUnderReview {
		t.Fatalf("status = %+v", st)
	}
	if st.Address == nil || st.Address.State != "SP" {
		t.Fatalf("address = %+v", st.Address)
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

// uploadAndReview drives a document submission all the way to a review decision.
func uploadAndReview(t *testing.T, svc *Service, presigner *memPresigner, userID, decision, reason string) {
	t.Helper()
	if err := submitWithDocuments(t, svc, presigner, userID, submission("52998224725", "Fulano")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := svc.Review(context.Background(), userID, decision, reason); err != nil {
		t.Fatalf("Review: %v", err)
	}
}
