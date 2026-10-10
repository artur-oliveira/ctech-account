package organization

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

// Guard counters (spec § 3.2). They are guards, not the source of truth: the
// counts in planlimit.go are. Each check re-derives the count, and the write
// carries the counter conditionally on the value that check read, so two
// requests that both saw 2 of 3 cannot both write.
const (
	ownerPKPrefix   = "OWNER#"
	spacesCounterSK = "PERSONAL_SPACES"
	spacesAttr      = "n"
	peopleAttr      = "people_n"

	reasonConditionalCheckFailed = "ConditionalCheckFailed"
)

func ownerPK(userID string) string { return ownerPKPrefix + userID }

// Counter is a guard counter as read. Exists is false for an item or attribute
// written before plan limits existed.
type Counter struct {
	N      int64
	Exists bool
}

// CounterWrite is the guard one write carries: the value it expects to find
// and the value it sets. Blind (unlimited plan) sets without a condition.
type CounterWrite struct {
	Expect Counter
	Next   int64
	Blind  bool
}

// ErrCounterMoved is a guard whose expectation no longer held — a concurrent
// write landed first, or a concurrent transaction conflicted. Nothing was
// written; recount and retry.
var ErrCounterMoved = errors.New("plan counter moved")

// CounterRepository is the guarded half of the repository. A separate interface
// so cmd/migrate-dfe-orgs's fake, which never meets a plan, is untouched.
type CounterRepository interface {
	SpaceCounter(ctx context.Context, ownerUserID string) (Counter, error)
	PeopleCounter(ctx context.Context, orgID string) (Counter, error)
	GetInvitation(ctx context.Context, orgID, email string) (*Invitation, error)
	CreateWithOwnerGuarded(ctx context.Context, org *Organization, ownerName string, g CounterWrite) error
	PutInvitationGuarded(ctx context.Context, inv *Invitation, g CounterWrite, now time.Time) error
	TransferOwnershipGuarded(ctx context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time, g CounterWrite) error
	DecrementSpaceCounter(ctx context.Context, ownerUserID string) error
	DecrementPeopleCounter(ctx context.Context, orgID string) error
	ReconcileSpaceCounter(ctx context.Context, ownerUserID string, seen Counter, real int64) error
}

// NewCounterRepository returns the same DynamoDB repository, seen through its
// guarded half.
func NewCounterRepository(db *dynamodb.Client, tablePrefix string) CounterRepository {
	return NewRepository(db, tablePrefix).(*repo)
}

func numberAV(n int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}
}

func keyOf(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk},
		"sk": &types.AttributeValueMemberS{Value: sk},
	}
}

// guardExpr is the SET and the condition for one guard counter. extra is
// ANDed in front (e.g. attribute_exists(pk) on a workspace's META).
func guardExpr(attr string, g CounterWrite, extra string) (update, cond string, names map[string]string, values map[string]types.AttributeValue) {
	names = map[string]string{"#c": attr}
	values = map[string]types.AttributeValue{":next": numberAV(g.Next)}
	parts := make([]string, 0, 2)
	if extra != "" {
		parts = append(parts, extra)
	}
	if !g.Blind {
		if g.Expect.Exists {
			parts = append(parts, "#c = :expect")
			values[":expect"] = numberAV(g.Expect.N)
		} else {
			parts = append(parts, "attribute_not_exists(#c)")
		}
	}
	return "SET #c = :next", strings.Join(parts, " AND "), names, values
}

// cancelledAt reports whether item index of a cancelled transaction failed
// with code.
func cancelledAt(err error, index int, code string) bool {
	tce, ok := errors.AsType[*types.TransactionCanceledException](err)
	if !ok || index >= len(tce.CancellationReasons) {
		return false
	}
	r := tce.CancellationReasons[index]
	return r.Code != nil && *r.Code == code
}

// guardOutcome maps a guarded transaction's error: the guard's own condition
// or any TransactionConflict → ErrCounterMoved (retry); any other condition →
// otherCondition; anything else unchanged.
func guardOutcome(err error, guardIndex int, otherCondition error) error {
	switch {
	case err == nil:
		return nil
	case database.IsTransactionConflict(err), cancelledAt(err, guardIndex, reasonConditionalCheckFailed):
		return ErrCounterMoved
	case database.IsConditionFailed(err):
		return otherCondition
	default:
		return err
	}
}

func (r *repo) readCounter(ctx context.Context, pk, sk, attr string) (Counter, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:                aws.String(r.orgsName),
		Key:                      keyOf(pk, sk),
		ConsistentRead:           aws.Bool(true),
		ProjectionExpression:     aws.String("#c"),
		ExpressionAttributeNames: map[string]string{"#c": attr},
	})
	if err != nil {
		return Counter{}, fmt.Errorf("reading counter %s: %w", attr, err)
	}
	v, ok := out.Item[attr].(*types.AttributeValueMemberN)
	if !ok {
		return Counter{}, nil
	}
	n, err := strconv.ParseInt(v.Value, 10, 64)
	if err != nil {
		return Counter{}, fmt.Errorf("parsing counter %s: %w", attr, err)
	}
	return Counter{N: n, Exists: true}, nil
}

