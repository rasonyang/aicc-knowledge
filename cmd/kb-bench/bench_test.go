// SPDX-License-Identifier: Apache-2.0

package main

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPercentileNearestRank(t *testing.T) {
	s := make([]float64, 100)
	for i := range s {
		s[i] = float64(i + 1)
	}
	for p, want := range map[float64]float64{50: 50, 90: 90, 99: 99, 100: 100, 1: 1} {
		if got := Percentile(s, p); got != want {
			t.Errorf("p%v = %v, want %v", p, got, want)
		}
	}
	if got := Percentile([]float64{7}, 99); got != 7 {
		t.Errorf("single = %v", got)
	}
	if !math.IsNaN(Percentile(nil, 50)) {
		t.Error("empty must be NaN")
	}
}

func TestSummarize(t *testing.T) {
	var ds []time.Duration
	for i := 10; i >= 1; i-- {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	s := Summarize(ds)
	if s.N != 10 || s.P50 != 5 || s.P90 != 9 || s.Max != 10 || s.Mean != 5.5 {
		t.Errorf("stat = %+v", s)
	}
}

func TestWorkloadIsDeterministicAndShaped(t *testing.T) {
	a, b := Generate(1), Generate(1)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed, different workload")
	}
	if reflect.DeepEqual(a, Generate(2)) {
		t.Fatal("different seeds gave the same workload")
	}
	if len(a.Queries) != 60 || len(a.Passages) != 200 {
		t.Fatalf("queries %d, passages %d", len(a.Queries), len(a.Passages))
	}
	for _, q := range a.Queries {
		if q.Lang == "zh" {
			if n := utf8.RuneCountInString(q.Q); n < 5 || n > 40 {
				t.Errorf("zh query %q has %d chars", q.Q, n)
			}
		} else if n := len(strings.Fields(q.Q)); n < 5 || n > 25 {
			t.Errorf("en query %q has %d words", q.Q, n)
		}
	}
	for _, p := range a.Passages {
		if n := utf8.RuneCountInString(p.A); n < 20 || n > 120 {
			t.Errorf("answer %q has %d chars", p.A, n)
		}
		for _, s := range []string{p.Q, p.A} {
			if strings.Contains(s, "%s") || strings.Contains(s, "%d") || strings.Contains(s, "%!") {
				t.Errorf("unrendered template: %q", s)
			}
		}
	}
	if got := len(a.FingerprintSentences()); got != 50 {
		t.Errorf("fingerprint sentences = %d", got)
	}
	sets := a.FingerprintRerankSets()
	if len(sets) != 4 {
		t.Fatalf("rerank sets = %d", len(sets))
	}
	for _, s := range sets {
		if len(s.Passages) != 10 {
			t.Errorf("set has %d passages", len(s.Passages))
		}
	}
	if !reflect.DeepEqual(sets, b.FingerprintRerankSets()) {
		t.Error("rerank sets not deterministic")
	}
	if len(a.CandidateSet(a.Queries[0], 3, 20)) != 20 {
		t.Error("candidate set size")
	}
}

func TestDiffVectors(t *testing.T) {
	a := [][]float32{{1, 0}, {0, 2}}
	d, err := DiffVectors(a, a)
	if err != nil || d.MaxAbs != 0 || d.MeanAbs != 0 || math.Abs(d.MinCos-1) > 1e-12 {
		t.Fatalf("identical: %+v %v", d, err)
	}
	b := [][]float32{{1, 0}, {2, 0}}
	d, _ = DiffVectors(a, b)
	if d.MaxAbs != 2 || d.MeanAbs != 1 || math.Abs(d.MinCos) > 1e-12 {
		t.Errorf("diff: %+v", d)
	}
	if _, err := DiffVectors(a, a[:1]); err == nil {
		t.Error("row mismatch must fail")
	}
}

func TestDiffScores(t *testing.T) {
	d, err := DiffScores([]float64{0.1, 0.9, 0.5, 0.3}, []float64{0.12, 0.88, 0.5, 0.31})
	if err != nil || !d.Top1Match || !d.Top3Order || !d.Top3Set || math.Abs(d.MaxAbs-0.02) > 1e-9 {
		t.Errorf("same order: %+v %v", d, err)
	}
	// 0.5 and 0.3 swap: top-1 holds, top-3 order breaks, the set holds.
	d, _ = DiffScores([]float64{0.1, 0.9, 0.5, 0.3}, []float64{0.1, 0.9, 0.3, 0.5})
	if !d.Top1Match || d.Top3Order || !d.Top3Set {
		t.Errorf("swap inside top-3 tail: %+v", d)
	}
	d, _ = DiffScores([]float64{0.1, 0.9, 0.5, 0.3}, []float64{0.1, 0.5, 0.9, 0.3})
	if d.Top1Match || d.Top3Order || !d.Top3Set {
		t.Errorf("top-2 swap: %+v", d)
	}
	d, _ = DiffScores([]float64{0.1, 0.9, 0.5, 0.3}, []float64{0.6, 0.9, 0.5, 0.3})
	if !d.Top1Match || d.Top3Set {
		t.Errorf("set change: %+v", d)
	}
}

func TestVectorsRoundTripAndCompare(t *testing.T) {
	dir := t.TempDir()
	vs := [][]float32{{1, 2, 3}, {-1.5, 0, 0.25}}
	p := filepath.Join(dir, "v.f32")
	if err := WriteVectors(p, vs); err != nil {
		t.Fatal(err)
	}
	got, err := ReadVectors(p, 2, 3)
	if err != nil || !reflect.DeepEqual(got, vs) {
		t.Fatalf("round trip: %v %v", got, err)
	}
	if _, err := ReadVectors(p, 3, 3); err == nil {
		t.Error("size mismatch must fail")
	}

	write := func(d string, shift float32) {
		if err := writeJSON(filepath.Join(d, "host.json"), Host{Hostname: "h", Workload: WorkloadVersion}); err != nil {
			t.Fatal(err)
		}
		v := [][]float32{{1, 2, 3 + shift}}
		if err := WriteVectors(filepath.Join(d, "embed-m.f32"), v); err != nil {
			t.Fatal(err)
		}
		r := &PhaseResult{Kind: "embed", Model: "m", VectorsFile: "embed-m.f32", Count: 1, Dim: 3}
		if err := writeJSON(phasePath(d, r), r); err != nil {
			t.Fatal(err)
		}
		rr := &PhaseResult{Kind: "rerank", Model: "r", Rerank: []RerankFPResult{{Query: "q", Scores: []float64{0.2, 0.8 + float64(shift)}}}}
		if err := writeJSON(phasePath(d, rr), rr); err != nil {
			t.Fatal(err)
		}
	}
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	mk := func(d string, s float32) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		write(d, s)
	}
	mk(a, 0)
	mk(b, 0.5)
	out, err := Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"max |Δ| 5.000e-01", "embed m", "rerank r", "| 1 | q |"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestParseCPUInfo(t *testing.T) {
	text := "processor\t: 0\nmodel name\t: Test CPU 9000\nflags\t\t: fpu sse2 avx avx2 fma avx512f avx512_vnni amx_tile foo\n\nprocessor\t: 1\nmodel name\t: Test CPU 9000\nflags\t\t: fpu avx2\n"
	model, cores, flags := ParseCPUInfo(text)
	want := []string{"amx_tile", "avx", "avx2", "avx512_vnni", "avx512f", "fma"}
	if model != "Test CPU 9000" || cores != 2 || !reflect.DeepEqual(flags, want) {
		t.Errorf("got %q %d %v", model, cores, flags)
	}
}
