package deletion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/api-commons/dynamo"
)

const (
	TableSuffix = "account_deletion_requests"
	DueIndex    = "gsi_state_due"
	markerSK    = "OPEN"
)

// Repository persists requests. Save is the only way to change a request and
// is conditional on the state it was read in.
type Repository interface {
	// Create stores r and the user's open marker; ErrOpenRequest if one exists.
	Create(ctx context.Context, r *Request) error
	Get(ctx context.Context, id string) (*Request, error)
	// GetOpen returns the user's open request, or ErrNotFound.
	GetOpen(ctx context.Context, userID string) (*Request, error)
	// Save writes r if the stored state is still from (ErrStateChanged
	// otherwise) and releases the open marker when r leaves the open states.
	Save(ctx context.Context, r *Request, from State) error
	// ListDue returns ids of requests in state whose next action is at or before now.
	ListDue(ctx context.Context, state State, now time.Time, limit int32) ([]string, error)
}

type marker struct {
	PK        string `dynamodbav:"pk"`
	SK        string `dynamodbav:"sk"`
	RequestID string `dynamodbav:"request_id"`
}

func markerPK(userID string) string { return "SUB#" + userID }

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk},
		"sk": &types.AttributeValueMemberS{Value: sk},
	}
}

func str(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

type dynamoRepository struct {
	db    *dynamodb.Client
	table string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{db: db, table: database.TableName(tablePrefix, TableSuffix)}
}

func (r *dynamoRepository) Create(ctx context.Context, req *Request) error {
	item, err := attributevalue.MarshalMap(req)
	if err != nil {
		return err
	}
	mk, err := attributevalue.MarshalMap(marker{PK: markerPK(req.UserID), SK: markerSK, RequestID: req.ID})
	if err != nil {
		return err
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(r.table), Item: mk, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(r.table), Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if dynamo.IsConditionFailed(err) {
		return ErrOpenRequest
	}
	if err != nil {
		return fmt.Errorf("deletion: create: %w", err)
	}
	return nil
}

func (r *dynamoRepository) Get(ctx context.Context, id string) (*Request, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.table), Key: key(BuildPK(id), metaSK), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("deletion: get %s: %w", id, err)
	}
	if len(out.Item) == 0 {
		return nil, ErrNotFound
	}
	var req Request
	if err := attributevalue.UnmarshalMap(out.Item, &req); err != nil {
		return nil, fmt.Errorf("deletion: decode %s: %w", id, err)
	}
	return &req, nil
}

func (r *dynamoRepository) GetOpen(ctx context.Context, userID string) (*Request, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.table), Key: key(markerPK(userID), markerSK), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("deletion: get open marker: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrNotFound
	}
	var mk marker
	if err := attributevalue.UnmarshalMap(out.Item, &mk); err != nil {
		return nil, err
	}
	return r.Get(ctx, mk.RequestID)
}

func (r *dynamoRepository) Save(ctx context.Context, req *Request, from State) error {
	item, err := attributevalue.MarshalMap(req)
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{{Put: &types.Put{
		TableName:                 aws.String(r.table),
		Item:                      item,
		ConditionExpression:       aws.String("request_state = :from"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":from": str(string(from))},
	}}}
	if from.Open() && !req.State.Open() {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{
			TableName:                 aws.String(r.table),
			Key:                       key(markerPK(req.UserID), markerSK),
			ConditionExpression:       aws.String("request_id = :id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":id": str(req.ID)},
		}})
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if dynamo.IsConditionFailed(err) {
		return ErrStateChanged
	}
	if err != nil {
		return fmt.Errorf("deletion: save %s: %w", req.ID, err)
	}
	return nil
}

// ListDue reads the KEYS_ONLY sparse index; callers re-read each request
// with Get (the index is eventually consistent).
func (r *dynamoRepository) ListDue(ctx context.Context, state State, now time.Time, limit int32) ([]string, error) {
	out, err := r.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(r.table),
		IndexName:              aws.String(DueIndex),
		KeyConditionExpression: aws.String("due_state = :s AND next_action_at <= :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": str(string(state)), ":now": str(ts(now)),
		},
		Limit: aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("deletion: list due %s: %w", state, err)
	}
	ids := make([]string, 0, len(out.Items))
	for _, it := range out.Items {
		if pk, ok := it["pk"].(*types.AttributeValueMemberS); ok {
			ids = append(ids, strings.TrimPrefix(pk.Value, "REQ#"))
		}
	}
	return ids, nil
}
