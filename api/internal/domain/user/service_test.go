package user_test

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/legal"
)

// mockRepo is an in-memory Repository implementation for unit tests.
type mockRepo struct {
	byID    map[string]*user.User
	byEmail map[string]*user.User
	nextID  int
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		byID:    make(map[string]*user.User),
		byEmail: make(map[string]*user.User),
	}
}

func (m *mockRepo) GetByID(_ context.Context, userID string) (*user.User, error) {
	u, ok := m.byID[userID]
	if !ok {
		return nil, user.ErrNotFound
	}
	return u, nil
}

func (m *mockRepo) GetByEmail(_ context.Context, email string) (*user.User, error) {
	u, ok := m.byEmail[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return u, nil
}

func (m *mockRepo) Create(_ context.Context, u *user.User) error {
	if u.PK == "" {
		m.nextID++
		u.PK = "USER_test-" + string(rune('0'+m.nextID))
	}
	m.byID[u.ID()] = u
	m.byEmail[u.Email] = u
	return nil
}

func (m *mockRepo) Update(_ context.Context, userID string, updates map[string]any) error {
	u, ok := m.byID[userID]
	if !ok {
		return user.ErrNotFound
	}
	if v, ok := updates["first_name"].(string); ok {
		u.FirstName = v
	}
	if v, ok := updates["last_name"].(string); ok {
		u.LastName = v
	}
	if v, ok := updates["display_name"].(string); ok {
		u.DisplayName = v
	}
	if v, ok := updates["password_hash"].(string); ok {
		u.PasswordHash = v
	}
	if v, ok := updates["avatar_url"].(string); ok {
		u.AvatarURL = v
	}
	if v, ok := updates["google_sub"].(string); ok {
		u.GoogleSub = v
	}
	if v, ok := updates["email_verified"].(bool); ok {
		u.EmailVerified = v
	}
	if v, ok := updates["tos_version"].(string); ok {
		u.TOSVersion = v
	}
	if v, ok := updates["tos_accepted_at"].(string); ok {
		u.TOSAcceptedAt = v
	}
	if v, ok := updates["privacy_version"].(string); ok {
		u.PrivacyVersion = v
	}
	if v, ok := updates["privacy_accepted_at"].(string); ok {
		u.PrivacyAcceptedAt = v
	}
	return nil
}

// errRepo returns errors for all operations.
type errRepo struct{}

func (e *errRepo) GetByID(_ context.Context, _ string) (*user.User, error) {
	return nil, errors.New("db error")
}
func (e *errRepo) GetByEmail(_ context.Context, _ string) (*user.User, error) {
	return nil, errors.New("db error")
}
func (e *errRepo) Create(_ context.Context, _ *user.User) error {
	return errors.New("db error")
}
func (e *errRepo) Update(_ context.Context, _ string, _ map[string]any) error {
	return errors.New("db error")
}

func TestRegister_Success(t *testing.T) {
	svc := user.NewService(newMockRepo())
	u, err := svc.Register(context.Background(), "test@example.com", "password123", "Alice", "Smith")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if u.Email != "test@example.com" {
		t.Errorf("email mismatch: %s", u.Email)
	}
	if u.FirstName != "Alice" {
		t.Errorf("first_name mismatch: %s", u.FirstName)
	}
}

func TestRegister_StampsTermsAcceptance(t *testing.T) {
	svc := user.NewService(newMockRepo())
	u, err := svc.Register(context.Background(), "terms@example.com", "password123", "Alice", "Smith")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if u.TOSVersion != legal.CurrentToSVersion || u.TOSAcceptedAt == "" {
		t.Errorf("expected ToS acceptance stamped, got version=%q at=%q", u.TOSVersion, u.TOSAcceptedAt)
	}
	if u.PrivacyVersion != legal.CurrentPrivacyVersion || u.PrivacyAcceptedAt == "" {
		t.Errorf("expected Privacy acceptance stamped, got version=%q at=%q", u.PrivacyVersion, u.PrivacyAcceptedAt)
	}
}

func TestRegister_EmailNormalised(t *testing.T) {
	svc := user.NewService(newMockRepo())
	u, err := svc.Register(context.Background(), "  ALICE@Example.COM  ", "password123", "Alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email not normalised: %s", u.Email)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	_, _ = svc.Register(context.Background(), "dup@example.com", "password123", "A", "")
	_, err := svc.Register(context.Background(), "dup@example.com", "password456", "B", "")
	if !errors.Is(err, user.ErrEmailConflict) {
		t.Errorf("expected ErrEmailConflict, got %v", err)
	}
}

// registerVerified creates an account and confirms its email, the state required
// for a successful password login.
func registerVerified(t *testing.T, svc *user.Service, email, password, firstName string) *user.User {
	t.Helper()
	u, err := svc.Register(context.Background(), email, password, firstName, "")
	if err != nil {
		t.Fatalf("registering %s: %v", email, err)
	}
	if err := svc.MarkEmailVerified(context.Background(), u.ID()); err != nil {
		t.Fatalf("verifying %s: %v", email, err)
	}
	return u
}

func TestLogin_Success(t *testing.T) {
	svc := user.NewService(newMockRepo())
	registerVerified(t, svc, "login@example.com", "securepass", "Bob")
	u, err := svc.Login(context.Background(), "login@example.com", "securepass")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if u.Email != "login@example.com" {
		t.Errorf("wrong user returned")
	}
}

func TestLogin_UnverifiedEmail(t *testing.T) {
	svc := user.NewService(newMockRepo())
	_, _ = svc.Register(context.Background(), "unver@example.com", "securepass", "Bob", "")
	_, err := svc.Login(context.Background(), "unver@example.com", "securepass")
	if !errors.Is(err, user.ErrEmailNotVerified) {
		t.Errorf("expected ErrEmailNotVerified, got %v", err)
	}
}

// A Google-created account has no password; password login must be refused with
// generic invalid-credentials, never a distinct "no password" signal.
func TestLogin_PasswordlessAccount(t *testing.T) {
	svc := user.NewService(newMockRepo())
	_, _, err := svc.FindOrCreateByGoogle(context.Background(), "gsub", "g@example.com", "G", "User", "")
	if err != nil {
		t.Fatalf("google create: %v", err)
	}
	_, err = svc.Login(context.Background(), "g@example.com", "anything")
	if !errors.Is(err, user.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

// Google sign-up must mark the address verified without an email round-trip.
func TestFindOrCreateByGoogle_AutoVerifiesEmail(t *testing.T) {
	svc := user.NewService(newMockRepo())
	u, created, err := svc.FindOrCreateByGoogle(context.Background(), "gsub", "auto@example.com", "A", "B", "http://pic")
	if err != nil {
		t.Fatalf("google create: %v", err)
	}
	if !created {
		t.Error("expected created=true for a brand-new Google account")
	}
	if !u.EmailVerified {
		t.Error("expected Google account to be email-verified")
	}
	if u.PasswordHash != "" {
		t.Error("expected Google account to have no password")
	}
	if u.AvatarURL != "http://pic" {
		t.Errorf("expected avatar to be captured, got %q", u.AvatarURL)
	}
	if u.TOSAcceptedAt != "" {
		t.Error("expected a brand-new Google account to NOT have accepted terms yet")
	}
}

// A returning Google user must not be re-flagged as created, and a second
// FindOrCreateByGoogle call must not re-trigger the terms gate.
func TestFindOrCreateByGoogle_ExistingUserNotFlaggedCreated(t *testing.T) {
	svc := user.NewService(newMockRepo())
	first, created, err := svc.FindOrCreateByGoogle(context.Background(), "gsub", "again@example.com", "A", "B", "")
	if err != nil {
		t.Fatalf("google create: %v", err)
	}
	if !created {
		t.Fatal("expected first call to report created=true")
	}
	if err := svc.AcceptTerms(context.Background(), first.ID(), true, true); err != nil {
		t.Fatalf("accept terms: %v", err)
	}

	second, created, err := svc.FindOrCreateByGoogle(context.Background(), "gsub", "again@example.com", "A", "B", "")
	if err != nil {
		t.Fatalf("google find: %v", err)
	}
	if created {
		t.Error("expected second call to report created=false")
	}
	if second.TOSAcceptedAt == "" || second.PrivacyVersion == "" {
		t.Error("expected terms acceptance to persist across calls")
	}
}

// ToS and Privacy version independently: a user re-accepting only the document
// that changed must not have the other one silently restamped to the current
// version — that would forge an acceptance they never gave.
func TestAcceptTerms_StampsOnlyTheAcceptedDocuments(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, _, err := svc.FindOrCreateByGoogle(context.Background(), "gsub", "partial@example.com", "P", "T", "")
	if err != nil {
		t.Fatalf("google create: %v", err)
	}

	if err := svc.AcceptTerms(context.Background(), u.ID(), true, false); err != nil {
		t.Fatalf("accept terms: %v", err)
	}

	got, err := svc.GetByID(context.Background(), u.ID())
	if err != nil {
		t.Fatalf("fetching user: %v", err)
	}
	if got.TOSVersion != legal.CurrentToSVersion || got.TOSAcceptedAt == "" {
		t.Errorf("expected ToS stamped at %s, got version=%q at=%q", legal.CurrentToSVersion, got.TOSVersion, got.TOSAcceptedAt)
	}
	if got.PrivacyVersion != "" || got.PrivacyAcceptedAt != "" {
		t.Errorf("expected privacy untouched, got version=%q at=%q", got.PrivacyVersion, got.PrivacyAcceptedAt)
	}
	if !legal.PendingFor(got.TOSVersion, got.PrivacyVersion).Privacy {
		t.Error("expected privacy to remain pending")
	}
}

func TestSetInitialPassword(t *testing.T) {
	svc := user.NewService(newMockRepo())
	u, _, _ := svc.FindOrCreateByGoogle(context.Background(), "gsub", "setpw@example.com", "S", "P", "")

	if err := svc.SetInitialPassword(context.Background(), u.ID(), "brandnew123"); err != nil {
		t.Fatalf("set initial password: %v", err)
	}
	// The account can now log in with a password (Google already verified the email).
	if _, err := svc.Login(context.Background(), "setpw@example.com", "brandnew123"); err != nil {
		t.Fatalf("login after setting password: %v", err)
	}
	// A second call must not silently overwrite the password.
	if err := svc.SetInitialPassword(context.Background(), u.ID(), "other12345"); !errors.Is(err, user.ErrPasswordAlreadySet) {
		t.Errorf("expected ErrPasswordAlreadySet, got %v", err)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc := user.NewService(newMockRepo())
	_, _ = svc.Register(context.Background(), "pw@example.com", "correctpass", "C", "")
	_, err := svc.Login(context.Background(), "pw@example.com", "wrongpass")
	if !errors.Is(err, user.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	svc := user.NewService(newMockRepo())
	_, err := svc.Login(context.Background(), "nobody@example.com", "anything")
	if !errors.Is(err, user.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_DisabledAccount(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	_, _ = svc.Register(context.Background(), "disabled@example.com", "pass", "D", "")
	u := repo.byEmail["disabled@example.com"]
	u.IsEnabled = false
	_, err := svc.Login(context.Background(), "disabled@example.com", "pass")
	if !errors.Is(err, user.ErrAccountDisabled) {
		t.Errorf("expected ErrAccountDisabled, got %v", err)
	}
}

func TestChangePassword_Success(t *testing.T) {
	svc := user.NewService(newMockRepo())
	_, _ = svc.Register(context.Background(), "chpw@example.com", "oldpass123", "E", "")

	repo := newMockRepo()
	svc2 := user.NewService(repo)
	u := registerVerified(t, svc2, "chpw@example.com", "oldpass123", "E")
	err := svc2.ChangePassword(context.Background(), u.ID(), "oldpass123", "newpass456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify new password works.
	_, err = svc2.Login(context.Background(), "chpw@example.com", "newpass456")
	if err != nil {
		t.Errorf("new password should work, got: %v", err)
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, _ := svc.Register(context.Background(), "chpw2@example.com", "correct", "F", "")
	err := svc.ChangePassword(context.Background(), u.ID(), "wrong", "newpass")
	if !errors.Is(err, user.ErrCurrentPasswordIncorrect) {
		t.Errorf("expected ErrCurrentPasswordIncorrect, got %v", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, _ := svc.Register(context.Background(), "profile@example.com", "pass", "Old", "Name")
	err := svc.UpdateProfile(context.Background(), u.ID(), "New", "Surname", "nick")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated, _ := svc.GetByID(context.Background(), u.ID())
	if updated.FirstName != "New" {
		t.Errorf("first_name not updated: %s", updated.FirstName)
	}
}

// Regression: a Google identity must never be merged into a password account.
// An attacker who pre-registers the victim's email with a password, then
// logs in via Google as the real owner, must be refused — not dropped into
// the victim's session (account takeover).
func TestFindOrCreateByGoogle_RefusesPasswordAccount(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	// Attacker pre-registers victim@example.com with a password.
	if _, err := svc.Register(context.Background(), "victim@example.com", "attackerpw", "A", ""); err != nil {
		t.Fatalf("pre-register: %v", err)
	}

	// Real owner signs in via Google (verified email == victim@example.com).
	_, _, err := svc.FindOrCreateByGoogle(context.Background(), "gsub-real-owner", "victim@example.com", "V", "Ictim", "")
	if !errors.Is(err, user.ErrGoogleEmailConflict) {
		t.Fatalf("expected ErrGoogleEmailConflict, got %v", err)
	}
}

// Legacy Google account (no google_sub stored yet) must bind its sub on the
// next login so future logins are keyed on it, not on the email.
func TestFindOrCreateByGoogle_BindsSubOnLegacyAccount(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	first, created, err := svc.FindOrCreateByGoogle(context.Background(), "gsub-legacy", "legacy@example.com", "L", "", "")
	if err != nil || !created {
		t.Fatalf("legacy google create: created=%v err=%v", created, err)
	}
	if first.GoogleSub != "gsub-legacy" {
		t.Fatalf("expected google_sub stamped at create, got %q", first.GoogleSub)
	}

	// Second login with the same sub still resolves to the same account
	// and is not re-flagged as created.
	second, created, err := svc.FindOrCreateByGoogle(context.Background(), "gsub-legacy", "legacy@example.com", "L", "", "")
	if err != nil {
		t.Fatalf("legacy google find: %v", err)
	}
	if created || second.ID() != first.ID() || second.GoogleSub != "gsub-legacy" {
		t.Errorf("expected same account keyed on sub, got created=%v id=%s sub=%q", created, second.ID(), second.GoogleSub)
	}
}

// Regression: an already-linked password account (google_sub matches the
// presented identity) must log in via Google, not be refused. This is
// the legit-owner case after authenticated linking has bound the sub.
func TestFindOrCreateByGoogle_LinkedPasswordAccountLogsIn(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	// Owner creates a password account and verifies it.
	u, _ := svc.Register(context.Background(), "owner@example.com", "pw", "O", "")
	if err := svc.MarkEmailVerified(context.Background(), u.ID()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Authenticated linking binds the Google sub to THEIR account.
	if err := svc.LinkGoogle(context.Background(), u.ID(), "gsub-owner", "owner@example.com"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// Subsequent anonymous Google login now resolves to the same account.
	got, created, err := svc.FindOrCreateByGoogle(context.Background(), "gsub-owner", "owner@example.com", "O", "", "")
	if err != nil {
		t.Fatalf("google find after link: %v", err)
	}
	if created || got.ID() != u.ID() {
		t.Errorf("expected existing linked account, got created=%v id=%s", created, got.ID())
	}
}

// LinkGoogle must reject when the Google-verified email does not match
// the authenticated account's email — no binding a Google identity for a
// different address onto someone else's account.
func TestLinkGoogle_EmailMismatch(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, _ := svc.Register(context.Background(), "a@example.com", "pw", "A", "")
	if err := svc.LinkGoogle(context.Background(), u.ID(), "gsub-x", "b@example.com"); !errors.Is(err, user.ErrGoogleEmailConflict) {
		t.Fatalf("expected ErrGoogleEmailConflict, got %v", err)
	}
}

// LinkGoogle must not silently swap an already-bound sub for a different
// one on the same account.
func TestLinkGoogle_AlreadyBoundDifferentSub(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	first, _, err := svc.FindOrCreateByGoogle(context.Background(), "gsub-x", "x@example.com", "X", "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.LinkGoogle(context.Background(), first.ID(), "gsub-y", "x@example.com"); !errors.Is(err, user.ErrGoogleEmailConflict) {
		t.Fatalf("expected ErrGoogleEmailConflict, got %v", err)
	}
}

// Linking the same identity twice is an idempotent no-op.
func TestLinkGoogle_Idempotent(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, _ := svc.Register(context.Background(), "i@example.com", "pw", "I", "")
	if err := svc.LinkGoogle(context.Background(), u.ID(), "gsub-i", "i@example.com"); err != nil {
		t.Fatalf("first link: %v", err)
	}
	if err := svc.LinkGoogle(context.Background(), u.ID(), "gsub-i", "i@example.com"); err != nil {
		t.Fatalf("second link: %v", err)
	}
}

// Unlink clears the bound Google sub so the account is no longer
// Google-loginable.
func TestUnlinkGoogle(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, _ := svc.Register(context.Background(), "unlink@example.com", "pw", "U", "")
	if err := svc.LinkGoogle(context.Background(), u.ID(), "gsub-u", "unlink@example.com"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := svc.UnlinkGoogle(context.Background(), u.ID()); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	got, _ := svc.GetByID(context.Background(), u.ID())
	if got.GoogleSub != "" {
		t.Errorf("expected google_sub cleared, got %q", got.GoogleSub)
	}
}

// Unlink must be refused for a passwordless (Google-only) account —
// otherwise it would lose its only login method.
func TestUnlinkGoogle_RefusesPasswordless(t *testing.T) {
	repo := newMockRepo()
	svc := user.NewService(repo)
	u, _, _ := svc.FindOrCreateByGoogle(context.Background(), "gsub-pl", "pl@example.com", "P", "", "")
	if err := svc.UnlinkGoogle(context.Background(), u.ID()); !errors.Is(err, user.ErrCannotUnlink) {
		t.Fatalf("expected ErrCannotUnlink, got %v", err)
	}
}
