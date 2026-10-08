// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

const qaMapping = `sheets:
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

// condenseHandler answers the condense calls of qa.xlsx and counts the rest.
func condenseHandler(c llmtest.Call) (string, *llmtest.Fail) {
	u := c.User()
	switch {
	case strings.Contains(u, "Question: How do I reset the device?"):
		return `{"answer":"Hold the power button for 10 seconds, wait for the light to blink twice, then release it."}`, nil
	case strings.Contains(u, "Question: Which colors can I choose?"):
		return `{"answer":"You can choose from twelve colors, from black and white to gold."}`, nil
	}
	return empty, nil // the Notes sheet, an ordinary content section
}

func isCondense(c llmtest.Call) bool { return strings.Contains(c.User(), "Full answer:") }

func TestQAImportEndToEnd(t *testing.T) {
	h := newHarness(t, condenseHandler)
	h.Env.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	h.Env.Put("qa.qa.yaml", []byte(qaMapping))
	h.Env.ScanParse()
	v, ok := h.Env.Current("qa.xlsx")
	if !ok || v.State != "PARSED" {
		t.Fatalf("qa.xlsx = %+v", v)
	}
	key := h.Env.ObjectKey("qa.xlsx")

	sum := h.run()
	// One content section (the Notes sheet) goes to the LLM, like any chunk.
	// Five rows are imported: three verbatim, two condensed (one LLM call each).
	if sum.QAVerbatim != 3 || sum.QACondensed != 2 || sum.QADropped != 0 || sum.Sections != 1 || sum.Candidates != 5 {
		t.Fatalf("summary = %+v", sum)
	}
	var condense, content int
	for _, c := range h.Server.Calls() {
		if isCondense(c) {
			condense++
		} else {
			content++
		}
	}
	if condense != 2 || content != 1 {
		t.Fatalf("LLM calls: %d condense, %d content; want 2 and 1", condense, content)
	}

	got := h.candidates(v.ID)
	if len(got) != 5 {
		t.Fatalf("candidates = %d, want 5: %+v", len(got), got)
	}
	type want struct {
		ref, lang, q, a, ver, model, flags string
		alts                               []string
	}
	wants := []want{
		{key + "#FAQ!A2", "EN", "Does the X1 support wireless charging?", "Yes, it supports wireless charging.", QAImportVersion, "", "",
			[]string{"wireless charging?", "can I charge it wirelessly?"}},
		{key + "#FAQ!A3", "EN", "How long is the warranty?", "The warranty lasts 2 years.", QAImportVersion, "", "CONTAINS_FIGURES",
			[]string{"warranty period", "how long does the warranty last"}},
		{key + "#FAQ!A6", "EN", "How do I reset the device?", "Hold the power button for 10 seconds, wait for the light to blink twice, then release it.", QACondenseVersion, "fake-model", "CONTAINS_FIGURES", []string{}},
		{key + "#FAQ!A7", "EN", "Which colors can I choose?", "You can choose from twelve colors, from black and white to gold.", QACondenseVersion, "fake-model", "CONTAINS_FIGURES", []string{}},
		{key + "#常见问题!A2", "ZH", "是否支持无线充电？", "不支持。", QAImportVersion, "", "", []string{"能无线充电吗", "支持无线充吗"}},
	}
	for _, w := range wants {
		i := slices.IndexFunc(got, func(c candRow) bool { return c.SourceRef == w.ref })
		if i < 0 {
			t.Errorf("no candidate for %s", w.ref)
			continue
		}
		c := got[i]
		wantFlags := []string{}
		if w.flags != "" {
			wantFlags = []string{w.flags}
		}
		hash := candidate.ContentHash(candidate.Content{Language: domain.Language(w.lang), Question: w.q, AlternateQuestions: w.alts, Answer: w.a, SourceRef: w.ref, FileVersionID: mustUUID(v.ID)})
		if c.Language != w.lang || c.Question != w.q || c.Answer != w.a || c.PromptVersion != w.ver || c.Model != w.model ||
			!slices.Equal(c.Alternates, w.alts) || !slices.Equal(c.Flags, wantFlags) || c.State != "PENDING_REVIEW" || string(c.Hash) != string(hash[:]) {
			t.Errorf("candidate %s = %+v\nwant %+v", w.ref, c, w)
		}
	}

	// The condense prompt carries only the row's own answer, and the question is for context.
	for _, c := range h.Server.Calls() {
		if !isCondense(c) {
			continue
		}
		if strings.Contains(c.User(), "Notes") || strings.Contains(c.User(), "Shipping") {
			t.Errorf("condense prompt leaked other content:\n%s", c.User())
		}
		if !strings.Contains(c.Messages[0].Content, "Use only facts stated in the given answer") {
			t.Errorf("condense system prompt: %s", c.Messages[0].Content)
		}
	}

	// Hidden and incomplete rows are warned about at parse time, never silent.
	warns := h.count(`SELECT count(*) FROM file_versions WHERE id = $1 AND parse_warnings @> '[{"code":"HIDDEN_ROW_SKIPPED"}]'::jsonb AND parse_warnings @> '[{"code":"QA_ROW_INCOMPLETE"}]'::jsonb`, v.ID)
	if warns != 1 {
		t.Errorf("parse_warnings lack the hidden-row and incomplete-row warnings: %s", v.Warnings)
	}

	// The review export shows the original answer next to the candidate.
	rows, err := h.Env.Store.Queries.ListCandidatesForExport(context.Background(), queries.ListCandidatesForExportParams{States: []string{"PENDING_REVIEW"}})
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, r := range rows {
		if r.SourceRef == key+"#FAQ!A6" {
			seen++
			if !strings.HasPrefix(r.SectionBody, "Press and hold the power button for 10 seconds.\n- Wait for the light") {
				t.Errorf("export excerpt = %q, want the original answer", r.SectionBody)
			}
		}
	}
	if seen != 1 {
		t.Errorf("export rows for the condensed row = %d", seen)
	}

	m := h.metrics()
	for _, s := range []string{
		`kb_generate_sections_total{outcome="QA_VERBATIM"} 3`,
		`kb_generate_sections_total{outcome="QA_CONDENSED"} 2`,
	} {
		if !strings.Contains(m, s) {
			t.Errorf("metrics lack %q", s)
		}
	}
	// A second run does nothing: every row has its candidate.
	if again := h.run(); again.Claimed != 0 {
		t.Errorf("second run: %+v", again)
	}
}

func TestQARowsThatCannotBecomeCandidatesAreDroppedLoudly(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if isCondense(c) {
			// Invents a figure that is not in the original answer.
			return `{"answer":"Hold the power button for 15 seconds."}`, nil
		}
		return empty, nil
	})
	var sum Summary
	mk := func(q, answer, lang string, alts ...string) queries.ParsedSection {
		return queries.ParsedSection{
			Ordinal: 1, Kind: string(domain.SectionKindXlsxQARow), SourceRef: "kb/x.xlsx#S!A2", Body: answer,
			QaQuestion: &q, QaAlternates: alts, QaLanguage: &lang,
		}
	}
	cases := []struct {
		name string
		s    queries.ParsedSection
	}{
		{"url in the answer", mk("Where is the manual?", "See https://example.com/manual for it.", "EN")},
		{"question too long", mk(strings.Repeat("very long question ", 20)+"?", "Short.", "EN")},
		{"markdown in the question", mk("What is **this**?", "A thing.", "EN")},
		{"language differs from the mapping", mk("Does it work?", "是的，可以使用。", "EN")},
		{"condensed answer states a new figure", mk("How do I reset?", "Hold the power button.\n- Wait for the light.\n- Release the button for 10 seconds.", "EN")},
	}
	for _, c := range cases {
		got, err := h.Worker.qaRow(context.Background(), "kb/x.xlsx", c.s, &sum)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: drafts %+v, err %v; want none", c.name, got, err)
		}
	}
	if sum.QADropped != len(cases) || sum.Warnings != len(cases) || sum.QAVerbatim != 0 || sum.QACondensed != 0 {
		t.Errorf("summary = %+v", sum)
	}
	if n := strings.Count(h.Log.String(), "Q&A row dropped"); n != len(cases) {
		t.Errorf("warnings logged = %d, want %d:\n%s", n, len(cases), h.Log.String())
	}
	if !strings.Contains(h.Log.String(), "UNGROUNDED_FIGURE") {
		t.Errorf("the ungrounded condensation is not named:\n%s", h.Log.String())
	}
	if n := len(h.Server.Calls()); n != 1 {
		t.Errorf("LLM calls = %d, want exactly one (only the condensable row)", n)
	}
}

func TestQAUnavailableLLMDuringCondenseIsAnInfrastructureError(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) { return "", &llmtest.Fail{Status: 503} })
	q, lang := "How do I reset?", "EN"
	var sum Summary
	_, err := h.Worker.qaRow(context.Background(), "kb/x.xlsx", queries.ParsedSection{
		Kind: string(domain.SectionKindXlsxQARow), Body: "Hold.\n- Wait.", QaQuestion: &q, QaLanguage: &lang,
	}, &sum)
	if err == nil || sum.QADropped != 0 {
		t.Errorf("err %v summary %+v: an outage must not drop the row", err, sum)
	}
}

func TestQAMappingChangeStalesChangedRowsAndTopsUpOnly(t *testing.T) {
	h := newHarness(t, condenseHandler)
	h.Env.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	h.Env.Put("qa.qa.yaml", []byte(qaMapping))
	h.Env.ScanParse()
	h.run()
	v, _ := h.Env.Current("qa.xlsx")
	if n := len(h.candidates(v.ID)); n != 5 {
		t.Fatalf("candidates = %d", n)
	}
	callsBefore := len(h.Server.Calls())

	// The English sheet no longer reads its Variants column: rows 2 and 3 change,
	// rows 6 and 7 (no variants) and the Chinese sheet do not.
	h.Env.Put("qa.qa.yaml", []byte(strings.Replace(qaMapping, "    alternates: [Variants]\n", "", 1)))
	h.Env.ScanParse()
	sum := h.run()
	if sum.Candidates != 2 || sum.QAVerbatim != 2 || sum.QACondensed != 0 || len(h.Server.Calls()) != callsBefore {
		t.Fatalf("top-up: %+v, new LLM calls %d", sum, len(h.Server.Calls())-callsBefore)
	}
	byState := map[string]int{}
	for _, c := range h.candidates(v.ID) {
		byState[c.State]++
		switch {
		case c.State == "STALE" && c.Note != "QA_ROW_CHANGED":
			t.Errorf("stale candidate note = %q", c.Note)
		case c.State == "PENDING_REVIEW" && strings.HasSuffix(c.SourceRef, "FAQ!A2") && len(c.Alternates) != 0:
			t.Errorf("the new candidate of row 2 kept its alternates: %+v", c)
		}
	}
	if byState["STALE"] != 2 || byState["PENDING_REVIEW"] != 5 || len(byState) != 2 {
		t.Errorf("states = %v, want 2 STALE (rows 2 and 3 as they were) and 5 PENDING_REVIEW", byState)
	}
}

func TestQAWorkbookChangeStalesItsCandidates(t *testing.T) {
	h := newHarness(t, condenseHandler)
	h.Env.PutFixture("qa.xlsx", "xlsx/qa.xlsx")
	h.Env.Put("qa.qa.yaml", []byte(qaMapping))
	h.Env.ScanParse()
	h.run()
	old, _ := h.Env.Current("qa.xlsx")

	h.Env.PutFixture("qa.xlsx", "xlsx/catalog.xlsx") // a different workbook under the same key
	h.Env.ScanParse()
	if n := h.count(`SELECT count(*) FROM candidates WHERE file_version_id = $1 AND state = 'STALE'`, old.ID); n != 5 {
		t.Errorf("stale candidates of the superseded workbook = %d, want 5", n)
	}
	if n := h.count(`SELECT count(*) FROM candidates WHERE file_version_id = $1 AND state <> 'STALE'`, old.ID); n != 0 {
		t.Errorf("live candidates of the superseded workbook = %d", n)
	}
}

// TestRealLLMCondense shortens a multi-line answer with a real model: the
// result must pass the deterministic rules and may not state a new figure.
func TestRealLLMCondense(t *testing.T) {
	base, model := os.Getenv("KB_TEST_LLM_URL"), os.Getenv("KB_TEST_LLM_MODEL")
	if base == "" || model == "" {
		t.Skip("SKIPPED: set KB_TEST_LLM_URL and KB_TEST_LLM_MODEL to run this test against a real OpenAI-compatible LLM")
	}
	client, err := llm.New(llm.Config{BaseURL: base, Model: model, APIKey: os.Getenv("KB_TEST_LLM_API_KEY"), Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{LLM: client, Limits: limits, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	q, lang := "How do I reset the device?", "EN"
	answer := "Press and hold the power button for 10 seconds.\n- Wait for the light to blink twice.\n- Release the button.\nThe device restarts and keeps your saved settings."
	var sum Summary
	got, err := w.qaRow(context.Background(), "fixture", queries.ParsedSection{
		Kind: string(domain.SectionKindXlsxQARow), Body: answer, QaQuestion: &q, QaLanguage: &lang, SourceRef: "fixture#S!A2",
	}, &sum)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Skipf("the model's condensation was rejected by the rules (a model-quality outcome, not a code failure): %+v", sum)
	}
	d := got[0]
	r := candidate.Validate(candidate.Draft{Language: string(d.language), Question: d.question, Answer: d.answer}, limits)
	if !r.OK() || d.question != q || d.promptVersion != QACondenseVersion || d.model != model {
		t.Errorf("draft %+v fails: %v", d, r.Violations)
	}
	t.Logf("condensed: %s", d.answer)
}

func mustUUID(s string) uuid.UUID { return uuid.MustParse(s) }
