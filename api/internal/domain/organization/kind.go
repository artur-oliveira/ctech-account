package organization

import "errors"

// The kinds of workspace. An organization is what every workspace was before
// kinds existed; a personal workspace is a space — a household, a shared
// budget — with no company and a three-rung ladder
// (docs/specs/2026-10-09-personal-workspaces.md).
//
// The kind is set at creation and never changes. Turning a space into an
// organization would be a new decision, not a field update.
const (
	KindOrganization = "organization"
	KindPersonal     = "personal"
)

var (
	// ErrInvalidKind is a kind that is neither of the two.
	ErrInvalidKind = errors.New("unknown workspace kind")
	// ErrTransferNeedsFullAccess is handing a space to somebody with read
	// access only. They are promoted first, deliberately: ownership is the
	// widest grant there is, and it should not skip the one below it.
	ErrTransferNeedsFullAccess = errors.New("a space can only be transferred to somebody with full access")
	// ErrNoCompaniesInASpace is an invitation to a space naming companies. A
	// space holds none, so the grant could only ever fail after the person
	// joined.
	ErrNoCompaniesInASpace = errors.New("a space holds no companies")
)

// NormalizeKind reads a stored or requested kind. Empty is an organization:
// every row written before kinds existed has no kind attribute, and reading it
// as anything else would change what an existing workspace is.
func NormalizeKind(kind string) (string, error) {
	switch kind {
	case "", KindOrganization:
		return KindOrganization, nil
	case KindPersonal:
		return KindPersonal, nil
	}
	return "", ErrInvalidKind
}

// KindOf returns the organization's kind, normalized.
func (o *Organization) KindOf() string {
	k, err := NormalizeKind(o.Kind)
	if err != nil {
		// A value that reached the table by a path nobody planned. Read as a
		// space, the kind that grants less: no admin rung, no companies.
		return KindPersonal
	}
	return k
}

// IsGrantableRoleIn reports whether member management may assign role in a
// workspace of this kind. A space has no admin: two levels — full access and
// read — and an owner, and the ladder must not quietly grow a third.
func IsGrantableRoleIn(kind, role string) bool {
	if !IsGrantableRole(role) {
		return false
	}
	return kind != KindPersonal || role != RoleAdmin
}
