// SPDX-License-Identifier: Apache-2.0

package meili_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

var scopeKeys = []string{"brand", "channel"}

func newClient(t *testing.T) *meili.Client {
	t.Helper()
	u, key := testdb.MeiliURL(t)
	c, err := meili.New(meili.Config{BaseURL: u, APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// uid returns a unique index uid for this test and registers its deletion.
func uid(t *testing.T, c *meili.Client, tag string) string {
	t.Helper()
	u := fmt.Sprintf("kbt_%s_%d", tag, time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if ok, _ := c.IndexExists(ctx, u); ok {
			if id, err := c.DeleteIndex(ctx, u); err == nil {
				_, _ = c.WaitTask(ctx, id)
			}
		}
	})
	return u
}

func mustWait(t *testing.T, c *meili.Client, id int64, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitTask(ctxT(t), id); err != nil {
		t.Fatal(err)
	}
}

// build creates and configures an index and indexes docs, waiting for each task.
func build(t *testing.T, c *meili.Client, u string, dims int, docs []meili.Document) {
	t.Helper()
	ctx := ctxT(t)
	id, err := c.CreateIndex(ctx, u, "id")
	mustWait(t, c, id, err)
	s, err := meili.DefaultSettings(dims, scopeKeys)
	if err != nil {
		t.Fatal(err)
	}
	id, err = c.ConfigureIndex(ctx, u, s)
	mustWait(t, c, id, err)
	ids, err := c.AddDocuments(ctx, u, docs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WaitTasks(ctx, ids); err != nil {
		t.Fatal(err)
	}
}

func unit(dims int, comps map[int]float32) []float32 {
	v := make([]float32, dims)
	var n float64
	for i, x := range comps {
		v[i] = x
		n += float64(x) * float64(x)
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(n))
	}
	return v
}

func doc(id string, vec []float32, scope map[string]string) meili.Document {
	d := meili.Document{
		ID: id, Question: "question " + id, AlternateQuestions: []string{"alt " + id},
		Answer: "answer " + id, SourceRef: "faq.xlsx#" + id, Scope: scope, PublicationID: "pub-1",
	}
	d.SetVector(vec)
	return d
}

func hitIDs(r *meili.SearchResult) []string {
	out := make([]string, len(r.Hits))
	for i, h := range r.Hits {
		out[i] = h.ID
	}
	return out
}

func sorted(s []string) []string { c := append([]string{}, s...); sort.Strings(c); return c }

func TestVectorSearchNearestFirstAndThreshold(t *testing.T) {
	c := newClient(t)
	const dims = 8
	u := uid(t, c, "vs")
	build(t, c, u, dims, []meili.Document{
		doc("11111111-1111-4111-8111-111111111111", unit(dims, map[int]float32{0: 1}), nil),
		doc("22222222-2222-4222-8222-222222222222", unit(dims, map[int]float32{1: 1}), nil),
		doc("33333333-3333-4333-8333-333333333333", unit(dims, map[int]float32{0: 1, 1: 1}), nil),
	})
	ctx := ctxT(t)
	if n, err := c.DocumentCount(ctx, u); err != nil || n != 3 {
		t.Fatalf("count = %d, err %v; want 3", n, err)
	}

	r, err := c.VectorSearch(ctx, u, unit(dims, map[int]float32{0: 1}), 10, "", 0.6)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("processingTimeMs = %d", r.ProcessingTimeMs)
	want := []string{"11111111-1111-4111-8111-111111111111", "33333333-3333-4333-8333-333333333333"}
	if !reflect.DeepEqual(hitIDs(r), want) {
		t.Fatalf("hits = %v, want %v (orthogonal doc must be cut by the threshold)", hitIDs(r), want)
	}
	if len(r.Hits) != 2 {
		t.Fatalf("len = %d", len(r.Hits))
	}
	for _, h := range r.Hits {
		if h.Score <= 0.5 || h.Score > 1.0000001 {
			t.Errorf("score %f outside (0.5,1]", h.Score)
		}
	}
	if r.Hits[0].Score < r.Hits[1].Score {
		t.Errorf("not nearest first: %f then %f", r.Hits[0].Score, r.Hits[1].Score)
	}
	h := r.Hits[0]
	if h.Question != "question "+h.ID || h.Answer != "answer "+h.ID || h.SourceRef != "faq.xlsx#"+h.ID {
		t.Errorf("hit fields not populated: %+v", h)
	}

	// Without a threshold the orthogonal doc comes back at exactly the 0.5 floor.
	all, err := c.VectorSearch(ctx, u, unit(dims, map[int]float32{0: 1}), 10, "", 0)
	if err != nil || len(all.Hits) != 3 {
		t.Fatalf("threshold 0: %d hits, err %v; want 3", len(all.Hits), err)
	}

	// An orthogonal query matches nothing above the floor: NO_MATCH.
	none, err := c.VectorSearch(ctx, u, unit(dims, map[int]float32{7: 1}), 10, "", 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Hits) != 0 || none.Hits == nil {
		t.Fatalf("orthogonal query: %d hits (nil=%v), want empty non-nil", len(none.Hits), none.Hits == nil)
	}

	// limit is honoured.
	lim, err := c.VectorSearch(ctx, u, unit(dims, map[int]float32{0: 1}), 1, "", 0)
	if err != nil || len(lim.Hits) != 1 {
		t.Fatalf("limit 1: %d hits, err %v", len(lim.Hits), err)
	}
}

func TestVectorSearch1024Dims(t *testing.T) {
	c := newClient(t)
	const dims = 1024
	u := uid(t, c, "d1024")
	build(t, c, u, dims, []meili.Document{
		doc("a", unit(dims, map[int]float32{0: 1, 5: 2}), nil),
		doc("b", unit(dims, map[int]float32{900: 1}), nil),
	})
	r, err := c.VectorSearch(ctxT(t), u, unit(dims, map[int]float32{0: 1, 5: 2}), 5, "", 0.75)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hitIDs(r), []string{"a"}) || r.Hits[0].Score < 0.99 {
		t.Fatalf("hits = %v", r.Hits)
	}
	t.Logf("1024-dim search processingTimeMs = %d", r.ProcessingTimeMs)
	// Wrong query dimension is a coded rejection, not a crash.
	_, err = c.VectorSearch(ctxT(t), u, []float32{1, 0}, 5, "", 0.5)
	if meili.CodeOf(err) != meili.CodeIndexRequestRejected {
		t.Fatalf("wrong query dims: code %q err %v", meili.CodeOf(err), err)
	}
}

func TestScopeFilter(t *testing.T) {
	c := newClient(t)
	const dims = 8
	u := uid(t, c, "scope")
	v := unit(dims, map[int]float32{0: 1})
	quoted := `ac"me \ co`
	build(t, c, u, dims, []meili.Document{
		doc("d1", v, map[string]string{"brand": "acme", "channel": "web"}),
		doc("d2", v, map[string]string{"brand": "globex", "channel": "web"}),
		doc("d3", v, map[string]string{"brand": quoted, "channel": "voice"}),
	})
	ctx := ctxT(t)
	search := func(scope map[string]string) []string {
		t.Helper()
		f, err := meili.BuildFilter(scopeKeys, scope)
		if err != nil {
			t.Fatal(err)
		}
		r, err := c.VectorSearch(ctx, u, v, 10, f, 0.6)
		if err != nil {
			t.Fatalf("filter %q: %v", f, err)
		}
		return sorted(hitIDs(r))
	}
	cases := []struct {
		name  string
		scope map[string]string
		want  []string
	}{
		{"no scope", nil, []string{"d1", "d2", "d3"}},
		{"match one", map[string]string{"brand": "acme"}, []string{"d1"}},
		{"two keys", map[string]string{"brand": "acme", "channel": "web"}, []string{"d1"}},
		{"shared key", map[string]string{"channel": "web"}, []string{"d1", "d2"}},
		{"non-match", map[string]string{"brand": "initech"}, []string{}},
		{"conflicting", map[string]string{"brand": "acme", "channel": "voice"}, []string{}},
		{"quote and backslash", map[string]string{"brand": quoted}, []string{"d3"}},
		{"injection attempt", map[string]string{"brand": `x" OR scope.brand != "x`}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := search(tc.scope)
			if !reflect.DeepEqual(got, tc.want) || len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildFilter(t *testing.T) {
	f, err := meili.BuildFilter([]string{"brand", "channel"}, map[string]string{"channel": "web", "brand": `a"b\c`})
	if err != nil {
		t.Fatal(err)
	}
	if want := `(scope.brand = "a\"b\\c" OR scope.brand NOT EXISTS) AND (scope.channel = "web" OR scope.channel NOT EXISTS)`; f != want {
		t.Fatalf("filter = %s, want %s", f, want)
	}
	for _, scope := range []map[string]string{
		{"region": "x"},
		{"brand": "ok", "region": "x"},
		{"brand = 1 OR scope.x": "x"},
	} {
		_, err := meili.BuildFilter([]string{"brand", "brand = 1 OR scope.x"}, scope)
		if meili.CodeOf(err) != meili.CodeUnknownScopeKey {
			t.Errorf("scope %v: code %q, want UNKNOWN_SCOPE_KEY", scope, meili.CodeOf(err))
		}
	}
	if f, err := meili.BuildFilter(nil, nil); f != "" || err != nil {
		t.Errorf("empty scope: %q, %v", f, err)
	}
	if _, err := meili.DefaultSettings(8, []string{"a b"}); meili.CodeOf(err) != meili.CodeUnknownScopeKey {
		t.Errorf("DefaultSettings accepted a bad key: %v", err)
	}
}

func TestSwapAndRollback(t *testing.T) {
	c := newClient(t)
	const dims = 8
	live := uid(t, c, "live")
	stage := uid(t, c, "stage")
	q := unit(dims, map[int]float32{0: 1})
	mk := func(ids ...string) []meili.Document {
		var ds []meili.Document
		for _, id := range ids {
			ds = append(ds, doc(id, q, nil))
		}
		return ds
	}
	build(t, c, live, dims, mk("a1", "a2"))
	build(t, c, stage, dims, mk("b1", "b2", "b3"))
	ctx := ctxT(t)
	ids := func(u string) []string {
		t.Helper()
		r, err := c.VectorSearch(ctx, u, q, 10, "", 0.6)
		if err != nil {
			t.Fatal(err)
		}
		return sorted(hitIDs(r))
	}
	a, b := []string{"a1", "a2"}, []string{"b1", "b2", "b3"}
	if got := ids(live); !reflect.DeepEqual(got, a) {
		t.Fatalf("before swap live = %v", got)
	}

	id, err := c.Swap(ctx, live, stage)
	mustWait(t, c, id, err)
	if got := ids(live); !reflect.DeepEqual(got, b) || len(got) != 3 {
		t.Fatalf("after swap live = %v, want %v", got, b)
	}
	if got := ids(stage); !reflect.DeepEqual(got, a) || len(got) != 2 {
		t.Fatalf("after swap stage = %v, want %v", got, a)
	}
	if n, _ := c.DocumentCount(ctx, live); n != 3 {
		t.Fatalf("live count = %d, want 3", n)
	}

	id, err = c.Swap(ctx, live, stage) // rollback
	mustWait(t, c, id, err)
	if got := ids(live); !reflect.DeepEqual(got, a) || len(got) != 2 {
		t.Fatalf("after rollback live = %v, want %v", got, a)
	}
	if got := ids(stage); !reflect.DeepEqual(got, b) {
		t.Fatalf("after rollback stage = %v, want %v", got, b)
	}
}

func TestSwapMissingIndexFailsAtWaitTask(t *testing.T) {
	c := newClient(t)
	const dims = 8
	live := uid(t, c, "swm")
	missing := uid(t, c, "swmissing") // never created
	build(t, c, live, dims, []meili.Document{doc("a1", unit(dims, map[int]float32{0: 1}), nil)})
	ctx := ctxT(t)
	id, err := c.Swap(ctx, live, missing)
	if err != nil {
		t.Fatalf("the swap request itself must be accepted (202), got %v", err)
	}
	_, err = c.WaitTask(ctx, id)
	var me *meili.Error
	if meili.CodeOf(err) != meili.CodeIndexTaskFailed {
		t.Fatalf("WaitTask code = %q, err %v", meili.CodeOf(err), err)
	}
	me = err.(*meili.Error)
	if me.MeiliCode != "index_not_found" || me.TaskUID != id {
		t.Fatalf("MeiliCode = %q, TaskUID = %d (want %d)", me.MeiliCode, me.TaskUID, id)
	}
	if n, _ := c.DocumentCount(ctx, live); n != 1 {
		t.Fatalf("live count = %d, want 1", n)
	}
}

func TestFailedBatchLeavesIndexUntouched(t *testing.T) {
	c := newClient(t)
	const dims = 8
	u := uid(t, c, "fail")
	build(t, c, u, dims, []meili.Document{
		doc("ok1", unit(dims, map[int]float32{0: 1}), nil),
		doc("ok2", unit(dims, map[int]float32{1: 1}), nil),
	})
	ctx := ctxT(t)
	bad := doc("bad", []float32{1, 0, 0}, nil) // 3 dims into an 8-dim embedder
	good := doc("fresh", unit(dims, map[int]float32{2: 1}), nil)
	tasks, err := c.AddDocuments(ctx, u, []meili.Document{good, bad}, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("AddDocuments: %v, %d tasks", err, len(tasks))
	}
	_, err = c.WaitTask(ctx, tasks[0])
	if meili.CodeOf(err) != meili.CodeIndexTaskFailed {
		t.Fatalf("code = %q, err %v", meili.CodeOf(err), err)
	}
	if mc := err.(*meili.Error).MeiliCode; mc != "invalid_vector_dimensions" {
		t.Fatalf("MeiliCode = %q, want invalid_vector_dimensions", mc)
	}
	if n, err := c.DocumentCount(ctx, u); err != nil || n != 2 {
		t.Fatalf("count = %d, err %v; want 2 (whole batch rolled back, 'fresh' not indexed)", n, err)
	}
	if err := c.WaitTasks(ctx, tasks); meili.CodeOf(err) != meili.CodeIndexTaskFailed {
		t.Fatalf("WaitTasks did not surface the failure: %v", err)
	}
}

func TestAddDocumentsSplitsIntoBatches(t *testing.T) {
	c := newClient(t)
	const dims = 8
	u := uid(t, c, "batches")
	var docs []meili.Document
	for i := range 7 {
		docs = append(docs, doc(fmt.Sprintf("d%d", i), unit(dims, map[int]float32{i % dims: 1}), nil))
	}
	ctx := ctxT(t)
	id, err := c.CreateIndex(ctx, u, "id")
	mustWait(t, c, id, err)
	s, _ := meili.DefaultSettings(dims, scopeKeys)
	id, err = c.ConfigureIndex(ctx, u, s)
	mustWait(t, c, id, err)
	tasks, err := c.AddDocuments(ctx, u, docs, 3)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("tasks = %d, err %v; want 3 (3+3+1)", len(tasks), err)
	}
	if err := c.WaitTasks(ctx, tasks); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.DocumentCount(ctx, u); n != 7 {
		t.Fatalf("count = %d, want 7", n)
	}
}

func TestIndexExistsAndDelete(t *testing.T) {
	c := newClient(t)
	u := uid(t, c, "ex")
	ctx := ctxT(t)
	if ok, err := c.IndexExists(ctx, u); err != nil || ok {
		t.Fatalf("before create: %v, %v", ok, err)
	}
	id, err := c.CreateIndex(ctx, u, "id")
	mustWait(t, c, id, err)
	if ok, err := c.IndexExists(ctx, u); err != nil || !ok {
		t.Fatalf("after create: %v, %v", ok, err)
	}
	uids, err := c.ListIndexUIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range uids {
		found = found || x == u
	}
	if !found {
		t.Fatalf("%s not in ListIndexUIDs", u)
	}
	id, err = c.DeleteIndex(ctx, u)
	mustWait(t, c, id, err)
	if ok, _ := c.IndexExists(ctx, u); ok {
		t.Fatal("still exists after delete")
	}
	if _, err := c.DocumentCount(ctx, u); meili.CodeOf(err) != meili.CodeIndexUnavailable {
		t.Fatalf("count on missing index: %v", err)
	}
}

func TestSearchMissingIndex(t *testing.T) {
	c := newClient(t)
	_, err := c.VectorSearch(ctxT(t), "kbt_does_not_exist_zzz", []float32{1, 0}, 3, "", 0.6)
	if meili.CodeOf(err) != meili.CodeIndexUnavailable {
		t.Fatalf("code = %q, err %v; want INDEX_UNAVAILABLE", meili.CodeOf(err), err)
	}
}

func TestTimeoutMapping(t *testing.T) {
	c := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	_, err := c.VectorSearch(ctx, "kbt_any", []float32{1, 0}, 3, "", 0.6)
	if meili.CodeOf(err) != meili.CodeUpstreamTimeout {
		t.Fatalf("code = %q, err %v; want UPSTREAM_TIMEOUT", meili.CodeOf(err), err)
	}
	// A client-level timeout maps the same way.
	u, key := testdb.MeiliURL(t)
	c2, _ := meili.New(meili.Config{BaseURL: u, APIKey: key, HTTPClient: &http.Client{Timeout: time.Nanosecond}})
	_, err = c2.VectorSearch(context.Background(), "kbt_any", []float32{1, 0}, 3, "", 0.6)
	if meili.CodeOf(err) != meili.CodeUpstreamTimeout {
		t.Fatalf("client timeout: code = %q, err %v", meili.CodeOf(err), err)
	}
}

func TestWaitTaskContextDeadline(t *testing.T) {
	c := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := c.WaitTask(ctx, 0); meili.CodeOf(err) != meili.CodeUpstreamTimeout {
		t.Fatalf("code = %q, err %v", meili.CodeOf(err), err)
	}
}

func TestUnavailableMapping(t *testing.T) {
	c, _ := meili.New(meili.Config{BaseURL: "http://127.0.0.1:1"})
	_, err := c.VectorSearch(context.Background(), "x", []float32{1}, 1, "", 0.6)
	if meili.CodeOf(err) != meili.CodeUpstreamUnavailable {
		t.Fatalf("connection refused: code %q", meili.CodeOf(err))
	}
	// 5xx is stubbed because a real Meilisearch cannot be told to answer 503.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"busy","code":"internal"}`))
	}))
	defer srv.Close()
	c, _ = meili.New(meili.Config{BaseURL: srv.URL})
	_, err = c.VectorSearch(context.Background(), "x", []float32{1}, 1, "", 0.6)
	if meili.CodeOf(err) != meili.CodeUpstreamUnavailable || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("5xx: %v", err)
	}
}

