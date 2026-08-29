package support

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
)

var ErrNotFound = errors.New("ticket not found")

const (
	tableSuffix        = "account_support_tickets"
	metricsTableSuffix = "account_support_metrics"
	counterPK          = "COUNTER"
	counterSK          = "TICKET_NUMBER"
	statusIndex        = "status-index"
	userIndex          = "user-index"
	anonTokenIndex     = "anon-token-index"
	ticketNumberIndex  = "ticket-number-index"
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
	UpdateActiveStatus(ctx context.Context, id, status, updatedAt string) error
	MarkAnswered(ctx context.Context, id, updatedAt string) error
	UpdateEscalation(ctx context.Context, id, level, agentUserID, updatedAt string) error
	PutMessage(ctx context.Context, m *Message, requireOpen bool) error
	ListMessages(ctx context.Context, ticketID string) ([]*Message, error)
	PutInternalNote(ctx context.Context, note *InternalNote) error
	ListInternalNotes(ctx context.Context, ticketID string) ([]*InternalNote, error)
	ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Ticket, string, error)
	ListByStatus(ctx context.Context, status, cursor string, limit int32) ([]*Ticket, string, error)
	CloseTicket(ctx context.Context, id string, closedAt time.Time, resolutionSeconds int64) error
	GetMetrics(ctx context.Context, now time.Time) ([]MetricBucket, error)
}

type dynamoRepository struct {
	table            database.Base
	db               *dynamodb.Client
	tableName        string
	metricsTableName string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{
		table:            database.NewBase(db, tablePrefix, tableSuffix),
		db:               db,
		tableName:        database.TableName(tablePrefix, tableSuffix),
		metricsTableName: database.TableName(tablePrefix, metricsTableSuffix),
	}
}

func (r *dynamoRepository) PutInternalNote(ctx context.Context, note *InternalNote) error {
	ticketID := note.PK
	note.PK = BuildPK(ticketID)
	note.SK = BuildNoteSK(note.CreatedAt)
	item, err := attributevalue.MarshalMap(note)
	if err != nil {
		return fmt.Errorf("marshaling internal note: %w", err)
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{ConditionCheck: &types.ConditionCheck{TableName: aws.String(r.tableName), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: BuildPK(ticketID)}, "sk": &types.AttributeValueMemberS{Value: metaSK}}, ConditionExpression: aws.String("#status <> :closed"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":closed": &types.AttributeValueMemberS{Value: StatusClosed}}}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: item}},
	}})
	return err
}

func (r *dynamoRepository) ListInternalNotes(ctx context.Context, ticketID string) ([]*InternalNote, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(ticketID), SKPrefix: noteSKPrefix})
	if err != nil {
		return nil, fmt.Errorf("querying internal notes: %w", err)
	}
	notes := make([]*InternalNote, 0, len(res.Items))
	for _, item := range res.Items {
		var note InternalNote
		if err := attributevalue.UnmarshalMap(item, &note); err != nil {
			return nil, fmt.Errorf("unmarshaling internal note: %w", err)
		}
		notes = append(notes, &note)
	}
	return notes, nil
}

func metricPeriods(now time.Time) []string {
	return []string{"day#" + now.Format("2006-01-02"), "month#" + now.Format("2006-01"), "year#" + now.Format("2006"), "all"}
}

func (r *dynamoRepository) CloseTicket(ctx context.Context, id string, closedAt time.Time, resolutionSeconds int64) error {
	if resolutionSeconds < 0 {
		resolutionSeconds = 0
	}
	items := make([]types.TransactWriteItem, 0, 5)
	items = append(items, types.TransactWriteItem{Update: &types.Update{
		TableName:                aws.String(r.tableName),
		Key:                      map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: BuildPK(id)}, "sk": &types.AttributeValueMemberS{Value: metaSK}},
		UpdateExpression:         aws.String("SET #status = :closed, updated_at = :updated, closed_at = :updated"),
		ConditionExpression:      aws.String("attribute_exists(pk) AND #status <> :closed"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":closed":  &types.AttributeValueMemberS{Value: StatusClosed},
			":updated": &types.AttributeValueMemberS{Value: closedAt.UTC().Format(time.RFC3339)},
		},
	}})
	for _, period := range metricPeriods(closedAt.UTC()) {
		items = append(items, types.TransactWriteItem{Update: &types.Update{
			TableName:        aws.String(r.metricsTableName),
			Key:              map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: period}},
			UpdateExpression: aws.String("ADD resolved_count :one, resolution_seconds_total :seconds SET updated_at = :updated"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":one":     &types.AttributeValueMemberN{Value: "1"},
				":seconds": &types.AttributeValueMemberN{Value: strconv.FormatInt(resolutionSeconds, 10)},
				":updated": &types.AttributeValueMemberS{Value: closedAt.UTC().Format(time.RFC3339)},
			},
		}})
	}
	_, err := r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	return err
}

