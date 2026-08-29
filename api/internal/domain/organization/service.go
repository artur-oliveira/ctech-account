package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotAMember separates "this organization does not exist" from "it does,
	// and you are not in it". The handler collapses both to 404 — telling a
	// stranger an organization exists is already an answer — but the service
	// keeps them apart so an audit line can say which happened.
	ErrNotAMember = errors.New("not a member of this organization")
	// ErrForbidden is a member whose role does not clear the floor.
	ErrForbidden = errors.New("insufficient role")
	// ErrInvalidName is a display name that is empty once trimmed.
	ErrInvalidName = errors.New("organization name is required")
	// ErrNotGrantable is an attempt to hand out owner through member routes.
	ErrNotGrantable = errors.New("role is not grantable")
)

// maxDisplayName is a storage bound, not a product rule. Long enough for any
// real company name, short enough that a name cannot be used as free storage.
const maxDisplayName = 120

// Service holds the rules a conditional write cannot express on its own —
// mostly "who is allowed to ask for this", which needs the actor's membership
// read before the write is attempted.
type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

// Create makes the caller the owner of a new organization.
//
// There is no invitation, no approval and no uniqueness check on the name: this
// is a workspace, not a claim on a company. Claiming a legal entity is a
// separate act with evidence behind it (phase 3), and conflating the two would
// mean the first person to type a name owns it.
func (s *Service) Create(ctx context.Context, ownerUserID, displayName string) (*Organization, error) {
	name := strings.TrimSpace(displayName)
	if name == "" || len(name) > maxDisplayName {
		return nil, ErrInvalidName
	}
	if strings.TrimSpace(ownerUserID) == "" {
		return nil, ErrNotAMember
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("minting organization id: %w", err)
	}
	now := s.now().UTC()
	org := &Organization{
		ID:          id.String(),
		DisplayName: name,
		OwnerUserID: ownerUserID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateWithOwner(ctx, org); err != nil {
		return nil, err
	}
	return org, nil
}

// Get returns the organization only to somebody who is in it.
func (s *Service) Get(ctx context.Context, orgID, actorUserID string) (*Organization, error) {
	if _, err := s.RoleOf(ctx, orgID, actorUserID); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, orgID)
}

// Rename requires admin. A workspace name is what everybody navigates by, so it
// is not a thing any member may change under the others.
func (s *Service) Rename(ctx context.Context, orgID, actorUserID, displayName string) error {
	name := strings.TrimSpace(displayName)
	if name == "" || len(name) > maxDisplayName {
		return ErrInvalidName
	}
	if err := s.require(ctx, orgID, actorUserID, RoleAdmin); err != nil {
		return err
	}
	return s.repo.UpdateDisplayName(ctx, orgID, name, s.now().UTC())
}

// ListForUser answers the question the console asks on every sign-in: which
// workspaces may this person act in. Empty is a legitimate answer, not an error
// — a brand new account is in none.
func (s *Service) ListForUser(ctx context.Context, userID string) ([]*Membership, error) {
	return s.repo.ListForUser(ctx, userID)
}

// ListMembers requires membership: the roster is not public to people outside.
func (s *Service) ListMembers(ctx context.Context, orgID, actorUserID string) ([]*Membership, error) {
	if err := s.require(ctx, orgID, actorUserID, RoleViewer); err != nil {
		return nil, err
	}
	return s.repo.ListMembers(ctx, orgID)
}

// RoleOf reads the role from the membership row, every time it is asked.
//
// Never from a token: a role in a JWT survives its own revocation for the
// token's lifetime, so a demotion would not take effect until the session ends.
func (s *Service) RoleOf(ctx context.Context, orgID, userID string) (string, error) {
	m, err := s.repo.GetMembership(ctx, orgID, userID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrNotAMember
	}
	if err != nil {
		return "", err
	}
	return m.Role, nil
}

// require is the one place a floor is enforced, so "who may do this" is a
// single line at the top of each use case rather than a comparison each of them
// spells out slightly differently.
func (s *Service) require(ctx context.Context, orgID, actorUserID, floor string) error {
	role, err := s.RoleOf(ctx, orgID, actorUserID)
	if err != nil {
		return err
	}
	if !AtLeast(role, floor) {
		return ErrForbidden
	}
	return nil
}

