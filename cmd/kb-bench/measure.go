// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MeasureOpts configures one measurement phase.
type MeasureOpts struct {
	Kind        string // "embed" or "rerank"
	URL         string
	Model       string
	Image       string
	OutDir      string
	StartupSec  float64
	N           int   // sample size per latency series
	ConcN       int   // requests per concurrency level
	Concurrency []int // e.g. 2, 4
}

func progress(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

// timeSeries calls fn n times sequentially and returns each latency.
func timeSeries(n int, fn func(i int) error) ([]time.Duration, error) {
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		t := time.Now()
		if err := fn(i); err != nil {
			return nil, err
		}
		out = append(out, time.Since(t))
	}
	return out, nil
}

// timeConcurrent runs n calls over w workers and returns latencies and the
// wall time of the whole batch.
func timeConcurrent(w, n int, fn func(i int) error) ([]time.Duration, time.Duration, error) {
	var mu sync.Mutex
	var lat []time.Duration
	var firstErr error
	next := 0
	var wg sync.WaitGroup
	start := time.Now()
	for k := 0; k < w; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= n {
					return
				}
				t := time.Now()
				err := fn(i)
				d := time.Since(t)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				lat = append(lat, d)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return lat, time.Since(start), firstErr
}

// Measure runs one phase and writes <kind>-<model>.json (and the vectors).
func Measure(ctx context.Context, o MeasureOpts) (*PhaseResult, error) {
	w := Generate(1)
	c := NewTEI(o.URL)
	res := &PhaseResult{Kind: o.Kind, Model: o.Model, Image: o.Image, Workload: WorkloadVersion,
		StartupSec: o.StartupSec, Stats: map[string]Stat{}, Throughput: map[string]float64{}}
	var err error
	switch o.Kind {
	case "embed":
		err = measureEmbed(ctx, c, w, o, res)
	case "rerank":
		err = measureRerank(ctx, c, w, o, res)
	default:
		err = fmt.Errorf("unknown kind %q", o.Kind)
	}
	if err != nil {
		return nil, err
	}
	if err := writeJSON(phasePath(o.OutDir, res), res); err != nil {
		return nil, err
	}
	return res, nil
}

func measureEmbed(ctx context.Context, c *TEI, w *Workload, o MeasureOpts, res *PhaseResult) error {
	q := func(i int) string { return w.Queries[i%len(w.Queries)].Q }
	progress("embed: warm-up")
	t0 := time.Now()
	for i := 0; i < 12; i++ {
		if _, err := c.Embed(ctx, []string{q(i)}); err != nil {
			return err
		}
	}
	if _, err := c.Embed(ctx, []string{q(0), q(1), q(2), q(3), q(4), q(5), q(6), q(7)}); err != nil {
		return err
	}
	res.WarmupSec = time.Since(t0).Seconds()

	progress("embed: fingerprint")
	sents := w.FingerprintSentences()
	// TEI caps a client batch at 32 inputs by default, so send chunks of 10.
	var vecs [][]float32
	for i := 0; i < len(sents); i += 10 {
		v, err := c.Embed(ctx, sents[i:min(i+10, len(sents))])
		if err != nil {
			return err
		}
		vecs = append(vecs, v...)
	}
	res.Count, res.Dim = len(vecs), len(vecs[0])
	res.VectorsFile = "embed-" + slug(o.Model) + ".f32"
	if err := WriteVectors(filepath.Join(o.OutDir, res.VectorsFile), vecs); err != nil {
		return err
	}

	progress("embed: single query x%d", o.N)
	d, err := timeSeries(o.N, func(i int) error { _, e := c.Embed(ctx, []string{q(i)}); return e })
	if err != nil {
		return err
	}
	res.Stats["embedSingle"] = Summarize(d)

	nb := max(o.N/2, 1)
	progress("embed: batch of 8 x%d", nb)
	d, err = timeSeries(nb, func(i int) error {
		b := make([]string, 8)
		for k := range b {
			b[k] = q(i*8 + k)
		}
		_, e := c.Embed(ctx, b)
		return e
	})
	if err != nil {
		return err
	}
	res.Stats["embedBatch8"] = Summarize(d)

	for _, cc := range o.Concurrency {
		progress("embed: single query, concurrency %d x%d", cc, o.ConcN)
		lat, wall, err := timeConcurrent(cc, o.ConcN, func(i int) error { _, e := c.Embed(ctx, []string{q(i)}); return e })
		if err != nil {
			return err
		}
		key := fmt.Sprintf("embedSingleC%d", cc)
		res.Stats[key] = Summarize(lat)
		res.Throughput[key] = float64(len(lat)) / wall.Seconds()
	}
	return nil
}

func measureRerank(ctx context.Context, c *TEI, w *Workload, o MeasureOpts, res *PhaseResult) error {
	call := func(i, n int, variant string) error {
		qi := w.Queries[i%len(w.Queries)]
		var texts []string
		for _, p := range w.CandidateSet(qi, i, n) {
			texts = append(texts, p.Text(variant))
		}
		_, e := c.Rerank(ctx, qi.Q, texts)
		return e
	}
	progress("rerank %s: warm-up", o.Model)
	t0 := time.Now()
	for i := 0; i < 3; i++ {
		if err := call(i, 10, "qa"); err != nil {
			return err
		}
	}
	res.WarmupSec = time.Since(t0).Seconds()

	progress("rerank: fingerprint")
	for _, fp := range w.FingerprintRerankSets() {
		s, err := c.Rerank(ctx, fp.Query, fp.Passages)
		if err != nil {
			return err
		}
		res.Rerank = append(res.Rerank, RerankFPResult{Query: fp.Query, Scores: s})
	}

	for _, n := range []int{10, 20} {
		for _, v := range []string{"q", "qa"} {
			progress("rerank: N=%d variant=%s x%d", n, v, o.N)
			d, err := timeSeries(o.N, func(i int) error { return call(i, n, v) })
			if err != nil {
				return err
			}
			res.Stats[fmt.Sprintf("rerankN%d_%s", n, v)] = Summarize(d)
		}
	}
	for _, cc := range o.Concurrency {
		progress("rerank: N=10 qa, concurrency %d x%d", cc, o.ConcN)
		lat, wall, err := timeConcurrent(cc, o.ConcN, func(i int) error { return call(i, 10, "qa") })
		if err != nil {
			return err
		}
		key := fmt.Sprintf("rerankN10_qa_C%d", cc)
		res.Stats[key] = Summarize(lat)
		res.Throughput[key] = float64(len(lat)) / wall.Seconds()
	}
	return nil
}
