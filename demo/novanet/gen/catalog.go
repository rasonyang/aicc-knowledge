// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/goccy/go-yaml"

	"github.com/rasonyang/aicc-knowledge/internal/products"
)

// The product catalog, products.yaml at the root of the corpus.
//
// Design:
//   - One product per name that differs by a word or a number: Plan 59 and Plan
//     59 Plus, Fiber 500 and Fiber 1000, R5 and R5 Pro, T2 and T2 Max, Booster
//     10 and Booster 30. None of these pairs is declared compatible, so the
//     guard drops the FAQ of the other one.
//   - Every plan carries its Chinese name from plans.go (NameZH) and the forms
//     people say and write: "59元Plus套餐", "光纤五百", "光纤一千".
//   - The families the guard knows are the leading Latin words of the aliases:
//     plan, fiber, basic, r, g, t, m (senior and booster are longer than the
//     five letters the guard reads). A question with an unlisted model of one
//     of them ("Plan 69", "R7", "T3") is answered NO_MATCH.
//   - M1 is compatible with R5 and R5 Pro, because the extender FAQ answers
//     questions about those routers (and the other way round).
//   - Deliberately not aliases: generic words ("plan", "router"), the brand,
//     and the ASR slip "光线五百". A question that uses them is not guarded.
//   - Three-digit numbers cannot be told apart from quantities by the guard,
//     so "Fiber 800" is not an unknown model; a question about it falls to the
//     similarity threshold.

func init() { register("catalog", writeCatalog) }

func catalogProducts() []products.Product {
	ps := []products.Product{
		{ID: "basic-plan", Names: []string{"Basic Plan", nvPlan("Basic").NameZH}},
		{ID: "plan-59", Names: []string{"Plan 59", nvPlan("Plan 59").NameZH, "59块套餐", "五十九元套餐"}},
		{ID: "plan-59-plus", Names: []string{"Plan 59 Plus", nvPlan("Plan 59 Plus").NameZH, "59块Plus套餐", "五十九元Plus套餐"}},
		{ID: "plan-99", Names: []string{"Plan 99", nvPlan("Plan 99").NameZH, "99块套餐", "九十九元套餐"}},
		{ID: "senior-care", Names: []string{"Senior Care", nvPlan("Senior Care").NameZH}},
		{ID: "fiber-500", Names: []string{"Fiber 500", nvPlan("Fiber 500").NameZH, "光纤五百"}},
		{ID: "fiber-1000", Names: []string{"Fiber 1000", nvPlan("Fiber 1000").NameZH, "光纤一千"}},
		{ID: "r3", Names: []string{"R3"}},
		{ID: "r5", Names: []string{"R5"}},
		{ID: "r5-pro", Names: []string{"R5 Pro"}},
		{ID: "g1", Names: []string{"G1"}},
		{ID: "g2", Names: []string{"G2"}},
		{ID: "t2", Names: []string{"T2"}},
		{ID: "t2-max", Names: []string{"T2 Max"}},
		{ID: "m1", Names: []string{"M1"}, CompatibleWith: []string{"r5", "r5-pro"}},
		{ID: "booster-10", Names: []string{"Booster 10", "加油包10"}},
		{ID: "booster-30", Names: []string{"Booster 30", "加油包30"}},
	}
	return ps
}

func writeCatalog(e *env) error {
	y, err := yaml.Marshal(products.File{Products: catalogProducts()})
	if err != nil {
		return err
	}
	return nvWrite(e.out, products.CatalogFileName, append([]byte("# SPDX-License-Identifier: Apache-2.0\n"), y...))
}
