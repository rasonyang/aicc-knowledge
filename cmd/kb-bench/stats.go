// SPDX-License-Identifier: Apache-2.0

package main

import (
	"math"
	"sort"
	"time"
)

// Stat summarizes latencies in milliseconds.
type Stat struct {
	N    int     `json:"n"`
	P50  float64 `json:"p50Ms"`
	P90  float64 `json:"p90Ms"`
	P99  float64 `json:"p99Ms"`
	Max  float64 `json:"maxMs"`
	Mean float64 `json:"meanMs"`
}

// Percentile is the nearest-rank percentile of an ascending slice, p in (0,100].
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// Summarize computes a Stat from durations.
func Summarize(ds []time.Duration) Stat {
	ms := make([]float64, len(ds))
	var sum float64
	for i, d := range ds {
		ms[i] = float64(d) / float64(time.Millisecond)
		sum += ms[i]
	}
	sort.Float64s(ms)
	if len(ms) == 0 {
		return Stat{}
	}
	return Stat{
		N: len(ms), P50: Percentile(ms, 50), P90: Percentile(ms, 90),
		P99: Percentile(ms, 99), Max: ms[len(ms)-1], Mean: sum / float64(len(ms)),
	}
}
