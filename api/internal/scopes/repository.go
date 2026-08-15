package scopes

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
)

// scopesTable is the platform-wide scope registry. It is deliberately named
// {env}_ctech_scopes (not {env}_account_*): every service's scopes live here.
const scopesTable = "ctech_scopes"

// catalogPK is the single partition holding all service items, so the whole
// catalog loads with one Query (never a Scan). SK = service code.
const catalogPK = "SERVICE"

// Repository is the data-access interface for the scope catalog.
type Repository interface {
	LoadCatalog(ctx context.Context) ([]ServiceScopes, error)
	PutService(ctx context.Context, svc ServiceScopes) error
}

type dynamoRepository struct {
	table     database.Base
	db        *dynamodb.Client
	tableName string
}

// NewRepository returns a DynamoDB-backed catalog Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) *dynamoRepository {
	return &dynamoRepository{
		table: database.NewBase(db, tablePrefix, scopesTable), db: db,
		tableName: database.TableName(tablePrefix, scopesTable),
	}
}

func (r *dynamoRepository) LoadCatalog(ctx context.Context) ([]ServiceScopes, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: catalogPK})
	if err != nil {
		return nil, fmt.Errorf("querying scope catalog: %w", err)
	}
	services := make([]ServiceScopes, 0, len(res.Items))
	for _, item := range res.Items {
		var svc ServiceScopes
		if err := attributevalue.UnmarshalMap(item, &svc); err != nil {
			return nil, fmt.Errorf("unmarshaling scope service: %w", err)
		}
		services = append(services, svc)
	}
	return services, nil
}

func (r *dynamoRepository) PutService(ctx context.Context, svc ServiceScopes) error {
	item, err := attributevalue.MarshalMap(svc)
	if err != nil {
		return fmt.Errorf("marshaling scope service: %w", err)
	}
	pk, err := attributevalue.Marshal(catalogPK)
	if err != nil {
		return fmt.Errorf("marshaling pk: %w", err)
	}
	item["pk"] = pk
	return r.table.PutItem(ctx, item)
}

// LoadResources returns all v2 resource-server registrations. Keeping them in
// one partition preserves the existing single-query catalog load.
func (r *dynamoRepository) LoadResources(ctx context.Context) ([]ResourceServer, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: ResourceCatalogPK})
	if err != nil {
		return nil, fmt.Errorf("querying resource servers: %w", err)
	}
	resources := make([]ResourceServer, 0, len(res.Items))
	for _, item := range res.Items {
		var resource ResourceServer
		if err := attributevalue.UnmarshalMap(item, &resource); err != nil {
			return nil, fmt.Errorf("unmarshaling resource server: %w", err)
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (r *dynamoRepository) GetResource(ctx context.Context, id string) (*ResourceServer, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: ResourceCatalogPK},
			"sk": &types.AttributeValueMemberS{Value: id},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("getting resource server: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrResourceNotFound
	}
	var resource ResourceServer
	if err := attributevalue.UnmarshalMap(out.Item, &resource); err != nil {
		return nil, fmt.Errorf("unmarshaling resource server: %w", err)
	}
	return &resource, nil
}

func (r *dynamoRepository) CreateResource(ctx context.Context, resource *ResourceServer) error {
	resource.PK = ResourceCatalogPK
	item, err := attributevalue.MarshalMap(resource)
	if err != nil {
		return fmt.Errorf("marshaling resource server: %w", err)
	}
	_, err = r.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName), Item: item,
		ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)"),
	})
	if err != nil {
		if database.IsConditionFailed(err) {
			return ErrResourceAlreadyExists
		}
		return fmt.Errorf("creating resource server: %w", err)
	}
	return nil
}

// ReconcileResource atomically replaces the current manifest under an
// optimistic revision condition and appends an immutable history snapshot.
func (r *dynamoRepository) ReconcileResource(ctx context.Context, previous *ResourceServer, next *ResourceServer) error {
	currentItem, err := attributevalue.MarshalMap(next)
	if err != nil {
		return fmt.Errorf("marshaling resource server: %w", err)
	}
	history := ResourceRevision{
		PK: historyPK(next.ID()), SK: historySK(next.Revision),
		ResourceServerID: next.ID(), Revision: next.Revision,
		PreviousHash: previous.ManifestHash, ManifestHash: next.ManifestHash,
		DisplayName: next.DisplayName, Scopes: next.Scopes, UpdatedAt: next.UpdatedAt,
		UpdatedBy: next.UpdatedBy, SourceRepository: next.SourceRepository,
		SourceRevision: next.SourceRevision,
	}
	historyItem, err := attributevalue.MarshalMap(history)
	if err != nil {
		return fmt.Errorf("marshaling resource revision: %w", err)
	}
	expected, err := attributevalue.Marshal(previous.Revision)
	if err != nil {
		return fmt.Errorf("marshaling expected revision: %w", err)
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{
			TableName: aws.String(r.tableName), Item: currentItem,
			ConditionExpression:       aws.String("revision = :expected"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":expected": expected},
		}},
		{Put: &types.Put{
			TableName: aws.String(r.tableName), Item: historyItem,
			ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)"),
		}},
	}})
	if err != nil {
		if database.IsConditionFailed(err) {
			return ErrRevisionConflict
		}
		return fmt.Errorf("reconciling resource server: %w", err)
	}
	return nil
}

func (r *dynamoRepository) GetRevision(ctx context.Context, id string, revision int64) (*ResourceRevision, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: historyPK(id)},
			"sk": &types.AttributeValueMemberS{Value: historySK(revision)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting resource revision: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrResourceNotFound
	}
	var result ResourceRevision
	if err := attributevalue.UnmarshalMap(out.Item, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling resource revision: %w", err)
	}
	return &result, nil
}
