package passkey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/cache"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const sessionTTL = 5 * time.Minute
const sessionKeyPrefix = "webauthn_session:"

var ErrSessionExpired = errors.New("webauthn session expired or not found")
var ErrInvalidResponse = errors.New("invalid webauthn response")
var ErrNoCredentials = errors.New("no passkey credentials found for user")
var ErrCacheRequired = errors.New("passkey sessions require an active cache backend")

type Service struct {
	wa    *webauthn.WebAuthn
	repo  Repository
	cache *cache.Client
}

func NewService(wa *webauthn.WebAuthn, repo Repository, valkeyCache *cache.Client) *Service {
	return &Service{wa: wa, repo: repo, cache: valkeyCache}
}

// ── Registration ─────────────────────────────────────────────────────────────

// BeginRegistration generates a WebAuthn credential creation challenge.
// Returns the options JSON to send to the browser and an opaque session token to include in the completion request.
func (s *Service) BeginRegistration(ctx context.Context, user *WebAuthnUser) (optionsJSON []byte, sessionToken string, err error) {
	if !s.cache.Enabled() {
		return nil, "", ErrCacheRequired
	}
	// Exclude credentials the user already has to avoid duplicates.
	excludeList := make([]protocol.CredentialDescriptor, len(user.Credentials))
	for i, c := range user.Credentials {
		excludeList[i] = protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: c.ID,
		}
	}

	options, session, err := s.wa.BeginRegistration(user,
		webauthn.WithExclusions(excludeList),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			UserVerification:   protocol.VerificationRequired,
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
		}),
	)
	if err != nil {
		return nil, "", fmt.Errorf("beginning registration: %w", err)
	}

	rawToken, hashHex, err := crypto.GenerateCode()
	if err != nil {
		return nil, "", fmt.Errorf("generating session token: %w", err)
	}

	if err := s.cache.Set(ctx, sessionKeyPrefix+hashHex, session, sessionTTL); err != nil {
		return nil, "", fmt.Errorf("storing session: %w", err)
	}

	optionsJSON, err = json.Marshal(options)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling options: %w", err)
	}

	return optionsJSON, rawToken, nil
}

// FinishRegistration validates the browser's registration response and persists the new credential.
func (s *Service) FinishRegistration(ctx context.Context, userID, name string, sessionToken string, responseBody []byte, user *WebAuthnUser) (*Credential, error) {
	session, err := s.consumeSession(ctx, sessionToken)
	if err != nil {
		return nil, err
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(responseBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}

	waCred, err := s.wa.CreateCredential(user, *session, parsed)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	credJSON, err := json.Marshal(waCred)
	if err != nil {
		return nil, fmt.Errorf("marshaling credential: %w", err)
	}

	transports := make([]string, len(waCred.Transport))
	for i, t := range waCred.Transport {
		transports[i] = string(t)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	c := &Credential{
		PK:             BuildPK(userID),
		SK:             BuildSK(waCred.ID),
		Name:           name,
		CredentialJSON: string(credJSON),
		Transports:     transports,
		AAGUID:         fmt.Sprintf("%x", waCred.Authenticator.AAGUID),
		CreatedAt:      now,
	}

	if err := s.repo.Create(ctx, c); err != nil {
		return nil, fmt.Errorf("persisting credential: %w", err)
	}

	return c, nil
}

// ── Authentication ────────────────────────────────────────────────────────────

// BeginAuthentication generates a WebAuthn discoverable-credential assertion challenge.
// No username is required — the browser presents passkeys registered for this RPID.
func (s *Service) BeginAuthentication(ctx context.Context) (optionsJSON []byte, sessionToken string, err error) {
	if !s.cache.Enabled() {
		return nil, "", ErrCacheRequired
	}
	options, session, err := s.wa.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, "", fmt.Errorf("beginning authentication: %w", err)
	}

	rawToken, hashHex, err := crypto.GenerateCode()
	if err != nil {
		return nil, "", fmt.Errorf("generating session token: %w", err)
	}

	if err := s.cache.Set(ctx, sessionKeyPrefix+hashHex, session, sessionTTL); err != nil {
		return nil, "", fmt.Errorf("storing session: %w", err)
	}

	optionsJSON, err = json.Marshal(options)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling options: %w", err)
	}

	return optionsJSON, rawToken, nil
}

