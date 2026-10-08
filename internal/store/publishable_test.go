// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"slices"
	"testing"
)

// TestListPublishableCandidatesReadsOnlyApprovedRowsOfLiveVersions pins what a
// publish may read: APPROVED candidates of a version that is current and not
// REMOVED. Rows are inserted directly, including inconsistent ones (an APPROVED
// candidate on a removed version, which the scan would have made STALE), to
// prove the query does not rely on the scan having done its job.
func TestListPublishableCandidatesReadsOnlyApprovedRowsOfLiveVersions(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO source_files (bucket, object_key) VALUES ('b', 'k')`)
	version := func(no int, state string, superseded bool) {
		exec(`INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at, state, superseded_at)
		      SELECT id, $1::int, decode(repeat(lpad($1::int::text, 2, '0'), 32), 'hex'), 1, 'e', now(), $2,
		             CASE WHEN $3::bool THEN now() END FROM source_files`, no, state, superseded)
	}
	version(1, "PARSED", true)  // superseded by version 2
	version(2, "REMOVED", true) // removed
	version(3, "PARSED", false) // the live one
	cand := func(versionNo int, language, state, question string) {
		exec(`INSERT INTO candidates (file_version_id, language, question, answer, state, content_hash)
		      SELECT id, $2, $4, 'a', $3::varchar, sha256(convert_to($4::text || $3::varchar::text, 'UTF8')) FROM file_versions WHERE version_no = $1`,
			versionNo, language, state, question)
	}
	cand(1, "EN", "APPROVED", "superseded but still APPROVED")
	cand(1, "EN", "STALE", "superseded and STALE")
	cand(2, "EN", "APPROVED", "removed but still APPROVED")
	cand(2, "EN", "STALE", "removed and STALE")
	cand(3, "EN", "APPROVED", "live approved 1")
	cand(3, "EN", "APPROVED", "live approved 2")
	cand(3, "EN", "PENDING_REVIEW", "live pending")
	cand(3, "EN", "REJECTED", "live rejected")
	cand(3, "EN", "STALE", "live stale")
	cand(3, "ZH", "APPROVED", "live approved zh")

	got, err := s.Queries.ListPublishableCandidates(ctx, "EN")
	if err != nil {
		t.Fatal(err)
	}
	var questions []string
	for _, c := range got {
		questions = append(questions, c.Question)
	}
	if want := []string{"live approved 1", "live approved 2"}; len(got) != 2 || !slices.Equal(questions, want) {
		t.Fatalf("publishable EN = %v, want %v", questions, want)
	}
	zh, err := s.Queries.ListPublishableCandidates(ctx, "ZH")
	if err != nil || len(zh) != 1 || zh[0].Question != "live approved zh" {
		t.Fatalf("publishable ZH = %v, %v", zh, err)
	}
}

func TestJobsDedupeIndexIgnoresRowsWithoutAKey(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	for range 2 {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO jobs (kind) VALUES ('PARSE')`); err != nil {
			t.Fatalf("two keyless jobs must coexist: %v", err)
		}
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO jobs (kind, dedupe_key) VALUES ('PARSE', 'k')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO jobs (kind, dedupe_key) VALUES ('PARSE', 'k')`); err == nil {
		t.Fatal("duplicate (kind, dedupe_key) was accepted")
	}
}
