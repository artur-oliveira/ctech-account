package erasurepub

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"gopkg.aoctech.app/api-commons/erasure"
)

func TestPublishInputCarriesServicesAttribute(t *testing.T) {
	in, err := publishInput("arn:topic", erasure.Message{
		Version: erasure.Version, Type: erasure.TypeErase, RequestID: "r1", Sub: "u1",
		Scope: erasure.ScopeAccount, Services: []string{"wallet", "dfe"}, Attempt: 2, IssuedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("publishInput: %v", err)
	}
	attr, ok := in.MessageAttributes["services"]
	if !ok || aws.ToString(attr.DataType) != "String.Array" || aws.ToString(attr.StringValue) != `["wallet","dfe"]` {
		t.Fatalf("services attribute %+v", attr)
	}
}
