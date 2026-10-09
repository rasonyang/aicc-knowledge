// SPDX-License-Identifier: Apache-2.0

// Package s3test gives a test a client for the real S3 server (see
// testdb.S3) and a key prefix of its own, with helpers to upload and delete.
package s3test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/rasonyang/aicc-knowledge/internal/s3store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

// Env is a client scoped to a unique prefix. Keys passed to Put, PutMultipart
// and Delete are relative to that prefix.
type Env struct {
	*s3store.Client
	Config s3store.Config
	t      testing.TB
}

// New skips the test when no S3 is configured. Every object written under the
// test's prefix is deleted when the test ends.
func New(t testing.TB) *Env {
	t.Helper()
	e := testdb.S3(t)
	cfg := s3store.Config{
		Endpoint: e.Endpoint, Region: "us-east-1", Bucket: e.Bucket, UsePathStyle: true,
		Prefix:      fmt.Sprintf("test-%s-%d/", sanitize(t.Name()), time.Now().UnixNano()),
		AccessKeyID: e.AccessKeyID, SecretAccessKey: e.SecretAccessKey,
	}
	c, err := s3store.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	env := &Env{Client: c, Config: cfg, t: t}
	t.Cleanup(env.cleanup)
	return env
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s)
}

// Put uploads data in one request (a single-part ETag, the MD5 of the bytes).
func (e *Env) Put(key string, data []byte) {
	e.t.Helper()
	_, err := e.API.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(e.Bucket), Key: aws.String(e.Prefix + key), Body: bytes.NewReader(data),
	})
	if err != nil {
		e.t.Fatalf("put %s: %v", key, err)
	}
}

// PutMultipart uploads data with the upload manager and the given part size,
// which must be at least 5 MiB for S3 and smaller than data so that there are
// at least two parts. The ETag then ends in -<parts>.
func (e *Env) PutMultipart(key string, data []byte, partSize int64) {
	e.t.Helper()
	up := manager.NewUploader(e.API, func(u *manager.Uploader) { u.PartSize = partSize; u.Concurrency = 1 })
	_, err := up.Upload(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(e.Bucket), Key: aws.String(e.Prefix + key), Body: bytes.NewReader(data),
	})
	if err != nil {
		e.t.Fatalf("multipart put %s: %v", key, err)
	}
}

// Delete removes one object.
func (e *Env) Delete(key string) {
	e.t.Helper()
	_, err := e.API.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(e.Bucket), Key: aws.String(e.Prefix + key),
	})
	if err != nil {
		e.t.Fatalf("delete %s: %v", key, err)
	}
}

// ETag returns the ETag the server reports for key.
func (e *Env) ETag(key string) string {
	e.t.Helper()
	out, err := e.API.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(e.Bucket), Key: aws.String(e.Prefix + key),
	})
	if err != nil {
		e.t.Fatalf("head %s: %v", key, err)
	}
	return strings.Trim(aws.ToString(out.ETag), `"`)
}

func (e *Env) cleanup() {
	ctx := context.Background()
	_ = e.List(ctx, 0, func(page []s3store.Object) error {
		for _, o := range page {
			_, _ = e.API.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(e.Bucket), Key: aws.String(o.Key)})
		}
		return nil
	})
}
