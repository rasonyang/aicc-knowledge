// SPDX-License-Identifier: Apache-2.0

// Package eval measures the search service against a hand-written question
// list: recall@3, NO_MATCH precision and recall, and per-stage latency. It also
// sweeps the NO_MATCH threshold so an operator can calibrate
// KB_SEARCH_THRESHOLD_EN and KB_SEARCH_THRESHOLD_ZH on their own data.
//
// The input is a CSV with a header and the columns
//
//	question,language,expectedIds,scope
//
// expectedIds are candidate ids separated by ";" (any of them is a correct
// answer); an empty cell means the question is not covered and the right
// answer is NO_MATCH. scope is optional: "brand=acme;channel=web".
package eval

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

// TopK is the cut-off of recall@K: a hit counts when an expected id is in the
// first TopK results.
const TopK = 3

// Question is one row of the input.
type Question struct {
	Line     int // 1-based CSV line, for messages
	Text     string
	Language domain.Language
	Expected []string // empty: the question must yield NO_MATCH
	Scope    map[string]string
}

// Answerable reports whether the question has expected ids.
func (q Question) Answerable() bool { return len(q.Expected) > 0 }

// ReadCSV parses the question file.
func ReadCSV(r io.Reader) ([]Question, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	var rows [][]string
	var lines []int // physical line of each row (csv skips blank lines)
	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV: %w", err)
		}
		line, _ := cr.FieldPos(0)
		rows, lines = append(rows, row), append(lines, line)
	}
	if len(rows) == 0 {
		return nil, errors.New("the CSV is empty; it needs a header row question,language,expectedIds,scope")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimPrefix(strings.TrimSpace(h), string(rune(0xFEFF)))] = i
	}
	for _, need := range []string{"question", "language", "expectedIds"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("the CSV header lacks the column %q (columns: question,language,expectedIds,scope)", need)
		}
	}
	get := func(row []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	var out []Question
	for n, row := range rows[1:] {
		line := lines[n+1]
		if len(row) == 0 || len(row) == 1 && strings.TrimSpace(row[0]) == "" {
			continue
		}
		q := Question{Line: line, Text: get(row, "question")}
		if q.Text == "" {
			return nil, fmt.Errorf("line %d: question is empty", line)
		}
		q.Language = domain.Language(strings.ToUpper(get(row, "language")))
		if !q.Language.Valid() {
			return nil, fmt.Errorf("line %d: language must be EN or ZH, got %q", line, get(row, "language"))
		}
		for _, id := range strings.Split(get(row, "expectedIds"), ";") {
			if id = strings.TrimSpace(id); id != "" {
				q.Expected = append(q.Expected, id)
			}
		}
		if sc := get(row, "scope"); sc != "" {
			q.Scope = map[string]string{}
			for _, kv := range strings.Split(sc, ";") {
				if kv = strings.TrimSpace(kv); kv == "" {
					continue
				}
				k, v, ok := strings.Cut(kv, "=")
				if !ok || strings.TrimSpace(k) == "" {
					return nil, fmt.Errorf("line %d: scope %q must look like k=v;k2=v2", line, sc)
				}
				q.Scope[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, errors.New("the CSV has a header but no questions")
	}
	return out, nil
}

// Outcome is what the service answered to one question.
type Outcome struct {
	Question Question
	// Status is HIT or NO_MATCH ("" when Err is set).
	Status string
	// IDs and Scores are the returned items, best first. In a sweep run they
	// are the top items with the threshold disabled.
	IDs     []string
	Scores  []float64
	Latency search.Latency
	Err     error
}

// Runner asks the service one question.
type Runner interface {
	Run(ctx context.Context, q Question, threshold *float64) Outcome
}

// InProcess runs questions through the same Searcher the HTTP handler uses.
type InProcess struct {
	Searcher *search.Searcher
	// Timeout is the budget of one question (0: none).
	Timeout time.Duration
}

// Run implements Runner.
func (p InProcess) Run(ctx context.Context, q Question, threshold *float64) Outcome {
	o := Outcome{Question: q}
	r, err := p.Searcher.Search(ctx, search.Request{Query: q.Text, Language: q.Language, Scope: q.Scope, TopK: TopK,
		Timeout: p.Timeout, Threshold: threshold})
	o.Latency = r.Latency
	if err != nil {
		o.Err = err
		return o
	}
	o.Status = string(r.Status)
	for _, it := range r.Items {
		o.IDs = append(o.IDs, it.ID)
		o.Scores = append(o.Scores, it.Score)
	}
	return o
}

// Metrics are the quality and latency numbers of a set of outcomes.
type Metrics struct {
	Questions  int
	Answerable int
	// Unanswerable counts questions that expect NO_MATCH.
	Unanswerable int
	Errors       int

	// RecallAt3 is the fraction of answerable questions with an expected id
	// among the first three results. NO_MATCH and errors are misses.
	RecallAt3  float64
	RecallHits int
	// NoMatchPrecision is, of the questions the service answered NO_MATCH, the
	// fraction that expected NO_MATCH. NaN when it answered NO_MATCH to none.
	NoMatchPrecision float64
	NoMatchAnswered  int
	NoMatchCorrect   int
	// NoMatchRecall is the fraction of unanswerable questions answered NO_MATCH.
	NoMatchRecall float64

	// Latency percentiles over the questions that did not fail.
	Embedding, Search, Total Percentiles
}

// Percentiles are in milliseconds.
type Percentiles struct{ P50, P90 float64 }

// Compute evaluates outcomes.
func Compute(outs []Outcome) Metrics {
	var m Metrics
	var emb, srch, tot []float64
	for _, o := range outs {
		m.Questions++
		if o.Question.Answerable() {
			m.Answerable++
		} else {
			m.Unanswerable++
		}
		if o.Err != nil {
			m.Errors++
			continue
		}
		emb = append(emb, ms(o.Latency.Embedding))
		srch = append(srch, ms(o.Latency.Search))
		tot = append(tot, ms(o.Latency.Total))
		if o.Status == string(search.StatusNoMatch) {
			m.NoMatchAnswered++
			if !o.Question.Answerable() {
				m.NoMatchCorrect++
			}
		}
		if o.Question.Answerable() && hitsExpected(o) {
			m.RecallHits++
		}
	}
	m.RecallAt3 = ratio(m.RecallHits, m.Answerable)
	m.NoMatchPrecision = ratio(m.NoMatchCorrect, m.NoMatchAnswered)
	m.NoMatchRecall = ratio(m.NoMatchCorrect, m.Unanswerable)
	m.Embedding, m.Search, m.Total = percentiles(emb), percentiles(srch), percentiles(tot)
	return m
}

func hitsExpected(o Outcome) bool {
	if o.Status != string(search.StatusHit) {
		return false
	}
	for _, id := range o.IDs[:min(len(o.IDs), TopK)] {
		if slices.Contains(o.Question.Expected, id) {
			return true
		}
	}
	return false
}

func ratio(a, b int) float64 {
	if b == 0 {
		return math.NaN()
	}
	return float64(a) / float64(b)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// percentiles uses the nearest-rank method: the smallest value with at least
// p percent of the sample at or below it.
func percentiles(xs []float64) Percentiles {
	if len(xs) == 0 {
		return Percentiles{math.NaN(), math.NaN()}
	}
	s := slices.Clone(xs)
	sort.Float64s(s)
	at := func(p float64) float64 { return s[max(int(math.Ceil(p*float64(len(s))))-1, 0)] }
	return Percentiles{P50: at(0.5), P90: at(0.9)}
}

// WithThreshold re-decides outcomes recorded with the threshold disabled, as if
// the service had run at threshold t: items scoring below t are dropped, and
// the status is HIT when any remain. Errors are kept as they are.
func WithThreshold(outs []Outcome, t float64) []Outcome {
	res := make([]Outcome, len(outs))
	for i, o := range outs {
		res[i] = o
		if o.Err != nil {
			continue
		}
		res[i].IDs, res[i].Scores = nil, nil
		for j, s := range o.Scores {
			if s >= t {
				res[i].IDs = append(res[i].IDs, o.IDs[j])
				res[i].Scores = append(res[i].Scores, s)
			}
		}
		res[i].Status = string(search.StatusNoMatch)
		if len(res[i].IDs) > 0 {
			res[i].Status = string(search.StatusHit)
		}
	}
	return res
}

// SweepRow is the quality at one threshold.
type SweepRow struct {
	Threshold        float64
	RecallAt3        float64
	NoMatchPrecision float64
	NoMatchRecall    float64
	NoMatchAnswered  int
}

// SweepThresholds are 0.500 to 0.950 in steps of 0.025.
func SweepThresholds() []float64 {
	var ts []float64
	for i := 0; i <= 18; i++ {
		ts = append(ts, math.Round((0.5+0.025*float64(i))*1000)/1000)
	}
	return ts
}

// Sweep computes the metrics of outs at each threshold.
func Sweep(outs []Outcome, thresholds []float64) []SweepRow {
	rows := make([]SweepRow, 0, len(thresholds))
	for _, t := range thresholds {
		m := Compute(WithThreshold(outs, t))
		rows = append(rows, SweepRow{Threshold: t, RecallAt3: m.RecallAt3, NoMatchPrecision: m.NoMatchPrecision,
			NoMatchRecall: m.NoMatchRecall, NoMatchAnswered: m.NoMatchAnswered})
	}
	return rows
}

// Best picks the sweep row with the highest recall@3 among those whose NO_MATCH
// precision is at least minPrecision (a threshold that answers NO_MATCH to
// nothing has an undefined precision and qualifies only when minPrecision is
// 0). Ties go to the higher threshold, the more cautious choice. ok is false
// when no row qualifies.
func Best(rows []SweepRow, minPrecision float64) (SweepRow, bool) {
	var best SweepRow
	found := false
	for _, r := range rows {
		if math.IsNaN(r.NoMatchPrecision) && minPrecision > 0 || r.NoMatchPrecision < minPrecision {
			continue
		}
		if !found || r.RecallAt3 > best.RecallAt3 || r.RecallAt3 == best.RecallAt3 && r.Threshold > best.Threshold {
			best, found = r, true
		}
	}
	return best, found
}
