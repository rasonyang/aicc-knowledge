// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/docx"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

var limits = candidate.Limits{MaxAnswerEN: 240, MaxAnswerZH: 90}

type harness struct {
	T       *testing.T
	Env     *parsetest.Env
	Worker  *Worker
	Log     *bytes.Buffer
	Prov    *obs.Providers
	Server  *llmtest.Server
	Handler llmtest.Handler
}

// newHarness builds the real stack (scratch PostgreSQL, SeaweedFS) with a
// scripted LLM in front of the worker.
func newHarness(t *testing.T, h llmtest.Handler) *harness {
	t.Helper()
	env := parsetest.New(t)
	srv := llmtest.New(t, h)
	prov, err := obs.Setup(context.Background(), obs.Options{ServiceName: "generate-test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prov.Shutdown(context.Background()) })
	logs := &bytes.Buffer{}
	return &harness{
		T: t, Env: env, Server: srv, Log: logs, Prov: prov,
		Worker: &Worker{
			Store: env.Store, LLM: srv.Client(t), Limits: limits, Metrics: prov.Metrics,
			Log: slog.New(slog.NewTextHandler(logs, nil)), Name: "test-generate",
		},
	}
}

func (h *harness) run() Summary {
	h.T.Helper()
	sum, err := h.Worker.Run(context.Background())
	if err != nil {
		h.T.Fatalf("generate: %v %+v", err, sum)
	}
	return sum
}

func (h *harness) metrics() string {
	rec := httptest.NewRecorder()
	h.Prov.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

type candRow struct {
	Ordinal       int
	Language      string
	Question      string
	Alternates    []string
	Answer        string
	SourceRef     string
	Flags         []string
	State         string
	Hash          []byte
	PromptVersion string
	Model         string
	Note          string
	VersionID     uuid.UUID
}

func (h *harness) candidates(versionID string) []candRow {
	h.T.Helper()
	q := `SELECT COALESCE(section_ordinal, -1), language, question, alternate_questions, answer, source_ref, flags, state,
	             content_hash, prompt_version, model, COALESCE(review_note, ''), file_version_id
	      FROM candidates`
	args := []any{}
	if versionID != "" {
		q += ` WHERE file_version_id = $1`
		args = append(args, versionID)
	}
	rows, err := h.Env.Store.Pool.Query(context.Background(), q+` ORDER BY source_ref, created_at, id`, args...)
	if err != nil {
		h.T.Fatal(err)
	}
	defer rows.Close()
	var out []candRow
	for rows.Next() {
		var c candRow
		if err := rows.Scan(&c.Ordinal, &c.Language, &c.Question, &c.Alternates, &c.Answer, &c.SourceRef, &c.Flags, &c.State,
			&c.Hash, &c.PromptVersion, &c.Model, &c.Note, &c.VersionID); err != nil {
			h.T.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func (h *harness) count(query string, args ...any) int {
	h.T.Helper()
	var n int
	if err := h.Env.Store.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		h.T.Fatal(err)
	}
	return n
}

func section(c llmtest.Call, heading string) bool {
	return strings.Contains(c.User(), "Section heading: "+heading+"\n")
}

const empty = `{"candidates":[]}`

func TestPipelineStoresValidatedCandidates(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		switch {
		case section(c, "Billing"):
			return llmtest.Reply(
				llmtest.Cand{Question: "When is my bill issued?", Alts: []string{"What day do I get my bill?", "when is my bill issued"}, Answer: "Your bill is issued on the first day of each month.", Language: "EN"},
				llmtest.Cand{Question: "How long do I have to pay my bill?", Answer: "You must pay it within 15 days.", Language: "EN"},
			), nil
		case section(c, "Refunds"):
			return llmtest.Reply(
				llmtest.Cand{Question: "How do I get a refund?", Answer: "Ask for a refund within the refund period and it goes back to your original payment method.", Language: "EN"},
				// Same question as in Billing, differently written: dropped as a duplicate.
				llmtest.Cand{Question: "WHEN is my bill issued??", Answer: "On the first of the month.", Language: "EN"},
			), nil
		case section(c, "Plans and prices"):
			return llmtest.Reply(llmtest.Cand{Question: "What does the Basic plan cost?", Answer: "The Basic plan costs 9.90 dollars per month.", Language: "EN"}), nil
		case section(c, "账单与缴费"):
			return llmtest.Reply(llmtest.Cand{Question: "账单什么时候出？", Alts: []string{"每月几号出账单？"}, Answer: "账单于每月1日出具。", Language: "ZH"}), nil
		case section(c, "退款"):
			return llmtest.Reply(llmtest.Cand{Question: "退款要在多久内申请？", Answer: "购买后三十天内可以申请退款。", Language: "ZH"}), nil
		}
		return empty, nil
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.PutFixture("faq_zh.docx", "docx/faq_zh.docx")
	h.Env.ScanParse()
	if n := h.count(`SELECT count(*) FROM jobs WHERE kind = 'GENERATE' AND state = 'QUEUED'`); n != 2 {
		t.Fatalf("GENERATE jobs after parse = %d, want 2", n)
	}

	sum := h.run()
	want := Summary{Claimed: 2, Generated: 2, Sections: 6, Candidates: 6, Duplicates: 1}
	sum.LLMTime = 0
	if sum != want {
		t.Fatalf("summary = %+v, want %+v", sum, want)
	}
	if n := len(h.Server.Calls()); n != 6 {
		t.Fatalf("LLM calls = %d, want one per section (6)", n)
	}

	en, zh := h.Env.ObjectKey("faq_en.docx"), h.Env.ObjectKey("faq_zh.docx")
	enV, _ := h.Env.Current("faq_en.docx")
	zhV, _ := h.Env.Current("faq_zh.docx")
	type exp struct {
		Ordinal             int
		Lang, Q, A, Ref, Fl string
		Alts                []string
		Version             string
	}
	wantRows := []exp{
		{1, "EN", "When is my bill issued?", "Your bill is issued on the first day of each month.", en + "#Billing#1", "", []string{"What day do I get my bill?"}, enV.ID},
		{1, "EN", "How long do I have to pay my bill?", "You must pay it within 15 days.", en + "#Billing#1", "CONTAINS_FIGURES", []string{}, enV.ID},
		{3, "EN", "What does the Basic plan cost?", "The Basic plan costs 9.90 dollars per month.", en + "#Plans and prices#3", "CONTAINS_FIGURES", []string{}, enV.ID},
		{2, "EN", "How do I get a refund?", "Ask for a refund within the refund period and it goes back to your original payment method.", en + "#Refunds#2", "", []string{}, enV.ID},
		{1, "ZH", "账单什么时候出？", "账单于每月1日出具。", zh + "#账单与缴费#1", "CONTAINS_FIGURES", []string{"每月几号出账单？"}, zhV.ID},
		{2, "ZH", "退款要在多久内申请？", "购买后三十天内可以申请退款。", zh + "#退款#2", "CONTAINS_FIGURES", []string{}, zhV.ID},
	}
	got := h.candidates("")
	if len(got) != len(wantRows) {
		t.Fatalf("candidates = %d, want %d: %+v", len(got), len(wantRows), got)
	}
	for _, w := range wantRows {
		i := slices.IndexFunc(got, func(c candRow) bool { return c.Question == w.Q })
		if i < 0 {
			t.Errorf("no candidate %q", w.Q)
			continue
		}
		c := got[i]
		wantFlags := []string{}
		if w.Fl != "" {
			wantFlags = []string{w.Fl}
		}
		hash := candidate.ContentHash(candidate.Content{
			Language: domain.Language(w.Lang), Question: w.Q, AlternateQuestions: w.Alts, Answer: w.A, SourceRef: w.Ref, FileVersionID: uuid.MustParse(w.Version),
		})
		switch {
		case c.Ordinal != w.Ordinal, c.Language != w.Lang, c.Answer != w.A, c.SourceRef != w.Ref, !slices.Equal(c.Alternates, w.Alts),
			!slices.Equal(c.Flags, wantFlags), c.State != "PENDING_REVIEW", !bytes.Equal(c.Hash, hash[:]),
			c.PromptVersion != PromptVersion, c.Model != "fake-model", c.VersionID.String() != w.Version, c.Note != "":
			t.Errorf("candidate %q = %+v\nwant %+v flags %v hash %x", w.Q, c, w, wantFlags, hash)
		}
	}

	m := h.metrics()
	for _, s := range []string{
		`kb_generate_candidates_total{language="EN",outcome="CREATED"} 4`,
		`kb_generate_candidates_total{language="ZH",outcome="CREATED"} 2`,
		`kb_generate_candidates_total{language="EN",outcome="DROPPED_DUPLICATE"} 1`,
		`kb_generate_sections_total{outcome="OK"} 5`,
		`kb_generate_sections_total{outcome="EMPTY"} 1`,
		`kb_llm_request_seconds_count{outcome="OK"} 6`,
	} {
		if !strings.Contains(m, s) {
			t.Errorf("metrics lack %q", s)
		}
	}

	// Unreviewed candidates are never publishable.
	for _, lang := range []string{"EN", "ZH"} {
		pub, err := h.Env.Store.Queries.ListPublishableCandidates(context.Background(), lang)
		if err != nil || len(pub) != 0 {
			t.Errorf("publishable %s = %d, %v; want 0", lang, len(pub), err)
		}
	}

	// A second run is a no-op, and so is re-arming the job of a done version.
	if again := h.run(); again.Claimed != 0 {
		t.Fatalf("second run claimed %+v", again)
	}
	vid := uuid.MustParse(enV.ID)
	if ok, err := h.Worker.Enqueue(context.Background(), vid); err != nil || !ok {
		t.Fatalf("Enqueue = %v, %v", ok, err)
	}
	again := h.run()
	if again.Claimed != 1 || again.Skipped != 1 || again.Candidates != 0 || len(h.Server.Calls()) != 6 || len(h.candidates("")) != 6 {
		t.Fatalf("re-run = %+v, calls %d, candidates %d", again, len(h.Server.Calls()), len(h.candidates("")))
	}
	if n := h.count(`SELECT count(*) FROM jobs WHERE kind = 'GENERATE'`); n != 2 {
		t.Errorf("GENERATE job rows = %d, want 2 (re-arm must not insert)", n)
	}
}

func TestOnlyVersionLeavesOtherQueuedJobsAlone(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		return llmtest.Reply(llmtest.Cand{Question: "When is my bill issued?", Answer: "On the first day of the month.", Language: "EN"}), nil
	})
	h.Env.PutFixture("a.docx", "docx/faq_en.docx")
	h.Env.PutFixture("b.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	a, _ := h.Env.Current("a.docx")
	b, _ := h.Env.Current("b.docx")
	if n := h.count(`SELECT count(*) FROM jobs WHERE kind = 'GENERATE' AND state = 'QUEUED'`); n != 2 {
		t.Fatalf("queued GENERATE jobs = %d, want 2", n)
	}

	h.Worker.Only = uuid.MustParse(b.ID)
	sum := h.run()
	if sum.Claimed != 1 || sum.Generated != 1 {
		t.Fatalf("summary = %+v, want exactly one job", sum)
	}
	if n := len(h.candidates(a.ID)); n != 0 {
		t.Errorf("version a has %d candidates, want 0 (not selected)", n)
	}
	if n := len(h.candidates(b.ID)); n == 0 {
		t.Error("version b has no candidates")
	}
	if n := h.count(`SELECT count(*) FROM jobs WHERE kind = 'GENERATE' AND state = 'QUEUED' AND dedupe_key = $1`, a.ID); n != 1 {
		t.Errorf("the job of version a is not QUEUED any more (%d)", n)
	}
	// Running again selects nothing new: the job of b is done.
	if again := h.run(); again.Claimed != 0 {
		t.Errorf("second run = %+v", again)
	}

	h.Worker.Only = uuid.Nil
	if rest := h.run(); rest.Claimed != 1 || len(h.candidates(a.ID)) == 0 {
		t.Errorf("the remaining job was not processed: %+v", rest)
	}
}

func TestOverlongAnswerIsRetriedWithFeedback(t *testing.T) {
	long := strings.Repeat("Your bill is issued monthly. ", 20)
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if !section(c, "Billing") {
			return empty, nil
		}
		if len(c.Messages) == 2 {
			return llmtest.Reply(llmtest.Cand{Question: "When is my bill issued?", Answer: long, Language: "EN"}), nil
		}
		return llmtest.Reply(llmtest.Cand{Question: "When is my bill issued?", Answer: "It is issued on the first day of each month.", Language: "EN"}), nil
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Candidates != 1 || sum.Warnings != 0 || sum.Sections != 3 {
		t.Fatalf("summary = %+v", sum)
	}
	calls := h.Server.Calls()
	if len(calls) != 4 { // 3 sections + 1 retry
		t.Fatalf("LLM calls = %d, want 4", len(calls))
	}
	var retry llmtest.Call
	for _, c := range calls {
		if len(c.Messages) == 4 {
			retry = c
		}
	}
	if retry.N == 0 || retry.Messages[2].Role != llm.RoleAssistant || !strings.Contains(retry.Messages[2].Content, long[:40]) ||
		!strings.Contains(retry.Last(), "ANSWER_TOO_LONG") || !strings.Contains(retry.Last(), "entry 1") {
		t.Fatalf("retry call = %+v", retry)
	}
	got := h.candidates("")
	if len(got) != 1 || got[0].Answer != "It is issued on the first day of each month." {
		t.Fatalf("candidates = %+v", got)
	}
	if m := h.metrics(); !strings.Contains(m, `kb_generate_sections_total{outcome="RETRIED"} 1`) {
		t.Errorf("metrics lack the RETRIED section\n%s", m)
	}
}

func TestStillInvalidAfterRetryIsDroppedWithAWarning(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if !section(c, "Billing") {
			return empty, nil
		}
		return llmtest.Reply(
			llmtest.Cand{Question: "When is my bill issued?", Answer: "It is issued on the first day of each month.", Language: "EN"},
			llmtest.Cand{Question: "Where do I pay?", Answer: "Pay at www.example.com/pay today.", Language: "EN"},
			llmtest.Cand{Question: "What are the payment options?", Answer: "- bank card - transfer - counter", Language: "EN"},
		), nil
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Candidates != 1 || sum.Warnings != 2 || sum.Generated != 1 || sum.Errors != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	got := h.candidates("")
	if len(got) != 1 || got[0].Question != "When is my bill issued?" {
		t.Fatalf("candidates = %+v", got)
	}
	if n := len(h.Server.Calls()); n != 4 { // billing twice, two other sections once
		t.Fatalf("LLM calls = %d, want 4", n)
	}
	logs := h.Log.String()
	if strings.Count(logs, "validation failed after the retry") != 2 || !strings.Contains(logs, "URL") || !strings.Contains(logs, "MARKDOWN") {
		t.Errorf("log lacks the two warnings:\n%s", logs)
	}
	if m := h.metrics(); !strings.Contains(m, `kb_generate_candidates_total{language="EN",outcome="DROPPED_INVALID"} 2`) ||
		!strings.Contains(m, `kb_generate_sections_total{outcome="PARTIAL"} 1`) {
		t.Errorf("metrics lack the warnings\n%s", m)
	}
}

func TestLanguageMismatchWithTheSectionIsDropped(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if section(c, "退款") {
			// Self-consistent English, but the section is Chinese.
			return llmtest.Reply(llmtest.Cand{Question: "How long do I have to ask for a refund?", Answer: "Within thirty days.", Language: "EN"}), nil
		}
		if section(c, "套餐价格") {
			// Says ZH but is written in English.
			return llmtest.Reply(llmtest.Cand{Question: "What does the basic plan cost?", Answer: "It costs 9.9 yuan.", Language: "ZH"}), nil
		}
		return empty, nil
	})
	h.Env.PutFixture("faq_zh.docx", "docx/faq_zh.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Candidates != 0 || sum.Warnings != 2 || len(h.candidates("")) != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if n := strings.Count(h.Log.String(), "LANGUAGE_MISMATCH"); n != 2 {
		t.Errorf("LANGUAGE_MISMATCH warnings = %d, want 2\n%s", n, h.Log.String())
	}
}

func TestContentThatIsNotTheSchemaCountsAsAWarningNotAJobFailure(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if section(c, "Billing") {
			return `{"candidates":[{"question":"q"}]}`, nil // required fields missing
		}
		return empty, nil
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Generated != 1 || sum.Errors != 0 || sum.Candidates != 0 || sum.Warnings != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if !strings.Contains(h.Log.String(), "LLM_OUTPUT_INVALID") {
		t.Errorf("log lacks LLM_OUTPUT_INVALID:\n%s", h.Log.String())
	}
}

func TestUnavailableLLMWritesNothingAndRequeuesTheJob(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) { return "", &llmtest.Fail{Status: 503} })
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Claimed != 1 || sum.Errors != 1 || sum.Generated != 0 || sum.Candidates != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	var state, code string
	if err := h.Env.Store.Pool.QueryRow(context.Background(),
		`SELECT state, last_error_code FROM jobs WHERE kind = 'GENERATE'`).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "QUEUED" || code != "UPSTREAM_UNAVAILABLE" || len(h.candidates("")) != 0 {
		t.Fatalf("job %s %s, candidates %d", state, code, len(h.candidates("")))
	}
}

