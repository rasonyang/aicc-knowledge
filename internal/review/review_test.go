// SPDX-License-Identifier: Apache-2.0

package review_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/generate"
	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
	"github.com/rasonyang/aicc-knowledge/internal/review"
)

var limits = candidate.Limits{MaxAnswerEN: 240, MaxAnswerZH: 90}

// fixture builds the world of a review: faq_en.docx (6 candidates: two per
// section) and faq_zh.docx (3 candidates: one per section), generated through
// the real pipeline with a scripted LLM. The Billing answers of the English
// document carry figures.
type fixture struct {
	T   *testing.T
	Env *parsetest.Env
	Dir string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := parsetest.New(t)
	srv := llmtest.New(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		u := c.User()
		heading := strings.TrimPrefix(strings.SplitN(u, "\n", 2)[0], "Section heading: ")
		if strings.ContainsAny(heading, "账单退款套餐") {
			return llmtest.Reply(llmtest.Cand{Question: "关于" + heading + "有什么规定？", Answer: "请参阅" + heading + "的规定。", Language: "ZH"}), nil
		}
		answerB := "Please read the " + heading + " rules for details."
		if heading == "Billing" {
			answerB = "You must pay within 15 days."
		}
		return llmtest.Reply(
			llmtest.Cand{Question: "What is the " + heading + " policy?", Alts: []string{"Tell me about " + heading + "."}, Answer: "The " + heading + " policy is in the handbook.", Language: "EN"},
			llmtest.Cand{Question: "Where can I read about " + heading + "?", Answer: answerB, Language: "EN"},
		), nil
	})
	w := &generate.Worker{Store: env.Store, LLM: srv.Client(t), Limits: limits}
	env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	env.PutFixture("faq_zh.docx", "docx/faq_zh.docx")
	env.ScanParse()
	sum, err := w.Run(context.Background())
	if err != nil || sum.Candidates != 9 {
		t.Fatalf("generate = %+v, %v", sum, err)
	}
	return &fixture{T: t, Env: env, Dir: t.TempDir()}
}

func (fx *fixture) export(opts review.ExportOptions) (path string, n int) {
	fx.T.Helper()
	f, n, err := review.Export(context.Background(), fx.Env.Store, opts)
	if err != nil {
		fx.T.Fatal(err)
	}
	defer f.Close()
	path = filepath.Join(fx.Dir, fmt.Sprintf("review-%d.xlsx", len(fx.T.Name())+n))
	if err := f.SaveAs(path); err != nil {
		fx.T.Fatal(err)
	}
	return path, n
}

func (fx *fixture) importFile(path string) review.Summary {
	fx.T.Helper()
	sum, err := review.Import(context.Background(), fx.Env.Store, path, review.ImportOptions{Reviewer: "alice", Limits: limits})
	if err != nil {
		fx.T.Fatal(err)
	}
	return sum
}

// edit opens a workbook, applies f and saves it in place.
func edit(t *testing.T, path string, f func(x *excelize.File)) {
	t.Helper()
	x, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	f(x)
	if err := x.Save(); err != nil {
		t.Fatal(err)
	}
}

func cellAt(col string, row int) string { return fmt.Sprintf("%s%d", col, row) }

func colOf(name string) string {
	i := slices.Index(review.Columns, name)
	l, _ := excelize.ColumnNumberToName(i + 1)
	return l
}

func set(t *testing.T, x *excelize.File, row int, col, value string) {
	t.Helper()
	if err := x.SetCellStr(review.SheetReview, cellAt(colOf(col), row), value); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, x *excelize.File, row int, col string) string {
	t.Helper()
	v, err := x.GetCellValue(review.SheetReview, cellAt(colOf(col), row))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type state struct{ State, Question, Answer, Note string }

func (fx *fixture) states() map[string]state {
	fx.T.Helper()
	rows, err := fx.Env.Store.Pool.Query(context.Background(), `SELECT id::text, state, question, answer, COALESCE(review_note, '') FROM candidates`)
	if err != nil {
		fx.T.Fatal(err)
	}
	defer rows.Close()
	out := map[string]state{}
	for rows.Next() {
		var id string
		var s state
		if err := rows.Scan(&id, &s.State, &s.Question, &s.Answer, &s.Note); err != nil {
			fx.T.Fatal(err)
		}
		out[id] = s
	}
	return out
}

func (fx *fixture) count(query string, args ...any) int {
	fx.T.Helper()
	var n int
	if err := fx.Env.Store.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		fx.T.Fatal(err)
	}
	return n
}