func (r *dynamoRepository) GetMetrics(ctx context.Context, now time.Time) ([]MetricBucket, error) {
	periods := metricPeriods(now.UTC())
	keys := make([]map[string]types.AttributeValue, 0, len(periods))
	for _, period := range periods {
		keys = append(keys, map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: period}})
	}
	out, err := r.db.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: map[string]types.KeysAndAttributes{r.metricsTableName: {Keys: keys}}})
	if err != nil {
		return nil, fmt.Errorf("reading support metrics: %w", err)
	}
	type metricItem struct {
		PK                     string `dynamodbav:"pk"`
		CreatedCount           int64  `dynamodbav:"created_count"`
		ResolvedCount          int64  `dynamodbav:"resolved_count"`
		ResolutionSecondsTotal int64  `dynamodbav:"resolution_seconds_total"`
		ProductAccount         int64  `dynamodbav:"product_account"`
		ProductKYC             int64  `dynamodbav:"product_kyc"`
		ProductWallet          int64  `dynamodbav:"product_wallet"`
		ProductDFe             int64  `dynamodbav:"product_dfe"`
		ProductBilling         int64  `dynamodbav:"product_billing"`
		ProductPoker           int64  `dynamodbav:"product_poker"`
		ProductOther           int64  `dynamodbav:"product_other"`
	}
	byPeriod := make(map[string]metricItem)
	for _, item := range out.Responses[r.metricsTableName] {
		var value metricItem
		if err := attributevalue.UnmarshalMap(item, &value); err != nil {
			return nil, fmt.Errorf("unmarshaling support metric: %w", err)
		}
		byPeriod[value.PK] = value
	}
	result := make([]MetricBucket, 0, len(periods))
	for _, period := range periods {
		value := byPeriod[period]
		average := float64(0)
		if value.ResolvedCount > 0 {
			average = float64(value.ResolutionSecondsTotal) / float64(value.ResolvedCount)
		}
		result = append(result, MetricBucket{Period: period, CreatedCount: value.CreatedCount, ResolvedCount: value.ResolvedCount, AverageResolutionSecs: average, TicketsByProduct: map[string]int64{
			CategoryAccount: value.ProductAccount, CategoryKYC: value.ProductKYC, CategoryWallet: value.ProductWallet,
			CategoryDFe: value.ProductDFe, CategoryBilling: value.ProductBilling, CategoryPoker: value.ProductPoker, CategoryOther: value.ProductOther,
		}})
	}
	return result, nil
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
	createdAt, err := time.Parse(time.RFC3339, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("parsing ticket created_at: %w", err)
	}
	items := []types.TransactWriteItem{{Put: &types.Put{
		TableName:           aws.String(r.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	}}}
	for _, period := range metricPeriods(createdAt.UTC()) {
		items = append(items, types.TransactWriteItem{Update: &types.Update{
			TableName:                aws.String(r.metricsTableName),
			Key:                      map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: period}},
			UpdateExpression:         aws.String("ADD created_count :one, #product :one SET updated_at = :updated"),
			ExpressionAttributeNames: map[string]string{"#product": "product_" + t.SubjectCategory},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":one":     &types.AttributeValueMemberN{Value: "1"},
				":updated": &types.AttributeValueMemberS{Value: createdAt.UTC().Format(time.RFC3339)},
			},
		}})
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	return err
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

func (r *dynamoRepository) UpdateActiveStatus(ctx context.Context, id, status, updatedAt string) error {
	_, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: BuildPK(id)},
			"sk": &types.AttributeValueMemberS{Value: metaSK},
		},
		UpdateExpression:         aws.String("SET #status = :status, updated_at = :updated"),
		ConditionExpression:      aws.String("attribute_exists(pk) AND #status <> :closed"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":  &types.AttributeValueMemberS{Value: status},
			":closed":  &types.AttributeValueMemberS{Value: StatusClosed},
			":updated": &types.AttributeValueMemberS{Value: updatedAt},
		},
	})
	return err
}

func (r *dynamoRepository) MarkAnswered(ctx context.Context, id, updatedAt string) error {
	_, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(r.tableName), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: BuildPK(id)}, "sk": &types.AttributeValueMemberS{Value: metaSK}}, UpdateExpression: aws.String("SET #status = :answered, updated_at = :updated"), ConditionExpression: aws.String("#status <> :closed"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":answered": &types.AttributeValueMemberS{Value: StatusAnswered}, ":closed": &types.AttributeValueMemberS{Value: StatusClosed}, ":updated": &types.AttributeValueMemberS{Value: updatedAt}}})
	return err
}

func (r *dynamoRepository) UpdateEscalation(ctx context.Context, id, level, agentUserID, updatedAt string) error {
	updateExpression := "SET escalation_level = :level, escalated_by = :agent, escalated_at = :updated, updated_at = :updated"
	if level == EscalationNone {
		updateExpression = "SET escalation_level = :level, escalated_by = :agent, updated_at = :updated REMOVE escalated_at"
	}
	_, err := r.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(r.tableName), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: BuildPK(id)}, "sk": &types.AttributeValueMemberS{Value: metaSK}}, UpdateExpression: aws.String(updateExpression), ConditionExpression: aws.String("#status <> :closed"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":level": &types.AttributeValueMemberS{Value: level}, ":agent": &types.AttributeValueMemberS{Value: agentUserID}, ":updated": &types.AttributeValueMemberS{Value: updatedAt}, ":closed": &types.AttributeValueMemberS{Value: StatusClosed}}})
	return err
}

// PutMessage builds both PK and SK from the ticket ID the caller passes in
// m.PK — callers never pre-build the TICKET_ prefix themselves.
func (r *dynamoRepository) PutMessage(ctx context.Context, m *Message, requireOpen bool) error {
	ticketID := m.PK
	m.PK = BuildPK(ticketID)
	m.SK = BuildMessageSK(m.CreatedAt)
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	if !requireOpen {
		return r.table.PutItem(ctx, item)
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{ConditionCheck: &types.ConditionCheck{
			TableName:                 aws.String(r.tableName),
			Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: BuildPK(ticketID)}, "sk": &types.AttributeValueMemberS{Value: metaSK}},
			ConditionExpression:       aws.String("attribute_exists(pk) AND #status <> :closed"),
			ExpressionAttributeNames:  map[string]string{"#status": "status"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":closed": &types.AttributeValueMemberS{Value: StatusClosed}},
		}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: item}},
	}})
	return err
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
