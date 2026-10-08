// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

func openMigrated(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, testdb.ScratchDSN(t, "store"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func tableNames(t *testing.T, s *store.Store) []string {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name <> 'goose_db_version' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestMigrateUpDownUp(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, testdb.ScratchDSN(t, "migrate"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	want := []string{"api_keys", "candidate_reviews", "candidates", "fact_rows", "fact_tables", "file_versions", "jobs", "parsed_sections", "publication_items", "publications", "source_files"}

	if err := s.Migrate(ctx); err != nil {
		t.Fatal("up from zero:", err)
	}
	if got := tableNames(t, s); !slices.Equal(got, want) {
		t.Fatalf("tables after up = %v, want %v", got, want)
	}
	applied, latest, err := s.MigrationVersions(ctx)
	if err != nil || applied != latest || latest != 13 {
		t.Fatalf("versions applied=%d latest=%d err=%v, want 13/13", applied, latest, err)
	}

	if err := s.MigrateDown(ctx); err != nil {
		t.Fatal("down to zero:", err)
	}
	if got := tableNames(t, s); len(got) != 0 {
		t.Fatalf("tables after down = %v, want none", got)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatal("up again:", err)
	}
	if got := tableNames(t, s); !slices.Equal(got, want) {
		t.Fatalf("tables after second up = %v, want %v", got, want)
	}
	// Idempotent: a second Migrate is a no-op.
	if err := s.Migrate(ctx); err != nil {
		t.Fatal("repeat up:", err)
	}
}

func TestMigrateIsSerializedByAdvisoryLock(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.ScratchDSN(t, "lock")
	a, err := store.Open(ctx, dsn, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(ctx, dsn, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	errs := make(chan error, 2)
	for _, s := range []*store.Store{a, b} {
		go func() { errs <- s.Migrate(ctx) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal("concurrent migrate:", err)
		}
	}
	if got := len(tableNames(t, a)); got != 11 {
		t.Fatalf("tables = %d, want 11", got)
	}
}

// checkValues extracts the quoted values of the CHECK constraint on
// table.column and compares them to the Go enum.
func TestGoEnumsEqualDatabaseCheckConstraints(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	quoted := regexp.MustCompile(`'([A-Z_]+)'`)

	dbValues := func(table, column string) []string {
		rows, err := s.Pool.Query(ctx, `
			SELECT pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			JOIN pg_class t ON t.oid = c.conrelid
			JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY (c.conkey)
			WHERE c.contype = 'c' AND array_length(c.conkey, 1) = 1 AND t.relname = $1 AND a.attname = $2`, table, column)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var vals []string
		n := 0
		for rows.Next() {
			var def string
			if err := rows.Scan(&def); err != nil {
				t.Fatal(err)
			}
			n++
			for _, m := range quoted.FindAllStringSubmatch(def, -1) {
				vals = append(vals, m[1])
			}
		}
		if n != 1 {
			t.Fatalf("%s.%s has %d CHECK constraints, want exactly 1", table, column, n)
		}
		slices.Sort(vals)
		return slices.Compact(vals)
	}
	goValues := func(in ...string) []string {
		slices.Sort(in)
		return in
	}
	strs := func(n int, f func(i int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}

	fv, cs, ps, js, ls := domain.FileVersionStates(), domain.CandidateStates(), domain.PublicationStates(), domain.JobStates(), domain.Languages()
	sk, ft := domain.SectionKinds(), domain.FactTableStatuses()
	cf, ra := domain.CandidateFlags(), domain.ReviewActions()
	cases := []struct {
		table, column string
		want          []string
	}{
		{"file_versions", "state", goValues(strs(len(fv), func(i int) string { return string(fv[i]) })...)},
		{"candidates", "state", goValues(strs(len(cs), func(i int) string { return string(cs[i]) })...)},
		{"publications", "state", goValues(strs(len(ps), func(i int) string { return string(ps[i]) })...)},
		{"jobs", "state", goValues(strs(len(js), func(i int) string { return string(js[i]) })...)},
		{"candidates", "language", goValues(strs(len(ls), func(i int) string { return string(ls[i]) })...)},
		{"publications", "language", goValues(strs(len(ls), func(i int) string { return string(ls[i]) })...)},
		{"parsed_sections", "kind", goValues(strs(len(sk), func(i int) string { return string(sk[i]) })...)},
		{"parsed_sections", "qa_language", goValues(strs(len(ls), func(i int) string { return string(ls[i]) })...)},
		{"candidates", "flags", goValues(strs(len(cf), func(i int) string { return string(cf[i]) })...)},
		{"candidate_reviews", "action", goValues(strs(len(ra), func(i int) string { return string(ra[i]) })...)},
		{"fact_tables", "status", goValues(strs(len(ft), func(i int) string { return string(ft[i]) })...)},
	}
	for _, tc := range cases {
		got := dbValues(tc.table, tc.column)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s.%s: database %v, Go %v", tc.table, tc.column, got, tc.want)
		}
		if len(got) == 0 {
			t.Errorf("%s.%s: no values found, the gate is broken", tc.table, tc.column)
		}
	}
}

func TestAPIKeyIssueAndAuthenticate(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	secret, err := s.IssueAPIKey(ctx, "aicc")
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 43 {
		t.Errorf("secret length = %d, want 43 (32 bytes, base64url)", len(secret))
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal("valid key rejected:", err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret+"x"); !errors.Is(err, store.ErrInvalidAPIKey) {
		t.Fatalf("wrong key error = %v", err)
	}
	// Only the digest is stored: the secret appears nowhere in the table.
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE key_hash = $1 AND key_prefix = $2`,
		store.HashAPIKey(secret), secret[:8]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("digest row count = %d, err = %v, want 1", n, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now()`); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); !errors.Is(err, store.ErrInvalidAPIKey) {
		t.Fatalf("revoked key error = %v", err)
	}
}

func TestAtMostOneLivePublicationPerLanguage(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	if _, err := s.LivePublication(ctx, domain.LanguageEN); !errors.Is(err, store.ErrNoLivePublication) {
		t.Fatalf("empty store: %v", err)
	}
	ins := func(lang, uid, state string) error {
		_, err := s.Pool.Exec(ctx, `INSERT INTO publications (language, index_uid, state) VALUES ($1, $2, $3)`, lang, uid, state)
		return err
	}
	if err := ins("EN", "faq_en_1", "LIVE"); err != nil {
		t.Fatal(err)
	}
	err := ins("EN", "faq_en_2", "LIVE")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "uq_publications_live_language" {
		t.Fatalf("second LIVE EN: %v, want uq_publications_live_language", err)
	}
	if err := ins("ZH", "faq_zh_1", "LIVE"); err != nil {
		t.Fatal("a LIVE ZH next to a LIVE EN must be allowed:", err)
	}
	if err := ins("EN", "faq_en_3", "SUPERSEDED"); err != nil {
		t.Fatal(err)
	}
	if err := ins("EN", "faq_en_4", "BUILDING"); err != nil {
		t.Fatal(err)
	}
	p, err := s.LivePublication(ctx, domain.LanguageEN)
	if err != nil || p.IndexUid != "faq_en_1" {
		t.Fatalf("live EN = %+v, %v", p, err)
	}
	// Rollback order: demote the current LIVE, then promote the target.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE publications SET state='SUPERSEDED' WHERE language='EN' AND state='LIVE'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE publications SET state='LIVE' WHERE index_uid='faq_en_3'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM publications WHERE language='EN' AND state='LIVE'`).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live EN count = %d, err = %v, want 1", live, err)
	}
}

func TestFileVersionsKeepHistoryAndAllowRevert(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	var fileID string
	if err := s.Pool.QueryRow(ctx, `INSERT INTO source_files (bucket, object_key) VALUES ('b', 'faq.docx') RETURNING id::text`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	hash := func(b byte) []byte {
		h := make([]byte, 32)
		h[0] = b
		return h
	}
	add := func(no int, h byte) error {
		_, err := s.Pool.Exec(ctx, `INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at)
			VALUES ($1, $2, $3, 10, 'e', now())`, fileID, no, hash(h))
		return err
	}
	supersede := func(no int) {
		if _, err := s.Pool.Exec(ctx, `UPDATE file_versions SET superseded_at = now() WHERE source_file_id = $1 AND version_no = $2`, fileID, no); err != nil {
			t.Fatal(err)
		}
	}
	if err := add(1, 'A'); err != nil {
		t.Fatal(err)
	}
	// A second current version while the first is current is refused.
	err := add(2, 'B')
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "uq_file_versions_current" {
		t.Fatalf("two current versions: %v, want uq_file_versions_current", err)
	}
	supersede(1)
	if err := add(2, 'B'); err != nil {
		t.Fatal(err)
	}
	supersede(2)
	// Content A comes back: a third version, not a collision.
	if err := add(3, 'A'); err != nil {
		t.Fatal("revert to old content must create a new version:", err)
	}
	var total, distinct, current int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT sha256), count(*) FILTER (WHERE superseded_at IS NULL) FROM file_versions`).
		Scan(&total, &distinct, &current); err != nil {
		t.Fatal(err)
	}
	if total != 3 || distinct != 2 || current != 1 {
		t.Fatalf("total=%d distinct=%d current=%d, want 3/2/1", total, distinct, current)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at)
		VALUES ($1, 9, '\x00', 1, 'e', now())`, fileID); err == nil {
		t.Fatal("a sha256 that is not 32 bytes must be refused")
	}
}

func TestSchemaNamingConventions(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	rows, err := s.Pool.Query(ctx, `SELECT conname, contype FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
		WHERE n.nspname = 'public' AND contype IN ('u', 'f')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatal(err)
		}
		seen++
		prefix := map[string]string{"u": "uq_", "f": "fk_"}[typ]
		if len(name) < len(prefix) || name[:len(prefix)] != prefix {
			t.Errorf("constraint %q should start with %s", name, prefix)
		}
	}
	// 7 unique + 12 foreign keys; the exact count guards against a constraint
	// that escaped the naming rule.
	if seen != 19 {
		t.Fatalf("unique and foreign-key constraints = %d, want 19", seen)
	}
}

// TestPublicationsTrackWhereContentLives pins migration 00012: content_uid is
// unique among the publications that have one, and an item's scope is a JSON
// object that defaults to empty.
func TestPublicationsTrackWhereContentLives(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	exec := func(sql string, args ...any) error {
		_, err := s.Pool.Exec(ctx, sql, args...)
		return err
	}
	if err := exec(`INSERT INTO publications (language, index_uid, state, content_uid) VALUES ('EN', 'faq_en_a', 'SUPERSEDED', 'faq_en_a')`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO publications (language, index_uid, state, content_uid) VALUES ('EN', 'faq_en_b', 'SUPERSEDED', NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO publications (language, index_uid, state, content_uid) VALUES ('EN', 'faq_en_c', 'SUPERSEDED', NULL)`); err != nil {
		t.Fatalf("two publications without content must be allowed: %v", err)
	}
	if err := exec(`UPDATE publications SET content_uid = 'faq_en_a' WHERE index_uid = 'faq_en_b'`); err == nil {
		t.Fatal("two publications claimed the same content_uid")
	}
	if err := exec(`INSERT INTO source_files (bucket, object_key) VALUES ('b', 'k')`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at, state)
		SELECT id, 1, sha256('x'), 1, 'e', now(), 'PARSED' FROM source_files`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO candidates (file_version_id, language, question, answer, content_hash)
		SELECT id, 'EN', 'q', 'a', sha256('c') FROM file_versions`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO publication_items (publication_id, candidate_id, content_hash, question, answer, source_ref)
		SELECT p.id, c.id, c.content_hash, 'q', 'a', 'r' FROM publications p, candidates c WHERE p.index_uid = 'faq_en_a'`); err != nil {
		t.Fatal(err)
	}
	var scope string
	if err := s.Pool.QueryRow(ctx, `SELECT scope::text FROM publication_items`).Scan(&scope); err != nil || scope != "{}" {
		t.Errorf("default scope = %q, %v", scope, err)
	}
	if err := exec(`UPDATE publication_items SET scope = '["acme"]'::jsonb`); err == nil {
		t.Error("a scope that is not a JSON object was accepted")
	}
}

// TestParsedSectionsHoldQARows pins migration 00013: a Q&A row section has a
// question, every other kind has none, and the language is EN or ZH.
func TestParsedSectionsHoldQARows(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	exec := func(sql string, args ...any) error {
		_, err := s.Pool.Exec(ctx, sql, args...)
		return err
	}
	if err := exec(`INSERT INTO source_files (bucket, object_key) VALUES ('b', 'k.xlsx')`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at, state)
		SELECT id, 1, sha256('x'), 1, 'e', now(), 'PARSED' FROM source_files`); err != nil {
		t.Fatal(err)
	}
	insert := func(ordinal int, kind string, question *string, lang *string) error {
		return exec(`INSERT INTO parsed_sections (file_version_id, ordinal, kind, body, source_ref, qa_question, qa_alternates, qa_language)
			SELECT id, $1, $2, 'answer', 'ref', $3, '{a,b}', $4 FROM file_versions`, ordinal, kind, question, lang)
	}
	q, en, fr := "question?", "EN", "FR"
	if err := insert(1, "XLSX_QA_ROW", &q, &en); err != nil {
		t.Fatalf("a Q&A row with a question: %v", err)
	}
	if err := insert(2, "XLSX_QA_ROW", nil, &en); err == nil {
		t.Error("a Q&A row without a question was accepted")
	}
	if err := insert(3, "XLSX_CHUNK", &q, nil); err == nil {
		t.Error("a chunk with a question was accepted")
	}
	if err := insert(4, "XLSX_QA_ROW", &q, &fr); err == nil {
		t.Error("language FR was accepted")
	}
	if err := insert(5, "XLSX_CHUNK", nil, nil); err != nil {
		t.Errorf("an ordinary chunk: %v", err)
	}
	var alts []string
	if err := s.Pool.QueryRow(ctx, `SELECT qa_alternates FROM parsed_sections WHERE ordinal = 1`).Scan(&alts); err != nil || len(alts) != 2 {
		t.Errorf("alternates = %v, %v", alts, err)
	}
}
