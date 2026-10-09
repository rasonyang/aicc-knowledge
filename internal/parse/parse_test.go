// SPDX-License-Identifier: Apache-2.0

package parse_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/facts"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
)

type sectionRow struct {
	Ordinal     int
	Kind        string
	HeadingPath []string
	Level       int
	Body        string
	SourceRef   string
}

func sections(t *testing.T, e *parsetest.Env, key string) []sectionRow {
	t.Helper()
	v, ok := e.Current(key)
	if !ok {
		t.Fatalf("%s has no current version", key)
	}
	return sectionsOf(t, e, v.ID)
}

func sectionsOf(t *testing.T, e *parsetest.Env, versionID string) []sectionRow {
	t.Helper()
	rows, err := e.Store.Pool.Query(context.Background(),
		`SELECT ordinal, kind, heading_path, level, body, source_ref FROM parsed_sections WHERE file_version_id = $1 ORDER BY ordinal`, versionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []sectionRow
	for rows.Next() {
		var s sectionRow
		if err := rows.Scan(&s.Ordinal, &s.Kind, &s.HeadingPath, &s.Level, &s.Body, &s.SourceRef); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

type warning struct{ Code, Detail, Location string }

func warnings(t *testing.T, v parsetest.Version) []warning {
	t.Helper()
	var out []warning
	if err := json.Unmarshal([]byte(v.Warnings), &out); err != nil {
		t.Fatalf("parse_warnings %q: %v", v.Warnings, err)
	}
	return out
}

func codes(ws []warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Code
	}
	slices.Sort(out)
	return out
}

func count(t *testing.T, e *parsetest.Env, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.Store.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type tableRow struct {
	Status, Code            string
	WorkbookVer, MappingVer string
	Rows                    int
}

func table(t *testing.T, e *parsetest.Env, name string) tableRow {
	t.Helper()
	var r tableRow
	err := e.Store.Pool.QueryRow(context.Background(), `
		SELECT status, COALESCE(last_error_code, ''), COALESCE(workbook_version_id::text, ''), COALESCE(mapping_version_id::text, ''),
		       (SELECT count(*) FROM fact_rows r WHERE r.fact_table_id = t.id)
		FROM fact_tables t WHERE name = $1`, name).Scan(&r.Status, &r.Code, &r.WorkbookVer, &r.MappingVer, &r.Rows)
	if err != nil {
		t.Fatalf("fact table %s: %v", name, err)
	}
	return r
}

func date(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func lookup(t *testing.T, e *parsetest.Env, plan, at string) (*facts.Match, error) {
	t.Helper()
	return facts.Lookup(context.Background(), e.Store, "plan_prices", map[string]any{"plan": plan}, date(at))
}

func TestEndToEndFixturesThroughS3(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("headings.docx", "docx/headings.docx")
	e.PutFixture("catalog.xlsx", "xlsx/catalog.xlsx")
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	sum := e.Scan()
	if sum.NewVersions != 4 || sum.JobsEnqueued != 4 {
		t.Fatalf("scan = %+v", sum)
	}
	ps := e.Parse()
	if ps.Claimed != 4 || ps.Parsed != 4 || ps.Failed != 0 || ps.Skipped != 0 || ps.Errors != 0 {
		t.Fatalf("parse = %+v", ps)
	}
	for _, k := range []string{"headings.docx", "catalog.xlsx", "pricing.xlsx", "pricing.facts.yaml"} {
		if v, ok := e.Current(k); !ok || v.State != "PARSED" || v.ErrCode != "" {
			t.Errorf("%s = %+v", k, v)
		}
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE state = 'SUCCEEDED'`); n != 4 {
		t.Errorf("succeeded jobs = %d, want 4", n)
	}

	// headings.docx: the golden sections, Chinese heading path included.
	ds := sections(t, e, "headings.docx")
	if len(ds) != 8 {
		t.Fatalf("headings.docx sections = %d, want 8: %+v", len(ds), ds)
	}
	key := e.ObjectKey("headings.docx")
	if ds[0].Ordinal != 0 || len(ds[0].HeadingPath) != 0 || ds[0].Kind != "DOCX_SECTION" || ds[0].SourceRef != key+"#"+"#0" ||
		ds[0].Body != "前言段落，位于第一个标题之前。" {
		t.Errorf("preamble = %+v", ds[0])
	}
	s2 := ds[2]
	if !slices.Equal(s2.HeadingPath, []string{"第一章 概述", "1.1 背景"}) || s2.Level != 2 ||
		s2.SourceRef != key+"#第一章 概述 > 1.1 背景#2" {
		t.Errorf("section 2 = %+v", s2)
	}
	// The paragraph styled "Heading 1" without an outline level is body text.
	if s2.Body != "背景正文。\nHeading 1 trap: styled like a heading but has no outlineLvl" {
		t.Errorf("section 2 body = %q", s2.Body)
	}
	for _, s := range ds {
		if slices.Contains(s.HeadingPath, "Heading 1 trap: styled like a heading but has no outlineLvl") {
			t.Errorf("the trap paragraph became a heading: %+v", s)
		}
	}
	if ds[3].Body != "" || !slices.Equal(ds[3].HeadingPath, []string{"第二章 价格"}) {
		t.Errorf("section 3 = %+v", ds[3])
	}

	// catalog.xlsx: chunks and the warnings that must never be silent.
	cs := sections(t, e, "catalog.xlsx")
	if len(cs) != 2 || cs[0].Kind != "XLSX_CHUNK" || !slices.Equal(cs[0].HeadingPath, []string{"Plans"}) ||
		cs[0].SourceRef != e.ObjectKey("catalog.xlsx")+"#Plans!A2:E9" || !strings.HasPrefix(cs[0].Body, "Category | Plan | Price | Discount | Start\nMobile | Basic | 9.9") ||
		cs[1].Ordinal != 2 || cs[1].SourceRef != e.ObjectKey("catalog.xlsx")+"#Calc!A2:C3" {
		t.Fatalf("catalog sections = %+v", cs)
	}
	cv, _ := e.Current("catalog.xlsx")
	if got := codes(warnings(t, cv)); !slices.Equal(got, []string{"FORMULA_NO_CACHED_VALUE", "HIDDEN_ROW_SKIPPED", "SHEET_HIDDEN_SKIPPED"}) {
		t.Errorf("catalog warnings = %v", got)
	}

	// pricing.xlsx: its only sheet is covered by the mapping, so there are no
	// content sections; the facts import's hidden-row warning is persisted.
	if ps := sections(t, e, "pricing.xlsx"); len(ps) != 0 {
		t.Errorf("pricing.xlsx sections = %+v, want none (facts-only sheet)", ps)
	}
	pv, _ := e.Current("pricing.xlsx")
	if got := codes(warnings(t, pv)); !slices.Equal(got, []string{"HIDDEN_ROW_SKIPPED"}) {
		t.Errorf("pricing warnings = %v", got)
	}
	mv, _ := e.Current("pricing.facts.yaml")
	if string(mv.Warnings) != "[]" {
		t.Errorf("mapping warnings = %s", mv.Warnings)
	}

	// Facts: 5 visible rows, pointer at exactly the current pair.
	tb := table(t, e, "plan_prices")
	if tb.Status != "AVAILABLE" || tb.Rows != 5 || tb.WorkbookVer != pv.ID || tb.MappingVer != mv.ID || tb.Code != "" {
		t.Fatalf("plan_prices = %+v (workbook %s mapping %s)", tb, pv.ID, mv.ID)
	}
	if n := count(t, e, `SELECT count(*) FROM fact_tables`); n != 1 {
		t.Errorf("fact tables = %d", n)
	}
	m, err := lookup(t, e, "Basic", "2024-03-01")
	if err != nil || m == nil {
		t.Fatalf("lookup = %+v, %v", m, err)
	}
	if m.SourceRef != e.ObjectKey("pricing.xlsx")+"#Pricing!A3:G3" || m.Row["monthly_price"].(json.Number).String() != "9.9" ||
		m.ValidFrom == nil || m.ValidFrom.Format(time.DateOnly) != "2024-01-01" || m.ValidTo == nil || m.ValidTo.Format(time.DateOnly) != "2024-07-01" {
		t.Errorf("match = %+v", m)
	}

	// Running again finds nothing to do.
	if ps := e.Parse(); ps.Claimed != 0 {
		t.Errorf("second parse = %+v", ps)
	}
}

func TestWorkbookParsedBeforeItsMappingIsReimportedWhenTheMappingParses(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.ScanParse()
	// Without a mapping the Pricing sheet is ordinary content.
	if ss := sections(t, e, "pricing.xlsx"); len(ss) != 1 || ss[0].SourceRef != e.ObjectKey("pricing.xlsx")+"#Pricing!A2:G9" {
		t.Fatalf("sections before the mapping = %+v, want the Pricing chunk", ss)
	}
	if n := count(t, e, `SELECT count(*) FROM fact_tables`); n != 0 {
		t.Fatalf("fact tables before the mapping = %d", n)
	}

	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	ps := e.ScanParse()
	if ps.Claimed != 1 || ps.Parsed != 1 {
		t.Fatalf("parse = %+v", ps)
	}
	// The mapping's parse re-derives the workbook: facts-only sheet, no sections.
	if ss := sections(t, e, "pricing.xlsx"); len(ss) != 0 {
		t.Errorf("sections after the mapping = %+v, want none", ss)
	}
	wv, _ := e.Current("pricing.xlsx")
	if wv.State != "PARSED" || !slices.Equal(codes(warnings(t, wv)), []string{"HIDDEN_ROW_SKIPPED"}) {
		t.Errorf("workbook = %+v", wv)
	}
	mv, _ := e.Current("pricing.facts.yaml")
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 || tb.WorkbookVer != wv.ID || tb.MappingVer != mv.ID {
		t.Fatalf("plan_prices = %+v", tb)
	}
}

func TestCorruptDocxFailsAndRetryFailedRearmsItsJob(t *testing.T) {
	e := parsetest.New(t)
	e.Put("bad.docx", []byte("this is not a zip archive"))
	e.PutFixture("good.docx", "docx/headings.docx")
	e.Put("note.pdf", []byte("%PDF-1.7"))
	e.Scan()
	ps := e.Parse()
	if ps.Claimed != 2 || ps.Parsed != 1 || ps.Failed != 1 {
		t.Fatalf("parse = %+v", ps)
	}
	bad, _ := e.Current("bad.docx")
	if bad.State != "PARSE_FAILED" || bad.ErrCode != "DOCX_CORRUPT" {
		t.Fatalf("bad.docx = %+v", bad)
	}
	if ws := warnings(t, bad); len(ws) != 1 || ws[0].Code != "DOCX_CORRUPT" || ws[0].Detail == "" {
		t.Errorf("bad.docx warnings = %+v", ws)
	}
	if ss := sectionsOf(t, e, bad.ID); len(ss) != 0 {
		t.Errorf("failed version has sections: %+v", ss)
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE state = 'SUCCEEDED' AND attempts = 1`); n != 2 {
		t.Fatalf("finished jobs = %d, want 2", n)
	}

	// Retry: only the PARSE_FAILED version is touched; PARSED and UNSUPPORTED never are.
	rs, err := e.Worker.RetryFailed(context.Background())
	if err != nil || rs.Versions != 1 || rs.JobsQueued != 1 {
		t.Fatalf("retry = %+v, %v", rs, err)
	}
	bad, _ = e.Current("bad.docx")
	if bad.State != "DISCOVERED" || bad.ErrCode != "" || bad.Warnings != "[]" {
		t.Fatalf("after retry bad.docx = %+v", bad)
	}
	if good, _ := e.Current("good.docx"); good.State != "PARSED" {
		t.Errorf("good.docx = %+v", good)
	}
	if pdf, _ := e.Current("note.pdf"); pdf.State != "UNSUPPORTED" || pdf.ErrCode != "UNSUPPORTED_FORMAT" {
		t.Errorf("note.pdf = %+v", pdf)
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE kind = 'PARSE'`); n != 2 {
		t.Fatalf("PARSE jobs = %d, want the same 2 rows (no duplicate)", n)
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE kind = 'GENERATE'`); n != 1 {
		t.Fatalf("GENERATE jobs = %d, want 1 (good.docx only; a failed parse queues none)", n)
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE kind = 'PARSE' AND state = 'QUEUED' AND attempts = 0 AND finished_at IS NULL AND last_error_code IS NULL`); n != 1 {
		t.Fatalf("re-armed jobs = %d, want 1", n)
	}
	// A second retry finds nothing in PARSE_FAILED (bad.docx is DISCOVERED now).
	if rs2, err := e.Worker.RetryFailed(context.Background()); err != nil || rs2.Versions != 0 || rs2.JobsQueued != 0 {
		t.Fatalf("second retry = %+v, %v", rs2, err)
	}

	// Parse again: still corrupt, so PARSE_FAILED again, attempts counted from 0.
	ps = e.Parse()
	if ps.Claimed != 1 || ps.Failed != 1 {
		t.Fatalf("parse after retry = %+v", ps)
	}
	bad, _ = e.Current("bad.docx")
	if bad.State != "PARSE_FAILED" || bad.ErrCode != "DOCX_CORRUPT" {
		t.Fatalf("bad.docx after retry = %+v", bad)
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE state = 'SUCCEEDED' AND attempts = 1`); n != 2 {
		t.Fatalf("jobs succeeded with attempts=1: %d, want 2 (attempts reset by the retry)", n)
	}
}

func TestParserRejectionsBecomeCodedFailures(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("actually-sheet.docx", "xlsx/pricing.xlsx")
	e.PutFixture("legacy.xlsx", "xlsx/legacy.xls")
	e.Put("broken.xlsx", []byte("PK nope"))
	e.PutFixture("bad.facts.yaml", "xlsx/pricing.xlsx") // not YAML mapping content
	e.ScanParse()
	want := map[string][2]string{
		"actually-sheet.docx": {"PARSE_FAILED", "UNSUPPORTED_FORMAT"},
		"legacy.xlsx":         {"PARSE_FAILED", "UNSUPPORTED_FORMAT"},
		"broken.xlsx":         {"PARSE_FAILED", "XLSX_CORRUPT"},
		"bad.facts.yaml":      {"PARSE_FAILED", "MAPPING_INVALID"},
	}
	for k, w := range want {
		v, ok := e.Current(k)
		if !ok || v.State != w[0] || v.ErrCode != w[1] || len(warnings(t, v)) == 0 {
			t.Errorf("%s = %+v, want %v with details", k, v, w)
		}
	}
	if n := count(t, e, `SELECT count(*) FROM file_versions WHERE state = 'PARSE_FAILED'`); n != 4 {
		t.Errorf("failed versions = %d, want 4", n)
	}
}

func TestSupersededAndRemovedVersionsAreSkippedWithoutStateChange(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("a.docx", "docx/headings.docx")
	e.PutFixture("gone.docx", "docx/tables.docx")
	e.Scan()
	e.PutFixture("a.docx", "docx/tracked.docx") // supersedes v1 before it was parsed
	e.S3.Delete("gone.docx")                    // removed before it was parsed
	e.Scan()
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE state = 'QUEUED'`); n != 3 {
		t.Fatalf("queued jobs = %d, want 3", n)
	}
	ps := e.Parse()
	if ps.Claimed != 3 || ps.Parsed != 1 || ps.Skipped != 2 || ps.Failed != 0 || ps.Errors != 0 {
		t.Fatalf("parse = %+v", ps)
	}
	rows, err := e.Store.Pool.Query(context.Background(), `
		SELECT sf.object_key, v.version_no, v.state, (SELECT count(*) FROM parsed_sections s WHERE s.file_version_id = v.id)
		FROM file_versions v JOIN source_files sf ON sf.id = v.source_file_id ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type r struct {
		key      string
		no       int
		state    string
		sections int
	}
	var got []r
	for rows.Next() {
		var x r
		if err := rows.Scan(&x.key, &x.no, &x.state, &x.sections); err != nil {
			t.Fatal(err)
		}
		x.key = strings.TrimPrefix(x.key, e.S3.Prefix)
		got = append(got, x)
	}
	if len(got) != 3 {
		t.Fatalf("versions = %+v", got)
	}
	if got[0].key != "a.docx" || got[0].no != 1 || got[0].state != "DISCOVERED" || got[0].sections != 0 {
		t.Errorf("superseded v1 = %+v, want untouched DISCOVERED without sections", got[0])
	}
	if got[1].key != "a.docx" || got[1].no != 2 || got[1].state != "PARSED" || got[1].sections == 0 {
		t.Errorf("current v2 = %+v", got[1])
	}
	if got[2].key != "gone.docx" || got[2].state != "REMOVED" || got[2].sections != 0 {
		t.Errorf("removed = %+v", got[2])
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE state = 'SUCCEEDED'`); n != 3 {
		t.Errorf("succeeded jobs = %d, want 3 (skipped jobs are completed)", n)
	}
}

func TestChangedObjectAfterTheScanIsSkippedNotParsedUnderTheOldVersion(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("doc.docx", "docx/headings.docx")
	e.Scan()
	e.PutFixture("doc.docx", "docx/tables.docx") // overwritten between scan and parse
	ps := e.Parse()
	if ps.Claimed != 1 || ps.Skipped != 1 || ps.Parsed != 0 {
		t.Fatalf("parse = %+v", ps)
	}
	v, _ := e.Current("doc.docx")
	if v.State != "DISCOVERED" || v.No != 1 || len(sectionsOf(t, e, v.ID)) != 0 {
		t.Fatalf("version = %+v, want untouched", v)
	}
	// The next scan creates the version these bytes belong to.
	ps = e.ScanParse()
	if ps.Claimed != 1 || ps.Parsed != 1 {
		t.Fatalf("parse after rescan = %+v", ps)
	}
	v, _ = e.Current("doc.docx")
	if v.No != 2 || v.State != "PARSED" || len(sectionsOf(t, e, v.ID)) == 0 {
		t.Fatalf("v2 = %+v", v)
	}
}

func TestAJobForAMissingVersionFailsTheJobNotTheRun(t *testing.T) {
	e := parsetest.New(t)
	_, err := e.Store.Pool.Exec(context.Background(),
		`INSERT INTO jobs (kind, payload, dedupe_key, max_attempts, run_after) VALUES ('PARSE', jsonb_build_object('fileVersionId', $1::text), 'x', 1, '-infinity')`, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	ps := e.Parse()
	if ps.Claimed != 1 || ps.Errors != 1 || ps.Parsed != 0 {
		t.Fatalf("parse = %+v", ps)
	}
	var state, code string
	if err := e.Store.Pool.QueryRow(context.Background(), `SELECT state, last_error_code FROM jobs`).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "FAILED" || code != "FILE_VERSION_NOT_FOUND" {
		t.Errorf("job = %s/%s", state, code)
	}
}

func brokenPricing(t *testing.T) []byte {
	return parsetest.EditPricing(t, func(f *excelize.File) {
		if err := f.SetCellValue("Pricing", "B3", "not a price"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFactsLifecycle(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e.ScanParse()
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 {
		t.Fatalf("after import: %+v", tb)
	}

	// Both validity periods of one key, a different key, and the half-open edge.
	for _, c := range []struct {
		plan, at, price string
	}{
		{"Basic", "2024-01-01", "9.9"}, {"Basic", "2024-06-30", "9.9"},
		{"Basic", "2024-07-01", "8.9"}, {"Basic", "2030-01-01", "8.9"},
		{"Max", "2024-01-01", "0.1"},
	} {
		m, err := lookup(t, e, c.plan, c.at)
		if err != nil || m == nil || m.Row["monthly_price"].(json.Number).String() != c.price {
			t.Errorf("%s at %s = %+v, %v, want price %s", c.plan, c.at, m, err, c.price)
		}
	}
	if m, err := lookup(t, e, "Basic", "2023-12-31"); err != nil || m != nil {
		t.Errorf("before every period = %+v, %v, want no row", m, err)
	}
	if m, err := lookup(t, e, "Nope", "2024-03-01"); err != nil || m != nil {
		t.Errorf("unknown key = %+v, %v, want no row", m, err)
	}
	// The hidden sheet row is not imported: exactly the 5 visible rows.
	if n := count(t, e, `SELECT count(*) FROM fact_rows`); n != 5 {
		t.Errorf("fact rows = %d", n)
	}

	// 1. The workbook changes to one with an invalid row.
	e.Put("pricing.xlsx", brokenPricing(t))
	ps := e.ScanParse()
	if ps.Claimed != 1 || ps.Failed != 1 {
		t.Fatalf("parse of the broken workbook = %+v", ps)
	}
	wv, _ := e.Current("pricing.xlsx")
	if wv.State != "PARSE_FAILED" || wv.ErrCode != "FACTS_INVALID" {
		t.Fatalf("workbook = %+v", wv)
	}
	ws := warnings(t, wv)
	if len(ws) == 0 || ws[0].Code != "CELL_TYPE_MISMATCH" || ws[0].Location != "Pricing!B3" {
		t.Errorf("issues = %+v", ws)
	}
	if tb := table(t, e, "plan_prices"); tb.Status != "UNAVAILABLE" || tb.Code != "FACTS_INVALID" || tb.WorkbookVer != "" || tb.MappingVer != "" {
		t.Fatalf("after the invalid workbook: %+v", tb)
	}
	var ue *facts.UnavailableError
	if _, err := lookup(t, e, "Basic", "2024-03-01"); !errors.As(err, &ue) || ue.Code != "FACTS_INVALID" {
		t.Fatalf("lookup after invalid workbook: %v, want unavailable (old rows must not be served)", err)
	}
	if sections(t, e, "pricing.xlsx"); count(t, e, `SELECT count(*) FROM parsed_sections s JOIN file_versions v ON v.id = s.file_version_id WHERE v.state = 'PARSE_FAILED'`) != 0 {
		t.Error("a failed version has sections")
	}

	// 2. The workbook is fixed (and a price differs): AVAILABLE again with the new rows only.
	e.Put("pricing.xlsx", parsetest.EditPricing(t, func(f *excelize.File) {
		if err := f.SetCellValue("Pricing", "B3", 7.5); err != nil {
			t.Fatal(err)
		}
	}))
	ps = e.ScanParse()
	if ps.Claimed != 1 || ps.Parsed != 1 {
		t.Fatalf("parse of the fixed workbook = %+v", ps)
	}
	wv, _ = e.Current("pricing.xlsx")
	mv, _ := e.Current("pricing.facts.yaml")
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 || tb.WorkbookVer != wv.ID || tb.MappingVer != mv.ID || tb.Code != "" {
		t.Fatalf("after the fix: %+v", tb)
	}
	if n := count(t, e, `SELECT count(*) FROM fact_rows`); n != 5 {
		t.Errorf("fact rows = %d, want 5 (the old import is replaced)", n)
	}
	if m, err := lookup(t, e, "Basic", "2024-03-01"); err != nil || m == nil || m.Row["monthly_price"].(json.Number).String() != "7.5" {
		t.Fatalf("after the fix: %+v, %v, want price 7.5", m, err)
	}

	// 3. The mapping is removed: UNAVAILABLE.
	e.S3.Delete("pricing.facts.yaml")
	e.ScanParse()
	if tb := table(t, e, "plan_prices"); tb.Status != "UNAVAILABLE" || tb.Code != "SOURCE_REMOVED" {
		t.Fatalf("after removing the mapping: %+v", tb)
	}
	if _, err := lookup(t, e, "Basic", "2024-03-01"); !errors.As(err, &ue) {
		t.Fatalf("lookup after mapping removal: %v", err)
	}

	// The mapping returns (a new version): AVAILABLE again against the PARSED workbook.
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e.ScanParse()
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 {
		t.Fatalf("after the mapping returned: %+v", tb)
	}

	// 4. The workbook is removed: UNAVAILABLE.
	e.S3.Delete("pricing.xlsx")
	e.ScanParse()
	if tb := table(t, e, "plan_prices"); tb.Status != "UNAVAILABLE" || tb.Code != "SOURCE_REMOVED" || tb.WorkbookVer != "" {
		t.Fatalf("after removing the workbook: %+v", tb)
	}
	if _, err := lookup(t, e, "Basic", "2024-03-01"); !errors.As(err, &ue) {
		t.Fatalf("lookup after workbook removal: %v", err)
	}
	if _, err := facts.Lookup(context.Background(), e.Store, "no_such_table", map[string]any{"plan": "x"}, date("2024-03-01")); !errors.Is(err, facts.ErrTableNotFound) {
		t.Errorf("unknown table: %v", err)
	}
}

func TestWorkbookDataProblemsBlameTheWorkbookNotTheMapping(t *testing.T) {
	e := parsetest.New(t)
	// Broken workbook and mapping arrive together: the mapping (sorted first)
	// parses, the workbook fails FACTS_INVALID.
	e.Put("pricing.xlsx", brokenPricing(t))
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	ps := e.ScanParse()
	if ps.Parsed != 1 || ps.Failed != 1 {
		t.Fatalf("parse = %+v", ps)
	}
	wv, _ := e.Current("pricing.xlsx")
	mv, _ := e.Current("pricing.facts.yaml")
	if wv.State != "PARSE_FAILED" || wv.ErrCode != "FACTS_INVALID" || mv.State != "PARSED" {
		t.Fatalf("workbook %+v mapping %+v", wv, mv)
	}
	if tb := table(t, e, "plan_prices"); tb.Status != "UNAVAILABLE" || tb.Code != "FACTS_INVALID" {
		t.Fatalf("table = %+v", tb)
	}
	// A fixed workbook imports with the still-PARSED mapping, no retry needed.
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	if ps := e.ScanParse(); ps.Claimed != 1 || ps.Parsed != 1 {
		t.Fatalf("parse = %+v", ps)
	}
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 {
		t.Fatalf("after the fix: %+v", tb)
	}

	// A mapping whose headers do not match the sheet is workbook-side: the
	// mapping stays PARSED, the table is UNAVAILABLE, the issues are recorded
	// on the (already PARSED) workbook.
	e2 := parsetest.New(t)
	e2.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e2.ScanParse()
	bad := strings.Replace(string(parsetest.PricingMapping(t)), "header: Plan", "header: Nonexistent", 1)
	e2.Put("pricing.facts.yaml", []byte(bad))
	e2.ScanParse()
	mv, _ = e2.Current("pricing.facts.yaml")
	wv, _ = e2.Current("pricing.xlsx")
	if mv.State != "PARSED" || wv.State != "PARSED" || codes(warnings(t, wv))[0] != "HEADER_NOT_FOUND" {
		t.Fatalf("mapping %+v workbook %+v", mv, wv)
	}
	if tb := table(t, e2, "plan_prices"); tb.Status != "UNAVAILABLE" || tb.Code != "FACTS_INVALID" {
		t.Fatalf("table = %+v", tb)
	}
	// Fixing the mapping re-imports against the PARSED workbook.
	e2.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e2.ScanParse()
	if tb := table(t, e2, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 {
		t.Fatalf("after the mapping fix: %+v", tb)
	}
}

func TestAnInvalidMappingFailsOnlyTheMapping(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.Put("pricing.facts.yaml", []byte("tables:\n  - table: Bad Name\n    sheet: Pricing\n    headerRows: 0\n    columns: []\n    keyColumns: []\n"))
	e.ScanParse()
	mv, _ := e.Current("pricing.facts.yaml")
	wv, _ := e.Current("pricing.xlsx")
	if mv.State != "PARSE_FAILED" || mv.ErrCode != "MAPPING_INVALID" || len(warnings(t, mv)) < 3 || wv.State != "PARSED" {
		t.Fatalf("mapping %+v workbook %+v", mv, wv)
	}
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e.ScanParse()
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 {
		t.Fatalf("after the mapping fix: %+v", tb)
	}
	if mv, _ = e.Current("pricing.facts.yaml"); mv.State != "PARSED" {
		t.Fatalf("mapping %+v", mv)
	}
}

func TestTwoMappingsClaimingOneTableNameFailsTheSecond(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e.ScanParse()
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" {
		t.Fatalf("first mapping: %+v", tb)
	}

	// Another workbook with its own mapping declares the same table name.
	e.PutFixture("other.xlsx", "xlsx/pricing.xlsx")
	e.Put("other.facts.yaml", parsetest.PricingMapping(t))
	ps := e.ScanParse()
	if ps.Claimed != 2 || ps.Failed != 1 || ps.Parsed != 1 {
		t.Fatalf("parse = %+v", ps)
	}
	ov, _ := e.Current("other.facts.yaml")
	if ov.State != "PARSE_FAILED" || ov.ErrCode != "FACT_TABLE_NAME_CONFLICT" {
		t.Fatalf("second mapping = %+v", ov)
	}
	if ws := warnings(t, ov); len(ws) != 1 || ws[0].Code != "FACT_TABLE_NAME_CONFLICT" || ws[0].Location != "plan_prices" {
		t.Errorf("conflict details = %+v", ws)
	}
	// The first owner is untouched and still served.
	pv, _ := e.Current("pricing.xlsx")
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 || tb.WorkbookVer != pv.ID {
		t.Fatalf("owner after the conflict: %+v", tb)
	}
	if n := count(t, e, `SELECT count(*) FROM fact_tables`); n != 1 {
		t.Errorf("fact tables = %d, want 1", n)
	}

	// Once the owner's mapping is removed the name is free for the other file.
	e.S3.Delete("pricing.facts.yaml")
	e.Scan()
	if _, err := e.Worker.RetryFailed(context.Background()); err != nil {
		t.Fatal(err)
	}
	ps = e.Parse()
	if ps.Parsed != 1 {
		t.Fatalf("parse after the owner left = %+v", ps)
	}
	ov, _ = e.Current("other.facts.yaml")
	ow, _ := e.Current("other.xlsx")
	if tb := table(t, e, "plan_prices"); ov.State != "PARSED" || tb.Status != "AVAILABLE" || tb.WorkbookVer != ow.ID || tb.MappingVer != ov.ID {
		t.Fatalf("after takeover: mapping %+v table %+v", ov, tb)
	}
}

func TestAMappingForAMissingWorkbookRegistersTheTableUnavailable(t *testing.T) {
	e := parsetest.New(t)
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e.ScanParse()
	if v, _ := e.Current("pricing.facts.yaml"); v.State != "PARSED" {
		t.Fatalf("mapping = %+v", v)
	}
	if tb := table(t, e, "plan_prices"); tb.Status != "UNAVAILABLE" || tb.Code != "WORKBOOK_NOT_PARSED" || tb.Rows != 0 {
		t.Fatalf("table = %+v", tb)
	}
	// The workbook arrives: its parse imports against the PARSED mapping.
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.ScanParse()
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 {
		t.Fatalf("table after the workbook arrived = %+v", tb)
	}
}

func TestAMappingThatDropsATableMakesItUnavailable(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	two := string(parsetest.PricingMapping(t))
	second := strings.Replace(strings.TrimPrefix(two, "tables:\n"), "plan_prices", "plan_prices_b", 1)
	e.Put("pricing.facts.yaml", []byte(two+second))
	e.ScanParse()
	if n := count(t, e, `SELECT count(*) FROM fact_tables WHERE status = 'AVAILABLE'`); n != 2 {
		t.Fatalf("available tables = %d, want 2", n)
	}
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e.ScanParse()
	if a, b := table(t, e, "plan_prices"), table(t, e, "plan_prices_b"); a.Status != "AVAILABLE" || b.Status != "UNAVAILABLE" || b.Code != "TABLE_NOT_DECLARED" || a.Rows != 5 {
		t.Fatalf("tables = %+v / %+v", a, b)
	}
}

func TestConcurrentWorkersConvergeOnTheImportInEitherOrder(t *testing.T) {
	e := parsetest.New(t)
	const pairs = 6
	for i := range pairs {
		name := "p" + string(rune('a'+i))
		e.PutFixture(name+".xlsx", "xlsx/pricing.xlsx")
		e.Put(name+".facts.yaml", []byte(strings.Replace(string(parsetest.PricingMapping(t)), "plan_prices", "plan_prices_"+name, 1)))
	}
	e.Scan()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := range 3 {
		w := *e.Worker
		w.Name = "w" + string(rune('0'+i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.Run(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, e, `SELECT count(*) FROM file_versions WHERE state = 'PARSED'`); n != 2*pairs {
		t.Fatalf("parsed versions = %d, want %d", n, 2*pairs)
	}
	if n := count(t, e, `SELECT count(*) FROM fact_tables WHERE status = 'AVAILABLE'`); n != pairs {
		t.Fatalf("available tables = %d, want %d", n, pairs)
	}
	if n := count(t, e, `SELECT count(*) FROM fact_rows`); n != 5*pairs {
		t.Fatalf("fact rows = %d, want %d", n, 5*pairs)
	}
	for i := range pairs {
		name := "p" + string(rune('a'+i))
		if ss := sections(t, e, name+".xlsx"); len(ss) != 0 {
			t.Errorf("%s.xlsx kept %d content sections of a facts-only sheet", name, len(ss))
		}
	}
	// Every pointer names the current pair.
	if n := count(t, e, `SELECT count(*) FROM fact_tables t
		JOIN file_versions w ON w.id = t.workbook_version_id AND w.superseded_at IS NULL AND w.state = 'PARSED'
		JOIN file_versions m ON m.id = t.mapping_version_id AND m.superseded_at IS NULL AND m.state = 'PARSED'`); n != pairs {
		t.Fatalf("tables pointing at the current pair = %d, want %d", n, pairs)
	}
}

func TestParseOutcomesAreCountedAndTimed(t *testing.T) {
	ctx := context.Background()
	p, err := obs.Setup(ctx, obs.Options{ServiceName: "test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })
	e := parsetest.New(t)
	e.Worker.Metrics = p.Metrics
	e.PutFixture("a.docx", "docx/headings.docx")
	e.Put("bad.docx", []byte("nope"))
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.Scan()
	e.PutFixture("a.docx", "docx/tracked.docx") // a.docx v1 is skipped
	e.ScanParse()
	rec := httptest.NewRecorder()
	p.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	text := rec.Body.String()
	for _, want := range []string{
		obs.MetricParseJobsTotal + `{kind="DOCX",outcome="PARSED"`,
		obs.MetricParseJobsTotal + `{kind="DOCX",outcome="PARSE_FAILED"`,
		obs.MetricParseJobsTotal + `{kind="DOCX",outcome="SKIPPED"`,
		obs.MetricParseJobsTotal + `{kind="XLSX",outcome="PARSED"`,
		obs.MetricParseSeconds + `_count{kind="DOCX"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
}
