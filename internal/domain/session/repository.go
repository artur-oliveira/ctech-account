package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/database"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var ErrNotFound = errors.New("session not found")

// Repository is the data-access interface for sessions.
type Repository interface {
	GetByID(ctx context.Context, userID, sessionID string) (*Session, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	Create(ctx context.Context, s *Session) error
	UpdateRefreshToken(ctx context.Context, userID, sessionID, newHash string) error
	UpdateGeoData(ctx context.Context, userID, sessionID, city, region string, lat, lon float64) error
	Delete(ctx context.Context, userID, sessionID string) error
	ListByUserID(ctx context.Context, userID string) ([]*Session, error)
}

type dynamoRepository struct {
	db    *database.Client
	table string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *database.Client) Repository {
	return &dynamoRepository{db: db, table: "account_sessions"}
}

func (r *dynamoRepository) Create(ctx context.Context, s *Session) error {
	item, err := attributevalue.MarshalMap(s)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}
	return r.db.PutItem(ctx, r.table, item)
}

func (r *dynamoRepository) GetByID(ctx context.Context, userID, sessionID string) (*Session, error) {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(sessionID),
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

	var s Session
	if err := attributevalue.UnmarshalMap(item, &s); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}
	return &s, nil
}

func (r *dynamoRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	items, err := r.db.QueryGSI(ctx, r.table, "token-hash-index", "refresh_token_hash", tokenHash, 1)
	if err != nil {
		return nil, fmt.Errorf("querying token hash index: %w", err)
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	var s Session
	if err := attributevalue.UnmarshalMap(items[0], &s); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}
	return &s, nil
}

func (r *dynamoRepository) ListByUserID(ctx context.Context, userID string) ([]*Session, error) {
	items, err := r.db.Query(ctx, r.table, BuildPK(userID), "SESSION_", 0)
	if err != nil {
		return nil, err
	}

	sessions := make([]*Session, 0, len(items))
	for _, item := range items {
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
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(sessionID),
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}
	return r.db.DeleteItem(ctx, r.table, key)
}

func (r *dynamoRepository) UpdateGeoData(ctx context.Context, userID, sessionID, city, region string, lat, lon float64) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(sessionID),
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	updates := map[string]types.AttributeValue{}
	for field, val := range map[string]any{
		"geo_city":      city,
		"geo_region":    region,
		"geo_latitude":  lat,
		"geo_longitude": lon,
	} {
		av, _ := attributevalue.Marshal(val)
		updates[field] = av
	}
	return r.db.UpdateItem(ctx, r.table, key, updates)
}

func (r *dynamoRepository) UpdateRefreshToken(ctx context.Context, userID, sessionID, newHash string) error {
	key, err := attributevalue.MarshalMap(map[string]string{
		"pk": BuildPK(userID),
		"sk": BuildSK(sessionID),
	})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	updates := map[string]types.AttributeValue{}
	for k, v := range map[string]string{
		"refresh_token_hash": newHash,
		"last_used_at":       now,
	} {
		av, _ := attributevalue.Marshal(v)
		updates[k] = av
	}
	return r.db.UpdateItem(ctx, r.table, key, updates)
}
