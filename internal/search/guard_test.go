// SPDX-License-Identifier: Apache-2.0

package search_test

import (
	"context"
	"io"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

const guardCatalogYAML = `products:
  - id: zq-3
    names: ["ZQ 3"]
    compatibleWith: [zq-3s]
  - id: zq-3s
    names: ["ZQ 3S"]
  - id: zq-ultra
    names: ["ZQ Ultra"]
`

type guardCorpus struct {
	zq3, zq3s, ultra, invoice uuid.UUID
}

// publishGuardCorpus publishes four synthetic FAQs about three fictional
// products and one generic topic, with the catalog above.
func publishGuardCorpus(e *publishtest.Env) guardCorpus {
	e.SetCatalog(guardCatalogYAML)
	c := guardCorpus{
		zq3:     e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "How long does the ZQ 3 battery last?", Answer: "About ten hours."}),
		zq3s:    e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "How do I charge the ZQ 3S?", Answer: "Use the USB-C port."}),
		ultra:   e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "How long does the ZQ Ultra battery last?", Answer: "About twenty hours."}),
		invoice: e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "How do I request an invoice for my purchase?", Answer: "Open Billing."}),
	}
	e.Publish(EN)
	return c
}

func searchIDs(t *testing.T, s *search.Searcher, q string) (search.Response, []string) {
	t.Helper()
	r, err := s.Search(context.Background(), search.Request{Query: q, Language: EN, TopK: 3, Timeout: 90e9})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, it := range r.Items {
		ids = append(ids, it.ID)
	}
	return r, ids
}

func TestGuardNeverAnswersWithAnotherProductsFAQ(t *testing.T) {
	e := publishtest.New(t)
	c := publishGuardCorpus(e)
	s := e.Searcher
	s.ThresholdEN = 0.8

	// Without a catalog the near-identical ZQ 3 FAQ is returned for a ZQ Ultra
	// question. This is the failure the guard exists for, and the control that
	// makes the next assertion meaningful.
	q := "How long does the ZQ Ultra battery last?"
	_, before := searchIDs(t, s, q)
	if !slices.Contains(before, c.zq3.String()) || len(before) < 2 {
		t.Fatalf("control: without a catalog the answer is %v, want it to include the ZQ 3 FAQ", before)
	}

	e.UseCatalogs()
	r, got := searchIDs(t, s, q)
	if !slices.Equal(got, []string{c.ultra.String()}) || len(got) != 1 || r.Status != search.StatusHit {
		t.Fatalf("with the catalog: %v (%+v), want only the ZQ Ultra FAQ", got, r)
	}
	if r.Guard.DroppedDisjoint < 1 || !r.Catalog {
		t.Errorf("guard = %+v catalog = %v", r.Guard, r.Catalog)
	}
	if !slices.Equal(r.Items[0].Products, []string{"zq-ultra"}) {
		t.Errorf("products = %v", r.Items[0].Products)
	}
}

func TestGuardLetsCompatibleProductsThrough(t *testing.T) {
	e := publishtest.New(t)
	c := publishGuardCorpus(e)
	e.UseCatalogs()
	s := e.Searcher
	s.ThresholdEN = 0.7

	// A ZQ 3 question may be answered by the ZQ 3S FAQ (declared compatible,
	// symmetric), but a ZQ Ultra question may not.
	_, got := searchIDs(t, s, "How do I charge the ZQ 3?")
	if !slices.Contains(got, c.zq3s.String()) {
		t.Errorf("ZQ 3 question: %v lacks the compatible ZQ 3S FAQ", got)
	}
	if slices.Contains(got, c.ultra.String()) {
		t.Errorf("ZQ 3 question returned the ZQ Ultra FAQ: %v", got)
	}
	_, got = searchIDs(t, s, "How long does the ZQ 3S battery last?")
	if !slices.Contains(got, c.zq3.String()) {
		t.Errorf("ZQ 3S question: %v lacks the compatible ZQ 3 FAQ", got)
	}
	_, got = searchIDs(t, s, "How do I charge the ZQ Ultra?")
	if slices.Contains(got, c.zq3s.String()) || slices.Contains(got, c.zq3.String()) {
		t.Errorf("ZQ Ultra question returned another product's FAQ: %v", got)
	}
}