// TestMultipleVectorsPerDocumentScoreByTheBestOne pins what publish relies on:
// a document with several user-provided vectors (one per question phrasing) is
// found by any of them, and scored by the best match, not by an average.
func TestMultipleVectorsPerDocumentScoreByTheBestOne(t *testing.T) {
	c := newClient(t)
	const dims = 8
	u := uid(t, c, "mv")
	e0, e1, e2 := unit(dims, map[int]float32{0: 1}), unit(dims, map[int]float32{1: 1}), unit(dims, map[int]float32{2: 1})
	multi := doc("multi", e0, nil)
	multi.SetVectors([][]float32{e0, e1})
	build(t, c, u, dims, []meili.Document{multi, doc("single", e2, nil)})
	ctx := ctxT(t)
	for name, q := range map[string][]float32{"first vector": e0, "second vector": e1} {
		r, err := c.VectorSearch(ctx, u, q, 5, "", 0.9)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(hitIDs(r), []string{"multi"}) || len(r.Hits) != 1 || r.Hits[0].Score < 0.999 {
			t.Errorf("%s: hits %+v, want exactly multi at ~1.0 (max over vectors, not a mean)", name, r.Hits)
		}
	}
	if n, err := c.DocumentCount(ctx, u); err != nil || n != 2 {
		t.Errorf("count = %d, %v; want 2 documents (not 3 vectors)", n, err)
	}
	ids, err := c.DocumentIDs(ctx, u)
	if err != nil || !reflect.DeepEqual(sorted(ids), []string{"multi", "single"}) || len(ids) != 2 {
		t.Errorf("DocumentIDs = %v, %v", ids, err)
	}
}

