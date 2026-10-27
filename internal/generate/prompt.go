// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// PromptVersion identifies the prompt template below. It is stored on every
// candidate. Bump it whenever the wording of the system prompt, the user
// message or the schema changes, so a quality problem can be traced to the
// revision that produced it.
//
// faq-v2: the user message carries the document title and the heading path,
// and may carry the text of preceding stub sections; the model is told to
// return no entries when the section states no answer.
//
// faq-v3: the output schema bounds every string (see schemaFor).
const PromptVersion = "faq-v3"

// Versions of the Q&A import. qa-import-v1 candidates are the sheet's own
// text, verbatim, and have no model. qa-condense-v1 candidates keep the
// question verbatim and have an answer the LLM shortened (condensePrompt).
const (
	QAImportVersion   = "qa-import-v1"
	QACondenseVersion = "qa-condense-v1"
)

// Fixed generation limits.
const (
	// MaxCandidatesPerSection is how many Q&A pairs one section may yield.
	MaxCandidatesPerSection = 5
	// MinMeaningfulChars: a section whose body, after its boilerplate lines
	// (short "label: value" lines such as an applicable-products line) are
	// removed, has fewer characters than this states nothing a caller could
	// ask about. It is a stub: it is not sent on its own, and its heading and
	// text are attached as context to the next section that is sent.
	// A section whose heading is itself a question is exempt (see
	// questionHeading): the heading is the question and a short body is its
	// answer.
	MinMeaningfulChars = 20
	// MaxContextChars bounds the stub context attached to a section.
	MaxContextChars = 600
	// MaxSectionChars bounds the text sent to the LLM. Longer text is cut, and
	// the cut is logged and counted. It equals xlsx.DefaultMaxChunkChars, so
	// workbook chunks are never cut here.
	MaxSectionChars = xlsx.DefaultMaxChunkChars
)

const schemaName = "faq_candidates"

// Bounds of the output schema. The grammar of a constrained server ends a
// string at maxLength, which stops a small model that repeats one sentence
// until the token cap. The bound is the limit candidate validation applies
// plus headroom, so validation (which names the rule and feeds the retry), not
// the grammar, decides what is too long: a string cut by the grammar is longer
// than the limit and is rejected as too long.
func headroom(limit int) int { return limit + limit/4 }

// Rough upper bounds of tokens per character, used only to derive max_tokens.
const (
	tokensPerCharEN = 0.5
	tokensPerCharZH = 1.5
	// tokensPerEntry covers the JSON keys and punctuation of one candidate.
	tokensPerEntry = 48
)

func tokensFor(lang domain.Language, chars int) int {
	per := tokensPerCharEN
	if lang == domain.LanguageZH {
		per = tokensPerCharZH
	}
	return int(float64(chars)*per) + 1
}

// schemaFor is the JSON schema the model is asked to follow for a section in
// lang. It is also enforced in Go (decodeOutput), because a server's grammar
// is not trusted.
func schemaFor(lang domain.Language, lim candidate.Limits) map[string]any {
	q := headroom(candidate.MaxQuestionChars)
	a := headroom(lim.MaxAnswer(lang))
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"candidates"},
		"properties": map[string]any{
			"candidates": map[string]any{
				"type":     "array",
				"maxItems": MaxCandidatesPerSection,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"question", "alternateQuestions", "answer", "language"},
					"properties": map[string]any{
						"question":           map[string]any{"type": "string", "maxLength": q},
						"alternateQuestions": map[string]any{"type": "array", "maxItems": candidate.MaxAlternates, "items": map[string]any{"type": "string", "maxLength": q}},
						"answer":             map[string]any{"type": "string", "maxLength": a},
						"language":           map[string]any{"type": "string", "enum": []string{"EN", "ZH"}},
					},
				},
			},
		},
	}
}

// maxTokensFor derives the completion cap of a section from the schema bounds:
// the most a schema-conforming answer can take (every entry at its bounds),
// so a valid answer is never cut and a runaway one ends as soon as it is
// longer than any valid one. The client clamps it to its own ceiling.
func maxTokensFor(lang domain.Language, lim candidate.Limits) int {
	q := headroom(candidate.MaxQuestionChars)
	a := headroom(lim.MaxAnswer(lang))
	perEntry := tokensFor(lang, q*(1+candidate.MaxAlternates)+a) + tokensPerEntry
	return MaxCandidatesPerSection*perEntry + tokensPerEntry
}

func systemPrompt(lang domain.Language, lim candidate.Limits) string {
	return fmt.Sprintf(`You write FAQ entries for the knowledge base of a phone call center. A caller asks a question aloud and a voice assistant reads the answer to them over the phone.

You receive one section of a company document. Write up to %d question and answer pairs that a caller might really ask and that this section clearly answers.

Rules:
- Use only facts stated in the section text. Never add, guess or generalize, and never use the document title or the heading as a source of facts: they only tell you what the section is about. If the section text does not state the answer, do not write that question. Returning fewer entries, or an empty list, is correct when the section holds little; a section that only has a heading, or only a line such as "applicable products", has no answer and gets an empty list.
- A heading that is itself a question, followed by a short statement, is a question and its answer: use the heading as the question and the statement as the answer.
- "Context" lines, when present, come from earlier headings and lines of the same document that had no text of their own. They may help you understand the section, but write entries only about what the section text states.
- Every question stands alone: never say "this section", "the document" or "above". Write it as a complete sentence with the normal capital letters and a question mark at the end. Questions must ask about different things; never repeat a question or reword it as another entry.
- alternateQuestions holds 0 to %d other ways to ask the SAME question, as callers would say them. No new topics.
- The answer is spoken aloud: ONE or TWO short sentences, at most %d characters. Plain words only: no Markdown, no lists, no bullet points, no headings, no tables, no URLs or web addresses, no emoji. State the key fact directly; do not copy sentences or paragraphs from the section.
- Keep numbers, prices, dates and names exactly as the section states them.
- Write the questions and the answer in the same language as the section (%s), and set language to %q.`,
		MaxCandidatesPerSection, candidate.MaxAlternates, lim.MaxAnswer(lang), languageName(lang), string(lang))
}

