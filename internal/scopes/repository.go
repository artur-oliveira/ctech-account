package scopes

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"gopkg.aoctech.app/account/internal/database"
)

// scopesTable is the platform-wide scope registry. It is deliberately named
// {env}_ctech_scopes (not {env}_account_*): every service's scopes live here.
const scopesTable = "ctech_scopes"

// catalogPK is the single partition holding all service items, so the whole
// catalog loads with one Query (never a Scan). SK = service code.
const catalogPK = "SERVICE"

// Repository is the data-access interface for the scope catalog.
type Repository interface {
	LoadCatalog(ctx context.Context) ([]ServiceScopes, error)
	PutService(ctx context.Context, svc ServiceScopes) error
}

type dynamoRepository struct {
	table database.Base
}

// NewRepository returns a DynamoDB-backed catalog Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{table: database.NewBase(db, tablePrefix, scopesTable)}
}

func (r *dynamoRepository) LoadCatalog(ctx context.Context) ([]ServiceScopes, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: catalogPK})
	if err != nil {
		return nil, fmt.Errorf("querying scope catalog: %w", err)
	}
	services := make([]ServiceScopes, 0, len(res.Items))
	for _, item := range res.Items {
		var svc ServiceScopes
		if err := attributevalue.UnmarshalMap(item, &svc); err != nil {
			return nil, fmt.Errorf("unmarshaling scope service: %w", err)
		}
		services = append(services, svc)
	}
	return services, nil
}

func (r *dynamoRepository) PutService(ctx context.Context, svc ServiceScopes) error {
	item, err := attributevalue.MarshalMap(svc)
	if err != nil {
		return fmt.Errorf("marshaling scope service: %w", err)
	}
	pk, err := attributevalue.Marshal(catalogPK)
	if err != nil {
		return fmt.Errorf("marshaling pk: %w", err)
	}
	item["pk"] = pk
	return r.table.PutItem(ctx, item)
}
