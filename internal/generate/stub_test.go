// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"context"
	"strings"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

func TestClassifyStubsAndQuestionHeadings(t *testing.T) {
	sec := func(body string, path ...string) queries.ParsedSection {
		return queries.ParsedSection{Kind: string(domain.SectionKindDocxSection), HeadingPath: path, Body: body}
	}
	cases := []struct {
		name string
		s    queries.ParsedSection
		stub bool
		ctx  string
	}{
		{"title only", sec("", "Warranty"), true, "Warranty"},
		{"title with a boilerplate line", sec("Applicable products: Gadget One, Gadget Two", "Guide"), true, "Guide: Applicable products: Gadget One, Gadget Two"},
		{"full-width colon boilerplate", sec("适用产品：甲型号、乙型号\n版本：2.1", "指南"), true, "指南: 适用产品：甲型号、乙型号 版本：2.1"},
		{"short body without a label", sec("Black.", "Colors"), true, "Colors: Black."},
		{"real text", sec("The battery lasts about ten hours on a single charge.", "Battery"), false, ""},
		{"real text next to boilerplate", sec("Applicable products: Gadget One\nThe battery lasts about ten hours on a single charge.", "Battery"), false, ""},
		{"a long line with a colon is text", sec("Note: the battery lasts about ten hours on a single charge, and charging takes two.", "Battery"), false, ""},
		{"question heading, short answer", sec("No.", "Does it support wireless charging?"), false, ""},
		{"full-width question heading", sec("不支持。", "Guide", "是否支持无线充电？"), false, ""},
		{"Q-number heading", sec("Black.", "Q3 Colors"), false, ""},
		{"Q-number with space", sec("Black.", "Q 12 Colors"), false, ""},
		{"question heading, empty body", sec("", "Does it work?"), true, "Does it work?"},
		{"question heading, punctuation only", sec("-", "Does it work?"), true, "Does it work?: -"},
		{"Q without a digit is not a question", sec("Black.", "Quick start"), true, "Quick start: Black."},
	}
	for _, c := range cases {
		got := classify(c.s)
		if got.stub != c.stub || (c.stub && got.context != c.ctx) {
			t.Errorf("%s: classify = %+v, want stub=%v context=%q", c.name, got, c.stub, c.ctx)
		}
	}
}

func TestIsBoilerplate(t *testing.T) {
	for line, want := range map[string]bool{
		"Applicable products: A, B": true, "适用产品：甲、乙": true, "Version:2.1": true, "更新日期 ： 2024": true,
		"No colon here at all": false, ": no label": false,
		"A label that is much too long to be a label: value": false,
		"Note: " + strings.Repeat("long text ", 10):          false,
	} {
		if got := isBoilerplate(line); got != want {
			t.Errorf("isBoilerplate(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestStubSectionsAreContextNotInput(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		switch {
		case section(c, "Gadget Guide > Battery"):
			return llmtest.Reply(llmtest.Cand{Question: "How long does the battery last?", Answer: "About ten hours on a single charge.", Language: "EN"}), nil
		case section(c, "Gadget Guide > Does it support wireless charging?"):
			return llmtest.Reply(llmtest.Cand{Question: "Does it support wireless charging?", Answer: "No.", Language: "EN"}), nil
		case section(c, "Gadget Guide > Water resistance"):
			return llmtest.Reply(llmtest.Cand{Question: "Is the Gadget One water resistant?", Answer: "It has an IP67 rating.", Language: "EN"}), nil
		case section(c, "Gadget Guide > Q3 Colors"):
			return llmtest.Reply(llmtest.Cand{Question: "Which colors are there?", Answer: "Black.", Language: "EN"}), nil
		case section(c, "Gadget Guide > 是否支持无线充电？"):
			return llmtest.Reply(llmtest.Cand{Question: "是否支持无线充电？", Answer: "不支持。", Language: "ZH"}), nil
		}
		return empty, nil
	})
	h.Env.PutFixture("stubs.docx", "docx/stubs.docx")
	h.Env.ScanParse()
	sum := h.run()

	// Gadget Guide, Warranty and Release notes are stubs: not sent, counted, logged.
	// The five other sections are sent.
	if sum.Sections != 5 || sum.SkippedStub != 3 || sum.Candidates != 5 || sum.Warnings != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	calls := h.Server.Calls()
	if len(calls) != 5 {
		t.Fatalf("LLM calls = %d, want 5", len(calls))
	}
	for _, c := range calls {
		for _, stub := range []string{"Section heading: Gadget Guide\n", "Section heading: Gadget Guide > Warranty\n", "Section heading: Gadget Guide > Release notes\n"} {
			if strings.Contains(c.User(), stub) {
				t.Errorf("a stub was sent to the LLM:\n%s", c.User())
			}
		}
		// Every section carries the document title and its heading path.
		if !strings.HasPrefix(c.User(), "Section heading: Gadget Guide > ") || !strings.Contains(c.User(), "\nDocument title: stubs\n") {
			t.Errorf("prompt lacks the document title and heading path:\n%s", c.User())
		}
	}
	byHeading := func(h string) llmtest.Call {
		for _, c := range calls {
			if section(c, h) {
				return c
			}
		}
		t.Fatalf("no call for %q", h)
		return llmtest.Call{}
	}
	// The title stub (with its boilerplate line) is context of the next section sent.
	battery := byHeading("Gadget Guide > Battery").User()
	if !strings.Contains(battery, "Context (earlier headings and lines without text of their own):\nGadget Guide: Applicable products: Gadget One, Gadget Two\n") {
		t.Errorf("Battery lacks the stub context:\n%s", battery)
	}
	// Consumed once: the next section does not repeat it.
	if wireless := byHeading("Gadget Guide > Does it support wireless charging?").User(); strings.Contains(wireless, "Context") || !strings.Contains(wireless, "\nNo.\n") {
		t.Errorf("wireless charging prompt:\n%s", wireless)
	}
	if water := byHeading("Gadget Guide > Water resistance").User(); !strings.Contains(water, "\nGadget Guide > Warranty\n") {
		t.Errorf("Water resistance lacks the Warranty context:\n%s", water)
	}
	// Short Q&A sections are sent, with the heading as the question.
	for _, hd := range []string{"Gadget Guide > Q3 Colors", "Gadget Guide > 是否支持无线充电？"} {
		byHeading(hd)
	}

	log := h.Log.String()
	for _, s := range []string{"section skipped", "outcome=SKIPPED_STUB", "Warranty", "Release notes"} {
		if !strings.Contains(log, s) {
			t.Errorf("log lacks %q:\n%s", s, log)
		}
	}
	if m := h.metrics(); !strings.Contains(m, `kb_generate_sections_total{outcome="SKIPPED_STUB"} 3`) {
		t.Errorf("metrics lack the skipped stubs:\n%s", m)
	}
	if n := len(h.candidates("")); n != 5 {
		t.Errorf("candidates = %d, want 5", n)
	}
}

