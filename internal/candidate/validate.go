// SPDX-License-Identifier: Apache-2.0

package candidate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
)

// Violation codes. A violation is why a candidate is not fit for review.
const (
	CodeEmptyQuestion   = "EMPTY_QUESTION"
	CodeEmptyAnswer     = "EMPTY_ANSWER"
	CodeQuestionTooLong = "QUESTION_TOO_LONG"
	CodeAnswerTooLong   = "ANSWER_TOO_LONG"
	CodeMultiline       = "MULTILINE"
	CodeMarkdown        = "MARKDOWN"
	CodeURL             = "URL"
	CodeBadLanguage     = "BAD_LANGUAGE"
	CodeLanguageMixed   = "LANGUAGE_MISMATCH"
)

// Limits bounds the text of a candidate. Lengths are in characters (runes).
type Limits struct {
	MaxAnswerEN int
	MaxAnswerZH int
}

// Fixed limits.
const (
	// MaxQuestionChars bounds a question and an alternate question.
	MaxQuestionChars = 200
	// MaxAlternates is the most alternate questions a candidate keeps.
	MaxAlternates = 3
)

// MaxAnswer returns the answer limit of a language.
func (l Limits) MaxAnswer(lang domain.Language) int {
	if lang == domain.LanguageZH {
		return l.MaxAnswerZH
	}
	return l.MaxAnswerEN
}

// Violation is one broken rule.
type Violation struct {
	Code   string
	Field  string // question, answer or language
	Detail string
}

func (v Violation) String() string {
	if v.Detail == "" {
		return v.Code
	}
	return v.Code + " (" + v.Detail + ")"
}

// Draft is candidate text before validation.
type Draft struct {
	Language           string
	Question           string
	AlternateQuestions []string
	Answer             string
}

// Result is a draft after Validate: cleaned text and the violations found.
// When there are violations the cleaned text is still returned (it is what
// feedback to the LLM quotes) but must not be stored.
type Result struct {
	Language           domain.Language
	Question           string
	AlternateQuestions []string
	Answer             string
	Violations         []Violation
}

// OK reports whether the draft passed.
func (r Result) OK() bool { return len(r.Violations) == 0 }

// Validate trims and cleans a draft and applies the deterministic rules:
//
//   - the language is EN or ZH, and the answer is written in it (script check);
//   - question and answer are non-empty, single-line, without Markdown or
//     HTML markup and without URLs, and not longer than their limits (the
//     answer limit depends on the language);
//   - alternate questions are normalised, not a rule: empty, over-long,
//     marked-up, URL-bearing ones are dropped, as are ones equal to the
//     question or to each other (compared by Normalize), and at most
//     MaxAlternates are kept.
func Validate(d Draft, lim Limits) Result {
	r := Result{Language: domain.Language(strings.ToUpper(strings.TrimSpace(d.Language)))}
	add := func(code, field, detail string) {
		r.Violations = append(r.Violations, Violation{Code: code, Field: field, Detail: detail})
	}
	if !r.Language.Valid() {
		add(CodeBadLanguage, "language", fmt.Sprintf("%q is not EN or ZH", d.Language))
	}
	q, a := strings.TrimSpace(d.Question), strings.TrimSpace(d.Answer)
	if strings.ContainsAny(q, "\r\n") {
		add(CodeMultiline, "question", "")
	}
	if strings.ContainsAny(a, "\r\n") {
		add(CodeMultiline, "answer", "a list or several paragraphs; use one or two sentences")
	}
	r.Question, r.Answer = Clean(q), Clean(a)

	if r.Question == "" {
		add(CodeEmptyQuestion, "question", "")
	} else if n := utf8.RuneCountInString(r.Question); n > MaxQuestionChars {
		add(CodeQuestionTooLong, "question", fmt.Sprintf("%d characters, the limit is %d", n, MaxQuestionChars))
	}
	if r.Answer == "" {
		add(CodeEmptyAnswer, "answer", "")
	} else if r.Language.Valid() {
		if n, max := utf8.RuneCountInString(r.Answer), lim.MaxAnswer(r.Language); n > max {
			add(CodeAnswerTooLong, "answer", fmt.Sprintf("%d characters, the limit for %s is %d", n, r.Language, max))
		}
	}
	for _, f := range []struct{ field, text string }{{"question", r.Question}, {"answer", r.Answer}} {
		if markup(f.text) {
			add(CodeMarkdown, f.field, "plain spoken text only: no Markdown, bullets or HTML")
		}
		if hasURL(f.text) {
			add(CodeURL, f.field, "no URLs or web addresses")
		}
	}
	if r.Language.Valid() && r.Answer != "" {
		if got, ok := DetectLanguage(r.Answer); ok && got != r.Language {
			add(CodeLanguageMixed, "answer", fmt.Sprintf("language is %s but the answer is written in %s", r.Language, got))
		}
	}

	seen := map[string]bool{Normalize(r.Question): true}
	r.AlternateQuestions = []string{}
	for _, alt := range d.AlternateQuestions {
		alt = Clean(alt)
		key := Normalize(alt)
		if alt == "" || key == "" || seen[key] || utf8.RuneCountInString(alt) > MaxQuestionChars || markup(alt) || hasURL(alt) {
			continue
		}
		if len(r.AlternateQuestions) == MaxAlternates {
			break
		}
		seen[key] = true
		r.AlternateQuestions = append(r.AlternateQuestions, alt)
	}
	return r
}

var markupPatterns = []*regexp.Regexp{
	regexp.MustCompile("\\*\\*|__|~~|`"),
	regexp.MustCompile(`^#{1,6}\s`),
	regexp.MustCompile(`^(?:[-*+•·]\s|\d{1,2}[.)、]\s|>\s)`),
	regexp.MustCompile(`\s[•·]\s|\s\|\s`),
	regexp.MustCompile(`\[[^\]]+\]\([^)]*\)`),
	regexp.MustCompile(`(?:^|\s)\*[^*\s][^*]*\*(?:\s|$)`),
	regexp.MustCompile(`</?[A-Za-z][^>]*>`),
}

func markup(s string) bool {
	for _, p := range markupPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

var urlPattern = regexp.MustCompile(`(?i)(?:https?://|ftp://|www\.)\S+|\b[a-z0-9][a-z0-9-]*(?:\.[a-z0-9-]+)*\.(?:com|net|org|cn|io|co|edu|gov|info|biz|app|dev)\b`)

func hasURL(s string) bool { return urlPattern.MatchString(s) }