func TestJobsOfSupersededOrRemovedVersionsAreSkipped(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		return llmtest.Reply(llmtest.Cand{Question: "When is my bill issued?", Answer: "On the first day of the month.", Language: "EN"}), nil
	})
	h.Env.PutFixture("a.docx", "docx/faq_en.docx")
	h.Env.PutFixture("b.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	// a.docx changes (its parsed version is superseded); b.docx is deleted.
	h.Env.PutFixture("a.docx", "docx/faq_zh.docx")
	h.Env.S3.Delete("b.docx")
	h.Env.Scan()

	sum := h.run()
	if sum.Claimed != 2 || sum.Skipped != 2 || sum.Generated != 0 || sum.Candidates != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if n := len(h.Server.Calls()); n != 0 {
		t.Fatalf("LLM calls = %d, want 0", n)
	}
	if n := len(h.candidates("")); n != 0 {
		t.Fatalf("candidates = %d, want 0", n)
	}
	log := h.Log.String()
	if !strings.Contains(log, "reason=SUPERSEDED") || !strings.Contains(log, "reason=REMOVED") {
		t.Errorf("skip reasons missing from the log:\n%s", log)
	}
	if n := h.count(`SELECT count(*) FROM jobs WHERE kind = 'GENERATE' AND state = 'SUCCEEDED'`); n != 2 {
		t.Errorf("completed GENERATE jobs = %d, want 2", n)
	}
}

