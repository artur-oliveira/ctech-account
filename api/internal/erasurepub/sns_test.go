package erasurepub

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"gopkg.aoctech.app/api-commons/dataexport"
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

// An export request is routed to exactly one participant's queue by the same
// attribute the erasure messages use.
func TestExportPublishInputRoutesToOneService(t *testing.T) {
	now := time.Now()
	in, err := exportPublishInput("arn:topic", dataexport.Message{
		Version: dataexport.Version, Type: dataexport.TypeExport, RequestID: "r1", Sub: "u1", Service: "poker",
		PutURL: "https://b.s3.amazonaws.com/k", ExpiresAt: now.Add(time.Minute), Attempt: 1, IssuedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attr := in.MessageAttributes["services"]; aws.ToString(attr.StringValue) != `["poker"]` {
		t.Fatalf("services attribute %+v", attr)
	}
	if _, err := exportPublishInput("arn:topic", dataexport.Message{}); err == nil {
		t.Fatal("an invalid message must not be published")
	}
}
