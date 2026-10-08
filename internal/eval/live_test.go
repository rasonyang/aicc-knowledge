// SPDX-License-Identifier: Apache-2.0

package eval_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/eval"
	"github.com/rasonyang/aicc-knowledge/internal/httpapi"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
)

// TestLiveRunAgainstRealServices publishes three FAQs and evaluates paraphrases
// and uncovered questions through the real TEI and Meilisearch, in process and
// over HTTP, and requires both to agree.
func TestLiveRunAgainstRealServices(t *testing.T) {
	e := publishtest.New(t)
	EN := domain.LanguageEN
	reset := e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?", Alts: []string{"I forgot my password"}})
	hours := e.AddCandidate(publishtest.Cand{Language: EN, Question: "What are your opening hours?"})
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "What is your refund policy?"})
	e.Publish(EN)

	qs := []eval.Question{
		{Text: "I can't remember my login password", Language: EN, Expected: []string{reset.String()}},
		{Text: "When are you open?", Language: EN, Expected: []string{hours.String()}},
		{Text: "What is the capital of France?", Language: EN},
		{Text: "Explain the theory of relativity", Language: EN},
		{Text: "When are you open?", Language: EN, Expected: []string{reset.String()}}, // deliberately wrong expectation
	}

	inproc := eval.Run(context.Background(), eval.InProcess{Searcher: e.Searcher, Timeout: time.Minute}, qs, nil)

	ctx := context.Background()
	key, err := e.Store.IssueAPIKey(ctx, "eval")
	if err != nil {
		t.Fatal(err)
	}
	p, err := obs.Setup(ctx, obs.Options{ServiceName: "eval-test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(ctx)
	srv := httptest.NewServer(httpapi.New(e.Store, p.Metrics, httpapi.Settings{ScopeKeys: publishtest.ScopeKeys, DefaultTimeoutMS: 60000, MaxTimeoutMS: 60000}, e.Searcher))
	defer srv.Close()
	overHTTP := eval.Run(ctx, eval.HTTP{BaseURL: srv.URL, APIKey: key, Client: &http.Client{Timeout: 2 * time.Minute}}, qs, nil)

	for i := range qs {
		a, b := inproc[i], overHTTP[i]
		if a.Err != nil || b.Err != nil {
			t.Fatalf("q%d errors: %v / %v", i, a.Err, b.Err)
		}
		if a.Status != b.Status || !slices.Equal(a.IDs, b.IDs) {
			t.Errorf("q%d: in-process %s %v, HTTP %s %v", i, a.Status, a.IDs, b.Status, b.IDs)
		}
		if b.Latency.Total <= 0 || b.Latency.Embedding <= 0 {
			t.Errorf("q%d: HTTP latency %+v", i, b.Latency)
		}
	}
	m := eval.Compute(inproc)
	// 3 answerable: the paraphrase and "open" hit, the deliberately wrong one misses.
	if m.Answerable != 3 || m.RecallHits != 2 || m.Unanswerable != 2 || m.NoMatchAnswered != 2 || m.NoMatchCorrect != 2 || m.Errors != 0 {
		t.Errorf("metrics = %+v", m)
	}
	if m.NoMatchPrecision != 1 || m.NoMatchRecall != 1 {
		t.Errorf("NO_MATCH precision %v recall %v", m.NoMatchPrecision, m.NoMatchRecall)
	}
	if m.Total.P50 <= 0 || m.Total.P90 < m.Total.P50 {
		t.Errorf("latency = %+v", m.Total)
	}

	// A sweep run with the threshold disabled reproduces the configured run at 0.75.
	zero := 0.0
	raw := eval.Run(ctx, eval.InProcess{Searcher: e.Searcher}, qs, &zero)
	at75 := eval.Compute(eval.WithThreshold(raw, 0.75))
	if at75.RecallHits != m.RecallHits || at75.NoMatchAnswered != m.NoMatchAnswered || at75.NoMatchCorrect != m.NoMatchCorrect {
		t.Errorf("sweep at the configured threshold %+v differs from the real run %+v", at75, m)
	}
}