func TestSectionsWithdrawnByAFactsMappingStaleTheirCandidates(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		return llmtest.Reply(
			llmtest.Cand{Question: "What does the Basic plan cost?", Answer: "Please check the price list for the current monthly price.", Language: "EN"},
			llmtest.Cand{Question: "What does the Pro plan cost?", Answer: "Please check the price list for the Pro monthly price.", Language: "EN"},
			llmtest.Cand{Question: "Is there an unlimited plan?", Answer: "The Max plan has unlimited data.", Language: "EN"},
		), nil
	})
	h.Env.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	h.Env.ScanParse()
	if sum := h.run(); sum.Candidates != 3 || sum.Sections != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	// One of them was already approved: STALE is reachable from every state.
	if _, err := h.Env.Store.Pool.Exec(context.Background(),
		`UPDATE candidates SET state = 'APPROVED' WHERE question = 'What does the Pro plan cost?'`); err != nil {
		t.Fatal(err)
	}
	if pub, err := h.Env.Store.Queries.ListPublishableCandidates(context.Background(), "EN"); err != nil || len(pub) != 1 {
		t.Fatalf("publishable before = %d, %v", len(pub), err)
	}

	h.Env.Put("pricing.facts.yaml", parsetest.PricingMapping(t))
	h.Env.ScanParse() // the mapping covers the Pricing sheet: its sections are withdrawn

	got := h.candidates("")
	if len(got) != 3 {
		t.Fatalf("candidates = %d, want 3 (kept for audit)", len(got))
	}
	for _, c := range got {
		if c.State != "STALE" || c.Note != "SECTION_WITHDRAWN" {
			t.Errorf("candidate %q = %s %q, want STALE SECTION_WITHDRAWN", c.Question, c.State, c.Note)
		}
	}
	if pub, err := h.Env.Store.Queries.ListPublishableCandidates(context.Background(), "EN"); err != nil || len(pub) != 0 {
		t.Fatalf("publishable after = %d, %v", len(pub), err)
	}
}

