// SPDX-License-Identifier: Apache-2.0

package embed_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/embed"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

const dims = 1024

func realClient(t *testing.T, mutate func(*embed.Config)) *embed.Client {
	t.Helper()
	url := testdb.TEIURL()
	if url == "" {
		t.Skip("SKIPPED: set KB_TEST_TEI_URL to run this test against a real TEI serving bge-m3")
	}
	cfg := embed.Config{BaseURL: url, Dimensions: dims, HTTPClient: &http.Client{Timeout: 60 * time.Second}}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := embed.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func TestEmbedEnglishAndChinese(t *testing.T) {
	c := realClient(t, nil)
	start := time.Now()
	vecs, err := c.Embed(context.Background(), []string{"How do I reset my password?", "我想查询一下我的账单"})
	t.Logf("2-text embed took %v", time.Since(start))
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len = %d, want 2", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != dims {
			t.Errorf("vec %d dims = %d, want %d", i, len(v), dims)
		}
		if n := norm(v); math.Abs(n-1) > 1e-3 {
			t.Errorf("vec %d norm = %f, want ~1", i, n)
		}
	}
}

func TestEmbedSingleLatency(t *testing.T) {
	c := realClient(t, nil)
	ctx := context.Background()
	if _, err := c.Embed(ctx, []string{"warm up"}); err != nil {
		t.Fatal(err)
	}
	var total time.Duration
	const n = 5
	for range n {
		s := time.Now()
		if _, err := c.Embed(ctx, []string{"What are your opening hours?"}); err != nil {
			t.Fatal(err)
		}
		total += time.Since(s)
	}
	t.Logf("single embed mean over %d warm calls: %v", n, total/n)
}

func TestEmbedEmptyInput(t *testing.T) {
	c := realClient(t, nil)
	vecs, err := c.Embed(context.Background(), nil)
	if err != nil || len(vecs) != 0 {
		t.Fatalf("got %d vectors, err %v; want 0, nil", len(vecs), err)
	}
}

func TestEmbedBatchSplitPreservesOrder(t *testing.T) {
	// MaxBatch 3 forces many sub-requests for 10 inputs.
	c := realClient(t, func(cfg *embed.Config) { cfg.MaxBatch = 3 })
	texts := make([]string, 10)
	for i := range texts {
		texts[i] = fmt.Sprintf("distinct question number %d about topic %c", i, 'a'+i)
	}
	texts[7] = "How do I reset my password?"
	all, err := c.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(texts) {
		t.Fatalf("len = %d, want %d", len(all), len(texts))
	}
	one, err := c.Embed(context.Background(), []string{texts[7]})
	if err != nil {
		t.Fatal(err)
	}
	if cs := cosine(one[0], all[7]); cs < 0.999 {
		t.Errorf("cosine(single, batched[7]) = %f, want ~1", cs)
	}
	if cs := cosine(one[0], all[6]); cs > 0.99 {
		t.Errorf("neighbouring slot is identical (%f); order or split is wrong", cs)
	}
}

func TestEmbedDiscoveredBatchLimitSplits(t *testing.T) {
	c := realClient(t, nil)
	mb, err := c.MaxBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("discovered max_client_batch_size = %d", mb)
	if mb <= 0 {
		t.Fatalf("max batch = %d", mb)
	}
	n := mb*2 + 1 // forces three requests
	texts := make([]string, n)
	for i := range texts {
		texts[i] = fmt.Sprintf("question %d", i)
	}
	texts[n-1] = "我想查询一下我的账单"
	all, err := c.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != n {
		t.Fatalf("len = %d, want %d", len(all), n)
	}
	one, _ := c.Embed(context.Background(), []string{texts[n-1]})
	if cs := cosine(one[0], all[n-1]); cs < 0.999 {
		t.Errorf("last slot cosine = %f, want ~1", cs)
	}
}

func TestEmbedSemanticSanity(t *testing.T) {
	c := realClient(t, nil)
	cases := []struct {
		name                       string
		anchor, paraphrase, unrelt string
	}{
		{"EN", "How do I reset my password?", "I forgot my password, how can I change it?", "What is the weather in Paris in July?"},
		{"ZH", "我想查询一下我的账单", "怎么查看我这个月的账单明细", "附近有什么好吃的餐厅推荐"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := c.Embed(context.Background(), []string{tc.anchor, tc.paraphrase, tc.unrelt})
			if err != nil {
				t.Fatal(err)
			}
			p, u := cosine(v[0], v[1]), cosine(v[0], v[2])
			t.Logf("paraphrase cosine %.3f, unrelated cosine %.3f", p, u)
			if p <= u {
				t.Errorf("paraphrase %.3f not above unrelated %.3f", p, u)
			}
		})
	}
}

func TestEmbedTruncatesLongInput(t *testing.T) {
	c := realClient(t, nil)
	long := ""
	for range 1500 {
		long += "refund policy words "
	}
	v, err := c.Embed(context.Background(), []string{long})
	if err != nil {
		t.Fatalf("truncate:true should accept over-long input: %v", err)
	}
	if len(v) != 1 || len(v[0]) != dims {
		t.Fatalf("bad result shape")
	}
}

