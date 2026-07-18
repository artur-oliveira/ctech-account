package consent

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"gopkg.aoctech.app/account/internal/database"
)

var ErrNotFound = errors.New("consent grant not found")

// Repository is the data-access interface for consent grants.
type Repository interface {
	Get(ctx context.Context, userID, clientID string) (*Grant, error)
	Put(ctx context.Context, g *Grant) error
	Delete(ctx context.Context, userID, clientID string) error
	ListByUser(ctx context.Context, userID string) ([]*Grant, error)
}

type dynamoRepository struct {
	table database.Base
}

// NewRepository returns a DynamoDB-backed Repository. Grants share the
// sessions table (pk/sk schema) under the CONSENT_ sort-key prefix.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{table: database.NewBase(db, tablePrefix, "account_sessions")}
}

func (r *dynamoRepository) Get(ctx context.Context, userID, clientID string) (*Grant, error) {
	item, err := r.table.GetItem(ctx, BuildPK(userID), BuildSK(clientID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}

	var g Grant
	if err := attributevalue.UnmarshalMap(item, &g); err != nil {
		return nil, fmt.Errorf("unmarshaling grant: %w", err)
	}
	return &g, nil
}

func (r *dynamoRepository) Put(ctx context.Context, g *Grant) error {
	item, err := attributevalue.MarshalMap(g)
	if err != nil {
		return fmt.Errorf("marshaling grant: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) Delete(ctx context.Context, userID, clientID string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(userID), BuildSK(clientID))
	return err
}

func (r *dynamoRepository) ListByUser(ctx context.Context, userID string) ([]*Grant, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(userID), SKPrefix: skPrefix})
	if err != nil {
		return nil, err
	}
	grants := make([]*Grant, 0, len(res.Items))
	for _, item := range res.Items {
		var g Grant
		if err := attributevalue.UnmarshalMap(item, &g); err != nil {
			return nil, fmt.Errorf("unmarshaling grant: %w", err)
		}
		grants = append(grants, &g)
	}
	return grants, nil
}
