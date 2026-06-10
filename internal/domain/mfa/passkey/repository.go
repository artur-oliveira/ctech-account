package passkey

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/artur-oliveira/ctech-account/internal/database"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var ErrNotFound = errors.New("passkey credential not found")

// Repository defines the persistence contract for passkey credentials.
type Repository interface {
	Create(ctx context.Context, c *Credential) error
	GetByCredentialID(ctx context.Context, userID string, credentialID []byte) (*Credential, error)
	ListByUserID(ctx context.Context, userID string) ([]*Credential, error)
	UpdateLastUsed(ctx context.Context, userID, credentialSK, lastUsedAt string) error
	Delete(ctx context.Context, userID, credentialSK string) error
}

type dynamoRepository struct {
	db    *database.Client
	table string
}

func NewRepository(db *database.Client) Repository {
	return &dynamoRepository{db: db, table: "account_passkeys"}
}

func (r *dynamoRepository) Create(ctx context.Context, c *Credential) error {
	item, err := attributevalue.MarshalMap(c)
	if err != nil {
		return fmt.Errorf("marshaling passkey: %w", err)
	}
	if err := r.db.PutItem(ctx, r.table, item); err != nil {
		return fmt.Errorf("storing passkey: %w", err)
	}
	return nil
}

func (r *dynamoRepository) GetByCredentialID(ctx context.Context, userID string, credentialID []byte) (*Credential, error) {
	sk := BuildSK(credentialID)
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": sk,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling key: %w", err)
	}

	item, err := r.db.GetItem(ctx, r.table, key)
	if err != nil {
		return nil, fmt.Errorf("getting passkey: %w", err)
	}
	if item == nil {
		return nil, ErrNotFound
	}

	var c Credential
	if err := attributevalue.UnmarshalMap(item, &c); err != nil {
		return nil, fmt.Errorf("unmarshaling passkey: %w", err)
	}
	return &c, nil
}

func (r *dynamoRepository) ListByUserID(ctx context.Context, userID string) ([]*Credential, error) {
	items, err := r.db.Query(ctx, r.table, BuildPK(userID), "PASSKEY_", 0)
	if err != nil {
		return nil, fmt.Errorf("querying passkeys: %w", err)
	}

	result := make([]*Credential, 0, len(items))
	for _, item := range items {
		var c Credential
		if err := attributevalue.UnmarshalMap(item, &c); err != nil {
			return nil, fmt.Errorf("unmarshaling passkey: %w", err)
		}
		result = append(result, &c)
	}
	return result, nil
}

func (r *dynamoRepository) UpdateLastUsed(ctx context.Context, userID, credentialSK, lastUsedAt string) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": credentialSK,
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	lastUsedAV, _ := attributevalue.Marshal(lastUsedAt)
	return r.db.UpdateItem(ctx, r.table, key, map[string]types.AttributeValue{
		"last_used_at": lastUsedAV,
	})
}

func (r *dynamoRepository) Delete(ctx context.Context, userID, credentialSK string) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": credentialSK,
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}
	return r.db.DeleteItem(ctx, r.table, key)
}

// CredentialSKFromHex reconstructs the sort key from a hex-encoded credential ID.
func CredentialSKFromHex(idHex string) (string, error) {
	if _, err := hex.DecodeString(idHex); err != nil {
		return "", fmt.Errorf("invalid credential ID: %w", err)
	}
	return "PASSKEY_" + idHex, nil
}
