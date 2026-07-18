package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/google/uuid"
	"gopkg.aoctech.app/account/api/internal/database"
)

// maxOwnedClients bounds ListByOwner — no user is expected to own anywhere
// near this many OAuth clients.
const maxOwnedClients = 100

var ErrNotFound = errors.New("oauth client not found")

// Repository is the data-access interface for OAuth clients.
type Repository interface {
	GetByID(ctx context.Context, clientID string) (*OAuthClient, error)
	Create(ctx context.Context, c *OAuthClient) error
	ListByOwner(ctx context.Context, ownerUserID string) ([]*OAuthClient, error)
	Update(ctx context.Context, clientID string, updates map[string]any) error
	Delete(ctx context.Context, clientID string) error
}

type dynamoRepository struct {
	table database.Base
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{table: database.NewBase(db, tablePrefix, "account_oauth_clients")}
}

func (r *dynamoRepository) GetByID(ctx context.Context, clientID string) (*OAuthClient, error) {
	item, err := r.table.GetItem(ctx, BuildPK(clientID))
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
	return r.table.PutItem(ctx, item)
}

// ownerIndex is the GSI keyed by owner_user_id for listing a user's clients.
const ownerIndex = "owner-index"

func (r *dynamoRepository) ListByOwner(ctx context.Context, ownerUserID string) ([]*OAuthClient, error) {
	res, err := r.table.QueryGSI(ctx, ownerIndex, "owner_user_id", ownerUserID, maxOwnedClients, nil)
	if err != nil {
		return nil, fmt.Errorf("querying owner index: %w", err)
	}
	clients := make([]*OAuthClient, 0, len(res.Items))
	for _, item := range res.Items {
		var c OAuthClient
		if err := attributevalue.UnmarshalMap(item, &c); err != nil {
			return nil, fmt.Errorf("unmarshaling client: %w", err)
		}
		clients = append(clients, &c)
	}
	return clients, nil
}

func (r *dynamoRepository) Delete(ctx context.Context, clientID string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(clientID))
	return err
}

func (r *dynamoRepository) Update(ctx context.Context, clientID string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	_, err := r.table.UpdateItem(ctx, BuildPK(clientID), nil, updates)
	return err
}
