// SPDX-License-Identifier: Apache-2.0

// Package testdb gives a test a throwaway PostgreSQL database of its own and
// the URL of the real Meilisearch the tests run against.
//
// Set KB_TEST_DATABASE_URL to a superuser-capable DSN (the dev stack's is
// printed by `make dev-up`) and KB_TEST_MEILI_URL to a Meilisearch base URL;
// without them the tests that need them skip loudly. Neither is ever mocked.
// The package imports nothing from this module, so internal/store's own tests
// can use it without an import cycle.
package testdb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	dsnEnv   = "KB_TEST_DATABASE_URL"
	meiliEnv = "KB_TEST_MEILI_URL"
	teiEnv   = "KB_TEST_TEI_URL"
	keyEnv   = "KB_TEST_MEILI_API_KEY"

	s3EndpointEnv  = "KB_TEST_S3_ENDPOINT"
	s3AccessKeyEnv = "KB_TEST_S3_ACCESS_KEY_ID"
	s3SecretEnv    = "KB_TEST_S3_SECRET_ACCESS_KEY"
	s3BucketEnv    = "KB_TEST_S3_BUCKET"
)

// ScratchDSN creates an empty database named kb_<prefix>_<UnixNano>, drops it
// WITH (FORCE) when the test ends, and returns a DSN for it. Each test gets
// its own, because migrating is a whole-database act.
func ScratchDSN(t testing.TB, prefix string) string {
	t.Helper()
	admin := os.Getenv(dsnEnv)
	if admin == "" {
		t.Skipf("SKIPPED: set %s to run this test against a real PostgreSQL", dsnEnv)
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("%s is not a URL: %v", dsnEnv, err)
	}
	name := fmt.Sprintf("kb_%s_%d", prefix, time.Now().UnixNano())

	db, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}
	t.Cleanup(func() {
		c, err := sql.Open("pgx", admin)
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	})
	u.Path = "/" + name
	return u.String()
}

// MeiliURL returns the real Meilisearch base URL and its API key (possibly
// empty), skipping the test when KB_TEST_MEILI_URL is unset.
func MeiliURL(t testing.TB) (baseURL, apiKey string) {
	t.Helper()
	baseURL = os.Getenv(meiliEnv)
	if baseURL == "" {
		t.Skipf("SKIPPED: set %s to run this test against a real Meilisearch", meiliEnv)
	}
	return baseURL, os.Getenv(keyEnv)
}

// TEIURL returns the URL of a real TEI instance, or "" when KB_TEST_TEI_URL is
// unset. TEI is optional in tests: where it is absent, tests that only wire
// /readyz use a stand-in HTTP server and say so.
func TEIURL() string { return os.Getenv(teiEnv) }

// S3Env is the connection to the real S3-compatible server (SeaweedFS in the
// dev stack and in CI) the scan tests run against.
type S3Env struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

// S3 reads KB_TEST_S3_ENDPOINT, KB_TEST_S3_ACCESS_KEY_ID,
// KB_TEST_S3_SECRET_ACCESS_KEY and KB_TEST_S3_BUCKET, skipping the test when
// the endpoint or the bucket is unset. The bucket must already exist (the dev
// stack's `weed mini -bucket=aicc-knowledge` creates it); each test works
// under a prefix of its own.
func S3(t testing.TB) S3Env {
	t.Helper()
	e := S3Env{
		Endpoint:        os.Getenv(s3EndpointEnv),
		AccessKeyID:     os.Getenv(s3AccessKeyEnv),
		SecretAccessKey: os.Getenv(s3SecretEnv),
		Bucket:          os.Getenv(s3BucketEnv),
	}
	if e.Endpoint == "" || e.Bucket == "" {
		t.Skipf("SKIPPED: set %s and %s to run this test against a real S3 (SeaweedFS)", s3EndpointEnv, s3BucketEnv)
	}
	return e
}