func TestExportedWorkbookCarriesItsProtections(t *testing.T) {
	fx := newFixture(t)
	path, n := fx.export(review.ExportOptions{})
	if n != 9 {
		t.Fatalf("exported = %d, want 9", n)
	}
	x, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()

	// Sheets and header.
	if got := x.GetSheetList(); !slices.Equal(got, []string{review.SheetReview, review.SheetInfo}) {
		t.Fatalf("sheets = %v", got)
	}
	if visible, _ := x.GetSheetVisible(review.SheetInfo); visible {
		t.Error("the info sheet is visible")
	}
	if visible, _ := x.GetSheetVisible(review.SheetReview); !visible {
		t.Error("the review sheet is hidden")
	}
	grid, err := x.GetRows(review.SheetReview)
	if err != nil || len(grid) != 10 {
		t.Fatalf("rows = %d, %v; want 10 (header + 9)", len(grid), err)
	}
	if !slices.Equal(grid[0], review.Columns) {
		t.Fatalf("header = %v", grid[0])
	}
	for r := 2; r <= 10; r++ {
		if get(t, x, r, review.ColAction) != "" {
			t.Errorf("row %d has a preset action", r)
		}
		if h := get(t, x, r, review.ColContentHash); len(h) != 64 {
			t.Errorf("row %d hash = %q", r, h)
		}
		if ex := get(t, x, r, review.ColSourceExcerpt); ex == "" || len([]rune(ex)) > review.ExcerptChars+3 {
			t.Errorf("row %d excerpt has %d chars", r, len([]rune(ex)))
		}
	}
	if get(t, x, 2, review.ColLanguage) != "EN" || get(t, x, 10, review.ColLanguage) != "ZH" {
		t.Errorf("rows are not ordered by source: first %q last %q", get(t, x, 2, review.ColLanguage), get(t, x, 10, review.ColLanguage))
	}
	if !strings.Contains(get(t, x, 2, review.ColAlternateQuestions), "Tell me about Billing.") ||
		get(t, x, 3, review.ColFlags) != "CONTAINS_FIGURES" || get(t, x, 2, review.ColFlags) != "" {
		t.Errorf("alternates %q flags %q/%q", get(t, x, 2, review.ColAlternateQuestions), get(t, x, 2, review.ColFlags), get(t, x, 3, review.ColFlags))
	}

	// The content hash column is hidden; the others are visible.
	for _, c := range review.Columns {
		visible, err := x.GetColVisible(review.SheetReview, colOf(c))
		if err != nil || visible != (c != review.ColContentHash) {
			t.Errorf("column %s visible = %v, %v", c, visible, err)
		}
	}
	// The action column has a list validation of exactly the three actions, covering every data row.
	dvs, err := x.GetDataValidations(review.SheetReview)
	if err != nil || len(dvs) != 1 {
		t.Fatalf("data validations = %d, %v", len(dvs), err)
	}
	if dvs[0].Type != "list" || dvs[0].Sqref != "C2:C10" || dvs[0].Formula1 != `"APPROVE,REJECT,EDIT"` || !dvs[0].AllowBlank {
		t.Errorf("validation = %+v", dvs[0])
	}
	// The used range covers the data (some readers trust it).
	if dim, err := x.GetSheetDimension(review.SheetReview); err != nil || dim != "A1:K10" {
		t.Errorf("dimension = %q, %v; want A1:K10", dim, err)
	}
	// The header is frozen.
	if p, err := x.GetPanes(review.SheetReview); err != nil || !p.Freeze || p.YSplit != 1 {
		t.Errorf("panes = %+v, %v", p, err)
	}
	// The sheet is protected; only the five editable columns are unlocked.
	raw := sheetXML(t, path)
	if !strings.Contains(raw, `<sheetProtection`) || !(strings.Contains(raw, `sheet="true"`) || strings.Contains(raw, `sheet="1"`)) {
		t.Errorf("sheet protection missing from the sheet XML: %s", raw[max(0, len(raw)-900):])
	}
	editable := map[string]bool{review.ColAction: true, review.ColQuestion: true, review.ColAlternateQuestions: true, review.ColAnswer: true, review.ColReviewNote: true}
	for _, c := range review.Columns {
		for _, row := range []int{2, 10} {
			id, err := x.GetCellStyle(review.SheetReview, cellAt(colOf(c), row))
			if err != nil {
				t.Fatal(err)
			}
			st, err := x.GetStyle(id)
			if err != nil || st.Protection == nil {
				t.Fatalf("style of %s%d = %+v, %v", colOf(c), row, st, err)
			}
			if st.Protection.Locked == editable[c] {
				t.Errorf("cell %s%d (%s): locked = %v, want %v", colOf(c), row, c, st.Protection.Locked, !editable[c])
			}
		}
	}
	// The info sheet records the filters.
	info, _ := x.GetRows(review.SheetInfo)
	want := map[string]string{"format": review.FormatVersion, "states": "PENDING_REVIEW", "language": "ALL", "rows": "9"}
	got := map[string]string{}
	for _, r := range info {
		got[r[0]] = r[1]
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("info %s = %q, want %q", k, got[k], v)
		}
	}
	if got["exportedAt"] == "" || len(info) != 5 {
		t.Errorf("info = %v", info)
	}
}

