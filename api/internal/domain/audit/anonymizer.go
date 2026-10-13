package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const deletionEventPrefix = "account.deletion."

type eventStore interface {
	QueryByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Event, string, error)
	Anonymize(ctx context.Context, pk, sk string, dropMetadata bool) error
}

// Anonymizer strips a deleted user's audit rows of IP, user agent and metadata
// (inventory §3). Deletion events keep their metadata (request_id) as proof
// the request was honoured.
type Anonymizer struct{ store eventStore }

func NewAnonymizer(db *dynamodb.Client, tablePrefix string) *Anonymizer {
	return &Anonymizer{store: NewRepository(db, tablePrefix).(*dynamoRepository)}
}

func newAnonymizerWith(s eventStore) *Anonymizer { return &Anonymizer{store: s} }

func (a *Anonymizer) AnonymizeUser(ctx context.Context, userID string) error {
	cursor := ""
	for {
		events, next, err := a.store.QueryByUser(ctx, userID, cursor, 100)
		if err != nil {
			return fmt.Errorf("listing audit events: %w", err)
		}
		for _, e := range events {
			keepMeta := strings.HasPrefix(e.EventType, deletionEventPrefix)
			if e.IP == "" && e.UserAgent == "" && (keepMeta || len(e.Metadata) == 0) {
				continue // already clean
			}
			if err := a.store.Anonymize(ctx, e.PK, e.SK, !keepMeta); err != nil {
				return fmt.Errorf("anonymizing audit event: %w", err)
			}
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

func (r *dynamoRepository) Anonymize(ctx context.Context, pk, sk string, dropMetadata bool) error {
	expr := "REMOVE ip, user_agent"
	if dropMetadata {
		expr += ", metadata"
	}
	_, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression:    aws.String(expr),
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	return err
}
