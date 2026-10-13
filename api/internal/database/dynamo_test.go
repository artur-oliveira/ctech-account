package database

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeTable struct{ items map[string]bool } // "pk|sk"

func (f *fakeTable) Query(_ context.Context, o QueryOpts) (*QueryResult, error) {
	res := &QueryResult{}
	for k := range f.items {
		pk, sk, _ := strings.Cut(k, "|")
		if pk == o.PK && strings.HasPrefix(sk, o.SKPrefix) && len(res.Items) < 2 { // tiny pages force the loop
			res.Items = append(res.Items, map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk},
			})
		}
	}
	return res, nil
}

func (f *fakeTable) DeleteItem(_ context.Context, pk string, sk ...string) (bool, error) {
	delete(f.items, pk+"|"+sk[0])
	return true, nil
}

func TestDeleteAllUnder(t *testing.T) {
	f := &fakeTable{items: map[string]bool{
		"ORG#1|MEMBER#a": true, "ORG#1|MEMBER#b": true, "ORG#1|MEMBER#c": true,
		"ORG#1|META": true, "ORG#2|MEMBER#z": true,
	}}
	if err := DeleteAllUnder(context.Background(), f, "ORG#1", "MEMBER#"); err != nil {
		t.Fatalf("DeleteAllUnder: %v", err)
	}
	if len(f.items) != 2 || !f.items["ORG#1|META"] || !f.items["ORG#2|MEMBER#z"] {
		t.Fatalf("left %v, want only ORG#1|META and ORG#2|MEMBER#z", f.items)
	}
}