func sheetXML(t *testing.T, path string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := f.Open()
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
	}
	t.Fatal("no sheet1.xml")
	return ""
}

func TestExportIsDeterministicAndFilters(t *testing.T) {
	fx := newFixture(t)
	read := func(opts review.ExportOptions) [][]string {
		path, _ := fx.export(opts)
		x, err := excelize.OpenFile(path)
		if err != nil {
			t.Fatal(err)
		}
		defer x.Close()
		g, _ := x.GetRows(review.SheetReview)
		return g
	}
	a, b := read(review.ExportOptions{}), read(review.ExportOptions{})
	if len(a) != 10 || !slices.EqualFunc(a, b, slices.Equal[[]string]) {
		t.Fatalf("two exports differ or have %d rows", len(a))
	}
	if zh := read(review.ExportOptions{Language: domain.LanguageZH}); len(zh) != 4 {
		t.Errorf("ZH rows = %d, want 3 + header", len(zh))
	}
	if en := read(review.ExportOptions{Language: domain.LanguageEN}); len(en) != 7 {
		t.Errorf("EN rows = %d, want 6 + header", len(en))
	}
	// No candidate is APPROVED yet: an empty export is a valid workbook with a header.
	empty := read(review.ExportOptions{States: []domain.CandidateState{domain.CandidateApproved}})
	if len(empty) != 1 || !slices.Equal(empty[0], review.Columns) {
		t.Errorf("empty export = %v", empty)
	}
	if _, _, err := review.Export(context.Background(), fx.Env.Store, review.ExportOptions{Language: "FR"}); err == nil {
		t.Error("language FR accepted")
	}
	if _, _, err := review.Export(context.Background(), fx.Env.Store, review.ExportOptions{States: []domain.CandidateState{"DONE"}}); err == nil {
		t.Error("state DONE accepted")
	}
}

