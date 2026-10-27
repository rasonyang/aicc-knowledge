// SPDX-License-Identifier: Apache-2.0

package generate

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
)

// An empty list is EMPTY only as the first, unretried answer. When the first
// attempt had tried to answer (bad JSON, or only invalid entries) and the retry
// comes back empty, the section is FAILED and --retry-failed picks it up.
func TestEmptyRetryAfterABadFirstAttemptIsFailedAndRetryable(t *testing.T) {
	for _, tc := range []struct{ name, first, code string }{
		{"invalid JSON", `{"candidates":[{"question":"q"}]}`, "LLM_OUTPUT_INVALID"},
		{"invalid entries", llmtest.Reply(llmtest.Cand{Question: billingQ, Answer: "Pay at www.example.com/pay today.", Language: "EN"}), CodeCandidatesInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recovered := false
			h := newHarness(t, func(c llmtest.Call) (string, *llmtest.Fail) {
				if !section(c, "Billing") {
					return empty, nil
				}
				if recovered {
					return llmtest.Reply(llmtest.Cand{Question: billingQ, Answer: billingA, Language: "EN"}), nil
				}
				if strings.Contains(c.User(), "previous attempt was rejected") || len(c.Messages) == 4 {
					return empty, nil // the retry gives up with an empty list
				}
				return tc.first, nil
			})
			h.Env.PutFixture("faq_en.docx", "docx/faq_en.docx")
			h.Env.ScanParse()
			sum := h.run()
			if sum.SectionsFailed != 1 || sum.SectionsEmpty != 2 || sum.Candidates != 0 {
				t.Fatalf("summary = %+v", sum)
			}
			if got := h.outcomes()["Billing"]; got != [2]string{"FAILED", tc.code} {
				t.Fatalf("Billing outcome = %v, want FAILED %s", got, tc.code)
			}
			// The two sections whose first answer was empty stay EMPTY.
			if n := h.count(`SELECT count(*) FROM parsed_sections WHERE generate_outcome = 'EMPTY'`); n != 2 {
				t.Fatalf("EMPTY sections = %d, want 2", n)
			}
			recovered = true
			r, err := h.Worker.RetryFailedSections(context.Background(), uuid.Nil)
			if err != nil || r.Sections != 1 || r.Candidates != 1 || r.SectionsFailed != 0 {
				t.Fatalf("retry = %+v %v", r, err)
			}
			if got := h.outcomes()["Billing"]; got != [2]string{"GENERATED", ""} {
				t.Fatalf("Billing outcome after retry = %v", got)
			}
		})
	}
}
