package apikey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/database"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var ErrNotFound = errors.New("api key not found")

// Repository is the data-access interface for API keys.
type Repository interface {
	Create(ctx context.Context, k *APIKey) error
	GetByID(ctx context.Context, userID, keyID string) (*APIKey, error)
	GetByHash(ctx context.Context, keyHash string) (*APIKey, error)
	ListByUserID(ctx context.Context, userID string) ([]*APIKey, error)
	UpdateLastUsed(ctx context.Context, userID, keyID string) error
	Delete(ctx context.Context, userID, keyID string) error
}

type dynamoRepository struct {
	db    *database.Client
	table string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *database.Client) Repository {
	return &dynamoRepository{db: db, table: "account_api_keys"}
}

func (r *dynamoRepository) Create(ctx context.Context, k *APIKey) error {
	item, err := attributevalue.MarshalMap(k)
	if err != nil {
		return fmt.Errorf("marshaling api key: %w", err)
	}
	return r.db.PutItem(ctx, r.table, item)
}

func (r *dynamoRepository) GetByID(ctx context.Context, userID, keyID string) (*APIKey, error) {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(keyID),
	})
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

	var k2 APIKey
	if err := attributevalue.UnmarshalMap(item, &k2); err != nil {
		return nil, fmt.Errorf("unmarshaling api key: %w", err)
	}
	return &k2, nil
}

func (r *dynamoRepository) GetByHash(ctx context.Context, keyHash string) (*APIKey, error) {
	items, err := r.db.QueryGSI(ctx, r.table, "key-hash-index", "key_hash", keyHash, 1)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}

	var k APIKey
	if err := attributevalue.UnmarshalMap(items[0], &k); err != nil {
		return nil, fmt.Errorf("unmarshaling api key: %w", err)
	}
	return &k, nil
}

func (r *dynamoRepository) ListByUserID(ctx context.Context, userID string) ([]*APIKey, error) {
	items, err := r.db.Query(ctx, r.table, BuildPK(userID), "APIKEY_", 0)
	if err != nil {
		return nil, err
	}

	keys := make([]*APIKey, 0, len(items))
	for _, item := range items {
		var k APIKey
		if err := attributevalue.UnmarshalMap(item, &k); err != nil {
			return nil, fmt.Errorf("unmarshaling api key: %w", err)
		}
		keys = append(keys, &k)
	}
	return keys, nil
}

func (r *dynamoRepository) UpdateLastUsed(ctx context.Context, userID, keyID string) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(keyID),
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	av, _ := attributevalue.Marshal(time.Now().UTC().Format(time.RFC3339))
	return r.db.UpdateItem(ctx, r.table, key, map[string]types.AttributeValue{
		"last_used_at": av,
	})
}

func (r *dynamoRepository) Delete(ctx context.Context, userID, keyID string) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(keyID),
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}
	return r.db.DeleteItem(ctx, r.table, key)
}
