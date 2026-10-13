package planlimit

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

// Eraser removes a deleted user's rows from the level queue so the worker
// never reports levels for an erased owner (account deletion, data inventory §3).
type Eraser struct {
	base database.Base
	name string
}

func NewEraser(db *dynamodb.Client, tablePrefix string) *Eraser {
	return &Eraser{base: database.NewBase(db, tablePrefix, queueTable), name: database.TableName(tablePrefix, queueTable)}
}

// EraseOwner deletes every queue row whose owner is userID. The queue is a
// short-lived work list, so reading its partition with a filter is cheap.
func (e *Eraser) EraseOwner(ctx context.Context, userID string) error {
	var start map[string]types.AttributeValue
	for {
		out, err := e.base.QueryRaw(ctx, &dynamodb.QueryInput{
			TableName:                aws.String(e.name),
			KeyConditionExpression:   aws.String("pk = :pk"),
			FilterExpression:         aws.String("#o = :owner"),
			ExpressionAttributeNames: map[string]string{"#o": attrOwner},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": s(dirtyPK), ":owner": s(userID),
			},
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return fmt.Errorf("reading level queue: %w", err)
		}
		for _, item := range out.Items {
			sk, _ := item["sk"].(*types.AttributeValueMemberS)
			if sk == nil {
				continue
			}
			if _, err := e.base.DeleteItem(ctx, dirtyPK, sk.Value); err != nil {
				return fmt.Errorf("deleting level queue row: %w", err)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return nil
		}
		start = out.LastEvaluatedKey
	}
}
