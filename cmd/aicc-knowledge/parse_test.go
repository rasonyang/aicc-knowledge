// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
	"github.com/rasonyang/aicc-knowledge/internal/s3store/s3test"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

func TestParseFailsFastNamingMissingConfig(t *testing.T) {
	clearKB(t)
	code, _, stderr := invoke(t, "parse")
	if code != exitFailure || !strings.Contains(stderr, "KB_DATABASE_URL") || !strings.Contains(stderr, "KB_S3_BUCKET") {
		t.Fatalf("parse = %d %q", code, stderr)
	}
	if code, _, _ := invoke(t, "parse", "stray"); code != exitUsage {
		t.Errorf("parse with an argument = %d, want usage", code)
	}
}

func TestParseSubcommandExitCodesAndRetryFailed(t *testing.T) {
	clearKB(t)
	env := s3test.New(t)
	t.Setenv("KB_DATABASE_URL", testdb.ScratchDSN(t, "cmdparse"))
	t.Setenv("KB_S3_ENDPOINT", env.Config.Endpoint)
	t.Setenv("KB_S3_BUCKET", env.Config.Bucket)
	t.Setenv("KB_S3_PREFIX", env.Config.Prefix)
	t.Setenv("KB_S3_ACCESS_KEY_ID", env.Config.AccessKeyID)
	t.Setenv("KB_S3_SECRET_ACCESS_KEY", env.Config.SecretAccessKey)

	// Nothing to do is success.
	if code, out, stderr := invoke(t, "parse"); code != exitOK || out != "claimed=0 parsed=0 parse_failed=0 skipped=0 errors=0\n" {
		t.Fatalf("empty parse = %d %q %s", code, out, stderr)
	}

	env.Put("good.docx", parsetest.Fixture(t, "docx/headings.docx"))
	if code, _, stderr := invoke(t, "scan"); code != exitOK {
		t.Fatalf("scan = %d %s", code, stderr)
	}
	code, out, stderr := invoke(t, "parse")
	if code != exitOK || out != "claimed=1 parsed=1 parse_failed=0 skipped=0 errors=0\n" {
		t.Fatalf("parse = %d %q %s", code, out, stderr)
	}

	// A corrupt file makes the exit code 1.
	env.Put("bad.docx", []byte("not a zip"))
	invoke(t, "scan")
	code, out, _ = invoke(t, "parse")
	if code != exitFailure || out != "claimed=1 parsed=0 parse_failed=1 skipped=0 errors=0\n" {
		t.Fatalf("parse with a corrupt file = %d %q", code, out)
	}
	// Failed versions are not retried by themselves.
	if code, out, _ := invoke(t, "parse"); code != exitOK || out != "claimed=0 parsed=0 parse_failed=0 skipped=0 errors=0\n" {
		t.Fatalf("second parse = %d %q", code, out)
	}
	// -retry-failed resets and re-parses (still corrupt, so exit 1 again).
	code, out, _ = invoke(t, "parse", "-retry-failed")
	want := "retried_versions=1 jobs_queued=1\nclaimed=1 parsed=0 parse_failed=1 skipped=0 errors=0\n"
	if code != exitFailure || out != want {
		t.Fatalf("parse -retry-failed = %d\n%q\nwant\n%q", code, out, want)
	}

	// Fix the file: a new version is parsed, and nothing remains to retry.
	env.Put("bad.docx", parsetest.Fixture(t, "docx/tracked.docx"))
	invoke(t, "scan")
	if code, out, _ := invoke(t, "parse", "-retry-failed"); code != exitOK || out != "retried_versions=0 jobs_queued=0\nclaimed=1 parsed=1 parse_failed=0 skipped=0 errors=0\n" {
		t.Fatalf("parse after the fix = %d %q", code, out)
	}
}
