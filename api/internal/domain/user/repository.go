package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/api-commons/observability"
)

var ErrNotFound = errors.New("user not found")
var ErrEmailConflict = errors.New("email already registered")

// Repository is the data-access interface for users.
type Repository interface {
	// Purge replaces the user item with tomb (only while it still belongs to
	// tomb.DeletionRequestID), writes the KYC retention item and deletes the
	// uniqueness markers, in one transaction.
	Purge(ctx context.Context, tomb *Tombstone, kyc *KYCRetention, markerPKs []string) error

	GetByID(ctx context.Context, userID string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	Create(ctx context.Context, u *User) error
	Update(ctx context.Context, userID string, updates map[string]any) error
	// ClearDeletionIfRequest removes the deletion block only while it still
	// belongs to requestID (conditional write); otherwise it is a no-op.
	ClearDeletionIfRequest(ctx context.Context, userID, requestID string) error
	// AddUnlinkedService and RemoveUnlinkedService change the unlinked_services
	// set atomically. Both are idempotent.
	AddUnlinkedService(ctx context.Context, userID, service string) error
	RemoveUnlinkedService(ctx context.Context, userID, service string) error
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
	markerPK := EmailMarkerPK(u.Email)
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
		if _, rollbackErr := r.table.DeleteItem(ctx, markerPK); rollbackErr != nil {
			observability.Error(ctx, "user repository: failed to roll back email marker after marshal failure", rollbackErr)
		}
		return fmt.Errorf("marshaling user: %w", err)
	}
	if err := r.table.PutItem(ctx, item); err != nil {
		if _, rollbackErr := r.table.DeleteItem(ctx, markerPK); rollbackErr != nil {
			observability.Error(ctx, "user repository: failed to roll back email marker after user write failure", rollbackErr)
		}
		return fmt.Errorf("putting user: %w", err)
	}
	return nil
}

func (r *dynamoRepository) Update(ctx context.Context, userID string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), nil, updates)
	return err
}

func (r *dynamoRepository) ClearDeletionIfRequest(ctx context.Context, userID, requestID string) error {
	_, err := database.ConditionalUpdate(ctx, r.db, r.tableName, BuildPK(userID), nil,
		map[string]any{
			"deletion_state":      nil,
			"deletion_request_id": nil,
			"updated_at":          time.Now().UTC().Format(time.RFC3339),
		},
		"#deletion_request_id = :rid", nil,
		map[string]types.AttributeValue{":rid": &types.AttributeValueMemberS{Value: requestID}},
	)
	return err // a failed condition means a newer request owns the block: nothing to do
}

func (r *dynamoRepository) changeUnlinked(ctx context.Context, userID, verb, service string) error {
	_, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(r.tableName),
		Key:                 map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: BuildPK(userID)}},
		UpdateExpression:    aws.String(verb + " unlinked_services :s SET updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(pk)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s":   &types.AttributeValueMemberSS{Value: []string{service}},
			":now": &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
	})
	return err
}

func (r *dynamoRepository) AddUnlinkedService(ctx context.Context, userID, service string) error {
	return r.changeUnlinked(ctx, userID, "ADD", service)
}

func (r *dynamoRepository) RemoveUnlinkedService(ctx context.Context, userID, service string) error {
	return r.changeUnlinked(ctx, userID, "DELETE", service)
}

var ErrPurgeConflict = errors.New("user: purge refused, the deletion block belongs to another request")

func (r *dynamoRepository) Purge(ctx context.Context, tomb *Tombstone, kyc *KYCRetention, markerPKs []string) error {
	tombItem, err := attributevalue.MarshalMap(tomb)
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{{Put: &types.Put{
		TableName:                 aws.String(r.tableName),
		Item:                      tombItem,
		ConditionExpression:       aws.String("deletion_request_id = :rid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":rid": &types.AttributeValueMemberS{Value: tomb.DeletionRequestID}},
	}}}
	if kyc != nil {
		kycItem, err := attributevalue.MarshalMap(kyc)
		if err != nil {
			return err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(r.tableName), Item: kycItem}})
	}
	for _, pk := range markerPKs {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{
			TableName: aws.String(r.tableName),
			Key:       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}},
		}})
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if database.IsConditionFailed(err) {
		return ErrPurgeConflict
	}
	return err
}