func TestEmbedTimeoutMapping(t *testing.T) {
	c := realClient(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := c.Embed(ctx, []string{"How do I reset my password?"})
	if embed.CodeOf(err) != embed.CodeUpstreamTimeout {
		t.Fatalf("code = %q (err %v), want UPSTREAM_TIMEOUT", embed.CodeOf(err), err)
	}
}

func TestEmbedClientTimeoutMapping(t *testing.T) {
	c := realClient(t, func(cfg *embed.Config) {
		cfg.HTTPClient = &http.Client{Timeout: time.Millisecond}
		cfg.MaxBatch = 8
	})
	_, err := c.Embed(context.Background(), []string{"hello"})
	if embed.CodeOf(err) != embed.CodeUpstreamTimeout {
		t.Fatalf("code = %q (err %v), want UPSTREAM_TIMEOUT", embed.CodeOf(err), err)
	}
}

func TestEmbedConnectionRefused(t *testing.T) {
	c, _ := embed.New(embed.Config{BaseURL: "http://127.0.0.1:1", Dimensions: 4, MaxBatch: 4})
	_, err := c.Embed(context.Background(), []string{"x"})
	if embed.CodeOf(err) != embed.CodeUpstreamUnavailable {
		t.Fatalf("code = %q, want UPSTREAM_UNAVAILABLE", embed.CodeOf(err))
	}
}

// Error-mapping tests below use httptest on purpose: a real TEI cannot be made
// to answer 5xx, wrong dimensions or NaN on demand.

func fake(t *testing.T, h http.HandlerFunc) *embed.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := embed.New(embed.Config{BaseURL: srv.URL, Dimensions: 4, MaxBatch: 8,
		Retry: embed.RetryPolicy{MaxAttempts: 3, Backoff: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
		want embed.Code
	}{
		{"5xx", `{"error":"boom"}`, 503, embed.CodeUpstreamUnavailable},
		{"429", `{"error":"busy"}`, 429, embed.CodeUpstreamUnavailable},
		{"4xx", `{"error":"too big"}`, 422, embed.CodeEmbeddingInvalid},
		{"wrong dims", `[[0.5,0.5,0.5]]`, 200, embed.CodeEmbeddingInvalid},
		{"wrong count", `[[1,0,0,0],[1,0,0,0]]`, 200, embed.CodeEmbeddingInvalid},
		{"zero vector", `[[0,0,0,0]]`, 200, embed.CodeEmbeddingInvalid},
		{"garbage", `not json`, 200, embed.CodeEmbeddingInvalid},
		{"huge value overflow", `[[1e999,0,0,0]]`, 200, embed.CodeEmbeddingInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.Embed(context.Background(), []string{"x"})
			if embed.CodeOf(err) != tc.want {
				t.Fatalf("code = %q (err %v), want %q", embed.CodeOf(err), err, tc.want)
			}
		})
	}
}

func TestNaNRejected(t *testing.T) {
	// JSON cannot carry NaN; TEI would fail to serialize. A decode error is the
	// observable path, and it must still be EMBEDDING_INVALID.
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[[NaN,0,0,0]]`))
	})
	_, err := c.Embed(context.Background(), []string{"x"})
	if embed.CodeOf(err) != embed.CodeEmbeddingInvalid {
		t.Fatalf("code = %q, want EMBEDDING_INVALID", embed.CodeOf(err))
	}
}

func TestDefensiveNormalization(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[[3,0,0,4]]`))
	})
	v, err := c.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if n := norm(v[0]); math.Abs(n-1) > 1e-6 {
		t.Fatalf("norm = %f, want 1", n)
	}
}

func TestEmbedDoesNotRetryButEmbedBatchDoes(t *testing.T) {
	var calls atomic.Int32
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`[[1,0,0,0]]`))
	})
	if _, err := c.Embed(context.Background(), []string{"x"}); embed.CodeOf(err) != embed.CodeUpstreamUnavailable {
		t.Fatalf("Embed: want UPSTREAM_UNAVAILABLE, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Embed made %d calls, want 1 (no hidden retry)", got)
	}
	calls.Store(0)
	v, err := c.EmbedBatch(context.Background(), []string{"x"})
	if err != nil || len(v) != 1 {
		t.Fatalf("EmbedBatch: %v, %d vectors", err, len(v))
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("EmbedBatch made %d calls, want 3", got)
	}
}

func TestEmbedBatchDoesNotRetryInvalid(t *testing.T) {
	var calls atomic.Int32
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`[[1,0]]`))
	})
	if _, err := c.EmbedBatch(context.Background(), []string{"x"}); embed.CodeOf(err) != embed.CodeEmbeddingInvalid {
		t.Fatalf("got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := embed.New(embed.Config{Dimensions: 4}); err == nil {
		t.Error("missing BaseURL accepted")
	}
	if _, err := embed.New(embed.Config{BaseURL: "http://x"}); err == nil {
		t.Error("missing Dimensions accepted")
	}
}