func TestUngroundedFigureIsRetriedThenDropped(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if section(c, "Gadget Guide > Water resistance") {
			// The section says IP67; the model keeps saying IP68 (a model-number-like figure).
			return llmtest.Reply(llmtest.Cand{Question: "Is the Gadget One water resistant?", Answer: "It has an IP68 rating, rated for 30 minutes.", Language: "EN"}), nil
		}
		return empty, nil
	})
	h.Env.PutFixture("stubs.docx", "docx/stubs.docx")
	h.Env.ScanParse()
	sum := h.run()
	if sum.Candidates != 0 || sum.Warnings != 1 {
		t.Fatalf("summary = %+v, want the ungrounded candidate dropped with one warning", sum)
	}
	var retry llmtest.Call
	for _, c := range h.Server.Calls() {
		if len(c.Messages) == 4 {
			retry = c
		}
	}
	if retry.N == 0 || !strings.Contains(retry.Last(), "UNGROUNDED_FIGURE") || !strings.Contains(retry.Last(), "68") {
		t.Fatalf("the retry feedback does not name the ungrounded figure: %+v", retry)
	}
	if log := h.Log.String(); !strings.Contains(log, "UNGROUNDED_FIGURE") || !strings.Contains(log, "candidate dropped") {
		t.Errorf("log lacks the warning:\n%s", log)
	}
}

func TestFigureOfALaterStubIsNotGrounded(t *testing.T) {
	// "2.1" is only in the "Release notes" stub, which comes after Battery, so
	// it is not part of Battery's source.
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		if section(c, "Gadget Guide > Battery") {
			return llmtest.Reply(llmtest.Cand{Question: "Which versions does this cover?", Answer: "Version 2.1 covers the battery of Gadget One.", Language: "EN"}), nil
		}
		return empty, nil
	})
	h.Env.PutFixture("stubs.docx", "docx/stubs.docx")
	h.Env.ScanParse()
	if sum := h.run(); sum.Candidates != 0 || sum.Warnings != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestTruncatedSectionIsLoggedAndCounted(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) { return empty, nil })
	long := strings.Repeat("The battery lasts a long time. ", 300) // 9300 characters
	var sum Summary
	res, err := h.Worker.section(context.Background(), "kb/long.docx", queries.ParsedSection{
		Kind: string(domain.SectionKindDocxSection), HeadingPath: []string{"Battery"}, Body: long, SourceRef: "kb/long.docx#Battery#1",
	}, "", &sum)
	if err != nil || res.skipped {
		t.Fatalf("section: %+v %v", res, err)
	}
	if sum.Truncated != 1 || sum.Sections != 1 {
		t.Errorf("summary = %+v", sum)
	}
	calls := h.Server.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	text := calls[0].User()
	body := text[strings.Index(text, "\"\"\"\n")+4 : strings.LastIndex(text, "\n\"\"\"")]
	if n := len([]rune(body)); n != MaxSectionChars {
		t.Errorf("body sent has %d characters, want %d", n, MaxSectionChars)
	}
	if log := h.Log.String(); !strings.Contains(log, "SECTION_TRUNCATED") || !strings.Contains(log, "kb/long.docx#Battery#1") {
		t.Errorf("log lacks the truncation warning:\n%s", log)
	}
	if m := h.metrics(); !strings.Contains(m, `kb_generate_sections_total{outcome="TRUNCATED"} 1`) {
		t.Errorf("metrics lack TRUNCATED:\n%s", m)
	}
}

func TestSectionWithoutLettersIsSkippedLoudly(t *testing.T) {
	h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) { return empty, nil })
	var sum Summary
	res, err := h.Worker.section(context.Background(), "kb/x.docx", queries.ParsedSection{
		Kind: string(domain.SectionKindDocxSection), HeadingPath: []string{"2024 - 59"}, Body: "1234567890 1234567890 1234567890", SourceRef: "kb/x.docx#x#1",
	}, "", &sum)
	if err != nil || !res.skipped || sum.SkippedNoLanguage != 1 || len(h.Server.Calls()) != 0 {
		t.Fatalf("res %+v sum %+v err %v", res, sum, err)
	}
	if !strings.Contains(h.Log.String(), "SKIPPED_NO_LANGUAGE") {
		t.Errorf("log: %s", h.Log.String())
	}
}

func TestMaxSectionCharsEqualsTheChunkLimit(t *testing.T) {
	if MaxSectionChars != 6000 {
		t.Errorf("MaxSectionChars = %d", MaxSectionChars)
	}
}