func (r *repo) SpaceCounter(ctx context.Context, ownerUserID string) (Counter, error) {
	return r.readCounter(ctx, ownerPK(ownerUserID), spacesCounterSK, spacesAttr)
}

func (r *repo) PeopleCounter(ctx context.Context, orgID string) (Counter, error) {
	return r.readCounter(ctx, orgPK(orgID), metaSK, peopleAttr)
}

// GetInvitation reads one pending row, strongly consistent: the invite path
// decides "same person again" or "a new person" from it.
func (r *repo) GetInvitation(ctx context.Context, orgID, email string) (*Invitation, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(r.invitationsName),
		Key:            keyOf(orgPK(orgID), inviteSK(email)),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("reading invitation: %w", err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	return unmarshalInvitation(out.Item)
}

func (r *repo) spacesGuardItem(ownerUserID string, g CounterWrite) types.TransactWriteItem {
	update, cond, names, values := guardExpr(spacesAttr, g, "")
	return r.orgs.BuildRawUpdateTxItem(ownerPK(ownerUserID), aws.String(spacesCounterSK), update, cond, names, values)
}

func (r *repo) CreateWithOwnerGuarded(ctx context.Context, org *Organization, ownerName string, g CounterWrite) error {
	items, err := r.createItems(org, ownerName)
	if err != nil {
		return err
	}
	items = append(items, r.spacesGuardItem(org.OwnerUserID, g))
	return guardOutcome(r.orgs.TransactWrite(ctx, items), 2, ErrAlreadyMember)
}

// PutInvitationGuarded writes a NEW person's invitation: the row must be absent
// or expired (an expired row is not counted, so replacing it adds a person),
// and the space's people_n moves with it. A live row means a concurrent invite
// of the same address landed first — ErrCounterMoved, and the retry finds it.
func (r *repo) PutInvitationGuarded(ctx context.Context, inv *Invitation, g CounterWrite, now time.Time) error {
	item, err := invitationItem(inv)
	if err != nil {
		return err
	}
	put := types.TransactWriteItem{Put: &types.Put{
		TableName:                 aws.String(r.invitationsName),
		Item:                      item,
		ConditionExpression:       aws.String("attribute_not_exists(pk) OR expires_at < :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":now": numberAV(now.Unix())},
	}}
	update, cond, names, values := guardExpr(peopleAttr, g, "attribute_exists(pk)")
	guard := r.orgs.BuildRawUpdateTxItem(orgPK(inv.OrganizationID), aws.String(metaSK), update, cond, names, values)
	err = r.orgs.TransactWrite(ctx, []types.TransactWriteItem{put, guard})
	if database.IsConditionFailed(err) || database.IsTransactionConflict(err) {
		return ErrCounterMoved
	}
	if err != nil {
		return fmt.Errorf("writing invitation: %w", err)
	}
	return nil
}

func (r *repo) TransferOwnershipGuarded(ctx context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time, g CounterWrite) error {
	items := append(r.transferItems(orgID, fromUserID, toUserID, demoteTo, now), r.spacesGuardItem(toUserID, g))
	err := guardOutcome(r.memberships.TransactWrite(ctx, items), 3, ErrNotFound)
	if err != nil && !errors.Is(err, ErrCounterMoved) && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("transferring ownership: %w", err)
	}
	return err
}

// decrement is best-effort and never below zero: reducing usage is never
// refused, and the next check re-derives the guard anyway (decision P3).
func (r *repo) decrement(ctx context.Context, pk, sk, attr string) error {
	_, err := r.orgs.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(r.orgsName),
		Key:                       keyOf(pk, sk),
		UpdateExpression:          aws.String("SET #c = #c - :one"),
		ConditionExpression:       aws.String("#c > :zero"),
		ExpressionAttributeNames:  map[string]string{"#c": attr},
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": numberAV(1), ":zero": numberAV(0)},
	})
	if database.IsConditionFailed(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decrementing %s: %w", attr, err)
	}
	return nil
}

func (r *repo) DecrementSpaceCounter(ctx context.Context, ownerUserID string) error {
	return r.decrement(ctx, ownerPK(ownerUserID), spacesCounterSK, spacesAttr)
}

func (r *repo) DecrementPeopleCounter(ctx context.Context, orgID string) error {
	return r.decrement(ctx, orgPK(orgID), metaSK, peopleAttr)
}

// ReconcileSpaceCounter sets the counter to the real count, only if it still
// holds what the caller read — a create that landed meanwhile wins.
func (r *repo) ReconcileSpaceCounter(ctx context.Context, ownerUserID string, seen Counter, real int64) error {
	update, cond, names, values := guardExpr(spacesAttr, CounterWrite{Expect: seen, Next: real}, "")
	_, err := r.orgs.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(r.orgsName),
		Key:                       keyOf(ownerPK(ownerUserID), spacesCounterSK),
		UpdateExpression:          aws.String(update),
		ConditionExpression:       aws.String(cond),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if database.IsConditionFailed(err) {
		return nil
	}
	return err
}
