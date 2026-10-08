// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/s3store/s3test"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

func TestScanFailsFastNamingMissingConfig(t *testing.T) {
	clearKB(t)
	code, _, stderr := invoke(t, "scan")
	if code != exitFailure || !strings.Contains(stderr, "KB_DATABASE_URL") || !strings.Contains(stderr, "KB_S3_BUCKET") {
		t.Fatalf("scan = %d %q", code, stderr)
	}
}

func TestScanSubcommandPrintsASummaryAndIsIdempotent(t *testing.T) {
	clearKB(t)
	env := s3test.New(t)
	t.Setenv("KB_DATABASE_URL", testdb.ScratchDSN(t, "cmdscan"))
	t.Setenv("KB_S3_ENDPOINT", env.Config.Endpoint)
	t.Setenv("KB_S3_BUCKET", env.Config.Bucket)
	t.Setenv("KB_S3_PREFIX", env.Config.Prefix)
	t.Setenv("KB_S3_ACCESS_KEY_ID", env.Config.AccessKeyID)
	t.Setenv("KB_S3_SECRET_ACCESS_KEY", env.Config.SecretAccessKey)
	env.Put("a.docx", []byte("aaa"))
	env.Put("b.doc", []byte("bbbb"))

	code, out, stderr := invoke(t, "scan")
	want := "seen=2 new_versions=2 unchanged=0 metadata_only=0 removed=0 unsupported=1 ignored=0 oversize=0 errors=0 jobs_enqueued=1 stale_candidates=0 bytes_downloaded=7\n"
	if code != exitOK || out != want {
		t.Fatalf("first scan = %d\n%q\nwant\n%q\nstderr: %s", code, out, want, stderr)
	}
	code, out, stderr = invoke(t, "scan")
	want = "seen=2 new_versions=0 unchanged=2 metadata_only=0 removed=0 unsupported=0 ignored=0 oversize=0 errors=0 jobs_enqueued=0 stale_candidates=0 bytes_downloaded=0\n"
	if code != exitOK || out != want {
		t.Fatalf("second scan = %d\n%q\nwant\n%q\nstderr: %s", code, out, want, stderr)
	}
}
