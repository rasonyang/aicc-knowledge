// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

// Report is the metrics of one language, or of all questions (Language "ALL").
type Report struct {
	Language string
	Metrics  Metrics
	// Sweep is set by RunSweep.
	Sweep []SweepRow
	// Best is the sweep row chosen by Best at the requested minimum precision.
	Best *SweepRow
	// Guard is what the product guard changed over the run (a non-sweep run),
	// and Catalog says whether a catalog applied to any question.
	Guard   search.Guard
	Catalog bool
}

// Run asks every question once and returns the outcomes in input order.
func Run(ctx context.Context, r Runner, qs []Question, threshold *float64) []Outcome {
	outs := make([]Outcome, 0, len(qs))
	for _, q := range qs {
		outs = append(outs, r.Run(ctx, q, threshold))
	}
	return outs
}

// Reports splits outcomes by language and adds the overall report when both
// languages are present.
func Reports(outs []Outcome) []Report {
	var reps []Report
	langs := []domain.Language{domain.LanguageEN, domain.LanguageZH}
	present := 0
	for _, l := range langs {
		var sub []Outcome
		for _, o := range outs {
			if o.Question.Language == l {
				sub = append(sub, o)
			}
		}
		if len(sub) > 0 {
			present++
			reps = append(reps, Report{Language: string(l), Metrics: Compute(sub), Guard: GuardTotal(sub), Catalog: HasCatalog(sub)})
		}
	}
	if present > 1 {
		reps = append(reps, Report{Language: "ALL", Metrics: Compute(outs), Guard: GuardTotal(outs), Catalog: HasCatalog(outs)})
	}
	return reps
}

// SweepReports computes, per language, the sweep over outcomes recorded with
// the threshold disabled, and the best row at minPrecision.
func SweepReports(outs []Outcome, minPrecision float64) []Report {
	reps := Reports(outs)
	for i := range reps {
		sub := outs
		if reps[i].Language != "ALL" {
			sub = nil
			for _, o := range outs {
				if string(o.Question.Language) == reps[i].Language {
					sub = append(sub, o)
				}
			}
		}
		margins := []float64{0}
		if reps[i].Catalog {
			margins = SweepMargins()
		}
		reps[i].Sweep = SweepGrid(sub, SweepThresholds(), margins)
		if b, ok := Best(reps[i].Sweep, minPrecision); ok {
			reps[i].Best = &b
		}
	}
	return reps
}

func pct(x float64) string {
	if math.IsNaN(x) {
		return "n/a"
	}
	return fmt.Sprintf("%.3f", x)
}

// WriteText prints reports for a human.
func WriteText(w io.Writer, reps []Report, minPrecision float64) {
	for _, r := range reps {
		m := r.Metrics
		fmt.Fprintf(w, "== %s: %d questions (%d answerable, %d expect NO_MATCH, %d errors)\n", r.Language, m.Questions, m.Answerable, m.Unanswerable, m.Errors)
		fmt.Fprintf(w, "recall@3            %s  (%d of %d answerable)\n", pct(m.RecallAt3), m.RecallHits, m.Answerable)
		fmt.Fprintf(w, "NO_MATCH precision  %s  (%d of %d answered NO_MATCH were right)\n", pct(m.NoMatchPrecision), m.NoMatchCorrect, m.NoMatchAnswered)
		fmt.Fprintf(w, "NO_MATCH recall     %s  (%d of %d uncovered questions)\n", pct(m.NoMatchRecall), m.NoMatchCorrect, m.Unanswerable)
		fmt.Fprintf(w, "latency ms          p50    p90\n")
		fmt.Fprintf(w, "  embedding        %6.1f %6.1f\n  search           %6.1f %6.1f\n  total            %6.1f %6.1f\n",
			m.Embedding.P50, m.Embedding.P90, m.Search.P50, m.Search.P90, m.Total.P50, m.Total.P90)
		if r.Catalog && len(r.Sweep) == 0 {
			fmt.Fprintf(w, "product guard       dropped_disjoint %d, unknown_model %d, generic_below_margin %d\n",
				r.Guard.DroppedDisjoint, r.Guard.UnknownModel, r.Guard.GenericBelowMargin)
		}
		if len(r.Sweep) > 0 {
			fmt.Fprintf(w, "threshold sweep")
			if r.Catalog {
				fmt.Fprintf(w, " (product catalog live: generic margin swept; guard counts are dropped_disjoint/unknown_model/generic_below_margin)")
			}
			fmt.Fprintf(w, "\n  threshold  margin  recall@3  NO_MATCH-precision  NO_MATCH-recall  answered-NO_MATCH  guard\n")
			for _, s := range r.Sweep {
				mark := ""
				if r.Best != nil && s.Threshold == r.Best.Threshold && s.Margin == r.Best.Margin {
					mark = "  <- best at precision >= " + fmt.Sprintf("%.2f", minPrecision)
				}
				guard := ""
				if r.Catalog {
					guard = fmt.Sprintf("%d/%d/%d", s.Guard.DroppedDisjoint, s.Guard.UnknownModel, s.Guard.GenericBelowMargin)
				}
				fmt.Fprintf(w, "  %.3f      %.2f    %s     %s               %s            %d              %s%s\n", s.Threshold, s.Margin, pct(s.RecallAt3), pct(s.NoMatchPrecision), pct(s.NoMatchRecall), s.NoMatchAnswered, guard, mark)
			}
			if r.Best == nil {
				fmt.Fprintf(w, "  no threshold reaches NO_MATCH precision %.2f\n", minPrecision)
			}
		}
		fmt.Fprintln(w)
	}
}

