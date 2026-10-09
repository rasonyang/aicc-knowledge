// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

// VectorDiff compares two equally shaped vector sets.
type VectorDiff struct {
	MaxAbs, MeanAbs, MinCos float64
}

// DiffVectors reports the max and mean absolute element difference and the
// smallest cosine similarity between corresponding rows.
func DiffVectors(a, b [][]float32) (VectorDiff, error) {
	if len(a) != len(b) {
		return VectorDiff{}, fmt.Errorf("row count %d vs %d", len(a), len(b))
	}
	d := VectorDiff{MinCos: 1}
	var sum float64
	var n int
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return VectorDiff{}, fmt.Errorf("row %d: dim %d vs %d", i, len(a[i]), len(b[i]))
		}
		var dot, na, nb float64
		for j := range a[i] {
			x, y := float64(a[i][j]), float64(b[i][j])
			ad := math.Abs(x - y)
			d.MaxAbs = math.Max(d.MaxAbs, ad)
			sum += ad
			n++
			dot += x * y
			na += x * x
			nb += y * y
		}
		if na > 0 && nb > 0 {
			d.MinCos = math.Min(d.MinCos, dot/(math.Sqrt(na)*math.Sqrt(nb)))
		}
	}
	if n > 0 {
		d.MeanAbs = sum / float64(n)
	}
	return d, nil
}

// ScoreDiff compares the scores of one fixed rerank set.
type ScoreDiff struct {
	MaxAbs    float64
	Top1Match bool
	Top3Order bool // the top three indexes are equal in the same order
	Top3Set   bool // the top three indexes are equal as a set
}

// order returns the indexes sorted by descending score (ties by index).
func order(s []float64) []int {
	idx := make([]int, len(s))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(x, y int) bool { return s[idx[x]] > s[idx[y]] })
	return idx
}

// DiffScores compares two score lists of the same passages.
func DiffScores(a, b []float64) (ScoreDiff, error) {
	if len(a) != len(b) || len(a) == 0 {
		return ScoreDiff{}, fmt.Errorf("score count %d vs %d", len(a), len(b))
	}
	var d ScoreDiff
	for i := range a {
		d.MaxAbs = math.Max(d.MaxAbs, math.Abs(a[i]-b[i]))
	}
	oa, ob := order(a), order(b)
	d.Top1Match = oa[0] == ob[0]
	k := min(3, len(a))
	d.Top3Order = true
	set := map[int]bool{}
	for i := 0; i < k; i++ {
		if oa[i] != ob[i] {
			d.Top3Order = false
		}
		set[oa[i]] = true
	}
	d.Top3Set = true
	for i := 0; i < k; i++ {
		if !set[ob[i]] {
			d.Top3Set = false
		}
	}
	return d, nil
}

// Compare loads two result directories and renders a Markdown report.
func Compare(dirA, dirB string) (string, error) {
	var ha, hb Host
	if err := readJSON(filepath.Join(dirA, "host.json"), &ha); err != nil {
		return "", err
	}
	if err := readJSON(filepath.Join(dirB, "host.json"), &hb); err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Numeric comparison\n\n- A: `%s` (%s, %s)\n- B: `%s` (%s, %s)\n\n", dirA, ha.UnameM, ha.CPUModel, dirB, hb.UnameM, hb.CPUModel)
	if ha.Workload != hb.Workload {
		fmt.Fprintf(&sb, "**Warning:** workload versions differ (%s vs %s); the fixed inputs are not the same.\n\n", ha.Workload, hb.Workload)
	}
	found := false
	for _, kind := range []string{"embed", "rerank"} {
		files, _ := filepath.Glob(filepath.Join(dirA, kind+"-*.json"))
		sort.Strings(files)
		for _, fa := range files {
			fb := filepath.Join(dirB, filepath.Base(fa))
			var ra, rb PhaseResult
			if err := readJSON(fa, &ra); err != nil {
				return "", err
			}
			if err := readJSON(fb, &rb); err != nil {
				fmt.Fprintf(&sb, "- `%s`: missing in B, skipped\n", filepath.Base(fa))
				continue
			}
			found = true
			fmt.Fprintf(&sb, "## %s %s\n\n", kind, ra.Model)
			if kind == "embed" {
				va, err := ReadVectors(filepath.Join(dirA, ra.VectorsFile), ra.Count, ra.Dim)
				if err != nil {
					return "", err
				}
				vb, err := ReadVectors(filepath.Join(dirB, rb.VectorsFile), rb.Count, rb.Dim)
				if err != nil {
					return "", err
				}
				d, err := DiffVectors(va, vb)
				if err != nil {
					return "", err
				}
				fmt.Fprintf(&sb, "%d vectors x %d dims: max |Δ| %.3e, mean |Δ| %.3e, min cosine %.8f\n\n", ra.Count, ra.Dim, d.MaxAbs, d.MeanAbs, d.MinCos)
				continue
			}
			if len(ra.Rerank) != len(rb.Rerank) {
				return "", fmt.Errorf("%s: %d vs %d rerank sets", ra.Model, len(ra.Rerank), len(rb.Rerank))
			}
			sb.WriteString("| set | query | max abs score Δ | top-1 | top-3 order | top-3 set |\n|---|---|---|---|---|---|\n")
			for i := range ra.Rerank {
				d, err := DiffScores(ra.Rerank[i].Scores, rb.Rerank[i].Scores)
				if err != nil {
					return "", err
				}
				fmt.Fprintf(&sb, "| %d | %s | %.3e | %v | %v | %v |\n", i+1, ra.Rerank[i].Query, d.MaxAbs, d.Top1Match, d.Top3Order, d.Top3Set)
			}
			sb.WriteString("\n")
		}
	}
	if !found {
		return "", fmt.Errorf("no common embed-*.json or rerank-*.json in %s and %s", dirA, dirB)
	}
	return sb.String(), nil
}
