package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// buildUpdateExpr mirrors api-commons' dynamo.buildUpdateExpr: nil values become
// REMOVE clauses (clearing the attribute without writing a NULL); non-nil values
// become SET clauses. It is replicated here because api-commons does not expose a
// conditional UpdateItem, which the concurrency fixes (refresh-token rotation,
// email-uniqueness, TOTP/backup-code single-use) require.
func buildUpdateExpr(updates map[string]any) (string, map[string]string, map[string]types.AttributeValue, error) {
	setParts := make([]string, 0, len(updates))
	removeParts := make([]string, 0)
	exprNames := make(map[string]string, len(updates))
	exprValues := make(map[string]types.AttributeValue)

	for attr, val := range updates {
		exprNames["#"+attr] = attr
		if val == nil {
			removeParts = append(removeParts, "#"+attr)
			continue
		}
		av, err := attributevalue.Marshal(val)
		if err != nil {
			return "", nil, nil, err
		}
		setParts = append(setParts, fmt.Sprintf("#%s = :%s", attr, attr))
		exprValues[":"+attr] = av
	}

	clauses := make([]string, 0, 2)
	if len(setParts) > 0 {
		clauses = append(clauses, "SET "+strings.Join(setParts, ", "))
	}
	if len(removeParts) > 0 {
		clauses = append(clauses, "REMOVE "+strings.Join(removeParts, ", "))
	}
	return strings.Join(clauses, " "), exprNames, exprValues, nil
}

// ConditionalUpdate applies a SET/REMOVE expression under a caller-supplied
// ConditionExpression. This is the building block for every distributed
// read-modify-write race fix: the condition pins the value we just read so a
// concurrent writer fails the check instead of clobbering our update.
//
//	(true, nil)   — the update applied
//	(false, nil)  — the condition was not met (ConditionalCheckFailed)
//	(false, err)  — transport/serialization error
//
// condNames/condValues are merged into the expression attribute maps produced
// from updates; reference the same #name tokens buildUpdateExpr emits (e.g.
// "#refresh_token_hash") to avoid collisions.
func ConditionalUpdate(
	ctx context.Context,
	db *dynamodb.Client,
	tableName, pk string,
	sk *string,
	updates map[string]any,
	condition string,
	condNames map[string]string,
	condValues map[string]types.AttributeValue,
) (bool, error) {
	key := map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk},
	}
	if sk != nil {
		key["sk"] = &types.AttributeValueMemberS{Value: *sk}
	}

	expr, exprNames, exprValues, err := buildUpdateExpr(updates)
	if err != nil {
		return false, err
	}
	for k, v := range condNames {
		exprNames[k] = v
	}
	for k, v := range condValues {
		exprValues[k] = v
	}

	input := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(tableName),
		Key:                       key,
		UpdateExpression:          aws.String(expr),
		ConditionExpression:       aws.String(condition),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprValues,
	}
	_, err = db.UpdateItem(ctx, input)
	if err != nil {
		if IsConditionFailed(err) {
			return false, nil
		}
		return false, fmt.Errorf("conditional update: %w", err)
	}
	return true, nil
}
