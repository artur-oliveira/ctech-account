package totp

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/database"
)

// Repository is the persistence contract for TOTP secrets. Services depend on
// this interface, never on DynamoDB directly.
type Repository interface {
	Create(ctx context.Context, s *TOTPSecret) error
	Get(ctx context.Context, userID string) (*TOTPSecret, error)
	// Confirm marks the secret verified and stores the initial backup codes.
	// Returns applied=false when the secret is already verified (idempotent).
	Confirm(ctx context.Context, userID string, backupCodes []string) (bool, error)
	// ConsumeBackupCode removes a used backup code under optimistic concurrency.
	// Returns applied=false when the version moved (already consumed by a racer).
	ConsumeBackupCode(ctx context.Context, userID string, remaining []string, version int64) (bool, error)
	ReplaceBackupCodes(ctx context.Context, userID string, backupCodes []string) error
	Remove(ctx context.Context, userID string) error
}

type dynamoRepository struct {
	base      database.Base
	db        *dynamodb.Client
	tableName string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{
		base:      database.NewBase(db, tablePrefix, "account_mfa"),
		db:        db,
		tableName: database.TableName(tablePrefix, "account_mfa"),
	}
}

func (r *dynamoRepository) Create(ctx context.Context, s *TOTPSecret) error {
	enc, err := crypto.Seal(s.Secret)
	if err != nil {
		return fmt.Errorf("encrypting totp secret: %w", err)
	}
	s.EncryptedSecret = enc
	item, err := attributevalue.MarshalMap(s)
	if err != nil {
		return fmt.Errorf("marshaling totp secret: %w", err)
	}
	if err := r.base.PutItem(ctx, item); err != nil {
		return fmt.Errorf("storing totp secret: %w", err)
	}
	return nil
}

func (r *dynamoRepository) Get(ctx context.Context, userID string) (*TOTPSecret, error) {
	item, err := r.base.GetItem(ctx, BuildPK(userID), BuildSK())
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}
	var t TOTPSecret
	if err := attributevalue.UnmarshalMap(item, &t); err != nil {
		return nil, fmt.Errorf("unmarshaling totp: %w", err)
	}
	// Decrypt the envelope-encrypted secret into the in-memory plaintext field.
	// Open fails on legacy plaintext records (written before encryption): keep
	// the stored value as-is so old data remains readable until migrated.
	if plain, derr := crypto.Open(t.EncryptedSecret); derr == nil {
		t.Secret = plain
	} else {
		t.Secret = t.EncryptedSecret
	}
	return &t, nil
}

// Confirm applies the verified=true + backup_codes write only when the secret
// is still unverified, preventing two concurrent /totp/confirm calls from
// clobbering each other's freshly generated backup codes.
func (r *dynamoRepository) Confirm(ctx context.Context, userID string, backupCodes []string) (bool, error) {
	pk := BuildPK(userID)
	sk := BuildSK()
	condValues := map[string]types.AttributeValue{
		":f": &types.AttributeValueMemberBOOL{Value: false},
	}
	applied, err := database.ConditionalUpdate(ctx, r.db, r.tableName, pk, &sk, map[string]any{
		"verified":     true,
		"backup_codes": backupCodes,
	}, "attribute_not_exists(#verified) OR #verified = :f", nil, condValues)
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ConsumeBackupCode applies the removal of a used backup code only when the
// stored version still matches the version we read, so a concurrent login
// using the same code loses the race and is rejected (TOCTOU double-spend).
func (r *dynamoRepository) ConsumeBackupCode(ctx context.Context, userID string, remaining []string, version int64) (bool, error) {
	pk := BuildPK(userID)
	sk := BuildSK()
	condValues := map[string]types.AttributeValue{
		":cv": &types.AttributeValueMemberN{Value: strconv.FormatInt(version, 10)},
	}
	applied, err := database.ConditionalUpdate(ctx, r.db, r.tableName, pk, &sk, map[string]any{
		"backup_codes": remaining,
		"version":      version + 1,
	}, "attribute_not_exists(#version) OR #version = :cv", nil, condValues)
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (r *dynamoRepository) ReplaceBackupCodes(ctx context.Context, userID string, backupCodes []string) error {
	sk := BuildSK()
	_, err := r.base.UpdateItem(ctx, BuildPK(userID), &sk, map[string]any{
		"backup_codes": backupCodes,
	})
	return err
}

func (r *dynamoRepository) Remove(ctx context.Context, userID string) error {
	_, err := r.base.DeleteItem(ctx, BuildPK(userID), BuildSK())
	return err
}
