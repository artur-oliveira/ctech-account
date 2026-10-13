package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/api/internal/database"
)

var ErrNotFound = errors.New("session not found")

// ErrRefreshTokenNotFound is returned when no RefreshToken item matches a lookup.
var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// Repository is the data-access interface for sessions and per-client refresh tokens.
type Repository interface {
	GetByID(ctx context.Context, userID, sessionID string) (*Session, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	Create(ctx context.Context, s *Session) error
	UpdateMFA(ctx context.Context, userID, sessionID string, amr []string, lastMFAAt int64) error
	Delete(ctx context.Context, userID, sessionID string) error
	ListByUserID(ctx context.Context, userID string) ([]*Session, error)

	PutRefreshToken(ctx context.Context, t *RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// RotateRefreshToken atomically replaces the active token and records the old hash as consumed.
	RotateRefreshToken(ctx context.Context, old, next *RefreshToken, consumedUntil int64) error
	// GetConsumedByHash returns the consumed-token marker for a hash, if one
	// exists (i.e. the hash was a valid refresh token that has since rotated).
	GetConsumedByHash(ctx context.Context, tokenHash string) (*ConsumedToken, error)
	ListRefreshTokensBySession(ctx context.Context, userID, sessionID string) ([]*RefreshToken, error)
	DeleteRefreshToken(ctx context.Context, userID, sessionID, clientID string) error
}

type dynamoRepository struct {
	table   database.Base
	tokenDB interface {
		GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
		TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	}
	tableName string
}

// NewRepository returns a DynamoDB-backed Repository.
func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{
		table:     database.NewBase(db, tablePrefix, "account_sessions"),
		tokenDB:   db,
		tableName: database.TableName(tablePrefix, "account_sessions"),
	}
}

func (r *dynamoRepository) Create(ctx context.Context, s *Session) error {
	item, err := attributevalue.MarshalMap(s)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}
	return r.table.PutItem(ctx, item)
}

func (r *dynamoRepository) GetByID(ctx context.Context, userID, sessionID string) (*Session, error) {
	item, err := r.table.GetItem(ctx, BuildPK(userID), BuildSK(sessionID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}

	var s Session
	if err := attributevalue.UnmarshalMap(item, &s); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}
	return &s, nil
}

// tokenHashIndex is the GSI on refresh_token_hash shared by Session and
// RefreshToken items; queries disambiguate the two by SK prefix.
const tokenHashIndex = "token-hash-index"

// queryByTokenHash selects the requested item kind. During legacy token
// rotation, the GSI can briefly contain both the active row and consumed marker.
func (r *dynamoRepository) queryByTokenHash(ctx context.Context, tokenHash, prefix string) (map[string]types.AttributeValue, error) {
	res, err := r.table.QueryGSI(ctx, tokenHashIndex, "refresh_token_hash", tokenHash, 2, nil)
	if err != nil {
		return nil, fmt.Errorf("querying token hash index: %w", err)
	}
	for _, item := range res.Items {
		if strings.HasPrefix(skPrefix(item), prefix) {
			return item, nil
		}
	}
	return nil, nil
}

func skPrefix(item map[string]types.AttributeValue) string {
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		return sk.Value
	}
	return ""
}

func (r *dynamoRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	item, err := r.queryByTokenHash(ctx, tokenHash, sessionSKPrefix)
	if err != nil {
		return nil, err
	}
	if item == nil || !strings.HasPrefix(skPrefix(item), sessionSKPrefix) {
		return nil, ErrNotFound
	}
	var s Session
	if err := attributevalue.UnmarshalMap(item, &s); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}
	return &s, nil
}

func (r *dynamoRepository) PutRefreshToken(ctx context.Context, t *RefreshToken) error {
	item, err := attributevalue.MarshalMap(t)
	if err != nil {
		return fmt.Errorf("marshaling refresh token: %w", err)
	}
	lookup, err := attributevalue.MarshalMap(tokenLookup{
		PK: tokenLookupPK(t.RefreshTokenHash), SK: tokenLookupSK,
		State: tokenLookupActive, UserID: t.UserID(), SessionID: t.SessionID,
		ClientID: t.ClientID, ExpiresAt: t.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("marshaling refresh-token lookup: %w", err)
	}
	_, err = r.tokenDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: item}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: lookup, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	return translateTokenTransactionError("persisting refresh token", err)
}

