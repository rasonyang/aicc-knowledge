// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/search"
)

// HTTP runs questions against a running service's POST /v1/search. The latency
// it reports is the one the service puts in the response.
type HTTP struct {
	BaseURL string
	APIKey  string
	Client  *http.Client // nil: a client with a 30s timeout
	// TimeoutMs is sent as timeoutMs when > 0.
	TimeoutMs int
}

// Run implements Runner. The threshold cannot be overridden over HTTP; callers
// must not ask for a sweep.
func (h HTTP) Run(ctx context.Context, q Question, _ *float64) Outcome {
	o := Outcome{Question: q}
	body := map[string]any{"query": q.Text, "language": string(q.Language), "topK": TopK}
	if len(q.Scope) > 0 {
		body["scope"] = q.Scope
	}
	if h.TimeoutMs > 0 {
		body["timeoutMs"] = h.TimeoutMs
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.BaseURL, "/")+"/v1/search", bytes.NewReader(b))
	if err != nil {
		o.Err = err
		return o
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	c := h.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		o.Err = err
		return o
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		o.Err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		return o
	}
	var r struct {
		Status string `json:"status"`
		Items  []struct {
			ID    string  `json:"id"`
			Score float64 `json:"score"`
		} `json:"items"`
		LatencyMs struct {
			Embedding float64 `json:"embedding"`
			Search    float64 `json:"search"`
			Total     float64 `json:"total"`
		} `json:"latencyMs"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		o.Err = fmt.Errorf("decode response: %w", err)
		return o
	}
	o.Status = r.Status
	for _, it := range r.Items {
		o.IDs = append(o.IDs, it.ID)
		o.Scores = append(o.Scores, it.Score)
	}
	toDur := func(ms float64) time.Duration { return time.Duration(ms * float64(time.Millisecond)) }
	o.Latency = search.Latency{Embedding: toDur(r.LatencyMs.Embedding), Search: toDur(r.LatencyMs.Search), Total: toDur(r.LatencyMs.Total)}
	return o
}
