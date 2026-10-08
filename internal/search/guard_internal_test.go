// SPDX-License-Identifier: Apache-2.0

package search

import (
	"slices"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/products"
)

const guardCatalog = `products:
  - id: zq-3
    names: ["ZQ 3"]
    compatibleWith: [zq-3s]
  - id: zq-3s
    names: ["ZQ 3S"]
  - id: zq-ultra
    names: ["ZQ Ultra"]
`

func hit(id string, score float64, prods ...string) meili.Hit {
	return meili.Hit{ID: id, Score: score, Products: prods}
}

func ids(hits []meili.Hit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

func TestApplyGuardRules(t *testing.T) {
	cat, err := products.Parse([]byte(guardCatalog))
	if err != nil {
		t.Fatal(err)
	}
	const th, margin = 0.875, 0.04
	hits := []meili.Hit{
		hit("only3", 0.97, "zq-3"),
		hit("only3s", 0.96, "zq-3s"),
		hit("ultra", 0.95, "zq-ultra"),
		hit("generic", 0.93),
		hit("weakGeneric", 0.90),
		hit("below", 0.80, "zq-ultra"),
	}
	for _, tc := range []struct {
		name  string
		cat   *products.Catalog
		query string
		want  []string
		guard Guard
	}{
		{"no catalog is the plain threshold", nil, "ZQ Ultra battery", []string{"only3", "only3s", "ultra", "generic", "weakGeneric"}, Guard{}},
		{"R1 drops the other product", cat, "ZQ Ultra battery", []string{"ultra", "generic"}, Guard{DroppedDisjoint: 2, GenericBelowMargin: 1}},
		{"compatible products stay (3 asks, 3S answers)", cat, "ZQ 3 battery", []string{"only3", "only3s", "generic"}, Guard{DroppedDisjoint: 1, GenericBelowMargin: 1}},
		{"compatibility is symmetric", cat, "ZQ 3S battery", []string{"only3", "only3s", "generic"}, Guard{DroppedDisjoint: 1, GenericBelowMargin: 1}},
		{"no product named keeps product hits, generic needs the margin", cat, "battery", []string{"only3", "only3s", "ultra", "generic"}, Guard{GenericBelowMargin: 1}},
		{"R2 unknown model is NO_MATCH", cat, "ZQ 9 battery", []string{}, Guard{UnknownModel: 1}},
		{"R2 wins over a known product", cat, "ZQ Ultra versus ZQ 9", []string{}, Guard{UnknownModel: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, g, _ := applyGuard(tc.cat, tc.query, slices.Clone(hits), th, margin, false)
			if !slices.Equal(ids(got), tc.want) || len(got) != len(tc.want) {
				t.Errorf("kept %v, want %v", ids(got), tc.want)
			}
			if g != tc.guard {
				t.Errorf("guard = %+v, want %+v", g, tc.guard)
			}
		})
	}
}

func TestApplyGuardCountsOnlyWhatChangedTheAnswer(t *testing.T) {
	cat, _ := products.Parse([]byte(guardCatalog))
	// Everything is below the threshold: the guard changed nothing.
	_, g, _ := applyGuard(cat, "ZQ 9", []meili.Hit{hit("a", 0.6, "zq-3")}, 0.875, 0.04, false)
	if g.Any() {
		t.Errorf("guard = %+v, want zero", g)
	}
	_, g, _ = applyGuard(cat, "ZQ Ultra", []meili.Hit{hit("a", 0.6, "zq-3")}, 0.875, 0.04, false)
	if g.Any() {
		t.Errorf("guard = %+v, want zero", g)
	}
}

func TestApplyGuardRecordsDropsForEval(t *testing.T) {
	cat, _ := products.Parse([]byte(guardCatalog))
	_, _, drops := applyGuard(cat, "ZQ Ultra", []meili.Hit{hit("a", 0.9, "zq-3"), hit("b", 0.8, "zq-ultra")}, 0, 0, true)
	if len(drops) != 1 || drops[0] != (Drop{GuardDroppedDisjoint, 0.9}) {
		t.Errorf("drops = %+v", drops)
	}
	_, _, drops = applyGuard(cat, "ZQ 9", []meili.Hit{hit("a", 0.9, "zq-3"), hit("b", 0.8)}, 0, 0, true)
	if len(drops) != 2 || drops[0].Reason != GuardUnknownModel {
		t.Errorf("drops = %+v", drops)
	}
}

// TestGuardLatencyIsNegligible: the whole guard (query extraction and the hit
// loop) over a catalog of 200 products and a full over-fetch set takes well
// under a millisecond. The bound is generous; the benchmark has the number.
func TestGuardLatencyIsNegligible(t *testing.T) {
	var ps []products.Product
	for i := range 200 {
		ps = append(ps, products.Product{ID: "p-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Names: []string{"Model " + string(rune('A'+i%26)) + string(rune('a'+i/26)) + " Plus", "型号" + string(rune('甲'+i))}})
	}
	cat, err := products.New(ps)
	if err != nil {
		t.Fatal(err)
	}
	hits := make([]meili.Hit, 10)
	for i := range hits {
		hits[i] = hit("h", 0.9-float64(i)*0.01, "p-aa")
	}
	const n = 2000
	query := "请问Model Ab Plus和型号甲的电池续航有什么区别，我想了解一下保修政策"
	start := time.Now()
	for range n {
		applyGuard(cat, query, hits, 0.875, 0.04, false)
	}
	if per := time.Since(start) / n; per > time.Millisecond {
		t.Errorf("guard takes %v per search, want under 1ms", per)
	} else {
		t.Logf("guard: %v per search", per)
	}
}

func BenchmarkApplyGuard(b *testing.B) {
	cat, _ := products.Parse([]byte(guardCatalog))
	hits := []meili.Hit{hit("a", 0.97, "zq-3"), hit("b", 0.9), hit("c", 0.88, "zq-ultra")}
	for b.Loop() {
		applyGuard(cat, "我想问一下ZQ Ultra的电池", hits, 0.875, 0.04, false)
	}
}
