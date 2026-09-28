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
)

type ObjectStore interface {
	Put(context.Context, string, string, []byte) error
}
type S3 struct {
	client *s3.Client
	bucket string
}

func NewS3(ctx context.Context, c config.Config) (*S3, error) {
	if c.S3Bucket == "" {
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
	return &S3{client: s3.NewFromConfig(cfg, opt), bucket: c.S3Bucket}, nil
}
func (s *S3) Put(ctx context.Context, key, contentType string, data []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, ContentType: &contentType, Body: bytes.NewReader(data)})
	return err
}
func ReadLimited(r io.Reader, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > n {
		return nil, fmt.Errorf("image exceeds %d byte limit", n)
	}
	return b, nil
}
func PublicPath(base, key string) string { return strings.TrimRight(base, "/") + "/" + key }
