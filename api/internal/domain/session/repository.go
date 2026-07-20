package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
)

var ErrNotFound = errors.New("session not found")

// ErrRefreshTokenNotFound is returned when no RefreshToken item matches a lookup.
var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// Repository is the data-access interface for sessions and per-client refresh tokens.
type Repository interface {
	GetByID(ctx context.Context, userID, sessionID string) (*Session, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	Create(ctx context.Context, s *Session) error
	UpdateGeoData(ctx context.Context, userID, sessionID, city, region string, lat, lon float64) error
	UpdateMFA(ctx context.Context, userID, sessionID string, amr []string, lastMFAAt int64) error
	Delete(ctx context.Context, userID, sessionID string) error
	ListByUserID(ctx context.Context, userID string) ([]*Session, error)

	PutRefreshToken(ctx context.Context, t *RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	UpdateRefreshTokenHash(ctx context.Context, userID, sessionID, clientID, newHash, oldHash string) error
	ListRefreshTokensBySession(ctx context.Context, userID, sessionID string) ([]*RefreshToken, error)
	DeleteRefreshToken(ctx context.Context, userID, sessionID, clientID string) error
}

type dynamoRepository struct {
	table     database.Base
	db        *dynamodb.Client
	tableName string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{
		table:     database.NewBase(db, tablePrefix, "account_sessions"),
		db:        db,
		tableName: database.TableName(tablePrefix, "account_sessions"),
	}
}

func (r *dynamoRepository) Create(ctx context.Context, s *Session) error {
	item, err := attributevalue.MarshalMap(s)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) GetByID(ctx context.Context, userID, sessionID string) (*Session, error) {
	item, err := r.table.GetItem(ctx, BuildPK(userID), BuildSK(sessionID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}

	var s Session
	if err := attributevalue.UnmarshalMap(item, &s); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}
	return &s, nil
}

// tokenHashIndex is the GSI on refresh_token_hash shared by Session and
// RefreshToken items; queries disambiguate the two by SK prefix.
const tokenHashIndex = "token-hash-index"

// queryByTokenHash fetches the single item carrying tokenHash. Token hashes are
// 256-bit random values, so at most one item ever matches.
func (r *dynamoRepository) queryByTokenHash(ctx context.Context, tokenHash string) (map[string]types.AttributeValue, error) {
	res, err := r.table.QueryGSI(ctx, tokenHashIndex, "refresh_token_hash", tokenHash, 1, nil)
	if err != nil {
		return nil, fmt.Errorf("querying token hash index: %w", err)
	}
	if len(res.Items) == 0 {
		return nil, nil
	}
	return res.Items[0], nil
}

func skPrefix(item map[string]types.AttributeValue) string {
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		return sk.Value
	}
	return ""
}

func (r *dynamoRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	item, err := r.queryByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if item == nil || !strings.HasPrefix(skPrefix(item), sessionSKPrefix) {
		return nil, ErrNotFound
	}
	var s Session
	if err := attributevalue.UnmarshalMap(item, &s); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}
	return &s, nil
}

func (r *dynamoRepository) PutRefreshToken(ctx context.Context, t *RefreshToken) error {
	item, err := attributevalue.MarshalMap(t)
	if err != nil {
		return fmt.Errorf("marshaling refresh token: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	item, err := r.queryByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if item == nil || !strings.HasPrefix(skPrefix(item), refreshSKPrefix) {
		return nil, ErrRefreshTokenNotFound
	}
	var t RefreshToken
	if err := attributevalue.UnmarshalMap(item, &t); err != nil {
		return nil, fmt.Errorf("unmarshaling refresh token: %w", err)
	}
	return &t, nil
}

func (r *dynamoRepository) UpdateRefreshTokenHash(ctx context.Context, userID, sessionID, clientID, newHash, oldHash string) error {
	sk := BuildRefreshSK(sessionID, clientID)
	oldAV, _ := attributevalue.Marshal(oldHash)
	updates := map[string]any{
		"refresh_token_hash": newHash,
		"last_used_at":       time.Now().UTC().Format(time.RFC3339),
	}
	applied, err := database.ConditionalUpdate(ctx, r.db, r.tableName, BuildPK(userID), &sk, updates,
		"#refresh_token_hash = :old_hash", nil, map[string]types.AttributeValue{":old_hash": oldAV})
	if err != nil {
		return fmt.Errorf("rotating refresh token hash: %w", err)
	}
	if !applied {
		return ErrTokenReuse
	}
	return nil
}

// UpdateMFA persists a fresh MFA proof on the session (amr set + last_mfa_at).
func (r *dynamoRepository) UpdateMFA(ctx context.Context, userID, sessionID string, amr []string, lastMFAAt int64) error {
	sk := BuildSK(sessionID)
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), &sk, map[string]any{
		"amr":         amr,
		"last_mfa_at": lastMFAAt,
	})
	return err
}

func (r *dynamoRepository) ListRefreshTokensBySession(ctx context.Context, userID, sessionID string) ([]*RefreshToken, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(userID), SKPrefix: refreshSKPrefix + sessionID + refreshSKSeparator})
	if err != nil {
		return nil, err
	}
	tokens := make([]*RefreshToken, 0, len(res.Items))
	for _, item := range res.Items {
		var t RefreshToken
		if err := attributevalue.UnmarshalMap(item, &t); err != nil {
			return nil, fmt.Errorf("unmarshaling refresh token: %w", err)
		}
		tokens = append(tokens, &t)
	}
	return tokens, nil
}

func (r *dynamoRepository) DeleteRefreshToken(ctx context.Context, userID, sessionID, clientID string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(userID), BuildRefreshSK(sessionID, clientID))
	return err
}

func (r *dynamoRepository) ListByUserID(ctx context.Context, userID string) ([]*Session, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(userID), SKPrefix: "SESSION_"})
	if err != nil {
		return nil, err
	}

	sessions := make([]*Session, 0, len(res.Items))
	for _, item := range res.Items {
		var s Session
		if err := attributevalue.UnmarshalMap(item, &s); err != nil {
			return nil, fmt.Errorf("unmarshaling session: %w", err)
		}
		if !s.IsExpired() {
			sessions = append(sessions, &s)
		}
	}
	return sessions, nil
}

func (r *dynamoRepository) Delete(ctx context.Context, userID, sessionID string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(userID), BuildSK(sessionID))
	return err
}

func (r *dynamoRepository) UpdateGeoData(ctx context.Context, userID, sessionID, city, region string, lat, lon float64) error {
	sk := BuildSK(sessionID)
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), &sk, map[string]any{
		"geo_city":      city,
		"geo_region":    region,
		"geo_latitude":  lat,
		"geo_longitude": lon,
	})
	return err
}
