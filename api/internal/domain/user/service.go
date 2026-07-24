package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/legal"
)

// ErrInvalidCredentials is returned when login credentials are incorrect or the account is disabled.
var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrAccountDisabled = errors.New("account is disabled")
var ErrCurrentPasswordIncorrect = errors.New("current password is incorrect")

// ErrEmailNotVerified is returned when the password is correct but the account's
// email address has not been confirmed yet. Only ever surfaced after a successful
// password check, so it cannot be used to enumerate registered emails.
var ErrEmailNotVerified = errors.New("email address is not verified")

// ErrPasswordAlreadySet guards SetInitialPassword against overwriting a password.
var ErrPasswordAlreadySet = errors.New("account already has a password")

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Register creates a password account. It always stamps the current ToS/Privacy
// versions as accepted — the caller (handler) must have already validated an
// explicit accept_terms=true on the request before calling this.
func (s *Service) Register(ctx context.Context, email, password, firstName, lastName string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	// SEC-021: burn Argon2 (the expensive work) on every path before we can
	// learn whether the email exists, so a registered address is not materially
	// faster to reject than a fresh one. The conflict branch below also burns a
	// dummy hash so both branches cost comparably — no timing oracle.
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	existing, err := s.repo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("checking existing user: %w", err)
	}
	if existing != nil {
		// Email taken: hash a random string so this branch costs as much as the
		// success branch. Closing the enumeration timing oracle.
		if dummy, _, derr := crypto.GenerateOpaqueToken(); derr == nil {
			_, _ = crypto.HashPassword(dummy)
		}
		return nil, ErrEmailConflict
	}

	now := time.Now().UTC().Format(time.RFC3339)
	u := &User{
		Email:             email,
		PasswordHash:      hash,
		FirstName:         strings.TrimSpace(firstName),
		LastName:          strings.TrimSpace(lastName),
		EmailVerified:     false,
		IsEnabled:         true,
		TOSVersion:        legal.CurrentToSVersion,
		TOSAcceptedAt:     now,
		PrivacyVersion:    legal.CurrentPrivacyVersion,
		PrivacyAcceptedAt: now,
	}
	u.DisplayName = u.FirstName + " " + u.LastName

	if err := s.repo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (*User, error) {
	u, err := s.repo.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Burn the same Argon2 work as a real verification so response time
			// doesn't reveal whether the email exists.
			crypto.VerifyDummyPassword(password)
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("fetching user: %w", err)
	}

	// Passwordless account (created via Google): no password login is possible.
	if u.PasswordHash == "" {
		crypto.VerifyDummyPassword(password)
		return nil, ErrInvalidCredentials
	}

	ok, err := crypto.VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}

	// Checks below run only after the password is proven correct, so they cannot
	// be used as an oracle for which emails are registered.
	if !u.IsEnabled {
		return nil, ErrAccountDisabled
	}
	if !u.EmailVerified {
		return nil, ErrEmailNotVerified
	}

	return u, nil
}

// SetInitialPassword sets a password on an account that has none (created via
// Google). It refuses to overwrite an existing password — that path is ChangePassword.
func (s *Service) SetInitialPassword(ctx context.Context, userID, newPassword string) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("fetching user: %w", err)
	}
	if u.PasswordHash != "" {
		return ErrPasswordAlreadySet
	}

	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hashing new password: %w", err)
	}
	return s.repo.Update(ctx, userID, map[string]any{"password_hash": hash})
}

// HasPassword reports whether the account can log in with a password.
func (s *Service) HasPassword(ctx context.Context, userID string) (bool, error) {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("fetching user: %w", err)
	}
	return u.PasswordHash != "", nil
}

func (s *Service) GetByID(ctx context.Context, userID string) (*User, error) {
	return s.repo.GetByID(ctx, userID)
}

func (s *Service) GetByEmail(ctx context.Context, email string) (*User, error) {
	return s.repo.GetByEmail(ctx, email)
}

func (s *Service) UpdateProfile(ctx context.Context, userID, firstName, lastName, displayName string) error {
	return s.repo.Update(ctx, userID, map[string]any{
		"first_name":   strings.TrimSpace(firstName),
		"last_name":    strings.TrimSpace(lastName),
		"display_name": strings.TrimSpace(displayName),
	})
}

func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("fetching user: %w", err)
	}

	// Passwordless (Google) account: there is no current password to verify.
	// The caller must use SetInitialPassword instead of guessing one here.
	if u.PasswordHash == "" {
		return ErrCurrentPasswordIncorrect
	}

	ok, err := crypto.VerifyPassword(currentPassword, u.PasswordHash)
	if err != nil {
		return fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return ErrCurrentPasswordIncorrect
	}

	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hashing new password: %w", err)
	}
	return s.repo.Update(ctx, userID, map[string]any{"password_hash": hash})
}

func (s *Service) MarkEmailVerified(ctx context.Context, userID string) error {
	return s.repo.Update(ctx, userID, map[string]any{"email_verified": true})
}

func (s *Service) ForceSetPassword(ctx context.Context, userID, passwordHash string) error {
	return s.repo.Update(ctx, userID, map[string]any{"password_hash": passwordHash})
}