// FinishAuthentication validates the browser's assertion and returns the authenticated userID and matched credential.
func (s *Service) FinishAuthentication(ctx context.Context, sessionToken string, responseBody []byte) (userID string, waCred *webauthn.Credential, err error) {
	session, err := s.consumeSession(ctx, sessionToken)
	if err != nil {
		return "", nil, err
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(responseBody)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}

	var resolvedUserID string

	// DiscoverableUserHandler: the browser provides the userHandle (= userID bytes) and rawID (credentialID).
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		uid := string(userHandle)
		creds, err := s.loadWACredentials(ctx, uid)
		if err != nil {
			return nil, fmt.Errorf("loading credentials for user %s: %w", uid, err)
		}
		resolvedUserID = uid
		return &WebAuthnUser{ID: userHandle, Credentials: creds}, nil
	}

	_, waCred, err = s.wa.ValidatePasskeyLogin(handler, *session, parsed)
	if err != nil {
		return "", nil, fmt.Errorf("validating passkey: %w", err)
	}

	// Persist updated sign count + last used.
	now := time.Now().UTC().Format(time.RFC3339)
	_ = s.repo.UpdateLastUsed(ctx, resolvedUserID, BuildSK(waCred.ID), now)

	return resolvedUserID, waCred, nil
}

// HasPasskeys reports whether the user has any registered passkey credentials.
func (s *Service) HasPasskeys(ctx context.Context, userID string) (bool, error) {
	creds, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return false, err
	}
	return len(creds) > 0, nil
}

// BeginUserAuthentication generates a WebAuthn assertion challenge for a known user.
// The browser will be prompted for a specific passkey from the user's allowCredentials list.
func (s *Service) BeginUserAuthentication(ctx context.Context, userID string) (optionsJSON []byte, sessionToken string, err error) {
	if !s.cache.Enabled() {
		return nil, "", ErrCacheRequired
	}
	waCreds, err := s.loadWACredentials(ctx, userID)
	if err != nil {
		return nil, "", fmt.Errorf("loading credentials: %w", err)
	}
	if len(waCreds) == 0 {
		return nil, "", ErrNoCredentials
	}

	waUser := &WebAuthnUser{ID: []byte(userID), Credentials: waCreds}

	options, session, err := s.wa.BeginLogin(waUser,
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, "", fmt.Errorf("beginning user authentication: %w", err)
	}

	rawToken, hashHex, err := crypto.GenerateCode()
	if err != nil {
		return nil, "", fmt.Errorf("generating session token: %w", err)
	}

	if err := s.cache.Set(ctx, sessionKeyPrefix+hashHex, session, sessionTTL); err != nil {
		return nil, "", fmt.Errorf("storing session: %w", err)
	}

	optionsJSON, err = json.Marshal(options)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling options: %w", err)
	}

	return optionsJSON, rawToken, nil
}

// FinishUserAuthentication validates a user-specific passkey assertion (non-discoverable).
func (s *Service) FinishUserAuthentication(ctx context.Context, userID, sessionToken string, responseBody []byte) error {
	session, err := s.consumeSession(ctx, sessionToken)
	if err != nil {
		return err
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(responseBody)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}

	waCreds, err := s.loadWACredentials(ctx, userID)
	if err != nil {
		return fmt.Errorf("loading credentials: %w", err)
	}

	waUser := &WebAuthnUser{ID: []byte(userID), Credentials: waCreds}

	waCred, err := s.wa.ValidateLogin(waUser, *session, parsed)
	if err != nil {
		return fmt.Errorf("validating passkey: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_ = s.repo.UpdateLastUsed(ctx, userID, BuildSK(waCred.ID), now)

	return nil
}

// ── Management ────────────────────────────────────────────────────────────────

func (s *Service) List(ctx context.Context, userID string) ([]*Credential, error) {
	return s.repo.ListByUserID(ctx, userID)
}

func (s *Service) Delete(ctx context.Context, userID, credentialSK string) error {
	return s.repo.Delete(ctx, userID, credentialSK)
}

// LoadUser builds a WebAuthnUser with the user's existing passkey credentials for registration exclusion.
func (s *Service) LoadUser(ctx context.Context, userID, name, displayName string) (*WebAuthnUser, error) {
	waCreds, err := s.loadWACredentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &WebAuthnUser{
		ID:          []byte(userID),
		Name:        name,
		DisplayName: displayName,
		Credentials: waCreds,
	}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *Service) consumeSession(ctx context.Context, rawToken string) (*webauthn.SessionData, error) {
	hashHex := crypto.HashToken(rawToken)
	var session webauthn.SessionData
	if err := s.cache.Get(ctx, sessionKeyPrefix+hashHex, &session); err != nil {
		return nil, ErrSessionExpired
	}
	_ = s.cache.Delete(ctx, sessionKeyPrefix+hashHex)
	return &session, nil
}

func (s *Service) loadWACredentials(ctx context.Context, userID string) ([]webauthn.Credential, error) {
	stored, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	result := make([]webauthn.Credential, 0, len(stored))
	for _, c := range stored {
		var waCred webauthn.Credential
		if err := json.Unmarshal([]byte(c.CredentialJSON), &waCred); err != nil {
			continue // skip corrupted entries
		}
		result = append(result, waCred)
	}
	return result, nil
}
