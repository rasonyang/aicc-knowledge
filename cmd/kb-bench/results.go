// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// PhaseResult is the measurement of one model (one TEI container).
type PhaseResult struct {
	Kind       string             `json:"kind"` // "embed" or "rerank"
	Model      string             `json:"model"`
	Image      string             `json:"image,omitempty"`
	Workload   string             `json:"workload"`
	StartupSec float64            `json:"startupSec"` // container start until /info answers
	WarmupSec  float64            `json:"warmupSec"`
	Stats      map[string]Stat    `json:"stats"`
	Throughput map[string]float64 `json:"throughputReqPerSec,omitempty"`
	// Embed fingerprint: raw little-endian float32, Count x Dim, in VectorsFile.
	VectorsFile string `json:"vectorsFile,omitempty"`
	Count       int    `json:"count,omitempty"`
	Dim         int    `json:"dim,omitempty"`
	// Rerank fingerprint: one score list per fixed set.
	Rerank []RerankFPResult `json:"rerankFingerprint,omitempty"`
}

// RerankFPResult holds the raw scores of one fixed (query, passages) set.
type RerankFPResult struct {
	Query  string    `json:"query"`
	Scores []float64 `json:"scores"`
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteVectors stores vectors as little-endian float32, row after row.
func WriteVectors(path string, vs [][]float32) error {
	var buf []byte
	for _, v := range vs {
		for _, x := range v {
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(x))
		}
	}
	return os.WriteFile(path, buf, 0o644)
}

// ReadVectors is the inverse of WriteVectors.
func ReadVectors(path string, count, dim int) ([][]float32, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != count*dim*4 {
		return nil, fmt.Errorf("%s: %d bytes, want %d x %d x 4", path, len(b), count, dim)
	}
	out := make([][]float32, count)
	for i := range out {
		out[i] = make([]float32, dim)
		for j := range out[i] {
			out[i][j] = math.Float32frombits(binary.LittleEndian.Uint32(b[(i*dim+j)*4:]))
		}
	}
	return out, nil
}

// slug turns a model id into a file-name part.
func slug(model string) string {
	b := []byte(model)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			b[i] = '_'
		}
	}
	return string(b)
}

func phasePath(dir string, r *PhaseResult) string {
	return filepath.Join(dir, r.Kind+"-"+slug(r.Model)+".json")
}
