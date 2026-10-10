package planlimit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

// The queue lives in account_organizations, beside the counters (decision P1).
const (
	queueTable  = "account_organizations"
	dirtyPK     = "LEVEL_DIRTY"
	nowSKPrefix = "NOW#"
	atSKPrefix  = "AT#"
	attrOwner   = "owner_user_id"
	attrDueAt   = "due_at"
)

func nowSK(owner string) string { return nowSKPrefix + owner }

func atSK(at time.Time, owner string) string {
	return fmt.Sprintf("%s%012d#%s", atSKPrefix, at.Unix(), owner)
}

// atUpperBound sorts after every AT# key due at or before now and before any
// due later: keys always continue with "#owner" after the twelve digits.
func atUpperBound(now time.Time) string { return fmt.Sprintf("%s%012d", atSKPrefix, now.Unix()+1) }

type DirtyRow struct {
	SK          string
	OwnerUserID string
	DueAt       time.Time
	Scheduled   bool
}

type Queue interface {
	MarkNow(ctx context.Context, owner string, at time.Time) error
	Schedule(ctx context.Context, owner string, at time.Time) error
	Due(ctx context.Context, now time.Time, limit int) ([]DirtyRow, error)
	// Done deletes a delivered row. A NOW row only if due_at is still the one
	// delivered — false, nil when a newer change replaced it.
	Done(ctx context.Context, row DirtyRow) (bool, error)
}

type dynamoQueue struct {
	base database.Base
	name string
}

func NewQueue(db *dynamodb.Client, tablePrefix string) Queue {
	return &dynamoQueue{
		base: database.NewBase(db, tablePrefix, queueTable),
		name: database.TableName(tablePrefix, queueTable),
	}
}

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func n(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}

func (q *dynamoQueue) put(ctx context.Context, sk, owner string, due time.Time) error {
	return q.base.PutItem(ctx, map[string]types.AttributeValue{
		"pk": s(dirtyPK), "sk": s(sk), attrOwner: s(owner), attrDueAt: n(due.UnixMilli()),
	})
}

func (q *dynamoQueue) MarkNow(ctx context.Context, owner string, at time.Time) error {
	return q.put(ctx, nowSK(owner), owner, at)
}

func (q *dynamoQueue) Schedule(ctx context.Context, owner string, at time.Time) error {
	return q.put(ctx, atSK(at, owner), owner, at)
}

func (q *dynamoQueue) query(ctx context.Context, cond string, values map[string]types.AttributeValue, limit int) ([]DirtyRow, error) {
	values[":pk"] = s(dirtyPK)
	out, err := q.base.QueryRaw(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(q.name),
		KeyConditionExpression:    aws.String("pk = :pk AND " + cond),
		ExpressionAttributeValues: values,
		ConsistentRead:            aws.Bool(true),
		Limit:                     aws.Int32(int32(limit)),
	})
	if err != nil {
		return nil, fmt.Errorf("reading level queue: %w", err)
	}
	rows := make([]DirtyRow, 0, len(out.Items))
	for _, item := range out.Items {
		sk, _ := item["sk"].(*types.AttributeValueMemberS)
		owner, _ := item[attrOwner].(*types.AttributeValueMemberS)
		due, _ := item[attrDueAt].(*types.AttributeValueMemberN)
		if sk == nil || owner == nil || due == nil {
			continue
		}
		ms, _ := strconv.ParseInt(due.Value, 10, 64)
		rows = append(rows, DirtyRow{
			SK: sk.Value, OwnerUserID: owner.Value, DueAt: time.UnixMilli(ms).UTC(),
			Scheduled: strings.HasPrefix(sk.Value, atSKPrefix),
		})
	}
	return rows, nil
}

func (q *dynamoQueue) Due(ctx context.Context, now time.Time, limit int) ([]DirtyRow, error) {
	rows, err := q.query(ctx, "begins_with(sk, :p)", map[string]types.AttributeValue{":p": s(nowSKPrefix)}, limit)
	if err != nil || len(rows) >= limit {
		return rows, err
	}
	scheduled, err := q.query(ctx, "sk BETWEEN :lo AND :hi",
		map[string]types.AttributeValue{":lo": s(atSKPrefix), ":hi": s(atUpperBound(now))}, limit-len(rows))
	if err != nil {
		return nil, err
	}
	return append(rows, scheduled...), nil
}

func (q *dynamoQueue) Done(ctx context.Context, row DirtyRow) (bool, error) {
	input := &dynamodb.DeleteItemInput{
		TableName: aws.String(q.name),
		Key:       map[string]types.AttributeValue{"pk": s(dirtyPK), "sk": s(row.SK)},
	}
	if !row.Scheduled {
		input.ConditionExpression = aws.String("#d = :due")
		input.ExpressionAttributeNames = map[string]string{"#d": attrDueAt}
		input.ExpressionAttributeValues = map[string]types.AttributeValue{":due": n(row.DueAt.UnixMilli())}
	}
	_, err := q.base.DeleteItemRaw(ctx, input)
	if database.IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("clearing level queue row: %w", err)
	}
	return true, nil
}
