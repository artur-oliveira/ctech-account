package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/artur-oliveira/ctech-account/internal/domain/user"
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

func TestLogin_Success(t *testing.T) {
	svc := user.NewService(newMockRepo())
	_, _ = svc.Register(context.Background(), "login@example.com", "securepass", "Bob", "")
	u, err := svc.Login(context.Background(), "login@example.com", "securepass")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if u.Email != "login@example.com" {
		t.Errorf("wrong user returned")
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
	u, _ := svc2.Register(context.Background(), "chpw@example.com", "oldpass123", "E", "")
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

func TestRegisterWithHash_Idempotent(t *testing.T) {
	svc := user.NewService(newMockRepo())
	u1, _ := svc.RegisterWithHash(context.Background(), "migrate@example.com", "$argon2id$fake", "Mig", "")
	u2, err := svc.RegisterWithHash(context.Background(), "migrate@example.com", "$argon2id$fake", "Mig", "")
	if err != nil {
		t.Fatalf("second call should be idempotent, got %v", err)
	}
	if u1.ID() != u2.ID() {
		t.Errorf("idempotent call returned different user")
	}
}
