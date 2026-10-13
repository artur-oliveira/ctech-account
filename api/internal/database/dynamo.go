// Package database provides the DynamoDB primitives used by every
// repository: a raw client constructor plus thin aliases onto the shared
// gopkg.aoctech.app/api-commons/dynamo package's per-table Base.
package database

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/awsconfig"
	"gopkg.aoctech.app/api-commons/dynamo"
)

// Base provides common DynamoDB operations for one table.
type Base = dynamo.Base

// QueryResult holds paginated query results.
type QueryResult = dynamo.QueryResult

// QueryOpts configures a Query call.
type QueryOpts = dynamo.QueryOpts

// NowStr returns the current UTC time as ISO 8601.
var NowStr = dynamo.NowStr

// IsConditionFailed reports whether err represents a DynamoDB conditional
// check failure, either from a single-item call or from within a
// TransactWrite.
var IsConditionFailed = dynamo.IsConditionFailed

// IsTransactionConflict reports a TransactWrite cancelled because another
// transaction touched one of its items. Nothing was written; retry it. Not a
// condition failure (api-commons narrowed IsConditionFailed in v1.11).
var IsTransactionConflict = dynamo.IsTransactionConflict

// New builds the raw DynamoDB client from the ambient AWS config (task role
// in ECS/EC2).
func New(ctx context.Context, region string) (*dynamodb.Client, error) {
	cfg, err := awsconfig.Load(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}
	return dynamodb.NewFromConfig(cfg), nil
}

// TableName returns the environment-prefixed physical table name
// ({prefix}_{table}). Exported for call sites outside the repository layer
// that need the physical name without a repository (e.g. a health probe).
func TableName(tablePrefix, table string) string {
	return dynamo.TableName(tablePrefix, table)
}

// NewBase creates a Base repository with an environment-prefixed table name.
func NewBase(db *dynamodb.Client, tablePrefix, table string) Base {
	return dynamo.NewBase(db, tablePrefix, table)
}

// ItemDeleter is the slice of *Base that DeleteAllUnder needs.
type ItemDeleter interface {
	Query(ctx context.Context, opts QueryOpts) (*QueryResult, error)
	DeleteItem(ctx context.Context, pk string, sk ...string) (bool, error)
}

// DeleteAllUnder deletes every item under pk whose sort key starts with
// skPrefix ("" for all), page by page until none is left. Idempotent: running
// it again on an emptied partition is a single empty query.
func DeleteAllUnder(ctx context.Context, b ItemDeleter, pk, skPrefix string) error {
	for {
		res, err := b.Query(ctx, QueryOpts{PK: pk, SKPrefix: skPrefix, Limit: 100})
		if err != nil {
			return fmt.Errorf("listing %s: %w", pk, err)
		}
		if len(res.Items) == 0 {
			return nil
		}
		for _, item := range res.Items {
			sk, _ := item["sk"].(*types.AttributeValueMemberS)
			if sk == nil {
				continue
			}
			if _, err := b.DeleteItem(ctx, pk, sk.Value); err != nil {
				return fmt.Errorf("deleting %s/%s: %w", pk, sk.Value, err)
			}
		}
	}
}
