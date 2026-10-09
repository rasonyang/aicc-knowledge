// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TEI is a minimal client for text-embeddings-inference.
type TEI struct {
	base string
	hc   *http.Client
}

func NewTEI(base string) *TEI {
	return &TEI{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: 5 * time.Minute}}
}

func (t *TEI) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d: %.200s", path, resp.StatusCode, data)
	}
	return json.Unmarshal(data, out)
}

// Embed returns one vector per input.
func (t *TEI) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	var out [][]float32
	if err := t.post(ctx, "/embed", map[string]any{"inputs": inputs, "truncate": true}, &out); err != nil {
		return nil, err
	}
	if len(out) != len(inputs) {
		return nil, fmt.Errorf("embed returned %d vectors for %d inputs", len(out), len(inputs))
	}
	return out, nil
}

// Rerank returns the raw score of every text, in input order.
func (t *TEI) Rerank(ctx context.Context, query string, texts []string) ([]float64, error) {
	var out []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	if err := t.post(ctx, "/rerank", map[string]any{"query": query, "texts": texts, "raw_scores": true, "truncate": true}, &out); err != nil {
		return nil, err
	}
	if len(out) != len(texts) {
		return nil, fmt.Errorf("rerank returned %d scores for %d texts", len(out), len(texts))
	}
	scores := make([]float64, len(texts))
	for _, r := range out {
		if r.Index < 0 || r.Index >= len(texts) {
			return nil, fmt.Errorf("rerank index %d out of range", r.Index)
		}
		scores[r.Index] = r.Score
	}
	return scores, nil
}

// Info fetches /info (the readiness probe: /health runs an inference).
func (t *TEI) Info(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.base+"/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/info: HTTP %d", resp.StatusCode)
	}
	var m map[string]any
	return m, json.NewDecoder(resp.Body).Decode(&m)
}

// WaitReady polls /info until it answers and returns the time it took.
func (t *TEI) WaitReady(ctx context.Context, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		c, cc := context.WithTimeout(ctx, 3*time.Second)
		_, err := t.Info(c)
		cc()
		if err == nil {
			return time.Since(start), nil
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("not ready after %s: %w", timeout, err)
		case <-time.After(time.Second):
		}
	}
}
