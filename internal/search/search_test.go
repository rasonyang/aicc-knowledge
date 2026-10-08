// SPDX-License-Identifier: Apache-2.0

package search_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

const (
	EN = domain.LanguageEN
	ZH = domain.LanguageZH
)

type faqs struct {
	reset, hours, cancel, refund, delivery uuid.UUID
}

// publishEN publishes five unrelated EN questions, each under its own scope.
func publishEN(e *publishtest.Env) faqs {
	var f faqs
	f.reset = e.AddCandidate(publishtest.Cand{Key: "kb/acme/web/a.docx", Language: EN, Question: "How do I reset my password?",
		Alts: []string{"I forgot my password"}, Answer: "Use the Forgot password link."})
	f.hours = e.AddCandidate(publishtest.Cand{Key: "kb/acme/b.docx", Language: EN, Question: "What are your opening hours?", Answer: "Nine to five."})
	f.cancel = e.AddCandidate(publishtest.Cand{Key: "kb/globex/c.docx", Language: EN, Question: "How can I cancel my subscription?", Answer: "Open Billing."})
	f.refund = e.AddCandidate(publishtest.Cand{Language: EN, Question: "What is your refund policy?", Answer: "Refunds within thirty days."})
	f.delivery = e.AddCandidate(publishtest.Cand{Language: EN, Question: "How long does delivery take?", Answer: "Three to five days."})
	e.Publish(EN)
	return f
}

