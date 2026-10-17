package dataexport

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/api-commons/dynamo"
)

// TableSuffix names the table: one CURRENT row per person.
const TableSuffix = "account_data_exports"

const currentSK = "CURRENT"

// item stores parts as a map keyed by service so one part can be updated (an
// ack landing while the account part builds) without rewriting the row.
type item struct {
	PK          string          `dynamodbav:"pk"`
	SK          string          `dynamodbav:"sk"`
	ExportID    string          `dynamodbav:"export_id"`
	UserID      string          `dynamodbav:"user_id"`
	State       string          `dynamodbav:"state"`
	RequestedAt string          `dynamodbav:"requested_at"`
	ReadyAt     string          `dynamodbav:"ready_at,omitempty"`
	ExpiresAt   string          `dynamodbav:"expires_at,omitempty"`
	Parts       map[string]part `dynamodbav:"parts,omitempty"`
	Error       string          `dynamodbav:"error,omitempty"`
	Notified    bool            `dynamodbav:"notified,omitempty"`
}

type part struct {
	Key          string `dynamodbav:"key,omitempty"`
	Size         int64  `dynamodbav:"size"`
	Status       string `dynamodbav:"status"`
	DispatchedAt string `dynamodbav:"dispatched_at,omitempty"`
	Attempts     int    `dynamodbav:"attempts,omitempty"`
}

func pk(userID string) string { return "SUB#" + userID }

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func toPart(p Part) part {
	return part{Key: p.Key, Size: p.Size, Status: p.Status, DispatchedAt: ts(p.DispatchedAt), Attempts: p.Attempts}
}

type dynamoRepository struct {
	db    *dynamodb.Client
	table string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{db: db, table: database.TableName(tablePrefix, TableSuffix)}
}

func key(userID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk(userID)},
		"sk": &types.AttributeValueMemberS{Value: currentSK},
	}
}

func sv(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func (r *dynamoRepository) Get(ctx context.Context, userID string) (*Export, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(r.table), Key: key(userID), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("dataexport: get: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrNotFound
	}
	var it item
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return nil, fmt.Errorf("dataexport: decode: %w", err)
	}
	e := &Export{ID: it.ExportID, UserID: it.UserID, State: State(it.State), RequestedAt: parse(it.RequestedAt),
		ReadyAt: parse(it.ReadyAt), ExpiresAt: parse(it.ExpiresAt), Error: it.Error, Notified: it.Notified}
	for svc, p := range it.Parts {
		e.Parts = append(e.Parts, Part{Service: svc, Key: p.Key, Size: p.Size, Status: p.Status,
			DispatchedAt: parse(p.DispatchedAt), Attempts: p.Attempts})
	}
	// The account part first, then the rest by name: a stable order for the UI.
	sort.Slice(e.Parts, func(i, j int) bool {
		a, b := e.Parts[i].Service, e.Parts[j].Service
		if (a == AccountService) != (b == AccountService) {
			return a == AccountService
		}
		return a < b
	})
	return e, nil
}

func (r *dynamoRepository) Save(ctx context.Context, e *Export, prev string) error {
	it := item{PK: pk(e.UserID), SK: currentSK, ExportID: e.ID, UserID: e.UserID, State: string(e.State),
		RequestedAt: ts(e.RequestedAt), ReadyAt: ts(e.ReadyAt), ExpiresAt: ts(e.ExpiresAt), Error: e.Error, Notified: e.Notified}
	if len(e.Parts) > 0 {
		it.Parts = map[string]part{}
		for _, p := range e.Parts {
			it.Parts[p.Service] = toPart(p)
		}
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return err
	}
	in := &dynamodb.PutItemInput{TableName: aws.String(r.table), Item: av}
	if prev == "" {
		in.ConditionExpression = aws.String("attribute_not_exists(pk)")
	} else {
		in.ConditionExpression = aws.String("export_id = :prev")
		in.ExpressionAttributeValues = map[string]types.AttributeValue{":prev": sv(prev)}
	}
	if _, err := r.db.PutItem(ctx, in); err != nil {
		if dynamo.IsConditionFailed(err) {
			return ErrBuilding
		}
		return fmt.Errorf("dataexport: save: %w", err)
	}
	return nil
}

func (r *dynamoRepository) update(ctx context.Context, userID, exportID, expr string, names map[string]string, vals map[string]types.AttributeValue, cond string) (bool, error) {
	vals[":id"] = sv(exportID)
	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(r.table), Key: key(userID), UpdateExpression: aws.String(expr),
		ConditionExpression: aws.String("export_id = :id" + cond), ExpressionAttributeValues: vals,
	}
	if len(names) > 0 {
		in.ExpressionAttributeNames = names
	}
	if _, err := r.db.UpdateItem(ctx, in); err != nil {
		if dynamo.IsConditionFailed(err) {
			return false, nil
		}
		return false, fmt.Errorf("dataexport: update: %w", err)
	}
	return true, nil
}

func (r *dynamoRepository) SetPart(ctx context.Context, userID, exportID string, p Part) error {
	av, err := attributevalue.Marshal(toPart(p))
	if err != nil {
		return err
	}
	ok, err := r.update(ctx, userID, exportID, "SET #parts.#svc = :p",
		map[string]string{"#parts": "parts", "#svc": p.Service}, map[string]types.AttributeValue{":p": av}, "")
	if err != nil {
		return err
	}
	if !ok {
		return ErrStale
	}
	return nil
}

func (r *dynamoRepository) Finish(ctx context.Context, userID, exportID string, f Finish) error {
	expr := "SET #state = :s, #err = :e"
	names := map[string]string{"#state": "state", "#err": "error"}
	vals := map[string]types.AttributeValue{":s": sv(string(f.State)), ":e": sv(f.Error)}
	if !f.ReadyAt.IsZero() {
		expr += ", ready_at = :r, expires_at = :x"
		vals[":r"], vals[":x"] = sv(ts(f.ReadyAt)), sv(ts(f.ExpiresAt))
	}
	if f.DropParts {
		expr += " REMOVE #parts"
		names["#parts"] = "parts"
	}
	ok, err := r.update(ctx, userID, exportID, expr, names, vals, "")
	if err != nil {
		return err
	}
	if !ok {
		return ErrStale
	}
	return nil
}

func (r *dynamoRepository) MarkNotified(ctx context.Context, userID, exportID string) (bool, error) {
	return r.update(ctx, userID, exportID, "SET notified = :t", nil,
		map[string]types.AttributeValue{":t": &types.AttributeValueMemberBOOL{Value: true}}, " AND attribute_not_exists(notified)")
}

func (r *dynamoRepository) Delete(ctx context.Context, userID string) error {
	if _, err := r.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(r.table), Key: key(userID)}); err != nil {
		return fmt.Errorf("dataexport: delete: %w", err)
	}
	return nil
}