type jsonNum float64

func (n jsonNum) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(n)) {
		return []byte("null"), nil
	}
	return json.Marshal(math.Round(float64(n)*1e6) / 1e6)
}

// WriteJSON prints reports as JSON (NaN becomes null).
func WriteJSON(w io.Writer, reps []Report, minPrecision float64) error {
	type lat struct {
		P50 jsonNum `json:"p50"`
		P90 jsonNum `json:"p90"`
	}
	type guard struct {
		DroppedDisjoint    int `json:"droppedDisjoint"`
		UnknownModel       int `json:"unknownModel"`
		GenericBelowMargin int `json:"genericBelowMargin"`
	}
	type row struct {
		Threshold        jsonNum `json:"threshold"`
		GenericMargin    jsonNum `json:"genericMargin"`
		Guard            *guard  `json:"guard,omitempty"`
		RecallAt3        jsonNum `json:"recallAt3"`
		NoMatchPrecision jsonNum `json:"noMatchPrecision"`
		NoMatchRecall    jsonNum `json:"noMatchRecall"`
		NoMatchAnswered  int     `json:"noMatchAnswered"`
	}
	type rep struct {
		Language         string  `json:"language"`
		Questions        int     `json:"questions"`
		Answerable       int     `json:"answerable"`
		Unanswerable     int     `json:"unanswerable"`
		Errors           int     `json:"errors"`
		RecallAt3        jsonNum `json:"recallAt3"`
		NoMatchPrecision jsonNum `json:"noMatchPrecision"`
		NoMatchRecall    jsonNum `json:"noMatchRecall"`
		NoMatchAnswered  int     `json:"noMatchAnswered"`
		LatencyMs        struct {
			Embedding lat `json:"embedding"`
			Search    lat `json:"search"`
			Total     lat `json:"total"`
		} `json:"latencyMs"`
		Catalog bool   `json:"productCatalog"`
		Guard   *guard `json:"guard,omitempty"`
		Sweep   []row  `json:"sweep,omitempty"`
		Best    *row   `json:"best,omitempty"`
	}
	toGuard := func(g search.Guard) *guard {
		return &guard{g.DroppedDisjoint, g.UnknownModel, g.GenericBelowMargin}
	}
	toRow := func(s SweepRow, catalog bool) row {
		r := row{Threshold: jsonNum(s.Threshold), GenericMargin: jsonNum(s.Margin), RecallAt3: jsonNum(s.RecallAt3),
			NoMatchPrecision: jsonNum(s.NoMatchPrecision), NoMatchRecall: jsonNum(s.NoMatchRecall), NoMatchAnswered: s.NoMatchAnswered}
		if catalog {
			r.Guard = toGuard(s.Guard)
		}
		return r
	}
	var out struct {
		MinPrecision jsonNum `json:"minNoMatchPrecision,omitempty"`
		Reports      []rep   `json:"reports"`
	}
	out.MinPrecision = jsonNum(minPrecision)
	for _, r := range reps {
		m := r.Metrics
		x := rep{Language: r.Language, Questions: m.Questions, Answerable: m.Answerable, Unanswerable: m.Unanswerable, Errors: m.Errors,
			RecallAt3: jsonNum(m.RecallAt3), NoMatchPrecision: jsonNum(m.NoMatchPrecision), NoMatchRecall: jsonNum(m.NoMatchRecall), NoMatchAnswered: m.NoMatchAnswered}
		x.LatencyMs.Embedding = lat{jsonNum(m.Embedding.P50), jsonNum(m.Embedding.P90)}
		x.LatencyMs.Search = lat{jsonNum(m.Search.P50), jsonNum(m.Search.P90)}
		x.LatencyMs.Total = lat{jsonNum(m.Total.P50), jsonNum(m.Total.P90)}
		x.Catalog = r.Catalog
		if r.Catalog && len(r.Sweep) == 0 {
			x.Guard = toGuard(r.Guard)
		}
		for _, s := range r.Sweep {
			x.Sweep = append(x.Sweep, toRow(s, r.Catalog))
		}
		if r.Best != nil {
			b := toRow(*r.Best, r.Catalog)
			x.Best = &b
		}
		out.Reports = append(out.Reports, x)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
