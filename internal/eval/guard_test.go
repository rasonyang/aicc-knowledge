// SPDX-License-Identifier: Apache-2.0

package eval_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/eval"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

// TestSweepGridByHand re-decides recorded outcomes at several thresholds and
// generic margins. Question 1: the best item is generic (0.90), a product item
// (0.86) follows. Question 2: the guard dropped a 0.95 hit about another
// product; the only survivor is generic at 0.89.
func TestSweepGridByHand(t *testing.T) {
	EN := domain.LanguageEN
	q1 := out(EN, []string{"prod"}, "HIT", []string{"gen", "prod"}, []float64{0.90, 0.86}, 1)
	q1.Generic, q1.Catalog = []bool{true, false}, true
	q2 := out(EN, nil, "HIT", []string{"gen2"}, []float64{0.89}, 1)
	q2.Generic, q2.Catalog = []bool{true}, true
	q2.Dropped = []search.Drop{{Reason: search.GuardDroppedDisjoint, Score: 0.95}}
	q3 := out(EN, nil, "NO_MATCH", nil, nil, 1)
	q3.Catalog = true
	q3.Dropped = []search.Drop{{Reason: search.GuardUnknownModel, Score: 0.91}, {Reason: search.GuardUnknownModel, Score: 0.7}}
	outs := []eval.Outcome{q1, q2, q3}

	rows := eval.SweepGrid(outs, []float64{0.85}, eval.SweepMargins())
	if len(rows) != 4 || rows[0].Margin != 0 || rows[3].Margin != 0.06 {
		t.Fatalf("rows = %+v, want one per margin", rows)
	}
	// The expectations spelled out, one margin at a time.
	at := func(m float64) []eval.Outcome { return eval.WithGuard(outs, 0.85, m) }
	if o := at(0); len(o[0].IDs) != 2 || o[1].Status != "HIT" || eval.GuardTotal(o) != (search.Guard{DroppedDisjoint: 1, UnknownModel: 1}) {
		t.Errorf("margin 0: %+v guard %+v", o[0], eval.GuardTotal(o))
	}
	// Margin 0.06: both generic items need 0.91; q1 keeps only the product item
	// (the generic 0.90 falls short), q2 becomes NO_MATCH.
	o := at(0.06)
	if len(o[0].IDs) != 1 || o[0].IDs[0] != "prod" || o[1].Status != "NO_MATCH" || len(o[1].IDs) != 0 {
		t.Errorf("margin 0.06: q1 %v q2 %s", o[0].IDs, o[1].Status)
	}
	if g := eval.GuardTotal(o); g.GenericBelowMargin != 2 || g.DroppedDisjoint != 1 || g.UnknownModel != 1 {
		t.Errorf("margin 0.06 guard = %+v", g)
	}
	// A dropped hit below the threshold would not have been an answer: not counted.
	if g := eval.GuardTotal(eval.WithGuard(outs, 0.96, 0)); g.Any() {
		t.Errorf("threshold 0.96 guard = %+v, want zero", g)
	}
}

func TestSweepReportsSweepMarginsOnlyWithACatalog(t *testing.T) {
	EN := domain.LanguageEN
	plain := []eval.Outcome{out(EN, []string{"a"}, "HIT", []string{"a"}, []float64{0.9}, 1)}
	rep := eval.SweepReports(plain, 0.9)[0]
	if rep.Catalog || len(rep.Sweep) != 19 {
		t.Errorf("without a catalog: catalog=%v rows=%d, want 19 thresholds only", rep.Catalog, len(rep.Sweep))
	}
	withCat := []eval.Outcome{plain[0]}
	withCat[0].Catalog = true
	rep = eval.SweepReports(withCat, 0.9)[0]
	if !rep.Catalog || len(rep.Sweep) != 19*4 {
		t.Errorf("with a catalog: catalog=%v rows=%d, want 19x4", rep.Catalog, len(rep.Sweep))
	}
	var txt bytes.Buffer
	eval.WriteText(&txt, []eval.Report{rep}, 0.9)
	if !strings.Contains(txt.String(), "margin") || !strings.Contains(txt.String(), "guard") {
		t.Errorf("text report lacks the margin and guard columns:\n%s", txt.String())
	}
	var js bytes.Buffer
	if err := eval.WriteJSON(&js, []eval.Report{rep}, 0.9); err != nil || !strings.Contains(js.String(), `"genericMargin"`) || !strings.Contains(js.String(), `"productCatalog": true`) {
		t.Errorf("JSON report: %v\n%s", err, js.String())
	}
}

// TestLiveGuardCountsAndSweepAgreeWithTheRealRun runs synthetic questions
// through the real services with a catalog, then checks that the sweep
// re-decided at the configured threshold and margin gives the real run's
// answers and guard counts.
func TestLiveGuardCountsAndSweepAgreeWithTheRealRun(t *testing.T) {
	e := publishtest.New(t)
	EN := domain.LanguageEN
	e.SetCatalog(`products:
  - id: zq-3
    names: ["ZQ 3"]
  - id: zq-ultra
    names: ["ZQ Ultra"]
`)
	zq3 := e.AddCandidate(publishtest.Cand{Language: EN, Question: "How long does the ZQ 3 battery last?"})
	ultra := e.AddCandidate(publishtest.Cand{Language: EN, Question: "How long does the ZQ Ultra battery last?"})
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I request an invoice for my purchase?"})
	e.Publish(EN)
	e.UseCatalogs()
	e.Searcher.ThresholdEN, e.Searcher.GenericMarginEN = 0.8, 0.2

	qs := []eval.Question{
		{Text: "How long does the ZQ Ultra battery last?", Language: EN, Expected: []string{ultra.String()}},
		{Text: "How long does the ZQ 3 battery last?", Language: EN, Expected: []string{zq3.String()}},
		{Text: "How long does the ZQ 9 battery last?", Language: EN},
		{Text: "How can I get an invoice?", Language: EN},
		{Text: "What is the capital of France?", Language: EN},
	}
	ctx := context.Background()
	runner := eval.InProcess{Searcher: e.Searcher, Timeout: 90 * time.Second}
	real := eval.Run(ctx, runner, qs, nil)
	zero := 0.0
	raw := eval.Run(ctx, runner, qs, &zero)

	for i := range qs {
		if real[i].Err != nil || raw[i].Err != nil {
			t.Fatalf("q%d: %v / %v", i, real[i].Err, raw[i].Err)
		}
	}
	g := eval.GuardTotal(real)
	if g.DroppedDisjoint < 2 || g.UnknownModel != 1 || g.GenericBelowMargin != 1 {
		t.Errorf("real guard counts = %+v, want >=2 dropped (each product question loses the other's FAQ), 1 unknown model, 1 generic below margin", g)
	}
	re := eval.WithGuard(raw, 0.8, 0.2)
	for i := range qs {
		if re[i].Status != real[i].Status || strings.Join(re[i].IDs, ",") != strings.Join(real[i].IDs, ",") {
			t.Errorf("q%d: sweep %s %v, real %s %v", i, re[i].Status, re[i].IDs, real[i].Status, real[i].IDs)
		}
	}
	if sg := eval.GuardTotal(re); sg != g {
		t.Errorf("sweep guard counts %+v differ from the real run's %+v", sg, g)
	}
	if !eval.HasCatalog(raw) {
		t.Error("the sweep run does not know a catalog applied")
	}
}
