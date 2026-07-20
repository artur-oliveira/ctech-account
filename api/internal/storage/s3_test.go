package storage

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// TestPresignPutInputHasContentLengthCap proves SEC-024: the PUT request the
// presigned URL signs carries a ContentLength ceiling so a presigned-URL holder
// cannot inflate the object beyond MaxDocumentSize.
func TestPresignPutInputHasContentLengthCap(t *testing.T) {
	in := buildPutObjectInput("bucket", "kyc/u1/doc", "image/jpeg")
	if in.ContentLength == nil {
		t.Fatal("ContentLength must be set on the presigned PUT input")
	}
	if got := aws.ToInt64(in.ContentLength); got != MaxDocumentSize {
		t.Fatalf("ContentLength = %d, want %d (MaxDocumentSize)", got, MaxDocumentSize)
	}
	if aws.ToString(in.ContentType) != "image/jpeg" {
		t.Fatalf("ContentType = %q, want image/jpeg", aws.ToString(in.ContentType))
	}
}