func TestHitForAParaphraseAndNoMatchOffTopic(t *testing.T) {
	e := publishtest.New(t)
	f := publishEN(e)

	r, err := e.Search(EN, "I can't remember my login password", nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != search.StatusHit || len(r.Items) == 0 || r.Items[0].ID != f.reset.String() {
		t.Fatalf("paraphrase = %+v", r)
	}
	it := r.Items[0]
	if it.Question != "How do I reset my password?" || it.Answer != "Use the Forgot password link." || it.SourceRef == "" || it.Score <= 0.75 {
		t.Errorf("item = %+v", it)
	}
	t.Logf("paraphrase scored %.3f in %+v", it.Score, r.Latency)
	if r.Latency.Embedding <= 0 || r.Latency.Search <= 0 || r.Latency.Total < r.Latency.Embedding+r.Latency.Search {
		t.Errorf("latency = %+v", r.Latency)
	}

	for _, off := range []string{"What is the capital of France?", "Explain the theory of relativity", "banana bread recipe with walnuts"} {
		r, err := e.Search(EN, off, nil, 3)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != search.StatusNoMatch || r.Items == nil || len(r.Items) != 0 {
			t.Errorf("%q: status %s items %+v (nil=%v), want NO_MATCH and an empty non-nil list", off, r.Status, r.Items, r.Items == nil)
		}
	}

	// A disabled gate shows what the threshold was holding back: the same query
	// returns the nearest items, at scores below the configured threshold.
	zero := 0.0
	r, err = e.Searcher.Search(context.Background(), search.Request{Query: "What is the capital of France?", Language: EN, TopK: 3, Threshold: &zero})
	if err != nil || len(r.Items) != 3 || r.Status != search.StatusHit {
		t.Fatalf("threshold 0: %+v, %v", r, err)
	}
	for _, it := range r.Items {
		if it.Score >= 0.75 {
			t.Errorf("off-topic item scored %.3f, above the threshold", it.Score)
		}
	}
	t.Logf("off-topic best score %.3f", r.Items[0].Score)
}

func TestThresholdDecidesNoMatch(t *testing.T) {
	e := publishtest.New(t)
	f := publishEN(e)
	q := "I can't remember my login password"
	r, _ := e.Search(EN, q, nil, 1)
	if len(r.Items) != 1 || r.Items[0].ID != f.reset.String() {
		t.Fatalf("setup: %+v", r)
	}
	score := r.Items[0].Score
	for _, tc := range []struct {
		threshold float64
		want      search.Status
	}{{score - 0.02, search.StatusHit}, {score + 0.02, search.StatusNoMatch}} {
		th := tc.threshold
		got, err := e.Searcher.Search(context.Background(), search.Request{Query: q, Language: EN, TopK: 1, Threshold: &th})
		if err != nil || got.Status != tc.want {
			t.Errorf("threshold %.3f: %s, %v; want %s", th, got.Status, err, tc.want)
		}
	}
	// The language's configured threshold applies when the request has none.
	e.Searcher.ThresholdEN = score + 0.02
	if got, _ := e.Search(EN, q, nil, 1); got.Status != search.StatusNoMatch {
		t.Errorf("configured threshold ignored: %+v", got)
	}
	e.Searcher.ThresholdEN = score - 0.02
	if got, _ := e.Search(EN, q, nil, 1); got.Status != search.StatusHit {
		t.Errorf("configured threshold ignored: %+v", got)
	}
}

func TestTopKIsHonored(t *testing.T) {
	e := publishtest.New(t)
	publishEN(e)
	zero := 0.0
	for _, k := range []int{1, 2, 3} {
		r, err := e.Searcher.Search(context.Background(), search.Request{Query: "customer service question", Language: EN, TopK: k, Threshold: &zero})
		if err != nil || len(r.Items) != k {
			t.Errorf("topK %d: %d items, %v", k, len(r.Items), err)
		}
		for i := 1; i < len(r.Items); i++ {
			if r.Items[i].Score > r.Items[i-1].Score {
				t.Errorf("topK %d: not ranked: %+v", k, r.Items)
			}
		}
	}
	// Default topK is 3.
	r, _ := e.Searcher.Search(context.Background(), search.Request{Query: "customer service question", Language: EN, Threshold: &zero})
	if len(r.Items) != 3 {
		t.Errorf("default topK: %d items", len(r.Items))
	}
}

func TestScopeFilterWithGlobalDocuments(t *testing.T) {
	e := publishtest.New(t)
	f := publishEN(e)
	zero := 0.0
	ids := func(scope map[string]string) []string {
		t.Helper()
		r, err := e.Searcher.Search(context.Background(), search.Request{Query: "customer service question", Language: EN, Scope: scope, TopK: 3, Threshold: &zero})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, it := range r.Items {
			out = append(out, it.ID)
		}
		return out
	}
	all := func(scope map[string]string) []string {
		t.Helper()
		// topK is capped at 3 by the contract, so ask for each document by its own question.
		got := []string{}
		for id, q := range map[uuid.UUID]string{
			f.reset: "How do I reset my password?", f.hours: "What are your opening hours?", f.cancel: "How can I cancel my subscription?",
			f.refund: "What is your refund policy?", f.delivery: "How long does delivery take?"} {
			r, err := e.Searcher.Search(context.Background(), search.Request{Query: q, Language: EN, Scope: scope, TopK: 1, Threshold: &zero})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Items) == 1 && r.Items[0].ID == id.String() {
				got = append(got, id.String())
			}
		}
		slices.Sort(got)
		return got
	}
	// reset: brand=acme channel=web; hours: brand=acme; cancel: brand=globex; refund, delivery: global.
	for _, tc := range []struct {
		name  string
		scope map[string]string
		want  []string
	}{
		{"no scope sees everything", nil, publishtest.IDs(f.reset, f.hours, f.cancel, f.refund, f.delivery)},
		{"brand acme: acme documents and global ones", map[string]string{"brand": "acme"}, publishtest.IDs(f.reset, f.hours, f.refund, f.delivery)},
		{"brand globex", map[string]string{"brand": "globex"}, publishtest.IDs(f.cancel, f.refund, f.delivery)},
		{"brand and channel", map[string]string{"brand": "acme", "channel": "web"}, publishtest.IDs(f.reset, f.hours, f.refund, f.delivery)},
		{"channel voice excludes the web document", map[string]string{"channel": "voice"}, publishtest.IDs(f.hours, f.cancel, f.refund, f.delivery)},
		{"unknown brand sees only global documents", map[string]string{"brand": "initech"}, publishtest.IDs(f.refund, f.delivery)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := all(tc.scope); !slices.Equal(got, tc.want) || len(got) != len(tc.want) {
				t.Errorf("reachable documents = %v, want %v", got, tc.want)
			}
		})
	}
	if got := ids(map[string]string{"brand": "initech"}); len(got) != 2 {
		t.Errorf("topK 3 under a scope with 2 reachable documents returned %v", got)
	}
	// A key outside the configured set never reaches Meilisearch.
	_, err := e.Searcher.Search(context.Background(), search.Request{Query: "q", Language: EN, Scope: map[string]string{"region": "x"}})
	if search.CodeOf(err) != search.CodeUnknownScopeKey {
		t.Errorf("unknown scope key: %v", err)
	}
}

