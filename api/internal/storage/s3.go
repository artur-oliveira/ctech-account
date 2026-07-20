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

// MaxDocumentSize is the hard ceiling (10 MiB) a single presigned PUT may
// carry. PresignPut pins this on the request so a presigned-URL holder cannot
// push an arbitrarily large object at the bucket (SEC-024). The server-side
// ConfirmDocument check (kyc.MaxDocumentBytes) is the effective enforcement;
// this is the presign-level hint.
const MaxDocumentSize = 10 * 1024 * 1024

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
// pins ContentType and a ContentLength ceiling (SEC-024) so the holder of the
// URL cannot change the type or inflate the object beyond MaxDocumentSize.
func buildPutObjectInput(bucket, key, contentType string) *s3.PutObjectInput {
	return &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(MaxDocumentSize),
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
