package audit

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
)

const tableSuffix = "account_audit"

type Repository interface {
	Put(ctx context.Context, e *Event) error
	// QueryByUser returns events newest-first. cursor is an opaque token from a
	// previous call ("" for the first page). Returns next cursor ("" when done).
	QueryByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Event, string, error)
}

type dynamoRepository struct {
	db    *dynamodb.Client
	table string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{db: db, table: database.TableName(tablePrefix, tableSuffix)}
}

func (r *dynamoRepository) Put(ctx context.Context, e *Event) error {
	item, err := attributevalue.MarshalMap(e)
	if err != nil {
		return fmt.Errorf("marshaling audit event: %w", err)
	}
	_, err = r.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(r.table), Item: item})
	return err
}

// QueryByUser paginates with the raw client because ExclusiveStartKey isn't
// exposed by the shared Base helpers.
func (r *dynamoRepository) QueryByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Event, string, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(r.table),
		KeyConditionExpression: aws.String("pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: BuildPK(userID)},
		},
		ScanIndexForward: aws.Bool(false), // newest first
		Limit:            aws.Int32(limit),
	}
	if cursor != "" {
		sk, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decoding cursor: %w", err)
		}
		in.ExclusiveStartKey = map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: BuildPK(userID)},
			"sk": &types.AttributeValueMemberS{Value: string(sk)},
		}
	}

	out, err := r.db.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("querying audit events: %w", err)
	}

	events := make([]*Event, 0, len(out.Items))
	for _, item := range out.Items {
		var e Event
		if err := attributevalue.UnmarshalMap(item, &e); err != nil {
			return nil, "", fmt.Errorf("unmarshaling audit event: %w", err)
		}
		events = append(events, &e)
	}

	next := ""
	if lek := out.LastEvaluatedKey; lek != nil {
		if sk, ok := lek["sk"].(*types.AttributeValueMemberS); ok {
			next = base64.RawURLEncoding.EncodeToString([]byte(sk.Value))
		}
	}
	return events, next, nil
}
