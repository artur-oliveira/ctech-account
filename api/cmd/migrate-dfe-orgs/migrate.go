package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"uuid"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

// sourceSystem namespaces every row this tool writes, so an imported
// organization can be told from one created through the product, and so a
// second import from somewhere else cannot collide with these.
const sourceSystem = "dfe"

// dfeOrg is the part of a ctech-dfe organization row this migration reads.
//
// The dfe row is schema-less past these fields — it carries the whole fiscal
// person, addresses, certificates and pickup locations. None of that comes
// along: a tax entity is a Company in dfe, not an organization here
// (ctech-billing ADR 0021). PK stays only as the source ref.
type dfeOrg struct {
	PK          string
	Name        string
	OwnerUserID string
	CreatedAt   string
}

// dfeMember is one row of dfe's organization_users.
type dfeMember struct {
	UserID      string
	Role        string
	Permissions []string
	InvitedBy   string
	CreatedAt   string
}

// roleMap is the whole translation. dfe's ladder and this one already agree in
// shape; USER -> member is the only rename.
var roleMap = map[string]string{
	"OWNER":  orgDomain.RoleOwner,
	"ADMIN":  orgDomain.RoleAdmin,
	"USER":   orgDomain.RoleMember,
	"VIEWER": orgDomain.RoleViewer,
}

// mapRole reports the platform role for a dfe one. An unknown role returns
// false rather than a plausible default: guessing what "SUPERUSER" meant is
// guessing at somebody's access.
func mapRole(dfeRole string) (string, bool) {
	role, ok := roleMap[strings.ToUpper(strings.TrimSpace(dfeRole))]
	return role, ok
}

const (
	actionCreate = "create"
	actionReview = "review"
)

// decision is what this migration proposes to do with one dfe organization.
//
// Review is the blocking bucket: anything in it means a human has to look
// before this organization moves. Notes is for things worth printing that are
// not questions.
type decision struct {
	SourceRef   string
	DisplayName string
	OwnerUserID string
	CreatedAt   time.Time
	Action      string
	Review      []string
	Notes       []string
	// Members excludes the owner: CreateWithOwner writes that one inside the
	// transaction that creates the organization, so listing it here would make
	// the write happen twice.
	Members []orgDomain.Membership
}

