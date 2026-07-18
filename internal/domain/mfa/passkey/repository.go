package passkey

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"gopkg.aoctech.app/account/internal/database"
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
	table database.Base
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{table: database.NewBase(db, tablePrefix, "account_passkeys")}
}

func (r *dynamoRepository) Create(ctx context.Context, c *Credential) error {
	item, err := attributevalue.MarshalMap(c)
	if err != nil {
		return fmt.Errorf("marshaling passkey: %w", err)
	}
	if err := r.table.PutItem(ctx, item); err != nil {
		return fmt.Errorf("storing passkey: %w", err)
	}
	return nil
}

func (r *dynamoRepository) GetByCredentialID(ctx context.Context, userID string, credentialID []byte) (*Credential, error) {
	item, err := r.table.GetItem(ctx, BuildPK(userID), BuildSK(credentialID))
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
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(userID), SKPrefix: "PASSKEY_"})
	if err != nil {
		return nil, fmt.Errorf("querying passkeys: %w", err)
	}

	result := make([]*Credential, 0, len(res.Items))
	for _, item := range res.Items {
		var c Credential
		if err := attributevalue.UnmarshalMap(item, &c); err != nil {
			return nil, fmt.Errorf("unmarshaling passkey: %w", err)
		}
		result = append(result, &c)
	}
	return result, nil
}

func (r *dynamoRepository) UpdateLastUsed(ctx context.Context, userID, credentialSK, lastUsedAt string) error {
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), &credentialSK, map[string]any{
		"last_used_at": lastUsedAt,
	})
	return err
}

func (r *dynamoRepository) Delete(ctx context.Context, userID, credentialSK string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(userID), credentialSK)
	return err
}

// CredentialSKFromHex reconstructs the sort key from a hex-encoded credential ID.
func CredentialSKFromHex(idHex string) (string, error) {
	if _, err := hex.DecodeString(idHex); err != nil {
		return "", fmt.Errorf("invalid credential ID: %w", err)
	}
	return "PASSKEY_" + idHex, nil
}