// The hook keeps candidates whose section survives a re-derivation and stales
// only the ones whose (ordinal, source_ref) is gone, preserving a reviewer's
// note.
func TestWithdrawnSectionQueryMatchesOnOrdinalAndSourceRef(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) { return empty, nil })
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := h.Env.Store.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO source_files (bucket, object_key) VALUES ('b', 'k')`)
	exec(`INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at, state)
	      SELECT id, 1, decode(repeat('01', 32), 'hex'), 1, 'e', now(), 'PARSED' FROM source_files`)
	for i, ref := range []string{"k#a", "k#b", "k#c"} {
		exec(`INSERT INTO parsed_sections (file_version_id, ordinal, kind, body, source_ref)
		      SELECT id, $1, 'DOCX_SECTION', 'body', $2 FROM file_versions`, i, ref)
	}
	cand := func(name string, ordinal any, ref, note string) {
		exec(`INSERT INTO candidates (file_version_id, section_ordinal, language, question, answer, source_ref, content_hash, review_note)
		      SELECT id, $1, 'EN', $2, 'a', $3, sha256(convert_to($2::text, 'UTF8')), NULLIF($4, '') FROM file_versions`, ordinal, name, ref, note)
	}
	cand("kept", 0, "k#a", "")
	cand("ref moved", 1, "k#OLD", "")
	cand("ordinal gone", 7, "k#b", "looks wrong")
	cand("no section", nil, "whatever", "")
	if n, err := h.Env.Store.Queries.MarkCandidatesOfWithdrawnSectionsStale(ctx, uuid.Nil); err != nil || n != 0 {
		t.Fatalf("unknown version staled %d, %v", n, err)
	}
	var vid uuid.UUID
	if err := h.Env.Store.Pool.QueryRow(ctx, `SELECT id FROM file_versions`).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	n, err := h.Env.Store.Queries.MarkCandidatesOfWithdrawnSectionsStale(ctx, vid)
	if err != nil || n != 2 {
		t.Fatalf("staled %d, %v; want 2", n, err)
	}
	type row struct{ State, Note string }
	got := map[string]row{}
	for _, c := range h.candidates("") {
		got[c.Question] = row{c.State, c.Note}
	}
	want := map[string]row{
		"kept": {"PENDING_REVIEW", ""}, "ref moved": {"STALE", "SECTION_WITHDRAWN"},
		"ordinal gone": {"STALE", "looks wrong\nSECTION_WITHDRAWN"}, "no section": {"PENDING_REVIEW", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v", got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %+v, want %+v", k, got[k], w)
		}
	}
	// Running it again changes nothing.
	if n, err := h.Env.Store.Queries.MarkCandidatesOfWithdrawnSectionsStale(ctx, vid); err != nil || n != 0 {
		t.Fatalf("second pass staled %d, %v", n, err)
	}
}

func TestDecodeOutputEnforcesTheSchemaInGo(t *testing.T) {
	ok := `{"candidates":[{"question":"q","alternateQuestions":[],"answer":"a","language":"EN"}]}`
	if out, err := decodeOutput([]byte(ok)); err != nil || len(out.Candidates) != 1 {
		t.Fatalf("valid output rejected: %v", err)
	}
	if out, err := decodeOutput([]byte(empty)); err != nil || len(out.Candidates) != 0 {
		t.Fatalf("empty list rejected: %v", err)
	}
	for name, in := range map[string]string{
		"not json":       `Sure! Here you go`,
		"array":          `[]`,
		"no candidates":  `{}`,
		"null list":      `{"candidates":null}`,
		"extra key":      `{"candidates":[],"containsFigures":true}`,
		"missing answer": `{"candidates":[{"question":"q","alternateQuestions":[],"language":"EN"}]}`,
		"null alts":      `{"candidates":[{"question":"q","alternateQuestions":null,"answer":"a","language":"EN"}]}`,
		"wrong type":     `{"candidates":[{"question":1,"alternateQuestions":[],"answer":"a","language":"EN"}]}`,
		"extra field":    `{"candidates":[{"question":"q","alternateQuestions":[],"answer":"a","language":"EN","x":1}]}`,
		"trailing":       ok + ` {}`,
	} {
		if _, err := decodeOutput([]byte(in)); err == nil {
			t.Errorf("%s: accepted %s", name, in)
		}
	}
}

func TestQuestionSetDedupesAcrossQuestionsAndAlternates(t *testing.T) {
	s := newQuestionSet()
	if !s.add("How do I get a refund?", []string{"Refund steps?"}) {
		t.Fatal("first add rejected")
	}
	if s.add("how do i get a REFUND", nil) || s.add("Refund steps", nil) {
		t.Fatal("duplicates accepted")
	}
	if !s.add("Why was I charged?", nil) {
		t.Fatal("distinct question rejected")
	}
}

// TestRealLLM runs the real prompt against a real model: one EN and one ZH
// section of the committed call-center documents must each give at least one
// candidate, and every candidate must pass the deterministic rules again.
// Quality beyond the rules is judged by a human; see the M4 notes.
func TestRealLLM(t *testing.T) {
	base, model := os.Getenv("KB_TEST_LLM_URL"), os.Getenv("KB_TEST_LLM_MODEL")
	if base == "" || model == "" {
		t.Skip("SKIPPED: set KB_TEST_LLM_URL and KB_TEST_LLM_MODEL to run this test against a real OpenAI-compatible LLM")
	}
	client, err := llm.New(llm.Config{BaseURL: base, Model: model, APIKey: os.Getenv("KB_TEST_LLM_API_KEY"), Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{LLM: client, Limits: limits, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, tc := range []struct {
		fixture, heading string
		lang             domain.Language
	}{
		{"docx/faq_en.docx", "Refunds", domain.LanguageEN},
		{"docx/faq_zh.docx", "套餐价格", domain.LanguageZH},
	} {
		t.Run(string(tc.lang), func(t *testing.T) {
			doc, err := docx.ParseBytes(parsetest.Fixture(t, tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			var sec *docx.Section
			for i := range doc.Sections {
				if strings.Join(doc.Sections[i].HeadingPath, " > ") == tc.heading {
					sec = &doc.Sections[i]
				}
			}
			if sec == nil {
				t.Fatalf("no section %q", tc.heading)
			}
			ps := parsedSection(sec)
			var sum Summary
			res, err := w.section(context.Background(), "fixture", ps, "", &sum)
			got := res.drafts
			if err != nil {
				t.Fatal(err)
			}
			if len(got) < 1 || len(got) > MaxCandidatesPerSection {
				t.Fatalf("candidates = %d, want 1..%d (warnings %d)", len(got), MaxCandidatesPerSection, sum.Warnings)
			}
			for _, d := range got {
				r := candidate.Validate(candidate.Draft{Language: string(d.language), Question: d.question, AlternateQuestions: d.alternates, Answer: d.answer}, limits)
				if !r.OK() || d.language != tc.lang {
					t.Errorf("candidate %+v fails the rules: %v (language %s)", d, r.Violations, d.language)
				}
				t.Logf("%s | %s | alts=%v | flags=%v", d.question, d.answer, d.alternates, candidate.Flags(d.question, d.alternates, d.answer))
			}
		})
	}
}

func parsedSection(s *docx.Section) queries.ParsedSection {
	return queries.ParsedSection{
		Ordinal: int32(s.Ordinal), Kind: string(domain.SectionKindDocxSection), HeadingPath: s.HeadingPath,
		Level: int32(s.Level), Body: s.Text(), SourceRef: s.SourceRef,
	}
}