func TestGuardUnknownModelIsNoMatchAndCounted(t *testing.T) {
	e := publishtest.New(t)
	publishGuardCorpus(e)
	p, err := obs.Setup(context.Background(), obs.Options{ServiceName: "guard-test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(context.Background())
	s := e.Searcher
	s.ThresholdEN = 0.7
	s.Metrics = p.Metrics

	q := "How long does the ZQ 9 battery last?"
	_, before := searchIDs(t, s, q)
	if len(before) == 0 {
		t.Fatal("control: without a catalog the unknown model gets an answer")
	}
	e.UseCatalogs()
	r, got := searchIDs(t, s, q)
	if r.Status != search.StatusNoMatch || len(got) != 0 || r.Guard.UnknownModel != 1 {
		t.Fatalf("unknown model: %+v", r)
	}
	// A known model of the same family is still answered.
	if r, got = searchIDs(t, s, "How long does the ZQ 3 battery last?"); r.Status != search.StatusHit || len(got) == 0 {
		t.Errorf("known model: %+v", r)
	}

	rec := httptest.NewRecorder()
	p.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	if want := obs.MetricProductGuardTotal + `{language="EN",outcome="unknown_model"`; !strings.Contains(string(body), want) {
		t.Errorf("/metrics lacks %s", want)
	}
}

func TestGenericFAQNeedsThresholdPlusMargin(t *testing.T) {
	const th = 0.8
	e := publishtest.New(t)
	c := publishGuardCorpus(e)
	e.UseCatalogs()
	s := e.Searcher
	s.ThresholdEN = th
	q := "How can I get an invoice?"

	r, got := searchIDs(t, s, q)
	if len(got) == 0 || got[0] != c.invoice.String() {
		t.Fatalf("control: %v", got)
	}
	score := r.Items[0].Score
	if score < th {
		t.Fatalf("score %.3f is under the threshold", score)
	}
	margin := func(m float64) *float64 { return &m }
	for _, tc := range []struct {
		name   string
		margin float64
		hit    bool
	}{
		{"margin just below the gap lets it through", score - th - 0.005, true},
		{"margin just above the gap blocks it", score - th + 0.005, false},
	} {
		r, err := s.Search(context.Background(), search.Request{Query: q, Language: EN, TopK: 3, GenericMargin: margin(tc.margin), Timeout: 90e9})
		if err != nil {
			t.Fatal(err)
		}
		if (r.Status == search.StatusHit) != tc.hit || len(r.Items) != map[bool]int{true: 1, false: 0}[tc.hit] {
			t.Errorf("%s: %+v", tc.name, r)
		}
		if !tc.hit && r.Guard.GenericBelowMargin != 1 {
			t.Errorf("%s: guard = %+v", tc.name, r.Guard)
		}
	}
	// The configured margin is the default.
	s.GenericMarginEN = score - th + 0.005
	if r, _ := searchIDs(t, s, q); r.Status != search.StatusNoMatch {
		t.Errorf("configured margin ignored: %+v", r)
	}
}

func TestWithoutACatalogSearchIsUnchanged(t *testing.T) {
	e := publishtest.New(t)
	f := publishEN(e) // no catalog object: the publication has none
	cache := e.UseCatalogs()
	if cache.Catalog(EN) != nil {
		t.Fatal("a publication without a catalog loaded one")
	}
	e.Searcher.GenericMarginEN, e.Searcher.GenericMarginZH = 0.2, 0.2 // must not matter without a catalog

	plain := *e.Searcher
	plain.Catalogs = nil
	for _, q := range []string{"I can't remember my login password", "What is the capital of France?", "ZQ 9 battery", "When do you open?"} {
		a, err := e.Searcher.Search(context.Background(), search.Request{Query: q, Language: EN, TopK: 3, Timeout: 90e9})
		if err != nil {
			t.Fatal(err)
		}
		b, err := plain.Search(context.Background(), search.Request{Query: q, Language: EN, TopK: 3, Timeout: 90e9})
		if err != nil {
			t.Fatal(err)
		}
		if a.Status != b.Status || len(a.Items) != len(b.Items) || a.Guard.Any() || a.Catalog {
			t.Errorf("%q: with an empty cache %+v, without %+v", q, a, b)
		}
		for i := range a.Items {
			if a.Items[i].ID != b.Items[i].ID || a.Items[i].Score != b.Items[i].Score {
				t.Errorf("%q item %d differs", q, i)
			}
		}
	}
	if r, ids := searchIDs(t, e.Searcher, "I can't remember my login password"); r.Status != search.StatusHit || len(ids) == 0 || ids[0] != f.reset.String() {
		t.Errorf("hit = %+v", r)
	}
}

func TestCatalogCacheFollowsTheLivePublication(t *testing.T) {
	e := publishtest.New(t)
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How long does the ZQ 3 battery last?"})
	e.Publish(EN) // P1: no catalog
	cache := e.UseCatalogs()
	if cache.Catalog(EN) != nil || cache.Catalog("ZH") != nil {
		t.Fatal("a catalog before any was published")
	}
	e.SetCatalog(guardCatalogYAML)
	p2 := e.Publish(EN)
	if cache.Catalog(EN) != nil {
		t.Fatal("the cache changed before a refresh (it must lag, not poll per search)")
	}
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := cache.Catalog(EN); c == nil || !c.Has("zq-ultra") || cache.Catalog("ZH") != nil {
		t.Fatal("refresh did not load the live publication's catalog")
	}
	// Rolling back to the publication without a catalog unloads it.
	if _, err := e.Publisher.Rollback(context.Background(), EN, nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cache.Catalog(EN) != nil {
		t.Error("the catalog of a superseded publication is still served")
	}
	_ = p2
}

// TestUnknownModelOnlyConcernsKnownFamilies: R2 fires for an unknown model of
// a family the catalog knows (ZQ 9, Nova K9), not for other model-like tokens
// such as iOS17, USB3, mp4 or Android 14.
func TestUnknownModelOnlyConcernsKnownFamilies(t *testing.T) {
	e := publishtest.New(t)
	e.SetCatalog(guardCatalogYAML + "  - id: nova-k2\n    names: [\"Nova K2\"]\n")
	zq3 := e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "How do I pair the ZQ 3 with a phone?"})
	gen := e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "How do I pair my headphones with a phone over Bluetooth?"})
	e.Publish(EN)
	e.UseCatalogs()
	s := e.Searcher
	s.ThresholdEN = 0.7

	for _, q := range []string{"Is the ZQ 9 waterproof?", "Is the Nova K9 waterproof?"} {
		if r, got := searchIDs(t, s, q); r.Status != search.StatusNoMatch || len(got) != 0 {
			t.Errorf("%q: %+v, want NO_MATCH (unknown model of a known family)", q, r)
		}
	}
	r, got := searchIDs(t, s, "How do I pair the ZQ 3 with my iPhone 15?")
	if r.Status != search.StatusHit || !slices.Contains(got, zq3.String()) || r.Guard.UnknownModel != 0 {
		t.Errorf("phone question: %+v", r)
	}
	for _, q := range []string{"Does it support mp4 when I pair with a phone?", "Does pairing work with wifi6 and USB3?", "Can I pair with iOS17 or Android 14 or H265 4K?"} {
		r, _ := searchIDs(t, s, q)
		if r.Guard.UnknownModel != 0 {
			t.Errorf("%q: counted as an unknown model: %+v", q, r.Guard)
		}
	}
	_ = gen
}