func TestRoundTripApproveRejectEditSkip(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	path, _ := fx.export(review.ExportOptions{})
	var ids []string
	edit(t, path, func(x *excelize.File) {
		for r := 2; r <= 10; r++ {
			ids = append(ids, get(t, x, r, review.ColID))
		}
		// rows 2,3 approve; 4,5 reject; 6 edit (adds a figure); 7 edit (no change);
		// 8 left empty; 9 approve with a note; 10 left empty.
		set(t, x, 2, review.ColAction, "APPROVE")
		set(t, x, 3, review.ColAction, "approve") // lower case is accepted
		set(t, x, 4, review.ColAction, "REJECT")
		set(t, x, 5, review.ColAction, "REJECT")
		set(t, x, 5, review.ColAnswer, "edits of a rejected row are ignored")
		set(t, x, 6, review.ColAction, "EDIT")
		set(t, x, 6, review.ColAnswer, "  The Refunds policy   is 30 days.  ")
		set(t, x, 6, review.ColAlternateQuestions, "Refund rules?\n\nRefund rules?\nWhat are the refund terms?")
		set(t, x, 6, review.ColReviewNote, "figure checked")
		set(t, x, 7, review.ColAction, "EDIT")
		set(t, x, 9, review.ColAction, "APPROVE")
		set(t, x, 9, review.ColReviewNote, "ok")
	})
	before := fx.states()
	sum := fx.importFile(path)

	wantCounts := map[string]int{review.OutcomeApproved: 3, review.OutcomeRejected: 2, review.OutcomeEdited: 2, review.OutcomeSkipped: 2}
	if sum.Rows != 9 || len(sum.Results) != 9 || !sum.Clean() || fmt.Sprint(sum.Counts) != fmt.Sprint(wantCounts) {
		t.Fatalf("summary = rows %d results %d counts %v errors %v", sum.Rows, len(sum.Results), sum.Counts, sum.Errors())
	}
	after := fx.states()
	wantState := []string{"APPROVED", "APPROVED", "REJECTED", "REJECTED", "APPROVED", "APPROVED", "PENDING_REVIEW", "APPROVED", "PENDING_REVIEW"}
	for i, id := range ids {
		if after[id].State != wantState[i] {
			t.Errorf("row %d state = %s, want %s", i+2, after[id].State, wantState[i])
		}
	}
	if after[ids[3]].Answer != before[ids[3]].Answer {
		t.Errorf("the edit of a rejected row was stored")
	}
	if after[ids[7]].Note != "ok" || after[ids[4]].Note != "figure checked" {
		t.Errorf("notes = %q %q", after[ids[7]].Note, after[ids[4]].Note)
	}

	// The edited candidate: text cleaned, alternates deduplicated, flags and hash recomputed.
	var q, a, src string
	var alts, flags []string
	var hash []byte
	var versionID uuid.UUID
	var lang string
	if err := fx.Env.Store.Pool.QueryRow(ctx, `SELECT question, alternate_questions, answer, flags, content_hash, source_ref, file_version_id, language FROM candidates WHERE id = $1`, ids[4]).
		Scan(&q, &alts, &a, &flags, &hash, &src, &versionID, &lang); err != nil {
		t.Fatal(err)
	}
	if a != "The Refunds policy is 30 days." || !slices.Equal(alts, []string{"Refund rules?", "What are the refund terms?"}) || !slices.Equal(flags, []string{"CONTAINS_FIGURES"}) {
		t.Errorf("edited candidate = %q %v %v", a, alts, flags)
	}
	want := candidate.ContentHash(candidate.Content{Language: domain.LanguageEN, Question: q, AlternateQuestions: alts, Answer: a, SourceRef: src, FileVersionID: versionID})
	if !bytes.Equal(hash, want[:]) {
		t.Errorf("hash = %x, want %x", hash, want)
	}
	// The unchanged EDIT row keeps its text and flags.
	if after[ids[5]].Answer != before[ids[5]].Answer || after[ids[5]].Question != before[ids[5]].Question {
		t.Errorf("an EDIT without changes altered the text")
	}

	// Audit: one row per applied decision, with before and after.
	if n := fx.count(`SELECT count(*) FROM candidate_reviews`); n != 7 {
		t.Fatalf("audit rows = %d, want 7", n)
	}
	for action, n := range map[string]int{"APPROVE": 3, "REJECT": 2, "EDIT": 2} {
		if got := fx.count(`SELECT count(*) FROM candidate_reviews WHERE action = $1 AND reviewer = 'alice' AND source_file_name = $2`, action, filepath.Base(path)); got != n {
			t.Errorf("audit %s = %d, want %d", action, got, n)
		}
	}
	var beforeJ, afterJ []byte
	if err := fx.Env.Store.Pool.QueryRow(ctx, `SELECT before, after FROM candidate_reviews WHERE candidate_id = $1`, ids[4]).Scan(&beforeJ, &afterJ); err != nil {
		t.Fatal(err)
	}
	var b, af map[string]any
	_ = json.Unmarshal(beforeJ, &b)
	_ = json.Unmarshal(afterJ, &af)
	if b["state"] != "PENDING_REVIEW" || af["state"] != "APPROVED" || b["answer"] != before[ids[4]].Answer || af["answer"] != a ||
		af["contentHash"] != hex.EncodeToString(hash) || b["contentHash"] == af["contentHash"] {
		t.Errorf("audit before/after = %v / %v", b, af)
	}

	// Only approved candidates are publishable: 4 EN (rows 2, 3, 6, 7) and 1 ZH (row 9).
	for lang, n := range map[string]int{"EN": 4, "ZH": 1} {
		pub, err := fx.Env.Store.Queries.ListPublishableCandidates(ctx, lang)
		if err != nil || len(pub) != n {
			t.Errorf("publishable %s = %d, %v; want %d", lang, len(pub), err, n)
		}
	}

	// Importing the same file again changes nothing: applied rows are NOT_PENDING.
	again := fx.importFile(path)
	wantAgain := map[string]int{review.CodeNotPending: 7, review.OutcomeSkipped: 2}
	if again.Clean() || len(again.Errors()) != 7 || fmt.Sprint(again.Counts) != fmt.Sprint(wantAgain) {
		t.Fatalf("second import = counts %v errors %d", again.Counts, len(again.Errors()))
	}
	if n := fx.count(`SELECT count(*) FROM candidate_reviews`); n != 7 {
		t.Errorf("audit rows after the second import = %d, want 7", n)
	}
	if !equalStates(after, fx.states()) {
		t.Error("the second import changed candidates")
	}
}

