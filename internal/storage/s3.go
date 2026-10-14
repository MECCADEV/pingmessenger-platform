package storage

import (
	"bytes"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"pingmessenger/internal/config"
	"strings"
	"time"
)

type ObjectStore interface {
	Put(context.Context, string, string, []byte) error
	Delete(context.Context, string) error
}
type S3 struct {
	client  *s3.Client
	bucket  string
	presign *s3.PresignClient
}

func NewS3(ctx context.Context, c config.Config) (*S3, error) {
	return NewS3WithBucket(ctx, c, c.S3Bucket)
}

func NewS3WithBucket(ctx context.Context, c config.Config, bucket string) (*S3, error) {
	if bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.AWSRegion))
	if err != nil {
		return nil, err
	}
	opt := func(o *s3.Options) {
		o.UsePathStyle = true
		if c.S3Endpoint != "" {
			o.BaseEndpoint = aws.String(c.S3Endpoint)
		}
	}
	client := s3.NewFromConfig(cfg, opt)
	return &S3{client: client, bucket: bucket, presign: s3.NewPresignClient(client)}, nil
}
func (s *S3) Put(ctx context.Context, key, contentType string, data []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, ContentType: &contentType, Body: bytes.NewReader(data)})
	return err
}
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return err
}

// PresignGet returns a short-lived URL for a private object. The platform
// keeps the document metadata and policy; clients use this URL only for the
// OpenIM fileElem sourceUrl field.
func (s *S3) PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if expiry <= 0 {
		expiry = 15 * time.Minute
	}
	result, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key}, func(opts *s3.PresignOptions) {
		opts.Expires = expiry
	})
	if err != nil {
		return "", err
	}
	return result.URL, nil
}
func ReadLimited(r io.Reader, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > n {
		return nil, fmt.Errorf("media exceeds %d byte limit", n)
	}
	return b, nil
}
func PublicPath(base, key string) string { return strings.TrimRight(base, "/") + "/" + key }
