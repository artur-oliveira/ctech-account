// Package storage wraps the S3 operations the API needs for KYC document
// uploads. Object bytes never pass through the API: the browser PUTs straight
// to a presigned URL, and reviewers GET through one too.
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"gopkg.aoctech.app/api-commons/awsconfig"
)

// ErrNotFound is returned by Size when the object does not exist.
var ErrNotFound = errors.New("object not found")

// S3 presigns uploads/downloads and reads object metadata for one bucket.
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 builds a client from the ambient AWS config (task role in ECS).
func NewS3(ctx context.Context, region, bucket string) (*S3, error) {
	cfg, err := awsconfig.Load(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}
	client := s3.NewFromConfig(cfg)
	return &S3{client: client, presign: s3.NewPresignClient(client), bucket: bucket}, nil
}

// buildPutObjectInput constructs the PUT request the presigned URL signs. It
// pins ContentType so the holder of the URL cannot change the type. It does
// NOT pin ContentLength: SigV4 signs that header's literal value, so pinning
// it to a constant made every upload whose real size differed from that
// constant fail with SignatureDoesNotMatch. Size is capped server-side after
// upload (kyc.MaxDocumentBytes, see ConfirmDocument) — that's the effective
// enforcement (SEC-024).
func buildPutObjectInput(bucket, key, contentType string) *s3.PutObjectInput {
	return &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}
}

// PresignPut returns an upload URL that only accepts contentType — the caller
// must send the same Content-Type header or S3 rejects the signature.
func (s *S3) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, buildPutObjectInput(s.bucket, key, contentType), s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presigning put %s: %w", key, err)
	}
	return req.URL, nil
}

// PresignGet returns a short-lived download URL for a reviewer.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presigning get %s: %w", key, err)
	}
	return req.URL, nil
}

// Size returns the stored object's length, or ErrNotFound when the presigned
// upload never happened.
func (s *S3) Size(ctx context.Context, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, ErrNotFound
	}
	if out.ContentLength == nil {
		return 0, nil
	}
	return *out.ContentLength, nil
}

// DeleteObject removes a stored object. Used to purge PII when a KYC submission
// is rejected (SEC-038). Errors are the caller's to handle (best-effort).
func (s *S3) DeleteObject(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("deleting object %s: %w", key, err)
	}
	return nil
}
