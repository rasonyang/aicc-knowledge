// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
)

// PromptVersion identifies the prompt template below. It is stored on every
// candidate. Bump it whenever the wording of the system prompt, the user
// message or the schema changes, so a quality problem can be traced to the
// revision that produced it.
const PromptVersion = "faq-v1"

// Fixed generation limits.
const (
	// MaxCandidatesPerSection is how many Q&A pairs one section may yield.
	MaxCandidatesPerSection = 5
	// MinSectionChars: a section with less text than this (a heading and a
	// few words) has nothing a caller could ask about and is not sent.
	MinSectionChars = 20
	// MaxSectionChars bounds the text sent to the LLM; the rest is cut.
	MaxSectionChars = 6000
)

const schemaName = "faq_candidates"

// schema is the JSON schema the model is asked to follow. It is also
// enforced in Go (decodeOutput), because a server's grammar is not trusted.
var schema = map[string]any{
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
					"question":           map[string]any{"type": "string"},
					"alternateQuestions": map[string]any{"type": "array", "maxItems": candidate.MaxAlternates, "items": map[string]any{"type": "string"}},
					"answer":             map[string]any{"type": "string"},
					"language":           map[string]any{"type": "string", "enum": []string{"EN", "ZH"}},
				},
			},
		},
	},
}

func systemPrompt(lang domain.Language, lim candidate.Limits) string {
	return fmt.Sprintf(`You write FAQ entries for the knowledge base of a phone call center. A caller asks a question aloud and a voice assistant reads the answer to them over the phone.

You receive one section of a company document. Write up to %d question and answer pairs that a caller might really ask and that this section clearly answers.

Rules:
- Use only facts stated in the section. Never add, guess or generalize. If the section does not state the answer, do not write that question. Returning fewer entries, or an empty list, is correct when the section holds little.
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

func userPrompt(headingPath []string, body string) string {
	if r := []rune(body); len(r) > MaxSectionChars {
		body = string(r[:MaxSectionChars])
	}
	heading := strings.Join(headingPath, " > ")
	if heading == "" {
		heading = "(none)"
	}
	return "Section heading: " + heading + "\nSection text:\n\"\"\"\n" + body + "\n\"\"\""
}

// messages builds the first request of a section.
func messages(lang domain.Language, lim candidate.Limits, headingPath []string, body string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt(lang, lim)},
		{Role: llm.RoleUser, Content: userPrompt(headingPath, body)},
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