func TestChineseHitAndNoMatch(t *testing.T) {
	e := publishtest.New(t)
	reset := e.AddCandidate(publishtest.Cand{Language: ZH, Question: "如何重置密码？", Alts: []string{"我忘记密码了"}, Answer: "请点击登录页面的忘记密码。"})
	e.AddCandidate(publishtest.Cand{Language: ZH, Question: "你们的营业时间是什么？", Answer: "周一至周五九点到五点。"})
	e.Publish(ZH)
	r, err := e.Search(ZH, "密码忘了怎么办", nil, 3)
	if err != nil || r.Status != search.StatusHit || r.Items[0].ID != reset.String() {
		t.Fatalf("zh paraphrase = %+v, %v", r, err)
	}
	t.Logf("zh paraphrase scored %.3f", r.Items[0].Score)
	r, err = e.Search(ZH, "法国的首都是哪里", nil, 3)
	if err != nil || r.Status != search.StatusNoMatch || len(r.Items) != 0 {
		t.Errorf("zh off-topic = %+v, %v", r, err)
	}
	// EN has no index here: the two languages never mix.
	if _, err := e.Search(EN, "How do I reset my password?", nil, 3); search.CodeOf(err) != search.CodeIndexUnavailable {
		t.Errorf("EN without a live index: %v", err)
	}
}

type sleepyEmbedder struct{}

func (sleepyEmbedder) Embed(ctx context.Context, _ []string) ([][]float32, error) {
	<-ctx.Done() // a TEI that never answers inside the budget
	return nil, ctx.Err()
}

func TestErrorsAreCoded(t *testing.T) {
	e := publishtest.New(t)
	publishEN(e)
	ctx := context.Background()
	base := func() *search.Searcher {
		s := *e.Searcher
		return &s
	}

	t.Run("budget exceeded while embedding", func(t *testing.T) {
		s := base()
		s.Embedder = sleepyEmbedder{}
		start := time.Now()
		_, err := s.Search(ctx, search.Request{Query: "q", Language: EN, Timeout: 30 * time.Millisecond})
		if search.CodeOf(err) != search.CodeUpstreamTimeout || time.Since(start) > 2*time.Second {
			t.Errorf("err = %v after %v", err, time.Since(start))
		}
	})
	t.Run("budget exceeded before anything started", func(t *testing.T) {
		dead, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer cancel()
		_, err := e.Searcher.Search(dead, search.Request{Query: "q", Language: EN})
		if search.CodeOf(err) != search.CodeUpstreamTimeout {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("caller cancels", func(t *testing.T) {
		c, cancel := context.WithCancel(ctx)
		cancel()
		_, err := e.Searcher.Search(c, search.Request{Query: "q", Language: EN})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("meilisearch unreachable", func(t *testing.T) {
		s := base()
		s.Meili, _ = meili.New(meili.Config{BaseURL: "http://127.0.0.1:1"})
		_, err := s.Search(ctx, search.Request{Query: "q", Language: EN, Timeout: 5 * time.Second})
		if search.CodeOf(err) != search.CodeUpstreamUnavailable {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("tei unreachable", func(t *testing.T) {
		s := base()
		s.Embedder = refusedEmbedder(t)
		_, err := s.Search(ctx, search.Request{Query: "q", Language: EN, Timeout: 5 * time.Second})
		if search.CodeOf(err) != search.CodeUpstreamUnavailable {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no live index", func(t *testing.T) {
		s := base()
		s.IndexPrefix = e.Prefix + "absent_"
		_, err := s.Search(ctx, search.Request{Query: "q", Language: EN, Timeout: 5 * time.Second})
		if search.CodeOf(err) != search.CodeIndexUnavailable {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStageLatencyHistogramsCarryLanguageAndStatus(t *testing.T) {
	e := publishtest.New(t)
	publishEN(e)
	p, err := obs.Setup(context.Background(), obs.Options{ServiceName: "search-test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	e.Searcher.Metrics = p.Metrics
	_, _ = e.Search(EN, "I can't remember my login password", nil, 3)
	_, _ = e.Search(EN, "What is the capital of France?", nil, 3)
	_, _ = e.Search(ZH, "密码", nil, 3) // no ZH index: INDEX_UNAVAILABLE

	rec := httptest.NewRecorder()
	p.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	for _, stage := range []string{"embedding", "search", "total"} {
		for _, labels := range []string{`language="EN"`, `status="HIT"`, `status="NO_MATCH"`, `status="INDEX_UNAVAILABLE"`, `language="ZH"`} {
			found := false
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, "kb_search_"+stage+"_seconds_count") && strings.Contains(line, labels) {
					found = true
				}
			}
			if !found {
				t.Errorf("no kb_search_%s_seconds_count series with %s", stage, labels)
			}
		}
	}
}
