package storage

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
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

// A reviewer opens this URL through normal browser navigation. Any signed
// header other than Host would have to be supplied by JavaScript/fetch and
// makes opening the document in a new tab fail with SignatureDoesNotMatch.
func TestPresignGetRequiresNoBrowserHeaders(t *testing.T) {
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
	}
	store := newS3FromConfig(cfg, "kyc-documents")
	raw, err := store.PresignGet(context.Background(), "kyc/u1/doc", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	signedHeaders := parsed.Query().Get("X-Amz-SignedHeaders")
	if signedHeaders != "host" {
		t.Fatalf("X-Amz-SignedHeaders = %q, want host", signedHeaders)
	}
	if strings.Contains(strings.ToLower(raw), "checksum") {
		t.Fatalf("presigned browser URL unexpectedly contains checksum negotiation: %s", raw)
	}
}
