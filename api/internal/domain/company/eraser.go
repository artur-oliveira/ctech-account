package company

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/account/api/internal/database"
)

// Eraser removes every company row of an organization: companies, their
// tax-id locks and actor edges all live under ORG#{id} in the companies table.
// Used only by account deletion (D4, D12).
type Eraser struct{ companies database.Base }

func NewEraser(db *dynamodb.Client, tablePrefix string) *Eraser {
	return &Eraser{companies: database.NewBase(db, tablePrefix, companiesTable)}
}

func (e *Eraser) EraseForOrganization(ctx context.Context, orgID string) error {
	return database.DeleteAllUnder(ctx, &e.companies, orgPK(orgID), "")
}