// planOrg decides what happens to one organization without writing anything.
//
// Everything this migration refuses to guess is decided here, so the report a
// human reads and the writes that follow come from the same function — a
// dry run that disagrees with the real run is worse than no dry run.
func planOrg(ctx context.Context, org dfeOrg, members []dfeMember, userExists func(context.Context, string) (bool, error)) (decision, error) {
	d := decision{
		SourceRef:   org.PK,
		DisplayName: strings.TrimSpace(org.Name),
		OwnerUserID: strings.TrimSpace(org.OwnerUserID),
		Action:      actionCreate,
	}
	if d.DisplayName == "" {
		// dfe let an organization exist without a name. A workspace nobody can
		// tell apart in a switcher needs one before it moves.
		d.Review = append(d.Review, "the organization has no name")
	}

	created, err := parseDFETime(org.CreatedAt)
	if err != nil {
		d.Notes = append(d.Notes, fmt.Sprintf("created_at %q is unreadable; importing with today's date", org.CreatedAt))
		created = time.Now().UTC()
	}
	d.CreatedAt = created

	if d.OwnerUserID == "" {
		// dfe's own repair path derives this from the oldest OWNER row
		// (services/billing.go). This one does not: inferring who owns a
		// company is exactly the kind of guess a migration should not make.
		d.Review = append(d.Review, "owner_user_id is empty; dfe never recorded who owns this organization")
	} else {
		ok, err := userExists(ctx, d.OwnerUserID)
		if err != nil {
			return d, fmt.Errorf("checking owner %s: %w", d.OwnerUserID, err)
		}
		if !ok {
			d.Review = append(d.Review, fmt.Sprintf("owner %s does not exist in this account store", d.OwnerUserID))
		}
	}

	owners := 0
	for _, m := range members {
		userID := strings.TrimSpace(m.UserID)
		if userID == "" {
			d.Review = append(d.Review, "a membership row has no user_id")
			continue
		}

		role, known := mapRole(m.Role)
		if !known {
			d.Review = append(d.Review, fmt.Sprintf("member %s has role %q, which has no equivalent here", userID, m.Role))
			continue
		}

		// dfe grants extra permissions per member on top of the role. This
		// model has none, deliberately — so importing one silently deletes
		// access somebody was explicitly given, and the person finds out when a
		// screen is gone. It is a question, not a rounding error.
		if len(m.Permissions) > 0 {
			d.Review = append(d.Review, fmt.Sprintf(
				"member %s carries extra dfe permissions [%s] that this model cannot express",
				userID, strings.Join(m.Permissions, ", ")))
			continue
		}

		exists, err := userExists(ctx, userID)
		if err != nil {
			return d, fmt.Errorf("checking member %s: %w", userID, err)
		}
		if !exists {
			// Never written. A membership pointing at nobody is an access grant
			// that cannot be audited.
			d.Review = append(d.Review, fmt.Sprintf("member %s does not exist in this account store; skipped", userID))
			continue
		}

		if role == orgDomain.RoleOwner {
			owners++
			if d.OwnerUserID != "" && userID != d.OwnerUserID {
				d.Review = append(d.Review, fmt.Sprintf(
					"the OWNER row is %s but owner_user_id is %s; dfe disagrees with itself", userID, d.OwnerUserID))
			}
			// The owner membership is written by CreateWithOwner, in the
			// transaction that creates the organization.
			continue
		}

		memberCreated, err := parseDFETime(m.CreatedAt)
		if err != nil {
			memberCreated = created
		}
		d.Members = append(d.Members, orgDomain.Membership{
			UserID:    userID,
			Role:      role,
			InvitedBy: strings.TrimSpace(m.InvitedBy),
			CreatedAt: memberCreated,
		})
	}

	if owners > 1 {
		d.Review = append(d.Review, fmt.Sprintf("%d OWNER rows; exactly one is the invariant here", owners))
	}
	if owners == 0 && d.OwnerUserID != "" {
		d.Notes = append(d.Notes, "no OWNER membership row; the owner comes from owner_user_id")
	}

	// Stable order so two dry runs of the same data read the same.
	sort.Slice(d.Members, func(i, j int) bool { return d.Members[i].UserID < d.Members[j].UserID })

	if len(d.Review) > 0 {
		d.Action = actionReview
	}
	return d, nil
}

func parseDFETime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", value)
}

type applyResult struct {
	OrganizationID      string
	CreatedOrganization bool
	CreatedMemberships  int
	ExistingMemberships int
}

// apply writes one planned organization. It is safe to run again: the source
// ref finds an organization already imported, and each membership is written
// only if absent.
//
// It also finishes a partial import rather than skipping it. A first run that
// died between the organization and its third member would otherwise leave two
// people locked out of a workspace that reports itself as migrated.
func apply(ctx context.Context, repo orgDomain.Repository, d decision) (applyResult, error) {
	var res applyResult

	existing, err := repo.GetBySourceRef(ctx, sourceSystem, d.SourceRef)
	switch {
	case err == nil:
		res.OrganizationID = existing.ID
	case isNotFound(err):
		org := &orgDomain.Organization{
			ID:           uuid.NewV7().String(),
			DisplayName:  d.DisplayName,
			OwnerUserID:  d.OwnerUserID,
			SourceSystem: sourceSystem,
			SourceRef:    d.SourceRef,
			CreatedAt:    d.CreatedAt,
			UpdatedAt:    d.CreatedAt,
		}
		if err := repo.CreateWithOwner(ctx, org); err != nil {
			return res, fmt.Errorf("creating organization for %s: %w", d.SourceRef, err)
		}
		res.OrganizationID = org.ID
		res.CreatedOrganization = true
	default:
		return res, fmt.Errorf("looking up %s: %w", d.SourceRef, err)
	}

	for _, m := range d.Members {
		m.OrganizationID = res.OrganizationID
		err := repo.PutMembership(ctx, &m)
		switch {
		case err == nil:
			res.CreatedMemberships++
		case isAlreadyMember(err):
			// Already there from an earlier run. Its role is left alone: an
			// import must not overwrite a role somebody changed since.
			res.ExistingMemberships++
		default:
			return res, fmt.Errorf("adding %s to %s: %w", m.UserID, res.OrganizationID, err)
		}
	}
	return res, nil
}