// TestGlobalDocumentsMatchAnyScopeValue pins the scope semantics: a document
// that lacks a scope key is global for that key.
func TestGlobalDocumentsMatchAnyScopeValue(t *testing.T) {
	c := newClient(t)
	const dims = 8
	u := uid(t, c, "global")
	v := unit(dims, map[int]float32{0: 1})
	build(t, c, u, dims, []meili.Document{
		doc("acme-web", v, map[string]string{"brand": "acme", "channel": "web"}),
		doc("acme-only", v, map[string]string{"brand": "acme"}),
		doc("globex-only", v, map[string]string{"brand": "globex"}),
		doc("global-nil", v, nil),
		doc("global-empty", v, map[string]string{}),
		doc("web-only", v, map[string]string{"channel": "web"}),
	})
	ctx := ctxT(t)
	for _, tc := range []struct {
		name  string
		scope map[string]string
		want  []string
	}{
		{"no filter returns everything", nil, []string{"acme-only", "acme-web", "global-empty", "global-nil", "globex-only", "web-only"}},
		{"brand acme", map[string]string{"brand": "acme"}, []string{"acme-only", "acme-web", "global-empty", "global-nil", "web-only"}},
		{"brand globex", map[string]string{"brand": "globex"}, []string{"global-empty", "global-nil", "globex-only", "web-only"}},
		{"brand and channel", map[string]string{"brand": "acme", "channel": "web"}, []string{"acme-only", "acme-web", "global-empty", "global-nil", "web-only"}},
		{"channel voice", map[string]string{"channel": "voice"}, []string{"acme-only", "global-empty", "global-nil", "globex-only"}},
		{"unknown brand sees only brand-less documents", map[string]string{"brand": "initech"}, []string{"global-empty", "global-nil", "web-only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := meili.BuildFilter(scopeKeys, tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			r, err := c.VectorSearch(ctx, u, v, 20, f, 0.6)
			if err != nil {
				t.Fatalf("filter %q: %v", f, err)
			}
			got := sorted(hitIDs(r))
			if !reflect.DeepEqual(got, tc.want) || len(got) != len(tc.want) {
				t.Fatalf("filter %q: got %v, want %v", f, got, tc.want)
			}
		})
	}
}

