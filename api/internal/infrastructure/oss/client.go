// Package oss is the API-side S3-compatible object-store client. It exists
// solely to mint short-lived presigned GET URLs for artifact downloads — no
// object bytes ever flow through the API process (design D1/D2). SeaweedFS (the
// MVP OSS) and MinIO (the test double) both speak the S3 protocol in path-style
// addressing, which is what this client is configured for.
package oss

import (
	"context"
	"time"

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
	PresignTTL      time.Duration
}

// Client wraps an S3 presign client bound to one bucket + TTL.
type Client struct {
	presign *s3.PresignClient
	bucket  string
	ttl     time.Duration
	// now is the clock used to compute the advisory expires_at; tests inject a
	// fixed clock. Production leaves it nil → time.Now.
	now func() time.Time
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
	return &Client{
		presign: s3.NewPresignClient(s3c),
		bucket:  cfg.Bucket,
		ttl:     cfg.PresignTTL,
	}
}

// PresignGet returns a presigned GET URL for a single object key plus the
// advisory expiry instant (mint-time + TTL, UTC). The URL is scoped to exactly
// {bucket, key} — never a prefix (design D3). expiresAt is what the API reports
// to clients; OSS is the authority on actual expiry (design D2 / clock-skew
// risk), which is why it is computed here rather than read back from the SDK
// (PresignGetObject returns only the URL).
func (c *Client) PresignGet(ctx context.Context, key string) (url string, expiresAt time.Time, err error) {
	now := c.clock().UTC()
	req, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(c.ttl))
	if err != nil {
		return "", time.Time{}, err
	}
	return req.URL, now.Add(c.ttl), nil
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
