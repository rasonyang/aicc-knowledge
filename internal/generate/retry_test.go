// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
)

const (
	billingQ = "When is my bill issued?"
	billingA = "Your bill is issued on the first day of each month."
	refundQ  = "How do I get a refund?"
	refundA  = "Ask for a refund within the refund period and it goes back to your original payment method."
)

// outcomes returns the recorded outcome and error code of every section that
// has one, keyed by its heading path.
func (h *harness) outcomes() map[string][2]string {
	h.T.Helper()
	rows, err := h.Env.Store.Pool.Query(context.Background(),
		`SELECT array_to_string(heading_path, ' > '), generate_outcome, COALESCE(generate_error_code, '')
		 FROM parsed_sections WHERE generate_outcome IS NOT NULL`)
	if err != nil {
		h.T.Fatal(err)
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var head, o, code string
		if err := rows.Scan(&head, &o, &code); err != nil {
			h.T.Fatal(err)
		}
		out[head] = [2]string{o, code}
	}
	return out
}

func (h *harness) versionID() uuid.UUID {
	h.T.Helper()
	v, ok := h.Env.Current("faq_en.docx")
	if !ok {
		h.T.Fatal("no current version of faq_en.docx")
	}
	id, err := uuid.Parse(v.ID)
	if err != nil {
		h.T.Fatal(err)
	}
	return id
}

func hashes(cs []candRow) []string {
	var out []string
	for _, c := range cs {
		out = append(out, string(c.Hash))
	}
	slices.Sort(out)
	return out
}

// billingFlaky answers Billing with invalid JSON (both attempts) while *broken
// is set, Refunds with one candidate and Plans with an empty list.
func billingFlaky(broken *bool) llmtest.Handler {
	return func(c llmtest.Call) (string, *llmtest.Fail) {
		switch {
		case section(c, "Billing"):
			if *broken {
				return `{"candidates":[{"question":"q"}]}`, nil
			}
			return llmtest.Reply(llmtest.Cand{Question: billingQ, Answer: billingA, Language: "EN"}), nil
		case section(c, "Refunds"):
			return llmtest.Reply(llmtest.Cand{Question: refundQ, Answer: refundA, Language: "EN"}), nil
		}
		return empty, nil
	}
}

func TestRequestCarriesTheStringBoundsAndADerivedTokenCap(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) { return empty, nil })
	// A client with a high ceiling shows the cap the worker asks for.
	client, err := llm.New(llm.Config{BaseURL: h.Server.URL + "/v1", Model: "fake-model", Timeout: 10 * time.Second, MaxTokens: 100000, Retries: -1})
	if err != nil {
		t.Fatal(err)
	}
	h.Worker.LLM = client
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.PutFixture("faq_zh.docx", "docx/faq_zh.docx")
	h.Env.ScanParse()
	h.run()
	calls := h.Server.Calls()
	if len(calls) != 6 {
		t.Fatalf("LLM calls = %d, want 6", len(calls))
	}
	for _, c := range calls {
		lang := domain.LanguageEN
		if strings.Contains(c.User(), "退款") || strings.Contains(c.User(), "账单") || strings.Contains(c.User(), "套餐") {
			lang = domain.LanguageZH
		}
		item := c.Schema["properties"].(map[string]any)["candidates"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
		get := func(m any) float64 { return m.(map[string]any)["maxLength"].(float64) }
		wantAnswer := float64(limits.MaxAnswer(lang) + limits.MaxAnswer(lang)/4)
		wantQuestion := float64(candidate.MaxQuestionChars + candidate.MaxQuestionChars/4)
		alts := item["alternateQuestions"].(map[string]any)["items"]
		if get(item["answer"]) != wantAnswer || get(item["question"]) != wantQuestion || get(alts) != wantQuestion {
			t.Errorf("%s call %d: bounds answer %v question %v alt %v, want %v/%v", lang, c.N, get(item["answer"]), get(item["question"]), get(alts), wantAnswer, wantQuestion)
		}
		if c.MaxTokens != maxTokensFor(lang, limits) {
			t.Errorf("%s call %d: max_tokens = %d, want %d", lang, c.N, c.MaxTokens, maxTokensFor(lang, limits))
		}
	}
	// The cap is derived from the bounds: Chinese needs more tokens per
	// character but its answers are shorter; a condense answer is far smaller.
	if condenseMaxTokens(domain.LanguageEN, limits) >= maxTokensFor(domain.LanguageEN, limits)/4 {
		t.Errorf("condense cap %d is not much smaller than the candidates cap %d", condenseMaxTokens(domain.LanguageEN, limits), maxTokensFor(domain.LanguageEN, limits))
	}
	if !llm.HasStringBounds(condenseSchemaFor(domain.LanguageZH, limits)) {
		t.Error("the condense schema has no string bound")
	}
}

