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
	"gopkg.aoctech.app/account/api/internal/crypto"
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
	table     database.Base
	db        *dynamodb.Client
	tableName string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	tableName := database.TableName(tablePrefix, "account_users")
	return &dynamoRepository{
		table:     database.NewBase(db, tablePrefix, "account_users"),
		db:        db,
		tableName: tableName,
	}
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

	// CON-008: enforce email uniqueness with a conditional marker write. The
	// marker PK is a hash of the email, so two concurrent creates for the same
	// address race on the SAME item; attribute_not_exists(pk) lets exactly one
	// win and the loser sees ErrEmailConflict — closing the non-atomic
	// check-then-create gap. The marker deliberately omits the `email`
	// attribute so it is NOT projected into the email-index GSI and cannot be
	// returned by GetByEmail in place of the real user.
	markerPK := "EMAIL#" + crypto.HashToken(u.Email)
	applied, err := database.ConditionalUpdate(ctx, r.db, r.tableName, markerPK, nil,
		map[string]any{"kind": "email-lock", "created_at": now},
		"attribute_not_exists(pk)", nil, nil)
	if err != nil {
		return fmt.Errorf("creating email marker: %w", err)
	}
	if !applied {
		return ErrEmailConflict
	}

	item, err := attributevalue.MarshalMap(u)
	if err != nil {
		// Best-effort rollback: a failed marshal must not leave the marker
		// permanently blocking this email.
		_, _ = r.table.DeleteItem(ctx, markerPK)
		return fmt.Errorf("marshaling user: %w", err)
	}
	if err := r.table.PutItem(ctx, item); err != nil {
		_, _ = r.table.DeleteItem(ctx, markerPK)
		return fmt.Errorf("putting user: %w", err)
	}
	return nil
}

func (r *dynamoRepository) Update(ctx context.Context, userID string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), nil, updates)
	return err
}
