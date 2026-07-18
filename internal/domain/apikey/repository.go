package apikey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"gopkg.aoctech.app/account/internal/database"
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
	table database.Base
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{table: database.NewBase(db, tablePrefix, "account_api_keys")}
}

func (r *dynamoRepository) Create(ctx context.Context, k *APIKey) error {
	item, err := attributevalue.MarshalMap(k)
	if err != nil {
		return fmt.Errorf("marshaling api key: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) GetByID(ctx context.Context, userID, keyID string) (*APIKey, error) {
	item, err := r.table.GetItem(ctx, BuildPK(userID), BuildSK(keyID))
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
	res, err := r.table.QueryGSI(ctx, "key-hash-index", "key_hash", keyHash, 1, nil)
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}

	var k APIKey
	if err := attributevalue.UnmarshalMap(res.Items[0], &k); err != nil {
		return nil, fmt.Errorf("unmarshaling api key: %w", err)
	}
	return &k, nil
}

func (r *dynamoRepository) ListByUserID(ctx context.Context, userID string) ([]*APIKey, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(userID), SKPrefix: "APIKEY_"})
	if err != nil {
		return nil, err
	}

	keys := make([]*APIKey, 0, len(res.Items))
	for _, item := range res.Items {
		var k APIKey
		if err := attributevalue.UnmarshalMap(item, &k); err != nil {
			return nil, fmt.Errorf("unmarshaling api key: %w", err)
		}
		keys = append(keys, &k)
	}
	return keys, nil
}

func (r *dynamoRepository) UpdateLastUsed(ctx context.Context, userID, keyID string) error {
	sk := BuildSK(keyID)
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), &sk, map[string]any{
		"last_used_at": time.Now().UTC().Format(time.RFC3339),
	})
	return err
}

func (r *dynamoRepository) Delete(ctx context.Context, userID, keyID string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(userID), BuildSK(keyID))
	return err
}
