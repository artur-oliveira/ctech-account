package totp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/artur-oliveira/ctech-account/internal/database"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

var ErrNotFound = errors.New("totp not configured")
var ErrAlreadyVerified = errors.New("totp already verified")
var ErrInvalidCode = errors.New("invalid or expired totp code")

type Service struct {
	db    *database.Client
	table string
}

func NewService(db *database.Client) *Service {
	return &Service{db: db, table: "ctech_mfa"}
}

// Generate creates an unverified TOTP secret and returns the provisioning URI for QR code display.
func (s *Service) Generate(ctx context.Context, userID, accountName, issuer string) (*TOTPSecret, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Algorithm:   otp.AlgorithmSHA1,
		Digits:      otp.DigitsSix,
		Period:      30,
	})
	if err != nil {
		return nil, "", fmt.Errorf("generating totp key: %w", err)
	}

	secret := &TOTPSecret{
		PK:        BuildPK(userID),
		SK:        BuildSK(),
		Secret:    key.Secret(),
		Verified:  false,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	item, err := attributevalue.MarshalMap(secret)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling totp secret: %w", err)
	}
	if err := s.db.PutItem(ctx, s.table, item); err != nil {
		return nil, "", fmt.Errorf("storing totp secret: %w", err)
	}

	return secret, key.URL(), nil
}

// Verify validates the TOTP code against the stored secret, marks it verified, and generates backup codes.
func (s *Service) Verify(ctx context.Context, userID, code string) ([]string, error) {
	secret, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if secret.Verified {
		return nil, ErrAlreadyVerified
	}

	valid := totp.Validate(code, secret.Secret)
	if !valid {
		return nil, ErrInvalidCode
	}

	// Generate 10 one-time backup codes.
	rawCodes := make([]string, 10)
	hashedCodes := make([]string, 10)
	for i := range rawCodes {
		raw, _, err := crypto.GenerateCode()
		if err != nil {
			return nil, fmt.Errorf("generating backup code: %w", err)
		}
		hash, err := crypto.HashPassword(raw)
		if err != nil {
			return nil, fmt.Errorf("hashing backup code: %w", err)
		}
		rawCodes[i] = raw
		hashedCodes[i] = hash
	}

	dbKey, _ := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(),
	})

	verifiedAV, _ := attributevalue.Marshal(true)
	codesAV, _ := attributevalue.Marshal(hashedCodes)

	if err := s.db.UpdateItem(ctx, s.table, dbKey, map[string]types.AttributeValue{
		"verified":     verifiedAV,
		"backup_codes": codesAV,
	}); err != nil {
		return nil, fmt.Errorf("activating totp: %w", err)
	}

	return rawCodes, nil
}

// Validate checks a TOTP code (or backup code) during login.
func (s *Service) Validate(ctx context.Context, userID, code string) (bool, error) {
	secret, err := s.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if !secret.Verified {
		return false, nil
	}

	// Try TOTP first (allow ±1 period = 90 second window for clock skew).
	valid, err := totp.ValidateCustom(code, secret.Secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return false, nil
	}
	if valid {
		return true, nil
	}

	// Try backup codes (constant-time comparison).
	return s.validateBackupCode(ctx, secret, userID, code)
}

func (s *Service) validateBackupCode(ctx context.Context, secret *TOTPSecret, userID, code string) (bool, error) {
	for i, hash := range secret.BackupCodes {
		ok, err := crypto.VerifyPassword(code, hash)
		if err != nil || !ok {
			continue
		}
		// Consume backup code by removing it from the list.
		newCodes := append(secret.BackupCodes[:i], secret.BackupCodes[i+1:]...)
		dbKey, _ := attributevalue.MarshalMap(map[string]string{
			"pk": BuildPK(userID),
			"sk": BuildSK(),
		})
		codesAV, _ := attributevalue.Marshal(newCodes)
		_ = s.db.UpdateItem(ctx, s.table, dbKey, map[string]types.AttributeValue{
			"backup_codes": codesAV,
		})
		return true, nil
	}
	return false, nil
}

// RegenerateBackupCodes replaces all backup codes with a fresh set of 10 and returns the raw codes.
func (s *Service) RegenerateBackupCodes(ctx context.Context, userID string) ([]string, error) {
	secret, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !secret.Verified {
		return nil, ErrNotFound
	}

	rawCodes := make([]string, 10)
	hashedCodes := make([]string, 10)
	for i := range rawCodes {
		raw, _, err := crypto.GenerateCode()
		if err != nil {
			return nil, fmt.Errorf("generating backup code: %w", err)
		}
		hash, err := crypto.HashPassword(raw)
		if err != nil {
			return nil, fmt.Errorf("hashing backup code: %w", err)
		}
		rawCodes[i] = raw
		hashedCodes[i] = hash
	}

	dbKey, _ := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(),
	})
	codesAV, _ := attributevalue.Marshal(hashedCodes)
	if err := s.db.UpdateItem(ctx, s.table, dbKey, map[string]types.AttributeValue{
		"backup_codes": codesAV,
	}); err != nil {
		return nil, fmt.Errorf("updating backup codes: %w", err)
	}
	return rawCodes, nil
}

func (s *Service) Remove(ctx context.Context, userID string) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(),
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}
	return s.db.DeleteItem(ctx, s.table, key)
}

func (s *Service) Get(ctx context.Context, userID string) (*TOTPSecret, error) {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling key: %w", err)
	}

	item, err := s.db.GetItem(ctx, s.table, key)
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
	return &t, nil
}
