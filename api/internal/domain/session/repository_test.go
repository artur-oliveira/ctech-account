package session

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type tokenStoreFake struct {
	item      map[string]types.AttributeValue
	getInputs []*dynamodb.GetItemInput
	write     *dynamodb.TransactWriteItemsInput
}

func (f *tokenStoreFake) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.getInputs = append(f.getInputs, in)
	return &dynamodb.GetItemOutput{Item: f.item}, nil
}

func (f *tokenStoreFake) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.write = in
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestRefreshTokenLookupUsesConsistentRecordBeforeLegacyIndex(t *testing.T) {
	item, err := attributevalue.MarshalMap(tokenLookup{
		PK: tokenLookupPK("old-hash"), SK: tokenLookupSK, State: tokenLookupConsumed,
		UserID: "user", SessionID: "session", ClientID: "client",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &tokenStoreFake{item: item}
	repo := &dynamoRepository{tokenDB: store, tableName: "sessions"}

	if _, err := repo.GetRefreshTokenByHash(context.Background(), "old-hash"); err != ErrRefreshTokenNotFound {
		t.Fatalf("consumed hash must not resolve as active: %v", err)
	}
	if len(store.getInputs) != 1 || aws.ToBool(store.getInputs[0].ConsistentRead) != true {
		t.Fatalf("lookup must use one strongly consistent read, got %+v", store.getInputs)
	}
}

func TestRotateRefreshTokenUsesOneTransaction(t *testing.T) {
	store := &tokenStoreFake{}
	repo := &dynamoRepository{tokenDB: store, tableName: "sessions"}
	old := &RefreshToken{
		PK: BuildPK("user"), SK: BuildRefreshSK("session", "client"),
		RefreshTokenHash: "old-hash", SessionID: "session", ClientID: "client",
	}
	next := &RefreshToken{
		PK: old.PK, SK: old.SK, RefreshTokenHash: "new-hash", SessionID: old.SessionID,
		ClientID: old.ClientID, CreatedAt: "2026-10-13T00:00:00Z", LastUsedAt: "2026-10-13T00:00:00Z", ExpiresAt: 100,
	}
	if err := repo.RotateRefreshToken(context.Background(), old, next, 200); err != nil {
		t.Fatal(err)
	}
	if got := len(store.write.TransactItems); got != 3 {
		t.Fatalf("rotation should update the active row and atomically write two lookup rows, got %d actions", got)
	}
	if store.write.TransactItems[0].Update == nil || store.write.TransactItems[1].Put == nil || store.write.TransactItems[2].Put == nil {
		t.Fatal("transaction must update active token, mark old hash consumed, and add new active lookup")
	}
	if _, ok := store.write.TransactItems[1].Put.Item["refresh_token_hash"]; ok {
		t.Fatal("lookup rows must stay out of the legacy token-hash index during rolling deploys")
	}
}
