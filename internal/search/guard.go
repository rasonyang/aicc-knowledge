// SPDX-License-Identifier: Apache-2.0

package search

import (
	"slices"

	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/products"
)

// Outcomes of the product guard, as metric label values.
const (
	GuardDroppedDisjoint    = "dropped_disjoint"
	GuardUnknownModel       = "unknown_model"
	GuardGenericBelowMargin = "generic_below_margin"
)

// Guard counts what the product guard changed in one search. A count is only
// raised for a hit that would otherwise have been a HIT (it cleared the plain
// threshold), so the counters say how many answers the guard took away.
type Guard struct {
	DroppedDisjoint    int
	UnknownModel       int
	GenericBelowMargin int
}

// Drop is one hit the guard removed, kept for eval so a sweep can re-decide it
// at other thresholds and margins.
type Drop struct {
	Reason string
	Score  float64
}

// Add sums two guards.
func (g Guard) Add(o Guard) Guard {
	return Guard{g.DroppedDisjoint + o.DroppedDisjoint, g.UnknownModel + o.UnknownModel, g.GenericBelowMargin + o.GenericBelowMargin}
}

// Any reports whether the guard changed anything.
func (g Guard) Any() bool { return g.DroppedDisjoint+g.UnknownModel+g.GenericBelowMargin > 0 }

// applyGuard filters hits (sorted by score, best first) with the catalog and
// the threshold, and returns the survivors. Without a catalog (nil) it is the
// plain threshold.
//
//   - R2: a model-like token of a known product family (see products.Extract) that no catalog alias covers means the
//     caller asks about a product the catalog does not know: nothing is
//     returned.
//   - R1: when the query names products, a hit whose product set is non-empty
//     and disjoint from the query's products (expanded with the products they
//     are compatible with) is dropped.
//   - R3: a generic hit (no products) counts only from threshold plus margin.
//
// The cost is one catalog extraction of the query text, microseconds.
func applyGuard(cat *products.Catalog, query string, hits []meili.Hit, threshold, margin float64, record bool) ([]meili.Hit, Guard, []Drop) {
	var g Guard
	var drops []Drop
	cleared := func(h meili.Hit) bool { return h.Score >= threshold }
	if cat == nil {
		return slices.DeleteFunc(slices.Clone(hits), func(h meili.Hit) bool { return !cleared(h) }), g, nil
	}
	ex := cat.Extract(query)
	if len(ex.UnknownModels) > 0 {
		if slices.ContainsFunc(hits, cleared) {
			g.UnknownModel = 1
		}
		if record {
			for _, h := range hits {
				drops = append(drops, Drop{GuardUnknownModel, h.Score})
			}
		}
		return nil, g, drops
	}
	var allowed map[string]bool
	if len(ex.IDs) > 0 {
		allowed = cat.Expand(ex.IDs)
	}
	kept := make([]meili.Hit, 0, len(hits))
	for _, h := range hits {
		if !cleared(h) {
			continue
		}
		switch {
		case len(h.Products) == 0:
			if h.Score < threshold+margin {
				g.GenericBelowMargin++
				continue
			}
		case allowed != nil && !slices.ContainsFunc(h.Products, func(p string) bool { return allowed[p] }):
			g.DroppedDisjoint++
			if record {
				drops = append(drops, Drop{GuardDroppedDisjoint, h.Score})
			}
			continue
		}
		kept = append(kept, h)
	}
	return kept, g, drops
}
