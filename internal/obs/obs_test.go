// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsAreExposedUnderTheDeclaredNames(t *testing.T) {
	ctx := context.Background()
	p, err := Setup(ctx, Options{ServiceName: "aicc-knowledge-test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(ctx)
	p.Metrics.ObserveSearch(ctx, "EN", "NO_MATCH", SearchStages{Embedding: 30 * time.Millisecond, Search: 5 * time.Millisecond, Total: 40 * time.Millisecond})
	p.Metrics.ObserveHTTP(ctx, "POST", "/v1/search", 200, 41*time.Millisecond)
	p.Metrics.ObserveProductGuard(ctx, "ZH", "dropped_disjoint", 2)

	rec := httptest.NewRecorder()
	p.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	for _, want := range []string{
		MetricSearchEmbeddingSeconds + "_bucket{",
		MetricSearchSearchSeconds + "_bucket{",
		MetricSearchTotalSeconds + "_bucket{",
		MetricSearchTotalSeconds + `_count{language="EN",status="NO_MATCH"`,
		MetricHTTPRequestsTotal + "{",
		MetricHTTPRequestSeconds + "_bucket{",
		MetricProductGuardTotal + `{language="ZH",outcome="dropped_disjoint"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	if n := strings.Count(text, "# TYPE kb_search_"); n != 3 {
		t.Errorf("kb_search_* metric families = %d, want 3", n)
	}
}

func TestGenerateAndReviewMetricsAreExposed(t *testing.T) {
	ctx := context.Background()
	p, err := Setup(ctx, Options{ServiceName: "aicc-knowledge-test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(ctx)
	p.Metrics.ObserveGenerateCandidates(ctx, "ZH", "CREATED", 3)
	p.Metrics.ObserveGenerateSection(ctx, "OK")
	p.Metrics.ObserveLLMRequest(ctx, "OK", 2*time.Second)
	p.Metrics.ObserveReviewRow(ctx, "STALE")

	rec := httptest.NewRecorder()
	p.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		MetricGenerateCandidates + `{language="ZH",outcome="CREATED"`,
		MetricGenerateSections + `{outcome="OK"`,
		MetricLLMRequestSeconds + `_bucket{outcome="OK"`,
		MetricReviewRowsTotal + `{outcome="STALE"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics lacks %q\n%s", want, body)
		}
	}
}
