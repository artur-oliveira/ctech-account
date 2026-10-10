package organization

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Plan limits on personal spaces (docs/specs/2026-10-10-space-plan-limits.md).
// The numbers come from ctech-billing on every check; this file holds what the
// domain needs to apply them, and the counting rules of spec § 2.

// Unlimited is billing's -1: no ceiling. The level is still reported.
const Unlimited int64 = -1

const (
	ResourceSpaces = "spaces"
	ResourcePeople = "people"
)

// Quotas is one person's Finanças plan as this domain needs it.
type Quotas struct {
	Plan           string
	Spaces         int64
	PeoplePerSpace int64
}

// allows reports whether one more fits under limit.
func allows(limit, used int64) bool { return limit == Unlimited || used < limit }

// PlanLimits is what the organization service needs from billing. Implemented
// by planlimit.Service; an interface so this package never imports billing.
type PlanLimits interface {
	// Quotas reads the owner's plan live. Any failure wraps ErrPlanUnavailable.
	Quotas(ctx context.Context, ownerUserID string) (Quotas, error)
	// LevelsChanged is called after a committed change to the owner's levels.
	// It never fails the caller: the write already happened.
	LevelsChanged(ctx context.Context, ownerUserID string)
	// ScheduleLevels asks for a report at a later instant (an invitation's expiry).
	ScheduleLevels(ctx context.Context, ownerUserID string, at time.Time)
}

var (
	// ErrPlanUnavailable is billing unreachable, slow or erroring. Nothing was written.
	ErrPlanUnavailable = errors.New("the plan could not be read")
	// ErrPlanBusy is a guard that kept moving under concurrent writes. Nothing was written.
	ErrPlanBusy = errors.New("the plan counter kept changing")
	// ErrNotASpace is a plan question about an organization.
	ErrNotASpace = errors.New("not a personal space")
)

// PlanLimitError is a write refused because the plan does not allow it.
type PlanLimitError struct {
	Resource string
	Limit    int64
	Used     int64
	Plan     string
	// Hidden: the plan belongs to somebody other than the caller (a transfer's
	// new owner), so the response carries none of Limit, Used and Plan.
	Hidden bool
}

func (e *PlanLimitError) Error() string {
	return fmt.Sprintf("plan limit reached: %s %d of %d", e.Resource, e.Used, e.Limit)
}

// Levels is what is reported to billing for one owner.
type Levels struct {
	Spaces int64
	People int64
}

// SpaceUsage is the people page's counter: people + pending of limit.
type SpaceUsage struct {
	People             int64
	PendingInvitations int64
	Limit              int64
	Plan               string
}

// EmailOwner resolves the account an address belongs to; "" when none.
type EmailOwner func(ctx context.Context, email string) (userID string, err error)

// WithEmailOwner wires the lookup finance_people needs to recognise an invited
// address that already belongs to a member. Without it every pending address
// counts as a person.
func (s *Service) WithEmailOwner(f EmailOwner) *Service {
	s.emailOwner = f
	return s
}

// InvitationLive is the same rule Accept applies: an invitation is spendable up
// to and including its expiry instant. The TTL reaps the row later; the count
// must not wait for it.
func InvitationLive(inv *Invitation, now time.Time) bool {
	return inv.ExpiresAt.IsZero() || !now.After(inv.ExpiresAt)
}

// ownedSpaces lists the personal workspaces this person owns. It reads the
// membership index, which is eventually consistent — see spaceGuard.
func (s *Service) ownedSpaces(ctx context.Context, ownerUserID string) ([]*Organization, error) {
	memberships, err := s.repo.ListForUser(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	out := make([]*Organization, 0, len(memberships))
	for _, m := range memberships {
		if m.Role != RoleOwner {
			continue
		}
		org, err := s.repo.Get(ctx, m.OrganizationID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if org.KindOf() == KindPersonal && org.OwnerUserID == ownerUserID {
			out = append(out, org)
		}
	}
	return out, nil
}

// spacePeople is who counts in one space: member ids (owner excluded) and the
// normalised addresses of live invitations.
type spacePeople struct {
	members []string
	invited []string
}

func (p spacePeople) count() int64 { return int64(len(p.members) + len(p.invited)) }

func (s *Service) peopleIn(ctx context.Context, org *Organization) (spacePeople, error) {
	members, err := s.repo.ListMembers(ctx, org.ID)
	if err != nil {
		return spacePeople{}, err
	}
	invitations, err := s.repo.ListInvitations(ctx, org.ID)
	if err != nil {
		return spacePeople{}, err
	}
	now := s.now().UTC()
	var p spacePeople
	for _, m := range members {
		if m.UserID == org.OwnerUserID || m.Role == RoleOwner {
			continue
		}
		p.members = append(p.members, m.UserID)
	}
	for _, inv := range invitations {
		if InvitationLive(inv, now) {
			p.invited = append(p.invited, NormalizeEmail(inv.Email))
		}
	}
	return p, nil
}

// Levels counts what billing is told: owned personal spaces, and distinct
// people across them (spec § 2).
func (s *Service) Levels(ctx context.Context, ownerUserID string) (Levels, error) {
	spaces, err := s.ownedSpaces(ctx, ownerUserID)
	if err != nil {
		return Levels{}, err
	}
	memberIDs := map[string]bool{}
	emails := map[string]bool{}
	for _, space := range spaces {
		p, err := s.peopleIn(ctx, space)
		if err != nil {
			return Levels{}, err
		}
		for _, id := range p.members {
			memberIDs[id] = true
		}
		for _, e := range p.invited {
			emails[e] = true
		}
	}
	people := int64(len(memberIDs))
	for email := range emails {
		if s.emailOwner != nil {
			id, err := s.emailOwner(ctx, email)
			if err != nil {
				return Levels{}, err
			}
			if id != "" && memberIDs[id] {
				continue
			}
		}
		people++
	}
	return Levels{Spaces: int64(len(spaces)), People: people}, nil
}

// SpaceCounts is the internal route's answer for one owned space (spec § 6).
func (s *Service) SpaceCounts(ctx context.Context, orgID string) (people, pending int64, err error) {
	org, err := s.repo.Get(ctx, orgID)
	if err != nil {
		return 0, 0, err
	}
	p, err := s.peopleIn(ctx, org)
	if err != nil {
		return 0, 0, err
	}
	return int64(len(p.members)), int64(len(p.invited)), nil
}

// CounterRepository is defined in counters.go (Task 2).
type CounterRepository interface{}
