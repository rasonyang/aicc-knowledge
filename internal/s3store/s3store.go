// SPDX-License-Identifier: Apache-2.0

// Package s3store is the S3 client of the scan: a paginated listing of one
// bucket and prefix, and a streaming download. It talks to AWS, MinIO or
// SeaweedFS through a configurable endpoint and addressing style; nothing here
// is hardcoded to a vendor.
package s3store

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config selects the bucket and how to reach it.
type Config struct {
	// Endpoint is the base URL of a non-AWS service; empty means AWS.
	Endpoint string
	Region   string
	Bucket   string
	// Prefix limits the listing; an empty prefix lists the whole bucket.
	Prefix       string
	UsePathStyle bool
	// AccessKeyID and SecretAccessKey are static credentials; when both are
	// empty the AWS default credential chain is used.
	AccessKeyID     string
	SecretAccessKey string
}

// Object is one listed key. ETag has its surrounding quotes removed. For a
// multipart upload it looks like "<md5>-<parts>" and is not a content hash.
type Object struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
}

// Client lists and reads one bucket.
type Client struct {
	// API is the underlying SDK client, exposed for tests that upload.
	API    *s3.Client
	Bucket string
	Prefix string
}

// New builds a client. It makes no network call.
func New(ctx context.Context, c Config) (*Client, error) {
	if c.Bucket == "" {
		return nil, fmt.Errorf("s3: bucket is required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(c.Region)}
	if c.AccessKeyID != "" || c.SecretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, "")))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load config: %w", err)
	}
	api := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
		o.UsePathStyle = c.UsePathStyle
		// S3-compatible servers do not all understand the trailing checksums
		// the SDK adds by default; ask for them only when the API requires.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &Client{API: api, Bucket: c.Bucket, Prefix: c.Prefix}, nil
}

// DefaultPageSize is S3's own maximum for ListObjectsV2.
const DefaultPageSize int32 = 1000

// List walks every key under the prefix with ListObjectsV2, page by page, and
// calls fn with each page. A zero pageSize means DefaultPageSize. It returns
// the first error from the listing or from fn; a partial listing is never
// reported as success.
func (c *Client) List(ctx context.Context, pageSize int32, fn func(page []Object) error) error {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	in := &s3.ListObjectsV2Input{Bucket: aws.String(c.Bucket), MaxKeys: aws.Int32(pageSize)}
	if c.Prefix != "" {
		in.Prefix = aws.String(c.Prefix)
	}
	pager := s3.NewListObjectsV2Paginator(c.API, in)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("s3: list %s/%s: %w", c.Bucket, c.Prefix, err)
		}
		page := make([]Object, 0, len(out.Contents))
		for _, o := range out.Contents {
			page = append(page, Object{
				Key:          aws.ToString(o.Key),
				Size:         aws.ToInt64(o.Size),
				ETag:         strings.Trim(aws.ToString(o.ETag), `"`),
				LastModified: aws.ToTime(o.LastModified),
			})
		}
		if err := fn(page); err != nil {
			return err
		}
	}
	return nil
}

// Body is an open object. Close it.
type Body struct {
	io.ReadCloser
	// Meta is what the server reported for the bytes being read, which can
	// differ from an earlier listing if the object changed in between.
	Meta Object
}

// Open starts a download of key.
func (c *Client) Open(ctx context.Context, key string) (*Body, error) {
	out, err := c.API.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("s3: get %s/%s: %w", c.Bucket, key, err)
	}
	return &Body{ReadCloser: out.Body, Meta: Object{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ETag:         strings.Trim(aws.ToString(out.ETag), `"`),
		LastModified: aws.ToTime(out.LastModified),
	}}, nil
}