// SetRole changes a member's role. Admin and above may call it; owner is not a
// role it can write, and the owner's own row is not a row it can touch.
func (s *Service) SetRole(ctx context.Context, orgID, actorUserID, targetUserID, role string) error {
	if !IsGrantableRole(role) {
		return ErrNotGrantable
	}
	if err := s.require(ctx, orgID, actorUserID, RoleAdmin); err != nil {
		return err
	}
	current, err := s.RoleOf(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	// The repository refuses this too. The check is here as well so the caller
	// gets an error that says what happened rather than a condition failure
	// that reads as "not found".
	if current == RoleOwner {
		return ErrNotGrantable
	}
	return s.repo.SetRole(ctx, orgID, targetUserID, role)
}

// Remove takes a member out of the organization.
//
// The owner is not removable — not by an admin, and not by themselves. An
// organization whose owner walked out has nobody who can invite, rename or
// transfer it, and no support path short of a database edit. Leaving is
// Transfer followed by Remove, in that order, deliberately.
func (s *Service) Remove(ctx context.Context, orgID, actorUserID, targetUserID string) error {
	// Leaving on your own account needs no admin role; removing somebody else
	// does.
	if actorUserID != targetUserID {
		if err := s.require(ctx, orgID, actorUserID, RoleAdmin); err != nil {
			return err
		}
	}
	role, err := s.RoleOf(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	if role == RoleOwner {
		return ErrForbidden
	}
	return s.repo.RemoveMembership(ctx, orgID, targetUserID)
}

// Transfer hands the organization to another member. Only the owner may do it,
// and only to somebody who already accepted a membership — handing a workspace
// to an address that never agreed to it is how an organization ends up owned by
// a stranger who cannot be reached.
func (s *Service) Transfer(ctx context.Context, orgID, actorUserID, toUserID string) error {
	role, err := s.RoleOf(ctx, orgID, actorUserID)
	if err != nil {
		return err
	}
	if role != RoleOwner {
		return ErrForbidden
	}
	if actorUserID == toUserID {
		return ErrForbidden
	}
	if _, err := s.RoleOf(ctx, orgID, toUserID); err != nil {
		return err
	}
	return s.repo.TransferOwnership(ctx, orgID, actorUserID, toUserID, s.now().UTC())
}

// invitationTTL bounds how long an offer stands. Seven days is long enough for
// somebody to read their e-mail on holiday and short enough that a link found
// in an old inbox is no longer a way in.
const invitationTTL = 7 * 24 * time.Hour

var (
	// ErrInvitationInvalid covers unknown, expired and already-consumed tokens
	// alike. Distinguishing them tells whoever is guessing which guess was
	// closer.
	ErrInvitationInvalid = errors.New("invitation is not valid")
	// ErrWrongInvitee is an invitation being accepted by a different address.
	ErrWrongInvitee = errors.New("invitation was sent to a different address")
)

// Invite offers membership to one e-mail address and returns the token once.
//
// The token is returned, never stored: the row holds its SHA-256, so a dump of
// the invitations table is a list of who was invited, not a set of keys to
// every organization in it.
func (s *Service) Invite(ctx context.Context, orgID, actorUserID, email, role string) (string, error) {
	if !IsGrantableRole(role) {
		return "", ErrNotGrantable
	}
	address := NormalizeEmail(email)
	if address == "" || !strings.Contains(address, "@") {
		return "", ErrInvalidName
	}
	if err := s.require(ctx, orgID, actorUserID, RoleAdmin); err != nil {
		return "", err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("minting invitation token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	now := s.now().UTC()
	// Re-inviting the same address overwrites the pending row rather than
	// adding a second, which also invalidates the previous token — the right
	// behaviour when somebody re-sends because the first was leaked.
	if err := s.repo.PutInvitation(ctx, &Invitation{
		OrganizationID: orgID,
		Email:          address,
		Role:           role,
		TokenHash:      HashToken(token),
		InvitedBy:      actorUserID,
		CreatedAt:      now,
		ExpiresAt:      now.Add(invitationTTL),
	}); err != nil {
		return "", err
	}
	return token, nil
}

// HashToken is exported so a future re-send path hashes the same way. SHA-256
// with no salt on purpose: the token is 32 random bytes, so there is no
// dictionary to defend against and the hash has to be computable from the token
// alone to be looked up on the index.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Accept turns an invitation into a membership.
//
// userEmail must be the address the session has already verified. The
// invitation names one address, and a token that any signed-in account could
// spend would let a leaked link be redeemed by whoever found it.
func (s *Service) Accept(ctx context.Context, token, userID, userEmail string) (*Membership, error) {
	if token == "" || strings.TrimSpace(userID) == "" {
		return nil, ErrInvitationInvalid
	}
	inv, err := s.repo.GetInvitationByToken(ctx, HashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	// The TTL reaps the row eventually. Eventually is not a check.
	if !inv.ExpiresAt.IsZero() && s.now().UTC().After(inv.ExpiresAt) {
		return nil, ErrInvitationInvalid
	}
	if NormalizeEmail(userEmail) != NormalizeEmail(inv.Email) {
		return nil, ErrWrongInvitee
	}

	m := &Membership{
		OrganizationID: inv.OrganizationID,
		UserID:         userID,
		Role:           inv.Role,
		InvitedBy:      inv.InvitedBy,
		CreatedAt:      s.now().UTC(),
	}
	if err := s.repo.AcceptInvitation(ctx, m, inv.Email); err != nil {
		return nil, err
	}
	return m, nil
}

// ListInvitations shows who is pending. Admin and above: the list is a list of
// addresses of people who have not joined yet.
func (s *Service) ListInvitations(ctx context.Context, orgID, actorUserID string) ([]*Invitation, error) {
	if err := s.require(ctx, orgID, actorUserID, RoleAdmin); err != nil {
		return nil, err
	}
	return s.repo.ListInvitations(ctx, orgID)
}

// RevokeInvitation withdraws a standing offer.
func (s *Service) RevokeInvitation(ctx context.Context, orgID, actorUserID, email string) error {
	if err := s.require(ctx, orgID, actorUserID, RoleAdmin); err != nil {
		return err
	}
	return s.repo.DeleteInvitation(ctx, orgID, email)
}
