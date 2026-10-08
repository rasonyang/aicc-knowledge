// SPDX-License-Identifier: Apache-2.0

package scan_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/s3store/s3test"
	"github.com/rasonyang/aicc-knowledge/internal/scan"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

type harness struct {
	t   *testing.T
	st  *store.Store
	s3  *s3test.Env
	scn *scan.Scanner
}

func setup(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	env := s3test.New(t) // skips loudly without a real S3
	st, err := store.Open(ctx, testdb.ScratchDSN(t, "scan"), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, st: st, s3: env, scn: &scan.Scanner{Store: st, S3: env.Client, MaxObjectBytes: 64 << 20}}
}

func (h *harness) run() scan.Summary {
	h.t.Helper()
	sum, err := h.scn.Run(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	if sum.Errors != 0 {
		h.t.Fatalf("scan reported %d errors: %+v", sum.Errors, sum)
	}
	return sum
}

type version struct {
	Key        string
	No         int
	State      string
	ErrCode    string
	SHA        string
	Size       int64
	ETag       string
	Superseded bool
}

// versions returns every version of every file, ordered by key and number.
func (h *harness) versions() []version {
	h.t.Helper()
	rows, err := h.st.Pool.Query(context.Background(), `
		SELECT sf.object_key, v.version_no, v.state, COALESCE(v.parse_error_code, ''),
		       encode(v.sha256, 'hex'), v.size_bytes, v.etag, v.superseded_at IS NOT NULL
		FROM file_versions v JOIN source_files sf ON sf.id = v.source_file_id
		ORDER BY sf.object_key, v.version_no`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []version
	for rows.Next() {
		var v version
		if err := rows.Scan(&v.Key, &v.No, &v.State, &v.ErrCode, &v.SHA, &v.Size, &v.ETag, &v.Superseded); err != nil {
			h.t.Fatal(err)
		}
		v.Key = strings.TrimPrefix(v.Key, h.s3.Prefix)
		out = append(out, v)
	}
	return out
}

func (h *harness) versionsOf(key string) []version {
	var out []version
	for _, v := range h.versions() {
		if v.Key == key {
			out = append(out, v)
		}
	}
	return out
}

func (h *harness) count(table string) int {
	h.t.Helper()
	var n int
	if err := h.st.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// fingerprint is a digest of every row of the scan's tables, so "nothing
// changed" is checked on content, not just on counts.
func (h *harness) fingerprint() string {
	h.t.Helper()
	var s string
	err := h.st.Pool.QueryRow(context.Background(), `
		SELECT md5(
		  COALESCE((SELECT string_agg(t::text, '|' ORDER BY t.id) FROM source_files t), '') ||
		  COALESCE((SELECT string_agg(t::text, '|' ORDER BY t.id) FROM file_versions t), '') ||
		  COALESCE((SELECT string_agg(t::text, '|' ORDER BY t.id) FROM candidates t), '') ||
		  COALESCE((SELECT string_agg(t::text, '|' ORDER BY t.id) FROM jobs t), ''))`).Scan(&s)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

// addCandidate inserts a candidate for the current version of key directly.
func (h *harness) addCandidate(key, state string) {
	h.t.Helper()
	_, err := h.st.Pool.Exec(context.Background(), `
		INSERT INTO candidates (file_version_id, language, question, answer, state, content_hash)
		SELECT v.id, 'EN', 'q?', 'a.', $2::varchar, sha256(convert_to($2::varchar::text, 'UTF8'))
		FROM file_versions v JOIN source_files sf ON sf.id = v.source_file_id
		WHERE sf.object_key = $1 AND v.superseded_at IS NULL`, h.s3.Prefix+key, state)
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) candidateStates(key string) []string {
	h.t.Helper()
	rows, err := h.st.Pool.Query(context.Background(), `
		SELECT c.state FROM candidates c
		JOIN file_versions v ON v.id = c.file_version_id
		JOIN source_files sf ON sf.id = v.source_file_id
		WHERE sf.object_key = $1 ORDER BY v.version_no, c.created_at, c.id`, h.s3.Prefix+key)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func sum256(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestScanIsIdempotent(t *testing.T) {
	h := setup(t)
	files := map[string][]byte{
		"a.docx":            []byte("docx bytes"),
		"b.xlsx":            []byte("xlsx bytes"),
		"prices.facts.yaml": []byte("sheet: x"),
		"old.doc":           []byte("legacy word"),
		"old.xls":           []byte("legacy excel"),
		"manual.pdf":        []byte("pdf bytes"),
	}
	var total int64
	for k, b := range files {
		h.s3.Put(k, b)
		total += int64(len(b))
	}
	h.s3.Put("folder/", nil) // a console "directory" marker
	h.s3.Put("empty.docx", nil)

	first := h.run()
	want := scan.Summary{Seen: 6, NewVersions: 6, Unsupported: 3, Ignored: 2, JobsEnqueued: 3, BytesDownloaded: total}
	if first != want {
		t.Fatalf("first scan = %+v\nwant         %+v", first, want)
	}
	if n := h.count("file_versions"); n != 6 {
		t.Fatalf("file_versions = %d, want 6", n)
	}
	if n := h.count("jobs"); n != 3 {
		t.Fatalf("jobs = %d, want 3", n)
	}
	before := h.fingerprint()

	second := h.run()
	want = scan.Summary{Seen: 6, Unchanged: 6, Ignored: 2}
	if second != want {
		t.Fatalf("second scan = %+v\nwant          %+v", second, want)
	}
	if after := h.fingerprint(); after != before {
		t.Fatal("second scan changed rows")
	}
	if h.count("file_versions") != 6 || h.count("jobs") != 3 || h.count("source_files") != 6 {
		t.Fatal("second scan changed row counts")
	}
}

func TestUnsupportedFormatsAreVersionedAndHashedWithoutAJob(t *testing.T) {
	h := setup(t)
	data := map[string][]byte{"w.doc": []byte("doc"), "x.xls": []byte("xls"), "p.pdf": []byte("pdf"), "NOEXT": []byte("n"), "Upper.DOCX": []byte("up")}
	for k, b := range data {
		h.s3.Put(k, b)
	}
	sum := h.run()
	if sum.NewVersions != 5 || sum.Unsupported != 4 || sum.JobsEnqueued != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	vs := h.versions()
	if len(vs) != 5 {
		t.Fatalf("versions = %d, want 5", len(vs))
	}
	for _, v := range vs {
		if v.Key == "Upper.DOCX" {
			if v.State != string(domain.FileVersionDiscovered) || v.ErrCode != "" {
				t.Errorf("%s = %+v, want DISCOVERED", v.Key, v)
			}
			continue
		}
		if v.State != "UNSUPPORTED" || v.ErrCode != "UNSUPPORTED_FORMAT" || v.SHA != sum256(data[v.Key]) {
			t.Errorf("%s = %+v, want UNSUPPORTED/UNSUPPORTED_FORMAT with the content hash", v.Key, v)
		}
	}
	if n := h.count("jobs"); n != 1 {
		t.Fatalf("jobs = %d, want 1 (only the .DOCX)", n)
	}
}

func TestMultipartETagIsNeverTheContentHash(t *testing.T) {
	h := setup(t)
	const part = 5 << 20
	data := bytes.Repeat([]byte("0123456789abcdef"), (part*2+4096)/16) // three parts
	h.s3.PutMultipart("big.docx", data, part)
	etag := h.s3.ETag("big.docx")
	if i := strings.LastIndexByte(etag, '-'); i < 0 || etag[i+1:] != "3" {
		t.Fatalf("ETag %q does not look like a 3-part multipart ETag", etag)
	}

	h.run()
	vs := h.versionsOf("big.docx")
	if len(vs) != 1 {
		t.Fatalf("versions = %d, want 1", len(vs))
	}
	if vs[0].SHA != sum256(data) {
		t.Fatalf("sha256 = %s, want the SHA-256 of the bytes %s", vs[0].SHA, sum256(data))
	}
	if vs[0].ETag != etag {
		t.Errorf("stored etag = %q, want the observed %q", vs[0].ETag, etag)
	}
	if strings.Contains(vs[0].SHA, strings.Split(etag, "-")[0]) {
		t.Error("sha256 contains the ETag's MD5")
	}

	// The same bytes re-uploaded in one request carry a different ETag. That is
	// not a new version: only the observed metadata moves, once.
	h.s3.Put("big.docx", data)
	single := h.s3.ETag("big.docx")
	if single == etag {
		t.Fatalf("test premise broken: ETag did not change (%s)", single)
	}
	sum := h.run()
	if sum.MetadataOnly != 1 || sum.NewVersions != 0 || sum.BytesDownloaded != int64(len(data)) {
		t.Fatalf("re-upload scan = %+v", sum)
	}
	vs = h.versionsOf("big.docx")
	if len(vs) != 1 || vs[0].No != 1 || vs[0].Superseded {
		t.Fatalf("versions after re-upload = %+v, want the one untouched version", vs)
	}
	if vs[0].ETag != etag {
		t.Errorf("version etag changed to %q; versions are immutable (at-discovery value %q)", vs[0].ETag, etag)
	}
	var observed string
	if err := h.st.Pool.QueryRow(context.Background(), `SELECT observed_etag FROM source_files`).Scan(&observed); err != nil || observed != single {
		t.Fatalf("observed_etag = %q, %v; want %q", observed, err, single)
	}
	if h.count("jobs") != 1 {
		t.Fatalf("jobs = %d, want 1", h.count("jobs"))
	}
	again := h.run() // and the next scan does not download again
	if again.Unchanged != 1 || again.BytesDownloaded != 0 || again.MetadataOnly != 0 {
		t.Fatalf("scan after metadata update = %+v", again)
	}
}

func TestContentChangeMakesANewVersionAndStalesTheOld(t *testing.T) {
	h := setup(t)
	a := []byte("content AAAA")
	b := []byte("content BBBB") // same size, different bytes
	h.s3.Put("f.docx", a)
	h.run()
	h.addCandidate("f.docx", "PENDING_REVIEW")
	h.addCandidate("f.docx", "APPROVED")
	h.addCandidate("f.docx", "REJECTED")

	h.s3.Put("f.docx", b)
	sum := h.run()
	if sum.NewVersions != 1 || sum.StaleCandidates != 3 || sum.JobsEnqueued != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	vs := h.versionsOf("f.docx")
	if len(vs) != 2 {
		t.Fatalf("versions = %d, want 2", len(vs))
	}
	if vs[0].No != 1 || !vs[0].Superseded || vs[0].SHA != sum256(a) || vs[0].State != "DISCOVERED" {
		t.Errorf("old version = %+v, want superseded and otherwise untouched", vs[0])
	}
	if vs[1].No != 2 || vs[1].Superseded || vs[1].SHA != sum256(b) || vs[1].State != "DISCOVERED" {
		t.Errorf("new version = %+v", vs[1])
	}
	got := h.candidateStates("f.docx")
	if !slices.Equal(got, []string{"STALE", "STALE", "STALE"}) {
		t.Fatalf("candidate states = %v, want 3 x STALE", got)
	}
	if n := h.count("jobs"); n != 2 {
		t.Fatalf("jobs = %d, want one PARSE per version", n)
	}
}

func TestRevertToEarlierContentIsAThirdVersion(t *testing.T) {
	h := setup(t)
	a, b := []byte("AAAA"), []byte("BBBBB")
	for _, data := range [][]byte{a, b, a} {
		h.s3.Put("f.xlsx", data)
		if sum := h.run(); sum.NewVersions != 1 {
			t.Fatalf("summary = %+v", sum)
		}
	}
	vs := h.versionsOf("f.xlsx")
	if len(vs) != 3 {
		t.Fatalf("versions = %d, want 3 (A, B, A)", len(vs))
	}
	wantSHA := []string{sum256(a), sum256(b), sum256(a)}
	for i, v := range vs {
		if v.No != i+1 || v.SHA != wantSHA[i] || v.Superseded != (i < 2) {
			t.Errorf("version %d = %+v", i+1, v)
		}
	}
}

func TestDeletedObjectBecomesRemovedAndReappearingMakesANewVersion(t *testing.T) {
	h := setup(t)
	h.s3.Put("keep.docx", []byte("keep"))
	h.s3.Put("gone.docx", []byte("gone"))
	h.run()
	h.addCandidate("gone.docx", "APPROVED")
	h.addCandidate("keep.docx", "APPROVED")
	ctx := context.Background()
	rows, err := h.st.Queries.ListPublishableCandidates(ctx, "EN")
	if err != nil || len(rows) != 2 {
		t.Fatalf("publishable before delete = %d, %v; want 2", len(rows), err)
	}

	h.s3.Delete("gone.docx")
	sum := h.run()
	if sum.Removed != 1 || sum.StaleCandidates != 1 || sum.NewVersions != 0 || sum.Unchanged != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	vs := h.versionsOf("gone.docx")
	if len(vs) != 1 || vs[0].State != "REMOVED" || !vs[0].Superseded {
		t.Fatalf("gone.docx = %+v, want one REMOVED superseded version", vs)
	}
	if got := h.candidateStates("gone.docx"); !slices.Equal(got, []string{"STALE"}) {
		t.Fatalf("candidates of gone.docx = %v", got)
	}
	if got := h.candidateStates("keep.docx"); !slices.Equal(got, []string{"APPROVED"}) {
		t.Fatalf("candidates of keep.docx = %v", got)
	}
	rows, err = h.st.Queries.ListPublishableCandidates(ctx, "EN")
	if err != nil || len(rows) != 1 {
		t.Fatalf("publishable after delete = %d, %v; want 1", len(rows), err)
	}
	if again := h.run(); again.Removed != 0 || again.Unchanged != 1 {
		t.Fatalf("second scan after delete = %+v, want no further change", again)
	}

	// The key comes back, with the very same bytes: still a new version.
	h.s3.Put("gone.docx", []byte("gone"))
	sum = h.run()
	if sum.NewVersions != 1 {
		t.Fatalf("summary after reappear = %+v", sum)
	}
	vs = h.versionsOf("gone.docx")
	if len(vs) != 2 || vs[0].State != "REMOVED" || vs[1].No != 2 || vs[1].State != "DISCOVERED" || vs[1].Superseded {
		t.Fatalf("gone.docx after reappear = %+v", vs)
	}
}

func TestPaginationVisitsEveryObject(t *testing.T) {
	h := setup(t)
	h.scn.PageSize = 3
	const n = 10
	for i := range n {
		h.s3.Put(fmt.Sprintf("doc-%02d.docx", i), []byte(fmt.Sprintf("body %d", i)))
	}
	if sum := h.run(); sum.Seen != n || sum.NewVersions != n {
		t.Fatalf("summary = %+v, want %d seen and new", sum, n)
	}
	var keys []string
	for _, v := range h.versions() {
		keys = append(keys, v.Key)
	}
	if len(keys) != n || keys[0] != "doc-00.docx" || keys[n-1] != "doc-09.docx" {
		t.Fatalf("keys = %v", keys)
	}
	// Deleting one that lives on a later page removes exactly that one.
	h.s3.Delete("doc-09.docx")
	if sum := h.run(); sum.Removed != 1 || sum.Unchanged != n-1 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestOversizeObjectsAreVersionedUnsupportedWithoutAJob(t *testing.T) {
	h := setup(t)
	h.scn.MaxObjectBytes = 16
	large := []byte("this is more than sixteen bytes")
	h.s3.Put("small.docx", []byte("tiny"))
	h.s3.Put("large.docx", large)
	sum := h.run()
	if sum.Seen != 2 || sum.NewVersions != 2 || sum.Oversize != 1 || sum.Unsupported != 1 || sum.JobsEnqueued != 1 ||
		sum.BytesDownloaded != int64(4+len(large)) {
		t.Fatalf("summary = %+v", sum)
	}
	vs := h.versions()
	if len(vs) != 2 {
		t.Fatalf("versions = %+v", vs)
	}
	for _, v := range vs {
		switch v.Key {
		case "large.docx":
			if v.State != "UNSUPPORTED" || v.ErrCode != "OBJECT_TOO_LARGE" || v.SHA != sum256(large) || v.Size != int64(len(large)) {
				t.Errorf("large.docx = %+v, want UNSUPPORTED/OBJECT_TOO_LARGE with its hash", v)
			}
		case "small.docx":
			if v.State != "DISCOVERED" {
				t.Errorf("small.docx = %+v", v)
			}
		default:
			t.Errorf("unexpected version %+v", v)
		}
	}
	if n := h.count("jobs"); n != 1 {
		t.Fatalf("jobs = %d, want 1 (only small.docx)", n)
	}

	// An unchanged oversize object is not downloaded again.
	if sum := h.run(); sum.Unchanged != 2 || sum.BytesDownloaded != 0 || sum.Oversize != 0 {
		t.Fatalf("second scan = %+v", sum)
	}

	// An existing file that grows past the cap gets a new UNSUPPORTED version;
	// it is not treated as removed.
	h.s3.Put("small.docx", []byte("now this one is far too big"))
	sum = h.run()
	if sum.Removed != 0 || sum.NewVersions != 1 || sum.Oversize != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	vs = h.versionsOf("small.docx")
	if len(vs) != 2 || vs[0].State != "DISCOVERED" || !vs[0].Superseded ||
		vs[1].State != "UNSUPPORTED" || vs[1].ErrCode != "OBJECT_TOO_LARGE" || vs[1].Superseded {
		t.Fatalf("small.docx = %+v", vs)
	}
}

func TestSupersedeAndRemoveClearTheFactTablesTheyFed(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	h.s3.Put("a.xlsx", []byte("v1 workbook"))
	h.s3.Put("a.facts.yaml", []byte("v1 mapping"))
	h.run()
	var wbVer, mapVer, wbFile, mapFile string
	err := h.st.Pool.QueryRow(ctx, `SELECT v.id::text, sf.id::text FROM file_versions v JOIN source_files sf ON sf.id = v.source_file_id
		WHERE sf.object_key = $1`, h.s3.Prefix+"a.xlsx").Scan(&wbVer, &wbFile)
	if err == nil {
		err = h.st.Pool.QueryRow(ctx, `SELECT v.id::text, sf.id::text FROM file_versions v JOIN source_files sf ON sf.id = v.source_file_id
			WHERE sf.object_key = $1`, h.s3.Prefix+"a.facts.yaml").Scan(&mapVer, &mapFile)
	}
	if err != nil {
		t.Fatal(err)
	}
	seed := func(name string) {
		t.Helper()
		_, err := h.st.Pool.Exec(ctx, `INSERT INTO fact_tables (name, workbook_source_file_id, mapping_source_file_id, columns, key_columns, workbook_version_id, mapping_version_id, status)
			VALUES ($1, $2::uuid, $3::uuid, '[]', ARRAY['k'], $4::uuid, $5::uuid, 'AVAILABLE')`, name, wbFile, mapFile, wbVer, mapVer)
		if err != nil {
			t.Fatal(err)
		}
	}
	state := func() (n int, codes string) {
		t.Helper()
		err := h.st.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'UNAVAILABLE' AND workbook_version_id IS NULL AND mapping_version_id IS NULL),
			COALESCE(string_agg(DISTINCT last_error_code, ',') FILTER (WHERE last_error_code IS NOT NULL), '') FROM fact_tables`).Scan(&n, &codes)
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	seed("t_one")
	seed("t_two")
	if n, _ := state(); n != 0 {
		t.Fatalf("seeded tables are already unavailable: %d", n)
	}
	// Superseding the workbook clears both tables that point at it.
	h.s3.Put("a.xlsx", []byte("v2 workbook"))
	h.run()
	if n, codes := state(); n != 2 || codes != "SOURCE_SUPERSEDED" || h.count("fact_tables") != 2 {
		t.Fatalf("after supersede: unavailable=%d codes=%q", n, codes)
	}

	// Removing the mapping clears a table that points at it.
	if _, err := h.st.Pool.Exec(ctx, `UPDATE fact_tables SET status = 'AVAILABLE', last_error_code = NULL, workbook_version_id = $1::uuid, mapping_version_id = $2::uuid WHERE name = 't_one'`, wbVer, mapVer); err != nil {
		t.Fatal(err)
	}
	h.s3.Delete("a.facts.yaml")
	h.run()
	var status, code string
	if err := h.st.Pool.QueryRow(ctx, `SELECT status, COALESCE(last_error_code, '') FROM fact_tables WHERE name = 't_one'`).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "UNAVAILABLE" || code != "SOURCE_REMOVED" {
		t.Fatalf("t_one = %s/%s, want UNAVAILABLE/SOURCE_REMOVED", status, code)
	}
}

func TestPrefixLimitsTheScanAndRemoval(t *testing.T) {
	h := setup(t)
	h.s3.Put("x.docx", []byte("x"))
	h.run()
	// A scanner for a prefix that matches nothing must not remove keys outside it.
	other := *h.scn
	c := *h.s3.Client
	c.Prefix = h.s3.Prefix + "nothing-here/"
	other.S3 = &c
	if sum, err := other.Run(context.Background()); err != nil || sum.Removed != 0 || sum.Seen != 0 {
		t.Fatalf("narrow scan = %+v, %v", sum, err)
	}
	if vs := h.versionsOf("x.docx"); len(vs) != 1 || vs[0].State != "DISCOVERED" {
		t.Fatalf("x.docx = %+v", vs)
	}
}

func TestConcurrentScansDoNotDuplicateVersions(t *testing.T) {
	h := setup(t)
	const files, scanners = 12, 5
	for i := range files {
		h.s3.Put(fmt.Sprintf("c-%02d.docx", i), []byte(fmt.Sprintf("concurrent %d", i)))
	}
	var wg sync.WaitGroup
	sums := make([]scan.Summary, scanners)
	errs := make([]error, scanners)
	for i := range scanners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sums[i], errs[i] = h.scn.Run(context.Background())
		}()
	}
	wg.Wait()
	totalNew, totalUnchanged := 0, 0
	for i := range scanners {
		if errs[i] != nil || sums[i].Errors != 0 {
			t.Fatalf("scan %d: %+v, %v", i, sums[i], errs[i])
		}
		totalNew += sums[i].NewVersions
		totalUnchanged += sums[i].Unchanged
	}
	if totalNew != files || totalUnchanged != files*(scanners-1) {
		t.Fatalf("new=%d unchanged=%d, want %d and %d", totalNew, totalUnchanged, files, files*(scanners-1))
	}
	if h.count("file_versions") != files || h.count("jobs") != files || h.count("source_files") != files {
		t.Fatalf("rows: versions=%d jobs=%d files=%d, want %d each", h.count("file_versions"), h.count("jobs"), h.count("source_files"), files)
	}
}

func TestStateMachinesAllowTheBulkTransitions(t *testing.T) {
	// MarkCandidatesStale moves every non-STALE candidate in one statement; that
	// is only legal while the state machine allows each of those edges.
	for _, s := range domain.CandidateStates() {
		if s != domain.CandidateStale && !s.CanTransition(domain.CandidateStale) {
			t.Errorf("%s -> STALE is not an allowed edge", s)
		}
	}
	for _, s := range domain.FileVersionStates() {
		if s != domain.FileVersionRemoved && !s.CanTransition(domain.FileVersionRemoved) {
			t.Errorf("%s -> REMOVED is not an allowed edge", s)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]scan.Format{
		"a.docx": scan.FormatDocx, "dir/A.DOCX": scan.FormatDocx, "b.xlsx": scan.FormatXlsx,
		"x.facts.yaml": scan.FormatFacts, "x.FACTS.YAML": scan.FormatFacts,
		"x.yaml": scan.FormatUnsupported, "x.facts.yml": scan.FormatUnsupported,
		"a.doc": scan.FormatUnsupported, "a.xls": scan.FormatUnsupported, "a.pdf": scan.FormatUnsupported,
		"docx": scan.FormatUnsupported, "a.docx.bak": scan.FormatUnsupported,
	}
	for k, want := range cases {
		if got := scan.Classify(k); got != want {
			t.Errorf("Classify(%q) = %v, want %v", k, got, want)
		}
	}
}
