package dataexport

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/api-commons/dynamo"
)

// TableSuffix names the table: one CURRENT row per person.
const TableSuffix = "account_data_exports"

const currentSK = "CURRENT"

type item struct {
	PK          string `dynamodbav:"pk"`
	SK          string `dynamodbav:"sk"`
	ExportID    string `dynamodbav:"export_id"`
	UserID      string `dynamodbav:"user_id"`
	State       string `dynamodbav:"state"`
	RequestedAt string `dynamodbav:"requested_at"`
	ReadyAt     string `dynamodbav:"ready_at,omitempty"`
	ExpiresAt   string `dynamodbav:"expires_at,omitempty"`
	Parts       []part `dynamodbav:"parts,omitempty"`
	Error       string `dynamodbav:"error,omitempty"`
}

type part struct {
	Service string `dynamodbav:"service"`
	Key     string `dynamodbav:"key,omitempty"`
	Size    int64  `dynamodbav:"size"`
	Status  string `dynamodbav:"status"`
}

func pk(userID string) string { return "SUB#" + userID }

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

type dynamoRepository struct {
	db    *dynamodb.Client
	table string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{db: db, table: database.TableName(tablePrefix, TableSuffix)}
}

func key(userID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk(userID)},
		"sk": &types.AttributeValueMemberS{Value: currentSK},
	}
}

func (r *dynamoRepository) Get(ctx context.Context, userID string) (*Export, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(r.table), Key: key(userID), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("dataexport: get: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrNotFound
	}
	var it item
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return nil, fmt.Errorf("dataexport: decode: %w", err)
	}
	e := &Export{ID: it.ExportID, UserID: it.UserID, State: State(it.State), RequestedAt: parse(it.RequestedAt),
		ReadyAt: parse(it.ReadyAt), ExpiresAt: parse(it.ExpiresAt), Error: it.Error}
	for _, p := range it.Parts {
		e.Parts = append(e.Parts, Part(p))
	}
	return e, nil
}

func (r *dynamoRepository) Save(ctx context.Context, e *Export, prev string) error {
	it := item{PK: pk(e.UserID), SK: currentSK, ExportID: e.ID, UserID: e.UserID, State: string(e.State),
		RequestedAt: ts(e.RequestedAt), ReadyAt: ts(e.ReadyAt), ExpiresAt: ts(e.ExpiresAt), Error: e.Error}
	for _, p := range e.Parts {
		it.Parts = append(it.Parts, part(p))
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return err
	}
	in := &dynamodb.PutItemInput{TableName: aws.String(r.table), Item: av}
	if prev == "" {
		in.ConditionExpression = aws.String("attribute_not_exists(pk)")
	} else {
		in.ConditionExpression = aws.String("export_id = :prev")
		in.ExpressionAttributeValues = map[string]types.AttributeValue{":prev": &types.AttributeValueMemberS{Value: prev}}
	}
	if _, err := r.db.PutItem(ctx, in); err != nil {
		if dynamo.IsConditionFailed(err) {
			return ErrBuilding
		}
		return fmt.Errorf("dataexport: save: %w", err)
	}
	return nil
}

func (r *dynamoRepository) Delete(ctx context.Context, userID string) error {
	if _, err := r.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(r.table), Key: key(userID)}); err != nil {
		return fmt.Errorf("dataexport: delete: %w", err)
	}
	return nil
}