func TestLoopingAnswerEndsAtTheSchemaBoundAndIsRetried(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if !section(c, "Billing") {
			return empty, nil
		}
		if len(c.Messages) == 2 {
			// What a grammar-constrained server does with a looping model:
			// the answer stops at maxLength.
			max := int(c.Schema["properties"].(map[string]any)["candidates"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["answer"].(map[string]any)["maxLength"].(float64))
			loop := strings.Repeat("Your bill is issued monthly. ", 40)
			return llmtest.Reply(llmtest.Cand{Question: billingQ, Answer: string([]rune(loop)[:max]), Language: "EN"}), nil
		}
		return llmtest.Reply(llmtest.Cand{Question: billingQ, Answer: billingA, Language: "EN"}), nil
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.CutOff != 0 || sum.Candidates != 1 || sum.Warnings != 0 || sum.SectionsFailed != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	var retry llmtest.Call
	for _, c := range h.Server.Calls() {
		if len(c.Messages) == 4 {
			retry = c
		}
	}
	if retry.N == 0 || !strings.Contains(retry.Last(), "ANSWER_TOO_LONG") {
		t.Fatalf("the over-long answer was not sent back: %+v", retry)
	}
	if got := h.candidates(""); len(got) != 1 || got[0].Answer != billingA {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestCutOffFirstAttemptGivesTheRetryNoEmptyAssistantTurn(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if !section(c, "Billing") {
			return empty, nil
		}
		if !strings.Contains(c.User(), "previous attempt was rejected") {
			return "", &llmtest.Fail{CutOff: true}
		}
		return llmtest.Reply(llmtest.Cand{Question: billingQ, Answer: billingA, Language: "EN"}), nil
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.CutOff != 1 || sum.Candidates != 1 || sum.SectionsFailed != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	var retry llmtest.Call
	for _, c := range h.Server.Calls() {
		if section(c, "Billing") && c.N > 1 && strings.Contains(c.User(), "previous attempt was rejected") {
			retry = c
		}
	}
	if retry.N == 0 || len(retry.Messages) != 2 {
		t.Fatalf("retry after a cut-off = %+v, want the original two messages with the problem added", retry)
	}
	for _, m := range retry.Messages {
		if m.Role == llm.RoleAssistant {
			t.Errorf("the retry carries an assistant turn: %+v", m)
		}
	}
	if !strings.Contains(retry.User(), "token limit") || !strings.Contains(retry.User(), "Section text:") {
		t.Errorf("retry user message = %q", retry.User())
	}
}

func TestSectionThatFailsTwiceIsFailedAndRetriedOnDemand(t *testing.T) {
	broken := true
	h := newHarness(t, billingFlaky(&broken))
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Generated != 1 || sum.Errors != 0 || sum.Sections != 3 || sum.SectionsFailed != 1 || sum.SectionsGenerated != 1 || sum.SectionsEmpty != 1 || sum.Candidates != 1 {
		t.Fatalf("first pass = %+v", sum)
	}
	want := map[string][2]string{
		"Billing":          {"FAILED", "LLM_OUTPUT_INVALID"},
		"Refunds":          {"GENERATED", ""},
		"Plans and prices": {"EMPTY", ""},
	}
	if got := h.outcomes(); !equalOutcomes(got, want) {
		t.Fatalf("outcomes = %v, want %v", got, want)
	}
	m := h.metrics()
	for _, s := range []string{`outcome="FAILED"} 1`, `outcome="EMPTY"} 1`, `outcome="OK"} 1`} {
		if !strings.Contains(m, `kb_generate_sections_total{`+s) {
			t.Errorf("metrics lack %s\n%s", s, m)
		}
	}
	// The version counts as generated: a plain re-run does nothing.
	if _, err := h.Worker.Enqueue(context.Background(), h.versionID()); err != nil {
		t.Fatal(err)
	}
	if again := h.run(); again.Skipped != 1 || again.Sections != 0 {
		t.Fatalf("plain re-run = %+v", again)
	}
	before := h.candidates("")
	callsBefore := len(h.Server.Calls())

	// The model recovers; the operator retries the failed sections.
	broken = false
	ctx := context.Background()
	retry, err := h.Worker.RetryFailedSections(ctx, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Claimed != 1 || retry.Sections != 1 || retry.SectionsGenerated != 1 || retry.SectionsFailed != 0 || retry.Candidates != 1 || retry.Errors != 0 {
		t.Fatalf("retry = %+v", retry)
	}
	if n := len(h.Server.Calls()) - callsBefore; n != 1 {
		t.Fatalf("retry LLM calls = %d, want exactly the one failed section", n)
	}
	after := h.candidates("")
	if len(after) != len(before)+1 {
		t.Fatalf("candidates %d -> %d, want one more", len(before), len(after))
	}
	for _, c := range before { // the other sections' candidates are the same rows
		if !slices.Contains(hashes(after), string(c.Hash)) {
			t.Errorf("candidate %q vanished", c.Question)
		}
	}
	want["Billing"] = [2]string{"GENERATED", ""}
	if got := h.outcomes(); !equalOutcomes(got, want) {
		t.Fatalf("outcomes after retry = %v, want %v", got, want)
	}

	// A second retry is a no-op: nothing is queued, called or stored.
	callsBefore = len(h.Server.Calls())
	again, err := h.Worker.RetryFailedSections(ctx, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if again != (Summary{}) || len(h.Server.Calls()) != callsBefore || len(h.candidates("")) != len(after) {
		t.Fatalf("second retry = %+v, calls %d -> %d, candidates %d", again, callsBefore, len(h.Server.Calls()), len(h.candidates("")))
	}
	if n := h.count(`SELECT count(*) FROM parsed_sections WHERE generate_outcome = 'EMPTY'`); n != 1 {
		t.Errorf("EMPTY sections = %d, want 1: the empty section must stay done", n)
	}
}

func TestRetryFailedRespectsVersionAndLeavesEmptySectionsAlone(t *testing.T) {
	broken := true
	h := newHarness(t, billingFlaky(&broken))
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.PutFixture("faq_zh.docx", "docx/faq_zh.docx")
	h.Env.ScanParse()
	h.run()
	broken = false
	ctx := context.Background()
	callsBefore := len(h.Server.Calls())

	// Another version's id: nothing to do.
	sum, err := h.Worker.RetryFailedSections(ctx, uuid.New())
	if err != nil || sum != (Summary{}) || len(h.Server.Calls()) != callsBefore {
		t.Fatalf("retry for an unknown version = %+v %v", sum, err)
	}
	sum, err = h.Worker.RetryFailedSections(ctx, h.versionID())
	if err != nil || sum.Sections != 1 || sum.Candidates != 1 {
		t.Fatalf("retry for the version = %+v %v", sum, err)
	}
	// The only section sent is the failed one, never an EMPTY one.
	for _, c := range h.Server.Calls()[callsBefore:] {
		if !section(c, "Billing") {
			t.Errorf("retry sent another section: %q", c.User())
		}
	}
}

func TestRetriedCandidateDuplicatingAKeptQuestionIsDropped(t *testing.T) {
	broken := true
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if section(c, "Billing") && !broken {
			// The same question as the one Refunds already has.
			return llmtest.Reply(llmtest.Cand{Question: refundQ, Answer: "Refunds go back to your card.", Language: "EN"}), nil
		}
		return billingFlaky(&broken)(c)
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	h.run()
	broken = false
	sum, err := h.Worker.RetryFailedSections(context.Background(), uuid.Nil)
	if err != nil || sum.Duplicates != 1 || sum.Candidates != 0 || len(h.candidates("")) != 1 {
		t.Fatalf("retry = %+v %v candidates %d", sum, err, len(h.candidates("")))
	}
}

func TestUpstreamFailureOfOneSectionIsFailedAndRetryable(t *testing.T) {
	down := true
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if section(c, "Billing") && down {
			return "", &llmtest.Fail{Status: 503}
		}
		return billingFlaky(new(bool))(c)
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Errors != 0 || sum.Generated != 1 || sum.SectionsFailed != 1 || sum.Candidates != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if got := h.outcomes()["Billing"]; got != [2]string{"FAILED", "UPSTREAM_UNAVAILABLE"} {
		t.Fatalf("Billing outcome = %v", got)
	}
	down = false
	if r, err := h.Worker.RetryFailedSections(context.Background(), uuid.Nil); err != nil || r.Candidates != 1 || r.SectionsFailed != 0 {
		t.Fatalf("retry = %+v %v", r, err)
	}
	if got := h.outcomes()["Billing"]; got != [2]string{"GENERATED", ""} {
		t.Fatalf("Billing outcome after retry = %v", got)
	}
}

func TestSectionWithOnlyInvalidCandidatesIsFailed(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if section(c, "Billing") {
			return llmtest.Reply(llmtest.Cand{Question: billingQ, Answer: "Pay at www.example.com/pay today.", Language: "EN"}), nil
		}
		return empty, nil
	})
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	h.run()
	if got := h.outcomes()["Billing"]; got != [2]string{"FAILED", CodeCandidatesInvalid} {
		t.Fatalf("Billing outcome = %v", got)
	}
}

func TestReparseKeepsTheOutcomeOfAnUnchangedSection(t *testing.T) {
	broken := true
	h := newHarness(t, billingFlaky(&broken))
	h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
	h.Env.ScanParse()
	h.run()
	before := h.outcomes()
	if _, err := h.Env.Store.Pool.Exec(context.Background(), `UPDATE file_versions SET state = 'DISCOVERED' WHERE state = 'PARSED'`); err != nil {
		t.Fatal(err)
	}
	h.Env.Parse()
	if got := h.outcomes(); !equalOutcomes(got, before) {
		t.Fatalf("outcomes after a re-parse = %v, want %v", got, before)
	}
}

func equalOutcomes(a, b map[string][2]string) bool {
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

// TestRealLLMStringBoundsEndALoop sends a request that makes a small model
// repeat a sentence, with the production schema: the answer must stop at the
// bound instead of running to the token cap.
func TestRealLLMStringBoundsEndALoop(t *testing.T) {
	base, model := os.Getenv("KB_TEST_LLM_URL"), os.Getenv("KB_TEST_LLM_MODEL")
	if base == "" || model == "" {
		t.Skip("SKIPPED: set KB_TEST_LLM_URL and KB_TEST_LLM_MODEL to run this test against a real OpenAI-compatible LLM")
	}
	client, err := llm.New(llm.Config{BaseURL: base, Model: model, APIKey: os.Getenv("KB_TEST_LLM_API_KEY"), Timeout: 3 * time.Minute, MaxTokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "You write FAQ entries as JSON. Always set language to \"EN\"."},
		{Role: llm.RoleUser, Content: "Write one entry. The question is \"How long is the refund window?\". The answer must be the sentence \"The refund window is thirty days.\" repeated at least 60 times, with no other words."},
	}
	start := time.Now()
	content, err := client.CompleteJSON(context.Background(), llm.Request{
		Messages: msgs, SchemaName: schemaName, Schema: schemaFor(domain.LanguageEN, limits), MaxTokens: maxTokensFor(domain.LanguageEN, limits),
	}, func(b []byte) error { _, err := decodeOutput(b); return err })
	if err != nil {
		t.Fatalf("the bounded request failed (cut off at the token cap?): %v", err)
	}
	out, _ := decodeOutput([]byte(content))
	bound := headroom(limits.MaxAnswer(domain.LanguageEN))
	for _, c := range out.Candidates {
		if n := utf8.RuneCountInString(c.Answer); n > bound {
			t.Errorf("answer has %d characters, the bound is %d", n, bound)
		}
	}
	b, _ := json.Marshal(out)
	t.Logf("answered in %s: %s", time.Since(start).Round(time.Millisecond), b)
}