func TestSwapRenameMovesAnIndexOntoAnAbsentUID(t *testing.T) {
	c := newClient(t)
	const dims = 8
	from, to := uid(t, c, "ren_from"), uid(t, c, "ren_to")
	build(t, c, from, dims, []meili.Document{doc("a", unit(dims, map[int]float32{0: 1}), nil)})
	ctx := ctxT(t)
	id, err := c.SwapRename(ctx, from, to)
	mustWait(t, c, id, err)
	if ok, _ := c.IndexExists(ctx, from); ok {
		t.Error("source uid still exists after rename")
	}
	if n, err := c.DocumentCount(ctx, to); err != nil || n != 1 {
		t.Errorf("renamed index count = %d, %v", n, err)
	}
	// A rename onto an existing index fails the task and changes nothing.
	other := uid(t, c, "ren_other")
	build(t, c, other, dims, []meili.Document{doc("b", unit(dims, map[int]float32{1: 1}), nil)})
	id, err = c.SwapRename(ctx, other, to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitTask(ctx, id); meili.CodeOf(err) != meili.CodeIndexTaskFailed {
		t.Fatalf("rename onto existing: %v, want INDEX_TASK_FAILED", err)
	}
	if got, _ := c.DocumentIDs(ctx, to); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("target changed: %v", got)
	}
}