const bareAliasCatalogYAML = `products:
  - id: zq-series
    names: ["ZQ", "ZQ series", "ZQ 系列"]
  - id: zq-3s
    names: ["ZQ 3S"]
`

// A bare family alias in the catalog must not turn an unlisted model into the
// series: "ZQ 5" is still an unknown model (R2).
func TestBareFamilyAliasKeepsUnknownModelNoMatch(t *testing.T) {
	e := publishtest.New(t)
	e.SetCatalog(bareAliasCatalogYAML)
	series := e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "Is the ZQ series waterproof?", Answer: "Splash resistant."})
	zq3s := e.AddCandidate(publishtest.Cand{Key: "kb/a.docx", Language: EN, Question: "Is the ZQ 3S waterproof?", Answer: "Water resistant to one metre."})
	e.Publish(EN)
	e.UseCatalogs()
	s := e.Searcher
	s.ThresholdEN = 0.7

	r, got := searchIDs(t, s, "Is the ZQ 5 waterproof?")
	if r.Status != search.StatusNoMatch || len(got) != 0 || r.Guard.UnknownModel != 1 {
		t.Errorf("ZQ 5: %+v", r)
	}
	r, got = searchIDs(t, s, "Is the ZQ 3S waterproof?")
	if r.Status != search.StatusHit || len(got) == 0 || got[0] != zq3s.String() {
		t.Errorf("ZQ 3S: %v %+v", got, r)
	}
	r, got = searchIDs(t, s, "Is the ZQ series waterproof?")
	if r.Status != search.StatusHit || len(got) == 0 || got[0] != series.String() {
		t.Errorf("ZQ series: %v %+v", got, r)
	}
	r, got = searchIDs(t, s, "ZQ 系列 waterproof?")
	if r.Status != search.StatusHit || !slices.Contains(got, series.String()) {
		t.Errorf("ZQ 系列: %v %+v", got, r)
	}
}