func equalStates(a, b map[string]state) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestRowsWhoseSourceChangedAreStale(t *testing.T) {
	fx := newFixture(t)
	path, _ := fx.export(review.ExportOptions{})
	var ids []string
	edit(t, path, func(x *excelize.File) {
		for r := 2; r <= 10; r++ {
			ids = append(ids, get(t, x, r, review.ColID))
			set(t, x, r, review.ColAction, "APPROVE")
		}
	})

	// The English document changes after the export, and the scan notices.
	fx.Env.PutFixture("faq_en.docx", "docx/faq_zh.docx")
	fx.Env.Scan()
	sum := fx.importFile(path)

	if sum.Counts[review.CodeStale] != 6 || sum.Counts[review.OutcomeApproved] != 3 || len(sum.Errors()) != 6 || sum.Clean() {
		t.Fatalf("counts = %v, errors = %d", sum.Counts, len(sum.Errors()))
	}
	got := fx.states()
	for i, id := range ids {
		want := "APPROVED"
		if i < 6 {
			want = "STALE"
		}
		if got[id].State != want {
			t.Errorf("row %d state = %s, want %s", i+2, got[id].State, want)
		}
	}
	for _, r := range sum.Errors() {
		if r.Outcome != review.CodeStale || r.Row < 2 || r.Row > 7 {
			t.Errorf("error %+v", r)
		}
	}
	if n := fx.count(`SELECT count(*) FROM candidate_reviews`); n != 3 {
		t.Errorf("audit rows = %d, want 3 (nothing applied for the stale rows)", n)
	}
	if pub, err := fx.Env.Store.Queries.ListPublishableCandidates(context.Background(), "EN"); err != nil || len(pub) != 0 {
		t.Errorf("publishable EN = %d, %v; want 0", len(pub), err)
	}
}

