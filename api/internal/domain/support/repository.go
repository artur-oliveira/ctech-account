package support

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
)

var ErrNotFound = errors.New("ticket not found")

const (
	tableSuffix       = "account_support_tickets"
	counterPK         = "COUNTER"
	counterSK         = "TICKET_NUMBER"
	statusIndex       = "status-index"
	userIndex         = "user-index"
	anonTokenIndex    = "anon-token-index"
	ticketNumberIndex = "ticket-number-index"
)

// Repository is the data-access interface for support tickets and their
// message threads.
type Repository interface {
	NextTicketNumber(ctx context.Context) (int64, error)
	CreateTicket(ctx context.Context, t *Ticket) error
	GetTicket(ctx context.Context, id string) (*Ticket, error)
	GetTicketByAnonToken(ctx context.Context, token string) (*Ticket, error)
	GetTicketByNumber(ctx context.Context, number int64) (*Ticket, error)
	UpdateTicket(ctx context.Context, id string, updates map[string]any) error
	PutMessage(ctx context.Context, m *Message) error
	ListMessages(ctx context.Context, ticketID string) ([]*Message, error)
	ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error)
	ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error)
}

type dynamoRepository struct {
	table     database.Base
	db        *dynamodb.Client
	tableName string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{
		table:     database.NewBase(db, tablePrefix, tableSuffix),
		db:        db,
		tableName: database.TableName(tablePrefix, tableSuffix),
	}
}

// NextTicketNumber atomically increments the single counter item and returns
// the new value. ADD is not exposed by database.Base, so this uses the raw
// client (same reasoning as audit.dynamoRepository's cursor pagination).
func (r *dynamoRepository) NextTicketNumber(ctx context.Context) (int64, error) {
	out, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: counterPK},
			"sk": &types.AttributeValueMemberS{Value: counterSK},
		},
		UpdateExpression:          aws.String("ADD #v :one"),
		ExpressionAttributeNames:  map[string]string{"#v": "value"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}},
		ReturnValues:              types.ReturnValueUpdatedNew,
	})
	if err != nil {
		return 0, fmt.Errorf("incrementing ticket counter: %w", err)
	}
	var result struct {
		Value int64 `dynamodbav:"value"`
	}
	if err := attributevalue.UnmarshalMap(out.Attributes, &result); err != nil {
		return 0, fmt.Errorf("unmarshaling ticket counter: %w", err)
	}
	return result.Value, nil
}