// ErrGoogleEmailConflict is returned when a Google identity resolves to an email
// that already belongs to a password account. We must not merge a Google login
// into a password account: anyone able to present a Google-verified token for
// that email (an attacker who pre-registered it, or a same-org Workspace
// identity) would otherwise be logged straight into it — account takeover.
var ErrGoogleEmailConflict = errors.New("email already registered with a password account")

// FindOrCreateByGoogle resolves a Google identity to a user, keyed on the
// Google OIDC sub (never on the email alone). The returned bool is true when a
// new account was created — the caller must gate a brand-new account behind
// explicit ToS/Privacy acceptance (AcceptTerms) before issuing a session, since
// Google sign-up never shows the register form's checkbox.
func (s *Service) FindOrCreateByGoogle(ctx context.Context, googleSub, email, firstName, lastName, avatarURL string) (*User, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, false, fmt.Errorf("looking up user: %w", err)
	}
	if u != nil {
		// (1) Same Google identity already bound here: it is the owner's
		// linked account (sub set at Google sign-up, or via authenticated
		// linking). Safe to log in even though it also has a password.
		if u.GoogleSub == googleSub {
			if avatarURL != "" && u.AvatarURL != avatarURL {
				_ = s.repo.Update(ctx, u.ID(), map[string]any{"avatar_url": avatarURL})
				u.AvatarURL = avatarURL
			}
			return u, false, nil
		}
		// (2) A DIFFERENT sub already bound here: reject (defense in depth).
		if u.GoogleSub != "" {
			return nil, false, ErrGoogleEmailConflict
		}
		// (3) Pristine account, no sub yet. A password account must not
		// auto-merge from an anonymous Google login — that is the takeover
		// path. Linking requires the user to be authenticated first (LinkGoogle).
		if u.PasswordHash != "" {
			return nil, false, ErrGoogleEmailConflict
		}
		// (4) Passwordless (legacy Google) account: bind the sub now so
		// future logins are keyed on it.
		if u.GoogleSub == "" {
			if err := s.repo.Update(ctx, u.ID(), map[string]any{"google_sub": googleSub}); err != nil {
				return nil, false, fmt.Errorf("binding google sub: %w", err)
			}
			u.GoogleSub = googleSub
		}
		// Existing user — update avatar if changed.
		if avatarURL != "" && u.AvatarURL != avatarURL {
			_ = s.repo.Update(ctx, u.ID(), map[string]any{"avatar_url": avatarURL})
			u.AvatarURL = avatarURL
		}
		return u, false, nil
	}

	// New user via Google — no password, already email-verified. ToS/Privacy are
	// deliberately left unaccepted here; AcceptTerms stamps them once the user
	// explicitly agrees on the post-signup interstitial.
	u = &User{
		Email:         email,
		GoogleSub:     googleSub,
		PasswordHash:  "",
		FirstName:     strings.TrimSpace(firstName),
		LastName:      strings.TrimSpace(lastName),
		AvatarURL:     avatarURL,
		EmailVerified: true,
		IsEnabled:     true,
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, false, fmt.Errorf("creating user: %w", err)
	}
	return u, true, nil
}

// LinkGoogle attaches a Google identity to an ALREADY-AUTHENTICATED
// account. That authentication (SSO session from a password or other
// factor) is the proof of ownership an anonymous callback cannot
// provide, so this is the only safe way to bind a Google sub to a
// password account. The Google-verified email must match the account's
// email — we never bind a Google identity for a different address.
func (s *Service) LinkGoogle(ctx context.Context, userID, googleSub, verifiedEmail string) error {
	verifiedEmail = strings.ToLower(strings.TrimSpace(verifiedEmail))
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Email != verifiedEmail {
		return ErrGoogleEmailConflict
	}
	// Already linked to this exact identity — idempotent no-op.
	if u.GoogleSub == googleSub {
		return nil
	}
	// A different Google identity is already bound here: refuse rather
	// than silently swap it.
	if u.GoogleSub != "" {
		return ErrGoogleEmailConflict
	}
	return s.repo.Update(ctx, userID, map[string]any{"google_sub": googleSub})
}

// ErrCannotUnlink is returned when unlinking Google would leave the
// account with no usable login method (passwordless accounts
// authenticate only via Google).
var ErrCannotUnlink = errors.New("cannot unlink Google without a password")

// UnlinkGoogle removes the bound Google identity. Refused for
// passwordless accounts, which would otherwise have no way to log in.
func (s *Service) UnlinkGoogle(ctx context.Context, userID string) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.PasswordHash == "" {
		return ErrCannotUnlink
	}
	return s.repo.Update(ctx, userID, map[string]any{"google_sub": ""})
}

// AcceptTerms stamps the current version of each document the user accepted.
// ToS and Privacy version independently, so a user re-accepting only the
// document that changed must not have the other one restamped.
func (s *Service) AcceptTerms(ctx context.Context, userID string, tos, privacy bool) error {
	updates := map[string]any{}
	now := time.Now().UTC().Format(time.RFC3339)

	if tos {
		updates["tos_version"] = legal.CurrentToSVersion
		updates["tos_accepted_at"] = now
	}
	if privacy {
		updates["privacy_version"] = legal.CurrentPrivacyVersion
		updates["privacy_accepted_at"] = now
	}
	if len(updates) == 0 {
		return nil
	}

	return s.repo.Update(ctx, userID, updates)
}
