// SPDX-License-Identifier: Apache-2.0

package eval_test

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/eval"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestReadCSV(t *testing.T) {
	in := "\ufeffquestion,language,expectedIds,scope\n" +
		"\"How do I pay, exactly?\",en,id-1;id-2,brand=acme;channel=web\n" +
		"How is the weather,EN,,\n" +
		"\n" +
		"如何重置密码,ZH,id-3\n"
	qs, err := eval.ReadCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 3 {
		t.Fatalf("got %d questions: %+v", len(qs), qs)
	}
	q := qs[0]
	if q.Text != "How do I pay, exactly?" || q.Language != domain.LanguageEN || len(q.Expected) != 2 || q.Expected[1] != "id-2" ||
		len(q.Scope) != 2 || q.Scope["brand"] != "acme" || q.Scope["channel"] != "web" || !q.Answerable() || q.Line != 2 {
		t.Errorf("q0 = %+v", q)
	}
	if qs[1].Answerable() || qs[1].Scope != nil || len(qs[1].Expected) != 0 {
		t.Errorf("q1 = %+v", qs[1])
	}
	if qs[2].Language != domain.LanguageZH || qs[2].Expected[0] != "id-3" || qs[2].Line != 5 {
		t.Errorf("q2 = %+v", qs[2])
	}

	for name, bad := range map[string]string{
		"empty":            "",
		"no question rows": "question,language,expectedIds,scope\n",
		"missing column":   "question,language\nq,EN\n",
		"bad language":     "question,language,expectedIds\nq,FR,a\n",
		"empty question":   "question,language,expectedIds\n,EN,a\n",
		"bad scope":        "question,language,expectedIds,scope\nq,EN,a,brand\n",
	} {
		if _, err := eval.ReadCSV(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func out(lang domain.Language, expected []string, status string, ids []string, scores []float64, totalMs int) eval.Outcome {
	d := time.Duration(totalMs) * time.Millisecond
	return eval.Outcome{
		Question: eval.Question{Language: lang, Expected: expected},
		Status:   status, IDs: ids, Scores: scores,
		Latency: search.Latency{Embedding: d / 2, Search: d / 4, Total: d},
	}
}

// TestComputeByHand checks every number against a hand count.
func TestComputeByHand(t *testing.T) {
	EN := domain.LanguageEN
	outs := []eval.Outcome{
		out(EN, []string{"A"}, "HIT", []string{"A", "x", "y"}, nil, 10),      // hit at rank 1
		out(EN, []string{"B", "C"}, "HIT", []string{"x", "y", "C"}, nil, 20), // hit at rank 3 via the second expected id
		out(EN, []string{"D"}, "HIT", []string{"x", "y", "z"}, nil, 30),      // wrong
		out(EN, []string{"E"}, "NO_MATCH", nil, nil, 40),                     // answerable but NO_MATCH: a miss
		out(EN, []string{"F"}, "HIT", []string{"x", "y", "z", "F"}, nil, 50), // F is 4th: outside top 3
		out(EN, nil, "NO_MATCH", nil, nil, 60),                               // correct NO_MATCH
		out(EN, nil, "HIT", []string{"x"}, nil, 70),                          // uncovered but answered
		out(EN, nil, "NO_MATCH", nil, nil, 80),                               // correct NO_MATCH
	}
	outs = append(outs, eval.Outcome{Question: eval.Question{Language: EN, Expected: []string{"G"}}, Err: context_err{}}) // error: a miss, not in latency
	m := eval.Compute(outs)

	if m.Questions != 9 || m.Answerable != 6 || m.Unanswerable != 3 || m.Errors != 1 {
		t.Errorf("counts = %+v", m)
	}
	// answerable: A hit, B/C hit, D miss, E miss, F miss, G error(miss): 2 of 6
	if m.RecallHits != 2 || !near(m.RecallAt3, 2.0/6) {
		t.Errorf("recall@3 = %v (%d hits), want 2/6", m.RecallAt3, m.RecallHits)
	}
	// NO_MATCH answers: E (wrong), two uncovered ones (right): precision 2/3
	if m.NoMatchAnswered != 3 || m.NoMatchCorrect != 2 || !near(m.NoMatchPrecision, 2.0/3) {
		t.Errorf("NO_MATCH precision = %v (%d of %d)", m.NoMatchPrecision, m.NoMatchCorrect, m.NoMatchAnswered)
	}
	// of 3 uncovered questions, 2 were answered NO_MATCH
	if !near(m.NoMatchRecall, 2.0/3) {
		t.Errorf("NO_MATCH recall = %v", m.NoMatchRecall)
	}
	// totals 10..80 (8 values): nearest rank p50 = 4th = 40, p90 = ceil(7.2) = 8th = 80
	if m.Total.P50 != 40 || m.Total.P90 != 80 {
		t.Errorf("total = %+v", m.Total)
	}
	if m.Embedding.P50 != 20 || m.Embedding.P90 != 40 || m.Search.P50 != 10 || m.Search.P90 != 20 {
		t.Errorf("embedding %+v search %+v", m.Embedding, m.Search)
	}

	// No NO_MATCH answer at all: precision is undefined, not 0 or 1.
	m = eval.Compute([]eval.Outcome{out(EN, []string{"A"}, "HIT", []string{"A"}, nil, 5), out(EN, nil, "HIT", []string{"x"}, nil, 5)})
	if !math.IsNaN(m.NoMatchPrecision) || m.NoMatchRecall != 0 || m.RecallAt3 != 1 {
		t.Errorf("no NO_MATCH answers: %+v", m)
	}
	// Nothing answerable: recall is undefined.
	if m := eval.Compute([]eval.Outcome{out(EN, nil, "NO_MATCH", nil, nil, 5)}); !math.IsNaN(m.RecallAt3) || m.NoMatchPrecision != 1 {
		t.Errorf("only uncovered: %+v", m)
	}
}

type context_err struct{}

func (context_err) Error() string { return "boom" }

func TestSweepByHand(t *testing.T) {
	EN := domain.LanguageEN
	// Recorded with the threshold disabled: top items with scores.
	outs := []eval.Outcome{
		out(EN, []string{"a"}, "HIT", []string{"a", "b", "c"}, []float64{0.9, 0.7, 0.6}, 1),
		out(EN, []string{"d"}, "HIT", []string{"e", "d", "f"}, []float64{0.8, 0.78, 0.55}, 1),
		out(EN, nil, "HIT", []string{"g", "h", "i"}, []float64{0.72, 0.6, 0.5}, 1),
		out(EN, nil, "HIT", []string{"j", "k", "l"}, []float64{0.55, 0.5, 0.5}, 1),
	}
	at := func(t float64) eval.SweepRow {
		rows := eval.Sweep(outs, []float64{t})
		return rows[0]
	}
	for _, tc := range []struct {
		t                     float64
		recall, prec, nmRecal float64 // NaN precision allowed
		answered              int
	}{
		{0.5, 1, math.NaN(), 0, 0}, // everything passes: no NO_MATCH answers
		{0.6, 1, 1, 0.5, 1},        // only the second uncovered question falls under 0.6
		{0.75, 1, 1, 1, 2},         // the first uncovered question's best is 0.72
		{0.775, 1, 1, 1, 2},        // d at 0.78 still passes
		{0.8, 0.5, 1, 1, 2},        // d at 0.78 is dropped; e at 0.80 passes (>=)
		{0.95, 0, 0.5, 1, 4},       // everything is NO_MATCH: two answerable ones are wrong
	} {
		r := at(tc.t)
		okP := near(r.NoMatchPrecision, tc.prec) || math.IsNaN(tc.prec) && math.IsNaN(r.NoMatchPrecision)
		if !near(r.RecallAt3, tc.recall) || !okP || !near(r.NoMatchRecall, tc.nmRecal) || r.NoMatchAnswered != tc.answered {
			t.Errorf("t=%.3f: %+v, want recall %v precision %v NO_MATCH recall %v answered %d", tc.t, r, tc.recall, tc.prec, tc.nmRecal, tc.answered)
		}
	}

	ts := eval.SweepThresholds()
	if len(ts) != 19 || ts[0] != 0.5 || ts[1] != 0.525 || ts[18] != 0.95 || ts[3] != 0.575 {
		t.Errorf("thresholds = %v", ts)
	}
	rows := eval.Sweep(outs, ts)
	if len(rows) != 19 {
		t.Fatalf("rows = %d", len(rows))
	}
	best, ok := eval.Best(rows, 0.9)
	if !ok || best.Threshold != 0.775 || best.RecallAt3 != 1 {
		t.Errorf("best at precision 0.9 = %+v, %v; want 0.775 (the highest threshold that keeps recall 1)", best, ok)
	}
	// With an impossible bar nothing qualifies.
	if _, ok := eval.Best([]eval.SweepRow{{Threshold: 0.9, NoMatchPrecision: 0.5}}, 0.9); ok {
		t.Error("a row below the precision bar was chosen")
	}
}

func TestReportsAreSplitByLanguageAndRenderAsTextAndJSON(t *testing.T) {
	outs := []eval.Outcome{
		out(domain.LanguageEN, []string{"a"}, "HIT", []string{"a"}, []float64{0.9}, 10),
		out(domain.LanguageEN, nil, "NO_MATCH", nil, nil, 10),
		out(domain.LanguageZH, []string{"z"}, "NO_MATCH", nil, nil, 10),
	}
	reps := eval.Reports(outs)
	if len(reps) != 3 || reps[0].Language != "EN" || reps[1].Language != "ZH" || reps[2].Language != "ALL" ||
		reps[0].Metrics.Questions != 2 || reps[1].Metrics.Questions != 1 || reps[2].Metrics.Questions != 3 {
		t.Fatalf("reports = %+v", reps)
	}
	var txt bytes.Buffer
	eval.WriteText(&txt, reps, 0.9)
	for _, want := range []string{"== EN", "== ZH", "== ALL", "recall@3", "NO_MATCH precision", "p50"} {
		if !strings.Contains(txt.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, txt.String())
		}
	}
	var js bytes.Buffer
	if err := eval.WriteJSON(&js, reps, 0.9); err != nil {
		t.Fatal(err)
	}
	var back struct {
		Reports []struct {
			Language         string   `json:"language"`
			RecallAt3        float64  `json:"recallAt3"`
			NoMatchPrecision *float64 `json:"noMatchPrecision"`
		} `json:"reports"`
	}
	if err := json.Unmarshal(js.Bytes(), &back); err != nil {
		t.Fatalf("%v\n%s", err, js.String())
	}
	if len(back.Reports) != 3 || back.Reports[0].RecallAt3 != 1 || back.Reports[0].NoMatchPrecision == nil || *back.Reports[0].NoMatchPrecision != 1 {
		t.Errorf("json = %s", js.String())
	}
	// ZH answered NO_MATCH to its only (answerable) question: precision 0; ALL 1/2.
	if back.Reports[1].NoMatchPrecision == nil || *back.Reports[1].NoMatchPrecision != 0 {
		t.Errorf("zh precision = %v", back.Reports[1].NoMatchPrecision)
	}
}
