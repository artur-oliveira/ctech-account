package company

import (
	"strings"
	"time"
)

// maxCompanyName is a storage bound, not a product rule. A razão social is
// long; free storage is longer.
const maxCompanyName = 200

// Company is a tax id an organization acts for.
//
// It is scoped to its organization, and the same tax id may appear in two of
// them. That is the accountant holding a client's CNPJ while the client's own
// staff also issues — the ordinary shape of this market, and unrepresentable
// under a global unique key (ctech-billing ADR 0022).
type Company struct {
	OrganizationID string `dynamodbav:"-"`
	ID             string `dynamodbav:"-"`
	// TaxID is canonical: mask stripped, letters uppercased. A CNPJ is
	// alphanumeric in its first twelve positions, so this is not digits.
	TaxID     string `dynamodbav:"tax_id"`
	TaxIDKind string `dynamodbav:"tax_id_kind"`
	LegalName string `dynamodbav:"legal_name"`
	TradeName string `dynamodbav:"trade_name,omitempty"`
	// SourceSystem/SourceRef record where an imported company came from. Set
	// only by a migration, empty for anything created through the product.
	SourceSystem string    `dynamodbav:"source_system,omitempty"`
	SourceRef    string    `dynamodbav:"source_ref,omitempty"`
	CreatedAt    time.Time `dynamodbav:"created_at"`
	UpdatedAt    time.Time `dynamodbav:"updated_at"`
}

// Actor is one person's permission to act for one company.
//
// Separate from organization membership on purpose: an accountant's
// organization holds forty CNPJs and a junior handles five of them. Membership
// is what gets you into the workspace; this is what lets you act for a company
// inside it.
//
// **It carries no role, and no ownership, on purpose.** This edge answers reach
// — whether a person may act for this company at all — and nothing about what
// they may do once there. A role is a named bundle of permissions, which is
// authorization vocabulary owned by whichever product defines the verbs
// (ctech-billing ADR 0023): only ctech-dfe knows what emit.nfe means.
//
// "Who may hand out an explicit grant" is answered the same way: by the
// product's own OWNER row, not by a field here and not by the organization's
// owner_user_id — an accountant owning a workspace of forty CNPJs would own
// every company in it, which is exactly the customer this model exists for and
// exactly the wrong answer.
//
// Adding Role, Permissions or Owner here is what ADR 0023's "Reopen if" is
// watching for, and TestTheActorEdgeCarriesNoRole is what notices.
type Actor struct {
	OrganizationID string `dynamodbav:"-"`
	CompanyID      string `dynamodbav:"-"`
	UserID         string `dynamodbav:"-"`
	// Name is a cache of the person's display name, same rationale as
	// Membership.Name: a roster is one query instead of one plus a lookup per
	// row.
	Name      string    `dynamodbav:"name,omitempty"`
	GrantedBy string    `dynamodbav:"granted_by,omitempty"`
	CreatedAt time.Time `dynamodbav:"created_at"`
}

// ValidateNames trims both names and enforces the storage bound. The legal name
// is required; the trade name is not, because most companies do not have one
// and requiring it would mean typing the razão social twice.
func ValidateNames(legal, trade string) (string, string, bool) {
	legal = strings.TrimSpace(legal)
	trade = strings.TrimSpace(trade)
	if legal == "" || len(legal) > maxCompanyName || len(trade) > maxCompanyName {
		return "", "", false
	}
	return legal, trade, true
}
