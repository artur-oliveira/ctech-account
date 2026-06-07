package database

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Client struct {
	svc         *dynamodb.Client
	tablePrefix string
}

func New(ctx context.Context, region, tablePrefix string) (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Client{
		svc:         dynamodb.NewFromConfig(cfg),
		tablePrefix: tablePrefix,
	}, nil
}

func (c *Client) TableName(name string) string {
	return c.tablePrefix + name
}

func (c *Client) GetItem(ctx context.Context, table string, key map[string]types.AttributeValue) (map[string]types.AttributeValue, error) {
	out, err := c.svc.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.TableName(table)),
		Key:       key,
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb GetItem %s: %w", table, err)
	}
	if out.Item == nil {
		return nil, nil
	}
	return out.Item, nil
}

func (c *Client) PutItem(ctx context.Context, table string, item map[string]types.AttributeValue) error {
	_, err := c.svc.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.TableName(table)),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("dynamodb PutItem %s: %w", table, err)
	}
	return nil
}

func (c *Client) UpdateItem(ctx context.Context, table string, key map[string]types.AttributeValue, updates map[string]types.AttributeValue) error {
	expr := "SET "
	names := map[string]string{}
	values := map[string]types.AttributeValue{}
	i := 0
	for k, v := range updates {
		alias := fmt.Sprintf("#f%d", i)
		valAlias := fmt.Sprintf(":v%d", i)
		if i > 0 {
			expr += ", "
		}
		expr += alias + " = " + valAlias
		names[alias] = k
		values[valAlias] = v
		i++
	}

	_, err := c.svc.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(c.TableName(table)),
		Key:                       key,
		UpdateExpression:          aws.String(expr),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if err != nil {
		return fmt.Errorf("dynamodb UpdateItem %s: %w", table, err)
	}
	return nil
}

func (c *Client) DeleteItem(ctx context.Context, table string, key map[string]types.AttributeValue) error {
	_, err := c.svc.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.TableName(table)),
		Key:       key,
	})
	if err != nil {
		return fmt.Errorf("dynamodb DeleteItem %s: %w", table, err)
	}
	return nil
}

func (c *Client) QueryGSI(ctx context.Context, table, indexName, keyName, keyValue string, limit int32) ([]map[string]types.AttributeValue, error) {
	var lim *int32
	if limit > 0 {
		lim = &limit
	}
	out, err := c.svc.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.TableName(table)),
		IndexName:              aws.String(indexName),
		KeyConditionExpression: aws.String("#k = :v"),
		ExpressionAttributeNames: map[string]string{
			"#k": keyName,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberS{Value: keyValue},
		},
		Limit: lim,
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb QueryGSI %s/%s: %w", table, indexName, err)
	}
	return out.Items, nil
}

func (c *Client) Query(ctx context.Context, table, pk, skPrefix string, limit int32) ([]map[string]types.AttributeValue, error) {
	var lim *int32
	if limit > 0 {
		lim = &limit
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(c.TableName(table)),
		KeyConditionExpression: aws.String("#pk = :pk AND begins_with(#sk, :sk)"),
		ExpressionAttributeNames: map[string]string{
			"#pk": "pk",
			"#sk": "sk",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: pk},
			":sk": &types.AttributeValueMemberS{Value: skPrefix},
		},
		Limit: lim,
	}

	out, err := c.svc.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("dynamodb Query %s: %w", table, err)
	}
	return out.Items, nil
}

func (c *Client) TransactWrite(ctx context.Context, items []types.TransactWriteItem) error {
	_, err := c.svc.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	})
	if err != nil {
		return fmt.Errorf("dynamodb TransactWrite: %w", err)
	}
	return nil
}

func (c *Client) RawClient() *dynamodb.Client {
	return c.svc
}

// Ping verifies connectivity by calling ListTables with a limit of 1.
func (c *Client) Ping(ctx context.Context) error {
	limit := int32(1)
	_, err := c.svc.ListTables(ctx, &dynamodb.ListTablesInput{Limit: &limit})
	if err != nil {
		return fmt.Errorf("dynamodb ping: %w", err)
	}
	return nil
}