func TestStaleChecksHoldWithoutTheScanHavingRun(t *testing.T) {
	fx := newFixture(t)
	path, _ := fx.export(review.ExportOptions{Language: domain.LanguageZH})
	edit(t, path, func(x *excelize.File) {
		for r := 2; r <= 4; r++ {
			set(t, x, r, review.ColAction, "APPROVE")
		}
		set(t, x, 3, review.ColContentHash, strings.Repeat("0", 64)) // tampered
	})
	// The version is superseded behind the candidates' back (no stale marking).
	if _, err := fx.Env.Store.Pool.Exec(context.Background(),
		`UPDATE file_versions SET superseded_at = now() WHERE id = (SELECT file_version_id FROM candidates WHERE language = 'ZH' LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	sum := fx.importFile(path)
	if sum.Counts[review.CodeStale] != 3 || len(sum.Errors()) != 3 {
		t.Fatalf("counts = %v", sum.Counts)
	}
	for id, s := range fx.states() {
		if s.State != "PENDING_REVIEW" {
			t.Errorf("%s moved to %s", id, s.State)
		}
	}

	// A tampered hash alone, on a live version, is STALE too.
	fx2 := newFixture(t)
	p2, _ := fx2.export(review.ExportOptions{})
	edit(t, p2, func(x *excelize.File) {
		set(t, x, 2, review.ColAction, "APPROVE")
		set(t, x, 2, review.ColContentHash, "not-a-hash")
		set(t, x, 3, review.ColAction, "REJECT")
		set(t, x, 3, review.ColContentHash, strings.ToUpper(get(t, x, 3, review.ColContentHash))) // hex case does not matter
	})
	s2 := fx2.importFile(p2)
	if s2.Counts[review.CodeStale] != 1 || s2.Counts[review.OutcomeRejected] != 1 || s2.Counts[review.OutcomeSkipped] != 7 {
		t.Fatalf("counts = %v", s2.Counts)
	}
}

func TestRefusedRowsChangeNothingAndDoNotStopOthers(t *testing.T) {
	fx := newFixture(t)
	path, _ := fx.export(review.ExportOptions{})
	var ids []string
	edit(t, path, func(x *excelize.File) {
		for r := 2; r <= 10; r++ {
			ids = append(ids, get(t, x, r, review.ColID))
		}
		// row 2: APPROVE with edited text; 3: EDIT with a URL; 4: EDIT with an empty answer;
		// 5: unknown id; 6: garbage id; 7: unknown action; 8: EDIT to the text of row 9 (another section, so no collision);
		// 9: fine; 10: REJECT (fine).
		set(t, x, 2, review.ColAction, "APPROVE")
		set(t, x, 2, review.ColAnswer, "Quietly changed.")
		set(t, x, 3, review.ColAction, "EDIT")
		set(t, x, 3, review.ColAnswer, "See www.example.com for details.")
		set(t, x, 4, review.ColAction, "EDIT")
		set(t, x, 4, review.ColAnswer, "  ")
		set(t, x, 10, review.ColAction, "EDIT")
		set(t, x, 10, review.ColAnswer, "First point.\nSecond point.")
		set(t, x, 5, review.ColAction, "APPROVE")
		set(t, x, 5, review.ColID, uuid.NewString())
		set(t, x, 6, review.ColAction, "APPROVE")
		set(t, x, 6, review.ColID, "nope")
		set(t, x, 7, review.ColAction, "MAYBE")
		set(t, x, 8, review.ColAction, "EDIT")
		set(t, x, 8, review.ColQuestion, get(t, x, 9, review.ColQuestion))
		set(t, x, 8, review.ColAlternateQuestions, get(t, x, 9, review.ColAlternateQuestions))
		set(t, x, 8, review.ColAnswer, get(t, x, 9, review.ColAnswer))
		set(t, x, 9, review.ColAction, "APPROVE")
		set(t, x, 10, review.ColAction, "REJECT")
	})
	sum := fx.importFile(path)
	byRow := map[int]string{}
	for _, r := range sum.Results {
		byRow[r.Row] = r.Outcome
	}
	want := map[int]string{
		2: review.CodeEditRequiresEditAction, 3: review.CodeInvalidEdit, 4: review.CodeInvalidEdit,
		5: review.CodeUnknownCandidate, 6: review.CodeUnknownCandidate, 7: review.CodeInvalidAction,
		8: review.OutcomeEdited, 9: review.OutcomeApproved, 10: review.OutcomeRejected,
	}
	for row, w := range want {
		if byRow[row] != w {
			t.Errorf("row %d = %q, want %q", row, byRow[row], w)
		}
	}
	got := fx.states()
	for i, id := range ids {
		wantState := map[int]string{8: "APPROVED", 9: "APPROVED", 10: "REJECTED"}[i+2]
		if wantState == "" {
			wantState = "PENDING_REVIEW"
		}
		if got[id].State != wantState {
			t.Errorf("row %d state = %s, want %s", i+2, got[id].State, wantState)
		}
	}
	if got[ids[0]].Answer == "Quietly changed." {
		t.Error("an APPROVE with edited text stored the edit")
	}
	if n := fx.count(`SELECT count(*) FROM candidate_reviews`); n != 3 {
		t.Errorf("audit rows = %d, want 3", n)
	}
	if sum.Clean() || len(sum.Errors()) != 6 {
		t.Errorf("errors = %d, want 6", len(sum.Errors()))
	}
}

func TestEditOntoAnotherCandidatesTextIsADuplicate(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	// Two candidates of one section: make the second's text equal the first's.
	rows, err := fx.Env.Store.Pool.Query(ctx, `SELECT id::text, question, answer FROM candidates WHERE section_ordinal = 1 AND language = 'EN' ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	type c struct{ id, q, a string }
	var cs []c
	for rows.Next() {
		var x c
		if err := rows.Scan(&x.id, &x.q, &x.a); err != nil {
			t.Fatal(err)
		}
		cs = append(cs, x)
	}
	rows.Close()
	if len(cs) != 2 {
		t.Fatalf("section 1 candidates = %d", len(cs))
	}
	path, _ := fx.export(review.ExportOptions{Language: domain.LanguageEN})
	edit(t, path, func(x *excelize.File) {
		for r := 2; r <= 7; r++ {
			if get(t, x, r, review.ColID) == cs[1].id {
				set(t, x, r, review.ColAction, "EDIT")
				set(t, x, r, review.ColQuestion, cs[0].q)
				set(t, x, r, review.ColAnswer, cs[0].a)
				set(t, x, r, review.ColAlternateQuestions, "")
			}
			if get(t, x, r, review.ColID) == cs[0].id {
				set(t, x, r, review.ColAlternateQuestions, "")
				set(t, x, r, review.ColAction, "EDIT") // first edits itself to the same text without alternates
			}
		}
	})
	sum := fx.importFile(path)
	// Both rows become the same text; the first EDIT lands, the second collides.
	if sum.Counts[review.OutcomeEdited] != 1 || sum.Counts[review.CodeDuplicateContent] != 1 {
		t.Fatalf("counts = %v", sum.Counts)
	}
	if st := fx.states()[cs[1].id].State; st != "PENDING_REVIEW" {
		t.Errorf("the colliding candidate is %s", st)
	}
	if n := fx.count(`SELECT count(*) FROM candidate_reviews`); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}

func TestImportRejectsFilesThatAreNotReviewExports(t *testing.T) {
	fx := newFixture(t)
	dir := t.TempDir()
	other := filepath.Join(dir, "other.xlsx")
	x := excelize.NewFile()
	_ = x.SetCellStr("Sheet1", "A1", "id")
	if err := x.SaveAs(other); err != nil {
		t.Fatal(err)
	}
	x.Close()
	noCol := filepath.Join(dir, "nocol.xlsx")
	y := excelize.NewFile()
	_ = y.SetSheetName("Sheet1", review.SheetReview)
	_ = y.SetCellStr(review.SheetReview, "A1", "id")
	if err := y.SaveAs(noCol); err != nil {
		t.Fatal(err)
	}
	y.Close()
	garbage := filepath.Join(dir, "garbage.xlsx")
	if err := writeFile(garbage, "not a workbook"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{other, noCol, garbage, filepath.Join(dir, "missing.xlsx")} {
		_, err := review.Import(context.Background(), fx.Env.Store, p, review.ImportOptions{Reviewer: "alice", Limits: limits})
		var fe *review.FileError
		if err == nil || !errorsAs(err, &fe) {
			t.Errorf("%s: err = %v, want a FileError", filepath.Base(p), err)
		}
	}
	good, _ := fx.export(review.ExportOptions{})
	if _, err := review.Import(context.Background(), fx.Env.Store, good, review.ImportOptions{Reviewer: "  ", Limits: limits}); err == nil {
		t.Error("an empty reviewer was accepted")
	}
}