func (r *dynamoRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	lookup, err := r.getTokenLookup(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if lookup != nil {
		if lookup.State != tokenLookupActive {
			return nil, ErrRefreshTokenNotFound
		}
		item, err := r.getRefreshTokenItem(ctx, lookup.UserID, lookup.SessionID, lookup.ClientID)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return nil, ErrRefreshTokenNotFound
		}
		var t RefreshToken
		if err := attributevalue.UnmarshalMap(item, &t); err != nil {
			return nil, fmt.Errorf("unmarshaling refresh token: %w", err)
		}
		if t.RefreshTokenHash != tokenHash {
			return nil, ErrRefreshTokenNotFound
		}
		return &t, nil
	}
	// Tokens issued before lookup records existed are resolved through the old GSI.
	item, err := r.queryByTokenHash(ctx, tokenHash, refreshSKPrefix)
	if err != nil {
		return nil, err
	}
	if item == nil || !strings.HasPrefix(skPrefix(item), refreshSKPrefix) {
		return nil, ErrRefreshTokenNotFound
	}
	var t RefreshToken
	if err := attributevalue.UnmarshalMap(item, &t); err != nil {
		return nil, fmt.Errorf("unmarshaling refresh token: %w", err)
	}
	return &t, nil
}

func (r *dynamoRepository) RotateRefreshToken(ctx context.Context, old, next *RefreshToken, consumedUntil int64) error {
	consumed, err := attributevalue.MarshalMap(tokenLookup{
		PK: tokenLookupPK(old.RefreshTokenHash), SK: tokenLookupSK,
		State: tokenLookupConsumed, UserID: old.UserID(), SessionID: old.SessionID,
		ClientID: old.ClientID, ExpiresAt: consumedUntil,
	})
	if err != nil {
		return fmt.Errorf("marshaling consumed-token lookup: %w", err)
	}
	nextLookup, err := attributevalue.MarshalMap(tokenLookup{
		PK: tokenLookupPK(next.RefreshTokenHash), SK: tokenLookupSK,
		State: tokenLookupActive, UserID: next.UserID(), SessionID: next.SessionID,
		ClientID: next.ClientID, ExpiresAt: next.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("marshaling refresh-token lookup: %w", err)
	}
	update := &types.Update{
		TableName: aws.String(r.tableName), Key: tokenDynamoKey(old.PK, old.SK),
		UpdateExpression:    aws.String("SET refresh_token_hash = :new_hash, created_at = :created, last_used_at = :used, expires_at = :expires"),
		ConditionExpression: aws.String("refresh_token_hash = :old_hash"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":old_hash": tokenDynamoString(old.RefreshTokenHash), ":new_hash": tokenDynamoString(next.RefreshTokenHash),
			":created": tokenDynamoString(next.CreatedAt), ":used": tokenDynamoString(next.LastUsedAt),
			":expires": &types.AttributeValueMemberN{Value: fmt.Sprint(next.ExpiresAt)},
		},
	}
	_, err = r.tokenDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Update: update},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: consumed,
			ConditionExpression:       aws.String("attribute_not_exists(pk) OR #state = :active"),
			ExpressionAttributeNames:  map[string]string{"#state": "state"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":active": tokenDynamoString(tokenLookupActive)},
		}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: nextLookup,
			ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	return translateTokenTransactionError("rotating refresh token", err)
}