func languageName(l domain.Language) string {
	if l == domain.LanguageZH {
		return "Chinese"
	}
	return "English"
}

// userPrompt renders the request for one section: the document title, the
// heading path, optional context from stub sections, and the (already bounded)
// text. The "Section heading: " line stays the first line: tests read it.
func userPrompt(docTitle string, headingPath []string, context, body string) string {
	heading := strings.Join(headingPath, " > ")
	if heading == "" {
		heading = "(none)"
	}
	var b strings.Builder
	b.WriteString("Section heading: " + heading + "\n")
	if docTitle != "" {
		b.WriteString("Document title: " + docTitle + "\n")
	}
	if context != "" {
		b.WriteString("Context (earlier headings and lines without text of their own):\n" + context + "\n")
	}
	b.WriteString("Section text:\n\"\"\"\n" + body + "\n\"\"\"")
	return b.String()
}

// messages builds the first request of a section.
func messages(lang domain.Language, lim candidate.Limits, docTitle string, headingPath []string, context, body string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt(lang, lim)},
		{Role: llm.RoleUser, Content: userPrompt(docTitle, headingPath, context, body)},
	}
}

const condenseSchemaName = "faq_condensed_answer"

// condenseSchemaFor is the JSON schema of the condense answer in lang.
func condenseSchemaFor(lang domain.Language, lim candidate.Limits) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string", "maxLength": headroom(lim.MaxAnswer(lang))},
		},
	}
}

// condenseMaxTokens is the completion cap of a condense request.
func condenseMaxTokens(lang domain.Language, lim candidate.Limits) int {
	return tokensFor(lang, headroom(lim.MaxAnswer(lang))) + tokensPerEntry
}

// condensePrompt asks for a short, speakable version of one curated answer
// (qa-condense-v1). The question is given for context only and stays
// verbatim in the candidate.
func condenseMessages(lang domain.Language, lim candidate.Limits, question, answer string) []llm.Message {
	sys := fmt.Sprintf(`You shorten answers for the knowledge base of a phone call center. A voice assistant reads the answer to a caller over the phone.

You receive a question and the full, curated answer to it. Rewrite the answer so that it can be spoken.

Rules:
- Use only facts stated in the given answer. Never add, guess or generalize. Keep numbers, prices, dates, names and model names exactly as written.
- ONE or TWO short sentences, at most %d characters. Plain words only: no Markdown, no lists, no bullet points, no line breaks, no tables, no URLs or web addresses, no emoji.
- Keep the most important fact first. Turn a list into one short sentence that names the key items, or the most important ones.
- Write in the same language as the answer (%s).`, lim.MaxAnswer(lang), languageName(lang))
	return []llm.Message{
		{Role: llm.RoleSystem, Content: sys},
		{Role: llm.RoleUser, Content: "Question: " + question + "\nFull answer:\n\"\"\"\n" + answer + "\n\"\"\""},
	}
}

// feedback builds the follow-up turn of the one retry: the model sees its own
// answer and the rules it broke.
func feedback(previous string, problems []problem) []llm.Message {
	var b strings.Builder
	b.WriteString("Your previous answer broke these rules:\n")
	for _, p := range problems {
		b.WriteString("- " + p.String() + "\n")
	}
	b.WriteString("Return the complete JSON again. Keep the entries that were fine and fix or remove the others. Answers must be one or two short spoken sentences in plain words, in the language of the section.")
	return []llm.Message{{Role: llm.RoleAssistant, Content: previous}, {Role: llm.RoleUser, Content: b.String()}}
}

// retryMessages builds the messages of the one retry. When the first attempt
// left no usable text (the completion was cut off or was not JSON), there is
// nothing to show the model as its previous answer: an empty assistant turn
// teaches nothing, so the problems go into the request itself.
func retryMessages(msgs []llm.Message, raw string, problems []problem) []llm.Message {
	if raw != "" {
		return append(append([]llm.Message{}, msgs...), feedback(raw, problems)...)
	}
	var b strings.Builder
	b.WriteString("\n\nYour previous attempt was rejected:\n")
	for _, p := range problems {
		b.WriteString("- " + p.String() + "\n")
	}
	b.WriteString("Answer again. Keep it short: at most a few entries, each answer one or two short spoken sentences. Never repeat a sentence.")
	out := append([]llm.Message{}, msgs...)
	out[len(out)-1].Content += b.String()
	return out
}

// problem is one rule a candidate of the model's answer broke.
type problem struct {
	Index      int // 1-based position in the model's list; 0 for the answer as a whole
	Question   string
	Violations []candidate.Violation
}

func (p problem) String() string {
	var parts []string
	for _, v := range p.Violations {
		parts = append(parts, v.String())
	}
	if p.Index == 0 {
		return strings.Join(parts, "; ")
	}
	q := p.Question
	if utf8.RuneCountInString(q) > 60 {
		q = string([]rune(q)[:60]) + "..."
	}
	return fmt.Sprintf("entry %d (%q): %s", p.Index, q, strings.Join(parts, "; "))
}
