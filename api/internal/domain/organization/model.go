// Package organization owns the platform's tenant identity: who shares a
// workspace, with what role, and who is invited to one.
//
// It deliberately holds nothing a product does with a tenant — no invoice
// numbering, no fiscal entity, no per-product quota. Those live in the product
// that means them (ctech-billing ADR 0021).
package organization

import "time"

// The role ladder. Four, fixed, and not a permission matrix: ctech-dfe already
// has an action.resource model, and replicating it here before anybody has
// asked for a custom role is how this record becomes a second RBAC lineage. A
// ladder can be widened later; a permission engine cannot be narrowed.
const (
	RoleViewer = "viewer"
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

// rank orders the ladder. Unknown roles rank 0, below every real one, so a
// value that reached the database by some path nobody planned fails closed.
var rank = map[string]int{RoleViewer: 1, RoleMember: 2, RoleAdmin: 3, RoleOwner: 4}

func RoleRank(role string) int { return rank[role] }

// IsGrantableRole reports whether member management may assign this role.
//
// Owner is absent, and its absence is the invariant: an organization has
// exactly one owner and it is whoever created it. Ownership moves through
// transfer, which demotes and promotes in one transaction — it is never a
// second row somebody adds.
func IsGrantableRole(role string) bool {
	return role == RoleAdmin || role == RoleMember || role == RoleViewer
}

// AtLeast reports whether role clears floor.
func AtLeast(role, floor string) bool {
	return RoleRank(role) >= RoleRank(floor) && RoleRank(role) > 0
}

// Organization is a workspace and a billing target: who shares access and who
// receives the invoice.
type Organization struct {
	ID          string    `dynamodbav:"-"`
	DisplayName string    `dynamodbav:"display_name"`
	OwnerUserID string    `dynamodbav:"owner_user_id"`
	CreatedAt   time.Time `dynamodbav:"created_at"`
	UpdatedAt   time.Time `dynamodbav:"updated_at"`
}

// Membership is one person's access to one organization.
type Membership struct {
	OrganizationID string    `dynamodbav:"-"`
	UserID         string    `dynamodbav:"-"`
	Role           string    `dynamodbav:"role"`
	InvitedBy      string    `dynamodbav:"invited_by,omitempty"`
	CreatedAt      time.Time `dynamodbav:"created_at"`
}

// Invitation is an offer of membership to one e-mail address.
//
// TokenHash, never the token: a row read out of the table must not be usable to
// accept the invitation it describes.
type Invitation struct {
	OrganizationID string    `dynamodbav:"-"`
	Email          string    `dynamodbav:"-"`
	Role           string    `dynamodbav:"role"`
	TokenHash      string    `dynamodbav:"token_hash"`
	InvitedBy      string    `dynamodbav:"invited_by"`
	CreatedAt      time.Time `dynamodbav:"created_at"`
	ExpiresAt      time.Time `dynamodbav:"-"`
}
