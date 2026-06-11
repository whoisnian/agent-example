// Package oss is the API-side S3-compatible object-store client. The download
// proxy route (add-artifact-download-proxy) streams artifact bytes from OSS
// through the API process — the browser never reaches OSS_ENDPOINT — so this
// client reads single objects; it still never writes. Download URLs are no
// longer OSS-presigned: the API signs its own tokens locally (internal/auth).
// SeaweedFS (the MVP OSS) and MinIO (the test double) both speak the S3
// protocol in path-style addressing, which is what this client is configured
// for.
package oss

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config carries the resolved OSS settings. It is built from config.Config at
// startup; credentials live here only transiently and are never logged.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	AccessKeySecret string
	UsePathStyle    bool
}

// Client wraps an S3 client bound to one bucket.
type Client struct {
	s3c    *s3.Client
	bucket string
}

// New builds a Client from cfg. The S3 client is constructed directly via
// s3.New (no shared aws.Config / env-credential chain) so the only credentials
// in play are the static keys we were handed — BaseEndpoint + UsePathStyle make
// it talk to SeaweedFS/MinIO instead of real AWS.
func New(cfg *Config) *Client {
	s3c := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: cfg.UsePathStyle,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.AccessKeySecret, "",
		),
	})
	return &Client{s3c: s3c, bucket: cfg.Bucket}
}

// GetObject opens one object for streaming. ContentLength is returned exactly
// as the SDK reports it (nil when unknown; 0 is a legitimate size for an empty
// object — callers must branch on nil, never on >0). The caller owns body and
// must Close it on every path; pass the request context so a disconnected
// client cancels the underlying read.
func (c *Client) GetObject(ctx context.Context, key string) (body io.ReadCloser, contentLength *int64, err error) {
	out, err := c.s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, err
	}
	return out.Body, out.ContentLength, nil
}
