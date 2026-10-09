// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/embed"
	"github.com/rasonyang/aicc-knowledge/internal/httpapi"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

// searchEnv is the HTTP server over the real stack, with a published EN index.
type searchEnv struct {
	*env
	pt *publishtest.Env
}

func newSearchEnv(t *testing.T, tweak func(*search.Searcher, *httpapi.Settings)) *searchEnv {
	t.Helper()
	pt := publishtest.New(t)
	ctx := context.Background()
	key, err := pt.Store.IssueAPIKey(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := obs.Setup(ctx, obs.Options{ServiceName: "test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })
	s := *pt.Searcher
	s.Metrics = p.Metrics
	set := httpapi.Settings{ScopeKeys: publishtest.ScopeKeys, DefaultTimeoutMS: 60000, MaxTimeoutMS: 60000}
	if tweak != nil {
		tweak(&s, &set)
	}
	srv := httptest.NewServer(httpapi.New(pt.Store, p.Metrics, set, &s))
	t.Cleanup(srv.Close)
	return &searchEnv{env: &env{srv: srv, store: pt.Store, key: key, obs: p}, pt: pt}
}

type searchBody struct {
	Status string `json:"status"`
	Items  []struct {
		ID        string  `json:"id"`
		Question  string  `json:"question"`
		Answer    string  `json:"answer"`
		SourceRef string  `json:"sourceRef"`
		Score     float64 `json:"score"`
	} `json:"items"`
	LatencyMs struct {
		Embedding float64 `json:"embedding"`
		Search    float64 `json:"search"`
		Total     float64 `json:"total"`
	} `json:"latencyMs"`
}

func (e *searchEnv) search(t *testing.T, body string) searchBody {
	t.Helper()
	resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key, body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	var out searchBody
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(raw, &top)
	if top["items"] == nil || string(top["items"]) == "null" {
		t.Fatalf("items must be an array, got %s", raw)
	}
	return out
}

func TestSearchOverHTTP(t *testing.T) {
	e := newSearchEnv(t, nil)
	reset := e.pt.AddCandidate(publishtest.Cand{Key: "kb/acme/a.docx", Language: domain.LanguageEN, Question: "How do I reset my password?",
		Alts: []string{"I forgot my password"}, Answer: "Use the Forgot password link."})
	e.pt.AddCandidate(publishtest.Cand{Key: "kb/globex/b.docx", Language: domain.LanguageEN, Question: "What are your opening hours?", Answer: "Nine to five."})
	e.pt.AddCandidate(publishtest.Cand{Language: domain.LanguageEN, Question: "What is your refund policy?", Answer: "Thirty days."})
	e.pt.Publish(domain.LanguageEN)

	t.Run("HIT with the contract's shape", func(t *testing.T) {
		got := e.search(t, `{"query":"I can't remember my login password","language":"EN"}`)
		if got.Status != "HIT" || len(got.Items) == 0 || got.Items[0].ID != reset.String() {
			t.Fatalf("got %+v", got)
		}
		it := got.Items[0]
		if it.Question != "How do I reset my password?" || it.Answer != "Use the Forgot password link." || it.SourceRef != "kb/acme/a.docx#How do I reset my password?" || it.Score < 0.75 || it.Score > 1 {
			t.Errorf("item = %+v", it)
		}
		l := got.LatencyMs
		if l.Embedding <= 0 || l.Search <= 0 || l.Total < l.Embedding+l.Search {
			t.Errorf("latencyMs = %+v", l)
		}
	})
	t.Run("NO_MATCH has an empty items array", func(t *testing.T) {
		got := e.search(t, `{"query":"What is the capital of France?","language":"EN"}`)
		if got.Status != "NO_MATCH" || got.Items == nil || len(got.Items) != 0 || got.LatencyMs.Total <= 0 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("topK caps the items", func(t *testing.T) {
		got := e.search(t, `{"query":"I can't remember my login password","language":"EN","topK":1}`)
		if len(got.Items) != 1 {
			t.Errorf("items = %d", len(got.Items))
		}
	})
	t.Run("scope filters, and documents without the key are global", func(t *testing.T) {
		got := e.search(t, `{"query":"What are your opening hours?","language":"EN","scope":{"brand":"acme"}}`)
		for _, it := range got.Items {
			if it.Question == "What are your opening hours?" {
				t.Errorf("a globex document answered an acme search: %+v", got.Items)
			}
		}
		got = e.search(t, `{"query":"What is your refund policy?","language":"EN","scope":{"brand":"acme"}}`)
		if len(got.Items) == 0 || got.Items[0].Question != "What is your refund policy?" {
			t.Errorf("a global document did not answer: %+v", got)
		}
	})
	t.Run("a language without a live index is INDEX_UNAVAILABLE", func(t *testing.T) {
		resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key, `{"query":"密码","language":"ZH","scope":{"brand":"acme"},"topK":1,"timeoutMs":60000}`)
		got := expectError(t, resp, raw, 503, "INDEX_UNAVAILABLE")
		if got.Error.Params["language"] != "ZH" {
			t.Errorf("params = %v", got.Error.Params)
		}
	})
	t.Run("unauthorized", func(t *testing.T) {
		resp, raw := e.do(t, "POST", "/v1/search", "", `{"query":"q","language":"EN"}`)
		expectError(t, resp, raw, 401, "UNAUTHORIZED")
	})
	t.Run("validation still comes first", func(t *testing.T) {
		resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key, `{"query":"q","language":"EN","scope":{"region":"x"}}`)
		expectError(t, resp, raw, 422, "UNKNOWN_SCOPE_KEY")
		resp, raw = e.do(t, "POST", "/v1/search", "Bearer "+e.key, `{"query":"q","language":"EN","topK":4}`)
		expectError(t, resp, raw, 422, "VALIDATION_FAILED")
	})
	t.Run("stage histograms are recorded", func(t *testing.T) {
		rec := httptest.NewRecorder()
		e.obs.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		b, _ := io.ReadAll(rec.Body)
		for _, want := range []string{`status="HIT"`, `status="NO_MATCH"`, `status="INDEX_UNAVAILABLE"`} {
			if !strings.Contains(string(b), "kb_search_total_seconds_count{") || !strings.Contains(string(b), want) {
				t.Errorf("metrics lack %s", want)
			}
		}
	})
}

// deadlineProbe is a TEI stand-in that never answers within the budget and
// remembers the deadline it was given.
type deadlineProbe struct {
	remaining atomic.Int64 // microseconds
}

func (d *deadlineProbe) Embed(ctx context.Context, _ []string) ([][]float32, error) {
	if dl, ok := ctx.Deadline(); ok {
		d.remaining.Store(time.Until(dl).Microseconds())
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSearchTimeoutBudget(t *testing.T) {
	probe := &deadlineProbe{}
	e := newSearchEnv(t, func(s *search.Searcher, set *httpapi.Settings) {
		s.Embedder = probe
		set.DefaultTimeoutMS, set.MaxTimeoutMS = 400, 700
	})
	for _, tc := range []struct {
		name, body string
		wantMS     float64
	}{
		{"explicit timeout", `{"query":"q","language":"EN","timeoutMs":50}`, 50},
		{"default timeout", `{"query":"q","language":"EN"}`, 400},
		{"capped by the configured maximum", `{"query":"q","language":"EN","timeoutMs":60000}`, 700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key, tc.body)
			took := time.Since(start)
			expectError(t, resp, raw, 504, "UPSTREAM_TIMEOUT")
			if ms := float64(took.Milliseconds()); ms < tc.wantMS-5 || ms > tc.wantMS+300 {
				t.Errorf("answered after %v, want about %vms", took, tc.wantMS)
			}
			if got := float64(probe.remaining.Load()) / 1000; got > tc.wantMS || got < tc.wantMS-100 {
				t.Errorf("the embedder saw a budget of %.0fms, want about %vms", got, tc.wantMS)
			}
		})
	}
	t.Run("a one millisecond budget against the real TEI", func(t *testing.T) {
		real := newSearchEnv(t, nil)
		resp, raw := real.do(t, "POST", "/v1/search", "Bearer "+real.key, `{"query":"How do I reset my password?","language":"EN","timeoutMs":1}`)
		expectError(t, resp, raw, 504, "UPSTREAM_TIMEOUT")
	})
}

func TestSearchWhenADependencyIsDown(t *testing.T) {
	t.Run("meilisearch", func(t *testing.T) {
		e := newSearchEnv(t, func(s *search.Searcher, _ *httpapi.Settings) {
			s.Meili, _ = meili.New(meili.Config{BaseURL: "http://127.0.0.1:1"})
		})
		resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key, `{"query":"q","language":"EN"}`)
		expectError(t, resp, raw, 503, "UPSTREAM_UNAVAILABLE")
	})
	t.Run("tei", func(t *testing.T) {
		e := newSearchEnv(t, func(s *search.Searcher, _ *httpapi.Settings) {
			s.Embedder = refused(t)
		})
		resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key, `{"query":"q","language":"EN"}`)
		expectError(t, resp, raw, 503, "UPSTREAM_UNAVAILABLE")
	})
}

// refused is a real TEI client pointed at a closed port.
func refused(t *testing.T) search.Embedder {
	t.Helper()
	c, err := embed.New(embed.Config{BaseURL: "http://127.0.0.1:1", Dimensions: 1024, MaxBatch: 8})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