func (r *dynamoRepository) CreateTicket(ctx context.Context, t *Ticket) error {
	t.SK = metaSK
	item, err := attributevalue.MarshalMap(t)
	if err != nil {
		return fmt.Errorf("marshaling ticket: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) GetTicket(ctx context.Context, id string) (*Ticket, error) {
	item, err := r.table.GetItem(ctx, BuildPK(id), metaSK)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}
	var t Ticket
	if err := attributevalue.UnmarshalMap(item, &t); err != nil {
		return nil, fmt.Errorf("unmarshaling ticket: %w", err)
	}
	return &t, nil
}

func (r *dynamoRepository) GetTicketByAnonToken(ctx context.Context, token string) (*Ticket, error) {
	res, err := r.table.QueryGSI(ctx, anonTokenIndex, "anonymous_token", token, 1, nil)
	if err != nil {
		return nil, fmt.Errorf("querying anon token index: %w", err)
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}
	var t Ticket
	if err := attributevalue.UnmarshalMap(res.Items[0], &t); err != nil {
		return nil, fmt.Errorf("unmarshaling ticket: %w", err)
	}
	return &t, nil
}

// GetTicketByNumber uses the raw client rather than the shared QueryGSI
// helper: ticket_number is a NUMBER-typed GSI key, but QueryGSI always
// builds a String equality condition — it would silently match nothing
// against a Number attribute.
func (r *dynamoRepository) GetTicketByNumber(ctx context.Context, number int64) (*Ticket, error) {
	out, err := r.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(r.tableName),
		IndexName:              aws.String(ticketNumberIndex),
		KeyConditionExpression: aws.String("#k = :v"),
		ExpressionAttributeNames: map[string]string{
			"#k": "ticket_number",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberN{Value: strconv.FormatInt(number, 10)},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("querying ticket number index: %w", err)
	}
	if len(out.Items) == 0 {
		return nil, ErrNotFound
	}
	var t Ticket
	if err := attributevalue.UnmarshalMap(out.Items[0], &t); err != nil {
		return nil, fmt.Errorf("unmarshaling ticket: %w", err)
	}
	return &t, nil
}

func (r *dynamoRepository) UpdateTicket(ctx context.Context, id string, updates map[string]any) error {
	sk := metaSK
	_, err := r.table.UpdateItem(ctx, BuildPK(id), &sk, updates)
	return err
}

// PutMessage builds both PK and SK from the ticket ID the caller passes in
// m.PK — callers never pre-build the TICKET_ prefix themselves.
func (r *dynamoRepository) PutMessage(ctx context.Context, m *Message) error {
	ticketID := m.PK
	m.PK = BuildPK(ticketID)
	m.SK = BuildMessageSK(m.CreatedAt)
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) ListMessages(ctx context.Context, ticketID string) ([]*Message, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(ticketID), SKPrefix: messageSKPrefix})
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	messages := make([]*Message, 0, len(res.Items))
	for _, item := range res.Items {
		var m Message
		if err := attributevalue.UnmarshalMap(item, &m); err != nil {
			return nil, fmt.Errorf("unmarshaling message: %w", err)
		}
		messages = append(messages, &m)
	}
	return messages, nil
}

func (r *dynamoRepository) ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error) {
	return r.queryIndexPage(ctx, userIndex, "user_id", &types.AttributeValueMemberS{Value: userID}, "created_at", cursor, limit)
}

func (r *dynamoRepository) ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error) {
	return r.queryIndexPage(ctx, statusIndex, "status", &types.AttributeValueMemberS{Value: status}, "last_message_at", cursor, limit)
}

// queryIndexPage runs a cursor-paginated, newest-first query against a
// {pk, sk} GSI using the raw client — ExclusiveStartKey/ScanIndexForward
// together aren't exposed by the shared QueryGSI helper (same pattern as
// audit.dynamoRepository.QueryByUser).
func (r *dynamoRepository) queryIndexPage(ctx context.Context, index, pkAttr string, pkVal types.AttributeValue, skAttr, cursor string, limit int32) ([]*Ticket, string, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(r.tableName),
		IndexName:              aws.String(index),
		KeyConditionExpression: aws.String("#pk = :pk"),
		ExpressionAttributeNames: map[string]string{
			"#pk": pkAttr,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": pkVal},
		ScanIndexForward:          aws.Bool(false),
		Limit:                     aws.Int32(limit),
	}
	if cursor != "" {
		sk, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		in.ExclusiveStartKey = map[string]types.AttributeValue{
			pkAttr: pkVal,
			skAttr: &types.AttributeValueMemberS{Value: sk},
		}
	}
	out, err := r.db.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("querying %s: %w", index, err)
	}
	tickets := make([]*Ticket, 0, len(out.Items))
	for _, item := range out.Items {
		var t Ticket
		if err := attributevalue.UnmarshalMap(item, &t); err != nil {
			return nil, "", fmt.Errorf("unmarshaling ticket: %w", err)
		}
		tickets = append(tickets, &t)
	}
	next := ""
	if lek := out.LastEvaluatedKey; lek != nil {
		if sk, ok := lek[skAttr].(*types.AttributeValueMemberS); ok {
			next = encodeCursor(sk.Value)
		}
	}
	return tickets, next, nil
}

func decodeCursor(cursor string) (string, error) {
	sk, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", fmt.Errorf("decoding cursor: %w", err)
	}
	return string(sk), nil
}

func encodeCursor(sk string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sk))
}
