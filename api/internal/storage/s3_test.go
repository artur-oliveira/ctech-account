package storage

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// TestPresignPutInputHasNoContentLengthCap proves the presigned PUT does not
// pin ContentLength: SigV4 signs that header's literal value, so a fixed
// ContentLength would only match uploads of exactly that size and break every
// other upload with SignatureDoesNotMatch. Size is capped server-side instead
// (kyc.MaxDocumentBytes, SEC-024).
func TestPresignPutInputHasNoContentLengthCap(t *testing.T) {
	in := buildPutObjectInput("bucket", "kyc/u1/doc", "image/jpeg")
	if in.ContentLength != nil {
		t.Fatalf("ContentLength must not be pinned on the presigned PUT input, got %d", aws.ToInt64(in.ContentLength))
	}
	if aws.ToString(in.ContentType) != "image/jpeg" {
		t.Fatalf("ContentType = %q, want image/jpeg", aws.ToString(in.ContentType))
	}
}
