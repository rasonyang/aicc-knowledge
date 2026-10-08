// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// qaRow turns one Q&A row (a section of kind XLSX_QA_ROW) into at most one
// draft, without the LLM unless the answer needs shortening:
//
//   - the question, the alternate questions and the answer are the sheet's own
//     text. If they pass candidate.Validate the draft is verbatim
//     (qa-import-v1, no model);
//   - if only the answer fails, and only because it is too long, has line
//     breaks or Markdown, the LLM is asked ONCE to shorten it using nothing but
//     that answer (qa-condense-v1). The question stays verbatim. The result
//     must pass the same rules and may not state a figure the original answer
//     does not;
//   - anything else (an invalid question, a URL in the answer, a language that
//     is not the mapping's, a failed condensation) drops the row with a
//     warning. A row is never silent.
func (w *Worker) qaRow(ctx context.Context, objectKey string, s queries.ParsedSection, sum *Summary) ([]draft, error) {
	lang := domain.Language("")
	if s.QaLanguage != nil {
		lang = domain.Language(*s.QaLanguage)
	}
	question := ""
	if s.QaQuestion != nil {
		question = *s.QaQuestion
	}
	answer := strings.TrimSpace(s.Body)
	in := candidate.Draft{Language: string(lang), Question: question, AlternateQuestions: s.QaAlternates, Answer: answer}
	r := candidate.Validate(in, w.Limits)

	outcome := SectionQAVerbatim
	ver, model := QAImportVersion, ""
	if !r.OK() {
		if !condensable(r.Violations) {
			return nil, w.dropQA(ctx, objectKey, s, lang, sum, "validation failed: "+violationList(r.Violations))
		}
		short, err := w.condense(ctx, lang, r.Question, answer, sum)
		if err != nil {
			return nil, err
		}
		if short.problem != "" {
			return nil, w.dropQA(ctx, objectKey, s, lang, sum, "condensing the answer failed: "+short.problem)
		}
		in.Answer = short.answer
		r = candidate.Validate(in, w.Limits)
		outcome, ver, model = SectionQACondensed, QACondenseVersion, w.LLM.Model()
	}
	if outcome == SectionQAVerbatim {
		sum.QAVerbatim++
	} else {
		sum.QACondensed++
	}
	if w.Metrics != nil {
		w.Metrics.ObserveGenerateSection(ctx, outcome)
	}
	w.count(ctx, string(lang), OutcomeCreated, 0) // keeps the series visible
	return []draft{{
		language: r.Language, question: r.Question, alternates: r.AlternateQuestions, answer: r.Answer,
		generated: time.Now(), promptVersion: ver, model: model,
	}}, nil
}

// condensable reports whether the violations are all about the answer being
// too long or not plain single-line text: what a rewrite can fix.
func condensable(vs []candidate.Violation) bool {
	for _, v := range vs {
		if v.Field != "answer" {
			return false
		}
		switch v.Code {
		case candidate.CodeAnswerTooLong, candidate.CodeMultiline, candidate.CodeMarkdown:
		default:
			return false
		}
	}
	return len(vs) > 0
}

func violationList(vs []candidate.Violation) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.Field + " " + v.String()
	}
	return strings.Join(parts, "; ")
}

func (w *Worker) dropQA(ctx context.Context, objectKey string, s queries.ParsedSection, lang domain.Language, sum *Summary, why string) error {
	sum.QADropped++
	sum.Warnings++
	w.log().Warn("Q&A row dropped", "sourceRef", s.SourceRef, "key", objectKey, "why", why)
	w.count(ctx, string(lang), OutcomeDroppedInvalid, 1)
	if w.Metrics != nil {
		w.Metrics.ObserveGenerateSection(ctx, SectionQADropped)
	}
	return nil
}

type condensed struct {
	answer  string
	problem string // set when the model's answer cannot be used
}

// condense makes the one LLM call that shortens an answer. An error is an
// infrastructure error; a model answer that is unusable is a problem.
func (w *Worker) condense(ctx context.Context, lang domain.Language, question, answer string, sum *Summary) (condensed, error) {
	content, err := w.complete(ctx, condenseMessages(lang, w.Limits, question, answer), condenseSchemaName, condenseSchema, func(b []byte) error {
		_, err := decodeCondensed(b)
		return err
	}, sum)
	var le *llm.Error
	if errors.Is(err, llm.ErrOutputInvalid) && errors.As(err, &le) {
		return condensed{problem: le.Error()}, nil
	}
	if err != nil {
		return condensed{}, err
	}
	short, _ := decodeCondensed([]byte(content))
	// The same rules as any answer, against the original answer as the source.
	r := candidate.Validate(candidate.Draft{Language: string(lang), Question: question, Answer: short}, w.Limits)
	violations := r.Violations
	if v, ungrounded := candidate.CheckGrounded(r.Answer, answer); ungrounded {
		violations = append(violations, v)
	}
	if len(violations) > 0 {
		return condensed{problem: violationList(violations)}, nil
	}
	return condensed{answer: r.Answer}, nil
}

// decodeCondensed checks the condense answer against its schema in Go: an
// object with exactly one key, "answer", holding a string.
func decodeCondensed(content []byte) (string, error) {
	dec := json.NewDecoder(strings.NewReader(string(content)))
	dec.DisallowUnknownFields()
	var out struct {
		Answer *string `json:"answer"`
	}
	if err := dec.Decode(&out); err != nil {
		return "", fmt.Errorf("not the expected JSON object: %w", err)
	}
	if dec.More() {
		return "", errors.New("trailing data after the JSON value")
	}
	if out.Answer == nil {
		return "", errors.New(`missing "answer"`)
	}
	return *out.Answer, nil
}