func translateTokenTransactionError(op string, err error) error {
	if database.IsConditionFailed(err) {
		return ErrTokenReuse
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func (r *dynamoRepository) getTokenLookup(ctx context.Context, tokenHash string) (*tokenLookup, error) {
	out, err := r.tokenDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName), Key: tokenDynamoKey(tokenLookupPK(tokenHash), tokenLookupSK), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("reading refresh-token lookup: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var lookup tokenLookup
	if err := attributevalue.UnmarshalMap(out.Item, &lookup); err != nil {
		return nil, fmt.Errorf("unmarshaling refresh-token lookup: %w", err)
	}
	return &lookup, nil
}

func (r *dynamoRepository) getRefreshTokenItem(ctx context.Context, userID, sessionID, clientID string) (map[string]types.AttributeValue, error) {
	out, err := r.tokenDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName), Key: tokenDynamoKey(BuildPK(userID), BuildRefreshSK(sessionID, clientID)), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("reading active refresh token: %w", err)
	}
	return out.Item, nil
}

func tokenDynamoKey(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": tokenDynamoString(pk), "sk": tokenDynamoString(sk),
	}
}

func tokenDynamoString(value string) types.AttributeValue {
	return &types.AttributeValueMemberS{Value: value}
}

func (r *dynamoRepository) GetConsumedByHash(ctx context.Context, tokenHash string) (*ConsumedToken, error) {
	lookup, err := r.getTokenLookup(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if lookup != nil {
		if lookup.State != tokenLookupConsumed {
			return nil, ErrRefreshTokenNotFound
		}
		return &ConsumedToken{
			PK: BuildPK(lookup.UserID), SK: consumedSKPrefix + tokenHash,
			RefreshTokenHash: tokenHash, UserID: lookup.UserID, SessionID: lookup.SessionID,
			ClientID: lookup.ClientID, ExpiresAt: lookup.ExpiresAt,
		}, nil
	}
	item, err := r.queryByTokenHash(ctx, tokenHash, consumedSKPrefix)
	if err != nil {
		return nil, err
	}
	if item == nil || !strings.HasPrefix(skPrefix(item), consumedSKPrefix) {
		return nil, ErrRefreshTokenNotFound
	}
	var c ConsumedToken
	if err := attributevalue.UnmarshalMap(item, &c); err != nil {
		return nil, fmt.Errorf("unmarshaling consumed token: %w", err)
	}
	return &c, nil
}

// UpdateMFA persists a fresh MFA proof on the session (amr set + last_mfa_at).
func (r *dynamoRepository) UpdateMFA(ctx context.Context, userID, sessionID string, amr []string, lastMFAAt int64) error {
	sk := BuildSK(sessionID)
	_, err := r.table.UpdateItem(ctx, BuildPK(userID), &sk, map[string]any{
		"amr":         amr,
		"last_mfa_at": lastMFAAt,
	})
	return err
}

func (r *dynamoRepository) ListRefreshTokensBySession(ctx context.Context, userID, sessionID string) ([]*RefreshToken, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(userID), SKPrefix: refreshSKPrefix + sessionID + refreshSKSeparator})
	if err != nil {
		return nil, err
	}
	tokens := make([]*RefreshToken, 0, len(res.Items))
	for _, item := range res.Items {
		var t RefreshToken
		if err := attributevalue.UnmarshalMap(item, &t); err != nil {
			return nil, fmt.Errorf("unmarshaling refresh token: %w", err)
		}
		tokens = append(tokens, &t)
	}
	return tokens, nil
}

func (r *dynamoRepository) DeleteRefreshToken(ctx context.Context, userID, sessionID, clientID string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(userID), BuildRefreshSK(sessionID, clientID))
	return err
}

func (r *dynamoRepository) ListByUserID(ctx context.Context, userID string) ([]*Session, error) {
	res, err := r.table.Query(ctx, database.QueryOpts{PK: BuildPK(userID), SKPrefix: "SESSION_"})
	if err != nil {
		return nil, err
	}

	sessions := make([]*Session, 0, len(res.Items))
	for _, item := range res.Items {
		var s Session
		if err := attributevalue.UnmarshalMap(item, &s); err != nil {
			return nil, fmt.Errorf("unmarshaling session: %w", err)
		}
		if !s.IsExpired() {
			sessions = append(sessions, &s)
		}
	}
	return sessions, nil
}

func (r *dynamoRepository) Delete(ctx context.Context, userID, sessionID string) error {
	_, err := r.table.DeleteItem(ctx, BuildPK(userID), BuildSK(sessionID))
	return err
}
