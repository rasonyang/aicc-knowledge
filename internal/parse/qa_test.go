// SPDX-License-Identifier: Apache-2.0

package parse_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
)

const qaMappingYAML = `sheets:
  - sheet: FAQ
    headerRows: 1
    question: Question
    answer: Answer
    alternates: [Variants]
  - sheet: 常见问题
    headerRows: 1
    question: 问题
    answer: 回答
    alternates: [其他问法]
    language: ZH
`

type qaRow struct {
	Ordinal    int
	Kind       string
	Path       []string
	Body       string
	Ref        string
	Question   *string
	Alternates []string
	Language   *string
}

func qaRows(t *testing.T, e *parsetest.Env, key string) []qaRow {
	t.Helper()
	v, ok := e.Current(key)
	if !ok {
		t.Fatalf("%s has no current version", key)
	}
	rows, err := e.Store.Pool.Query(context.Background(),
		`SELECT ordinal, kind, heading_path, body, source_ref, qa_question, qa_alternates, qa_language
		 FROM parsed_sections WHERE file_version_id = $1 ORDER BY ordinal`, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []qaRow
	for rows.Next() {
		var r qaRow
		if err := rows.Scan(&r.Ordinal, &r.Kind, &r.Path, &r.Body, &r.Ref, &r.Question, &r.Alternates, &r.Language); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestQAMappingMakesOneSectionPerRow(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	e.Put("qa.qa.yaml", []byte(qaMappingYAML))
	ps := e.ScanParse()
	if ps.Parsed != 2 || ps.Failed != 0 {
		t.Fatalf("parse = %+v", ps)
	}
	mv, _ := e.Current("qa.qa.yaml")
	if mv.State != "PARSED" {
		t.Fatalf("mapping = %+v", mv)
	}
	key := e.ObjectKey("qa.xlsx")
	rows := qaRows(t, e, "qa.xlsx")
	// The Notes sheet stays ordinary content; the two Q&A sheets are not content.
	if len(rows) != 6 || rows[0].Kind != "XLSX_CHUNK" || !slices.Equal(rows[0].Path, []string{"Notes"}) || rows[0].Ordinal != 1 {
		t.Fatalf("sections = %+v", rows)
	}
	for _, r := range rows[1:] {
		if r.Kind != "XLSX_QA_ROW" || r.Question == nil || r.Language == nil {
			t.Errorf("row %+v is not a complete Q&A row", r)
		}
	}
	type want struct {
		ref, q, a, lang string
		alts            []string
	}
	for i, w := range []want{
		{key + "#FAQ!A2", "Does the X1 support wireless charging?", "Yes, it supports wireless charging.", "EN", []string{"wireless charging?", "can I charge it wirelessly?"}},
		{key + "#FAQ!A3", "How long is the warranty?", "The warranty lasts 2 years.", "EN", []string{"warranty period", "how long does the warranty last"}},
		{key + "#FAQ!A6", "How do I reset the device?", "Press and hold the power button for 10 seconds.\n- Wait for the light to blink twice.\n- Release the button.\nThe device restarts.", "EN", []string{}},
		{key + "#FAQ!A7", "Which colors can I choose?", "", "EN", []string{}}, // answer checked by prefix below
		{key + "#常见问题!A2", "是否支持无线充电？", "不支持。", "ZH", []string{"能无线充电吗", "支持无线充吗"}},
	} {
		r := rows[i+1]
		okA := r.Body == w.a || (w.a == "" && strings.HasPrefix(r.Body, "You can choose from black"))
		if r.Ordinal != i+2 || r.Ref != w.ref || *r.Question != w.q || !okA || *r.Language != w.lang || !slices.Equal(r.Alternates, w.alts) || !slices.Equal(r.Path, []string{strings.Split(strings.Split(w.ref, "#")[1], "!")[0]}) {
			t.Errorf("section %d = %+v\nwant %+v", i+1, r, w)
		}
	}
	wv, _ := e.Current("qa.xlsx")
	if got := codes(warnings(t, wv)); !slices.Equal(got, []string{"HIDDEN_ROW_SKIPPED", "QA_ROW_INCOMPLETE"}) {
		t.Errorf("warnings = %v (%s)", got, wv.Warnings)
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE kind = 'GENERATE' AND state = 'QUEUED'`); n != 1 {
		t.Errorf("GENERATE jobs = %d, want 1", n)
	}
}

func TestQAMappingArrivingAfterTheWorkbookDerivesTheRowsAgain(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	e.ScanParse()
	if rows := qaRows(t, e, "qa.xlsx"); len(rows) != 3 || rows[0].Kind != "XLSX_CHUNK" {
		t.Fatalf("before the mapping: %+v", rows)
	}
	e.Put("qa.qa.yaml", []byte(qaMappingYAML))
	if ps := e.ScanParse(); ps.Parsed != 1 {
		t.Fatalf("parse = %+v", ps)
	}
	rows := qaRows(t, e, "qa.xlsx")
	if len(rows) != 6 || rows[0].Kind != "XLSX_CHUNK" || rows[1].Kind != "XLSX_QA_ROW" {
		t.Fatalf("after the mapping: %+v", rows)
	}
	wv, _ := e.Current("qa.xlsx")
	if wv.State != "PARSED" || !slices.Contains(codes(warnings(t, wv)), "QA_ROW_INCOMPLETE") {
		t.Errorf("workbook = %+v", wv)
	}
	// The workbook already had a GENERATE job (it ran for the chunks); the
	// mapping queues it again to top up the new rows.
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE kind = 'GENERATE' AND state = 'QUEUED'`); n != 1 {
		t.Errorf("queued GENERATE jobs = %d, want 1", n)
	}
}

func TestAnInvalidQAMappingFailsOnlyTheMapping(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	for name, yaml := range map[string]string{
		"unknown field": "sheets:\n  - {sheet: FAQ, headerRows: 1, question: Question, answer: Answer, colour: red}\n",
		"no sheets":     "sheets: []\n",
		"bad language":  "sheets:\n  - {sheet: FAQ, headerRows: 1, question: Question, answer: Answer, language: FR}\n",
		"not yaml":      "sheets: [\n",
	} {
		e.Put("qa.qa.yaml", []byte(yaml))
		e.ScanParse()
		mv, _ := e.Current("qa.qa.yaml")
		wv, _ := e.Current("qa.xlsx")
		if mv.State != "PARSE_FAILED" || mv.ErrCode != "MAPPING_INVALID" || len(warnings(t, mv)) == 0 || wv.State != "PARSED" {
			t.Errorf("%s: mapping %+v workbook %+v", name, mv, wv)
		}
		if rows := qaRows(t, e, "qa.xlsx"); len(rows) != 3 || rows[0].Kind != "XLSX_CHUNK" {
			t.Errorf("%s: the workbook's sections changed: %+v", name, rows)
		}
	}
}

func TestQAHeaderProblemsBlameTheWorkbook(t *testing.T) {
	bad := strings.Replace(qaMappingYAML, "question: Question", "question: Nonexistent", 1)
	// Together: the mapping parses, the workbook fails QA_INVALID.
	e := parsetest.New(t)
	e.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	e.Put("qa.qa.yaml", []byte(bad))
	ps := e.ScanParse()
	wv, _ := e.Current("qa.xlsx")
	mv, _ := e.Current("qa.qa.yaml")
	if ps.Parsed != 1 || ps.Failed != 1 || wv.State != "PARSE_FAILED" || wv.ErrCode != "QA_INVALID" || mv.State != "PARSED" {
		t.Fatalf("parse %+v workbook %+v mapping %+v", ps, wv, mv)
	}
	if got := codes(warnings(t, wv)); !slices.Contains(got, "HEADER_NOT_FOUND") {
		t.Errorf("warnings = %v", got)
	}
	// A fixed mapping is a new version; the failed workbook is retried by the operator.
	e.Put("qa.qa.yaml", []byte(qaMappingYAML))
	e.ScanParse()
	if _, err := e.Worker.RetryFailed(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.Parse()
	if wv, _ := e.Current("qa.xlsx"); wv.State != "PARSED" {
		t.Fatalf("after the retry: %+v", wv)
	}
	if rows := qaRows(t, e, "qa.xlsx"); len(rows) != 6 {
		t.Errorf("sections = %d, want 6", len(rows))
	}

	// Mapping after a PARSED workbook: the issues go to the workbook's warnings,
	// the mapping stays PARSED and the sections stay as they were.
	e2 := parsetest.New(t)
	e2.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	e2.ScanParse()
	e2.Put("qa.qa.yaml", []byte(bad))
	e2.ScanParse()
	wv, _ = e2.Current("qa.xlsx")
	mv, _ = e2.Current("qa.qa.yaml")
	if wv.State != "PARSED" || mv.State != "PARSED" || !slices.Contains(codes(warnings(t, wv)), "HEADER_NOT_FOUND") {
		t.Fatalf("workbook %+v mapping %+v", wv, mv)
	}
	if rows := qaRows(t, e2, "qa.xlsx"); len(rows) != 3 || rows[0].Kind != "XLSX_CHUNK" {
		t.Errorf("sections changed: %+v", rows)
	}
	// A missing sheet is a workbook problem too.
	e3 := parsetest.New(t)
	e3.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	e3.Put("qa.qa.yaml", []byte(strings.Replace(qaMappingYAML, "sheet: FAQ", "sheet: Gone", 1)))
	e3.ScanParse()
	if wv, _ := e3.Current("qa.xlsx"); wv.State != "PARSE_FAILED" || wv.ErrCode != "QA_INVALID" || !strings.Contains(wv.Warnings, "SHEET_NOT_FOUND") {
		t.Errorf("missing sheet: %+v", wv)
	}
}

func TestASheetClaimedByFactsAndQAIsACodedError(t *testing.T) {
	e := parsetest.New(t)
	e.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	e.Put("pricing.qa.yaml", []byte("sheets:\n  - {sheet: Pricing, headerRows: 1, question: Plan, answer: Region}\n"))
	e.ScanParse()
	wv, _ := e.Current("pricing.xlsx")
	if wv.State != "PARSE_FAILED" || wv.ErrCode != "QA_SHEET_CONFLICT" || !strings.Contains(wv.Warnings, `"Pricing"`) {
		t.Fatalf("workbook = %+v", wv)
	}
	if tb := table(t, e, "plan_prices"); tb.Status != "UNAVAILABLE" {
		t.Errorf("table = %+v", tb)
	}
}

func TestFactsAndQAMappingsCoverDifferentSheetsOfOneWorkbook(t *testing.T) {
	e := parsetest.New(t)
	e.Put("both.xlsx", parsetest.EditPricing(t, func(f *excelize.File) {
		if _, err := f.NewSheet("FAQ"); err != nil {
			t.Fatal(err)
		}
		for ref, v := range map[string]string{"A1": "Question", "B1": "Answer", "A2": "Is there a trial?", "B2": "Yes, for thirty days."} {
			if err := f.SetCellValue("FAQ", ref, v); err != nil {
				t.Fatal(err)
			}
		}
	}))
	e.Put("both.facts.yaml", parsetest.PricingMapping(t))
	e.Put("both.qa.yaml", []byte("sheets:\n  - {sheet: FAQ, headerRows: 1, question: Question, answer: Answer}\n"))
	e.ScanParse()
	wv, _ := e.Current("both.xlsx")
	if wv.State != "PARSED" {
		t.Fatalf("workbook = %+v", wv)
	}
	if tb := table(t, e, "plan_prices"); tb.Status != "AVAILABLE" || tb.Rows != 5 {
		t.Errorf("table = %+v", tb)
	}
	rows := qaRows(t, e, "both.xlsx")
	if len(rows) != 1 || rows[0].Kind != "XLSX_QA_ROW" || rows[0].Body != "Yes, for thirty days." || *rows[0].Language != "EN" {
		t.Errorf("sections = %+v, want the single Q&A row (Pricing is facts-only, FAQ is Q&A-only)", rows)
	}
	// A facts mapping that arrives later must keep the Q&A rows.
	e2 := parsetest.New(t)
	e2.Put("both.xlsx", parsetest.EditPricing(t, func(f *excelize.File) {
		_, _ = f.NewSheet("FAQ")
		_ = f.SetCellValue("FAQ", "A1", "Question")
		_ = f.SetCellValue("FAQ", "B1", "Answer")
		_ = f.SetCellValue("FAQ", "A2", "Is there a trial?")
		_ = f.SetCellValue("FAQ", "B2", "Yes, for thirty days.")
	}))
	e2.Put("both.qa.yaml", []byte("sheets:\n  - {sheet: FAQ, headerRows: 1, question: Question, answer: Answer}\n"))
	e2.ScanParse()
	e2.Put("both.facts.yaml", parsetest.PricingMapping(t))
	e2.ScanParse()
	if rows := qaRows(t, e2, "both.xlsx"); len(rows) != 1 || rows[0].Kind != "XLSX_QA_ROW" {
		t.Errorf("after the facts mapping: %+v", rows)
	}
}
