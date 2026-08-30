package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"uuid"
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
	// ErrOwnRole is somebody changing their own role. Separate from
	// ErrForbidden because it is the one refusal here that is almost always an
	// accident rather than an attempt.
	ErrOwnRole = errors.New("you cannot change your own role")
	// ErrOutranked is acting on somebody at or above your own rank.
	ErrOutranked = errors.New("you can only manage members below your own role")
)

// maxDisplayName is a storage bound, not a product rule. Long enough for any
// real company name, short enough that a name cannot be used as free storage.
const maxDisplayName = 120

// Service holds the rules a conditional write cannot express on its own —
// mostly "who is allowed to ask for this", which needs the actor's membership
// read before the write is attempted.
// ActorGranter writes one "may act for this company" edge.
//
// A function rather than an interface on the company package, so this package
// stays ignorant that companies exist: it knows an invitation may carry ids and
// that something grants them, and nothing about the shape of what it grants.
type ActorGranter func(ctx context.Context, orgID, companyID, userID, name, grantedBy string) error

type Service struct {
	repo    Repository
	now     func() time.Time
	granter ActorGranter
}

// WithActorGranter wires the grant an accepted invitation performs. Optional:
// without it an invitation's companies are recorded and never granted, which is
// what every deployment did before ctech-billing ADR 0023.
func (s *Service) WithActorGranter(g ActorGranter) *Service {
	s.granter = g
	return s
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
func (s *Service) Create(ctx context.Context, ownerUserID, ownerName, displayName string) (*Organization, error) {
	name := strings.TrimSpace(displayName)
	if name == "" || len(name) > maxDisplayName {
		return nil, ErrInvalidName
	}
	if strings.TrimSpace(ownerUserID) == "" {
		return nil, ErrNotAMember
	}
	now := s.now().UTC()
	org := &Organization{
		ID:          uuid.NewV7().String(),
		DisplayName: name,
		OwnerUserID: ownerUserID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateWithOwner(ctx, org, strings.TrimSpace(ownerName)); err != nil {
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

// SetRole changes a member's role.
//
// Three rules, and all three exist to stop an accident rather than an attack:
//
//   - Nobody changes their own role. Demoting yourself is one wrong click in a
//     column of dropdowns, and the person who did it may no longer hold the
//     role needed to undo it.
//   - You may only act on somebody you strictly outrank. Two admins able to
//     edit each other is a disagreement that resolves as a race.
//   - You may only grant a role you strictly outrank. An admin promoting
//     somebody to admin creates a peer who can then act back on them.
func (s *Service) SetRole(ctx context.Context, orgID, actorUserID, targetUserID, role string) error {
	if !IsGrantableRole(role) {
		return ErrNotGrantable
	}
	if actorUserID == targetUserID {
		return ErrOwnRole
	}
	actorRole, err := s.RoleOf(ctx, orgID, actorUserID)
	if err != nil {
		return err
	}
	if !AtLeast(actorRole, RoleAdmin) {
		return ErrForbidden
	}
	currentRole, err := s.RoleOf(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	if !Outranks(actorRole, currentRole) {
		return ErrOutranked
	}
	if !Outranks(actorRole, role) {
		return ErrOutranked
	}
	return s.repo.SetRole(ctx, orgID, targetUserID, role)
}

// Remove takes a member out of the organization.
//
// Removing yourself is leaving, and stays open to everybody but the owner:
// the self rule above is about roles, not about the door.
//
// Removing somebody else needs the same reach SetRole needs. Without it the
// rule would be a formality — an admin refused a demotion could remove the
// person outright, which is the worse of the two.
//
// The owner is not removable, not by an admin and not by themselves. An
// organization whose owner walked out has nobody who can invite, rename or
// transfer it, and no support path short of a database edit. Leaving is
// Transfer followed by Remove, in that order, deliberately.
func (s *Service) Remove(ctx context.Context, orgID, actorUserID, targetUserID string) error {
	targetRole, err := s.RoleOf(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	if targetRole == RoleOwner {
		return ErrForbidden
	}
	if actorUserID == targetUserID {
		return s.repo.RemoveMembership(ctx, orgID, targetUserID)
	}
	actorRole, err := s.RoleOf(ctx, orgID, actorUserID)
	if err != nil {
		return err
	}
	if !AtLeast(actorRole, RoleAdmin) {
		return ErrForbidden
	}
	if !Outranks(actorRole, targetRole) {
		return ErrOutranked
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
func (s *Service) Invite(ctx context.Context, orgID, actorUserID, email, role string, companyIDs []string) (string, error) {
	if !IsGrantableRole(role) {
		return "", ErrNotGrantable
	}
	address := NormalizeEmail(email)
	if address == "" || !strings.Contains(address, "@") {
		return "", ErrInvalidName
	}
	actorRole, err := s.RoleOf(ctx, orgID, actorUserID)
	if err != nil {
		return "", err
	}
	if !AtLeast(actorRole, RoleAdmin) {
		return "", ErrForbidden
	}
	// Inviting is granting, so it obeys the same reach SetRole does. Without
	// this an admin walks around every rule above by inviting a new admin
	// instead of promoting an existing member.
	if !Outranks(actorRole, role) {
		return "", ErrOutranked
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
		CompanyIDs:     companyIDs,
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
func (s *Service) Accept(ctx context.Context, token, userID, userEmail, userName string) (*Membership, error) {
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
		// From the account record, never from the invitation — the inviter
		// typed the address, and letting them name the person too would put
		// text they wrote on somebody else's row.
		Name:      strings.TrimSpace(userName),
		Role:      inv.Role,
		InvitedBy: inv.InvitedBy,
		CreatedAt: s.now().UTC(),
	}
	if err := s.repo.AcceptInvitation(ctx, m, inv.Email); err != nil {
		return nil, err
	}

	// The companies the inviter chose, granted after the membership landed.
	//
	// After, not inside: the membership and the invitation live in two tables
	// this package owns, and the edges live in a third it deliberately knows
	// nothing about. One transaction across all three would put the company
	// key layout in here, which is the coupling ADR 0023's split exists to
	// avoid.
	//
	// The cost is a window: a grant that fails leaves somebody in the workspace
	// reaching nothing. That is recoverable by an admin in one click, and it is
	// the state an invitation with no companies produces on purpose — so the
	// screen already has to explain it. A failed membership would not be
	// recoverable, and that one IS transactional.
	if s.granter != nil {
		for _, companyID := range inv.CompanyIDs {
			if err := s.granter(ctx, inv.OrganizationID, companyID, userID, m.Name, inv.InvitedBy); err != nil {
				return m, fmt.Errorf("granting company %s to %s: %w", companyID, userID, err)
			}
		}
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

// Workspace is one organization as the person who belongs to it sees it: the
// organization, plus their own standing in it.
//
// It exists because the two halves are always wanted together — a list of ids
// without names cannot be rendered, and a list of names without roles cannot
// decide what to offer — and fetching them separately makes one sign-in into as
// many requests as the person has workspaces.
type Workspace struct {
	ID          string
	DisplayName string
	OwnerUserID string
	Role        string
	JoinedAt    time.Time
}

// RenameMember refreshes this person's name on every roster they appear on.
//
// It is called after a profile rename and must never fail that rename: the name
// on a membership is a copy, and a stale copy is a cosmetic problem, while a
// profile save that reports failure is a real one. The caller logs and moves
// on.
func (s *Service) RenameMember(ctx context.Context, userID, name string) error {
	name = strings.TrimSpace(name)
	if strings.TrimSpace(userID) == "" || name == "" {
		return nil
	}
	return s.repo.RenameMember(ctx, userID, name)
}

// ListWorkspaces answers what the switcher and the organizations screen both
// need. Empty is a legitimate answer: a new account belongs to nothing.
func (s *Service) ListWorkspaces(ctx context.Context, userID string) ([]Workspace, error) {
	memberships, err := s.repo.ListForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(memberships))
	for _, m := range memberships {
		org, err := s.repo.Get(ctx, m.OrganizationID)
		if errors.Is(err, ErrNotFound) {
			// A membership pointing at an organization that is gone. Skipped
			// rather than surfaced: a row that leads to a 403 is worse than no
			// row, and one dangling membership must not empty the whole list.
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Workspace{
			ID:          org.ID,
			DisplayName: org.DisplayName,
			OwnerUserID: org.OwnerUserID,
			Role:        m.Role,
			JoinedAt:    m.CreatedAt,
		})
	}
	// By name, so the list does not reshuffle between renders — a switcher that
	// reorders itself is a switcher people misclick.
	sort.Slice(out, func(i, j int) bool {
		if out[i].DisplayName == out[j].DisplayName {
			return out[i].ID < out[j].ID
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out, nil
}
