package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/google/uuid"
	"gopkg.aoctech.app/account/api/internal/database"
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
	table database.Base
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{table: database.NewBase(db, tablePrefix, "account_users")}
}

func (r *dynamoRepository) GetByID(ctx context.Context, userID string) (*User, error) {
	item, err := r.table.GetItem(ctx, BuildPK(userID))
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
	res, err := r.table.QueryGSI(ctx, "email-index", "email", strings.ToLower(email), 1, nil)
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}

	var u User
	if err := attributevalue.UnmarshalMap(res.Items[0], &u); err != nil {
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
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) Update(ctx context.Context, userID string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), nil, updates)
	return err
}
