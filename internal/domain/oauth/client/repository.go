package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/database"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("oauth client not found")

// Repository is the data-access interface for OAuth clients.
type Repository interface {
	GetByID(ctx context.Context, clientID string) (*OAuthClient, error)
	Create(ctx context.Context, c *OAuthClient) error
}

type dynamoRepository struct {
	db    *database.Client
	table string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *database.Client) Repository {
	return &dynamoRepository{db: db, table: "account_oauth_clients"}
}

func (r *dynamoRepository) GetByID(ctx context.Context, clientID string) (*OAuthClient, error) {
	key, err := attributevalue.MarshalMap(map[string]string{"pk": BuildPK(clientID)})
	if err != nil {
		return nil, fmt.Errorf("marshaling key: %w", err)
	}

	item, err := r.db.GetItem(ctx, r.table, key)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}

	var c OAuthClient
	if err := attributevalue.UnmarshalMap(item, &c); err != nil {
		return nil, fmt.Errorf("unmarshaling client: %w", err)
	}
	return &c, nil
}

func (r *dynamoRepository) Create(ctx context.Context, c *OAuthClient) error {
	if c.PK == "" {
		c.PK = BuildPK(uuid.New().String())
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.CreatedAt = now
	c.UpdatedAt = now

	item, err := attributevalue.MarshalMap(c)
	if err != nil {
		return fmt.Errorf("marshaling client: %w", err)
	}
	return r.db.PutItem(ctx, r.table, item)
}

func (r *dynamoRepository) update(ctx context.Context, clientID string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	key, err := attributevalue.MarshalMap(map[string]string{"pk": BuildPK(clientID)})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	avUpdates := make(map[string]types.AttributeValue, len(updates))
	for k, v := range updates {
		av, err := attributevalue.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshaling field %s: %w", k, err)
		}
		avUpdates[k] = av
	}
	return r.db.UpdateItem(ctx, r.table, key, avUpdates)
}
