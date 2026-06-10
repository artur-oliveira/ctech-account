package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/database"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("user not found")
var ErrEmailConflict = errors.New("email already registered")

// Repository is the data-access interface for users.
type Repository interface {
	GetByID(ctx context.Context, userID string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	Create(ctx context.Context, u *User) error
	Update(ctx context.Context, userID string, updates map[string]any) error
}

type dynamoRepository struct {
	db    *database.Client
	table string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *database.Client) Repository {
	return &dynamoRepository{db: db, table: "account_users"}
}

func (r *dynamoRepository) GetByID(ctx context.Context, userID string) (*User, error) {
	key, err := attributevalue.MarshalMap(map[string]string{"pk": BuildPK(userID)})
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

	var u User
	if err := attributevalue.UnmarshalMap(item, &u); err != nil {
		return nil, fmt.Errorf("unmarshaling user: %w", err)
	}
	return &u, nil
}

func (r *dynamoRepository) GetByEmail(ctx context.Context, email string) (*User, error) {
	items, err := r.db.QueryGSI(ctx, r.table, "email-index", "email", strings.ToLower(email), 1)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}

	var u User
	if err := attributevalue.UnmarshalMap(items[0], &u); err != nil {
		return nil, fmt.Errorf("unmarshaling user: %w", err)
	}
	return &u, nil
}

func (r *dynamoRepository) Create(ctx context.Context, u *User) error {
	if u.PK == "" {
		u.PK = BuildPK(uuid.New().String())
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u.CreatedAt = now
	u.UpdatedAt = now
	u.Email = strings.ToLower(u.Email)

	item, err := attributevalue.MarshalMap(u)
	if err != nil {
		return fmt.Errorf("marshaling user: %w", err)
	}
	return r.db.PutItem(ctx, r.table, item)
}

func (r *dynamoRepository) Update(ctx context.Context, userID string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	key, err := attributevalue.MarshalMap(map[string]string{"pk": BuildPK(userID)})
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}

	avUpdates := make(map[string]types.AttributeValue, len(updates))
	for k, v := range updates {
		av, err := attributevalue.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshaling update field %s: %w", k, err)
		}
		avUpdates[k] = av
	}
	return r.db.UpdateItem(ctx, r.table, key, avUpdates)
}
