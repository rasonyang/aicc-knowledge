// SPDX-License-Identifier: Apache-2.0

// Package embed is the client for Text Embeddings Inference (TEI) serving
// bge-m3. Documents and queries are embedded by this service itself so that
// embedding and search latency are measured separately.
//
// Behaviour worth knowing:
//
//   - Requests send truncate:true. TEI then cuts an over-long input at the
//     model's maximum length instead of answering 422. A very long answer
//     therefore still embeds, using its leading tokens only.
//   - TEI's CPU (ONNX) backend refuses more than a few inputs per forward
//     pass, and /info advertises max_client_batch_size. The client reads it once
//     (lazily) and splits larger inputs into sequential requests, preserving
//     order. Config.MaxBatch overrides the discovered value.
//   - Vectors are L2-normalized by TEI and the flag normalize:false has no
//     effect on the ONNX backend (research caveat C3). The client therefore
//     does not send it, and re-normalizes defensively when a norm drifts from
//     1 by more than 1e-3.
//   - Embed never retries, so a caller's latency measurement is the truth.
//     EmbedBatch applies Config.Retry and belongs on the publish path only.
//   - The expected dimension is supplied by the caller (Config.Dimensions);
//     nothing here hardcodes 1024.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Code is a coded error, SCREAMING_SNAKE like every other code in the service.
type Code string

const (
	// CodeUpstreamTimeout: the context deadline or the HTTP client timeout hit.
	CodeUpstreamTimeout Code = "UPSTREAM_TIMEOUT"
	// CodeUpstreamUnavailable: connection failure, 5xx or 429 from TEI.
	CodeUpstreamUnavailable Code = "UPSTREAM_UNAVAILABLE"
	// CodeEmbeddingInvalid: TEI answered, but the vectors (or request) are unusable:
	// wrong count, wrong dimension, NaN/Inf, zero norm, or a 4xx rejection.
	CodeEmbeddingInvalid Code = "EMBEDDING_INVALID"
)

// Error is the only error type this package returns, apart from a bare
// context.Canceled when the caller cancels.
type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// CodeOf returns the code of err, or "" when err is not an *Error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// RetryPolicy is used by EmbedBatch only. Only UPSTREAM_UNAVAILABLE failures
// are retried; timeouts and invalid embeddings are returned at once.
type RetryPolicy struct {
	MaxAttempts int           // total attempts; values < 2 mean no retry
	Backoff     time.Duration // delay before the second attempt, doubling after
}

// Config configures a Client.
type Config struct {
	BaseURL    string       // e.g. http://tei:80, required
	Dimensions int          // expected vector size, required (> 0)
	MaxBatch   int          // 0 = discover from /info max_client_batch_size
	HTTPClient *http.Client // nil = a client with a 30s timeout
	Retry      RetryPolicy  // EmbedBatch only
}

// Client talks to one TEI instance. It is safe for concurrent use.
type Client struct {
	base  string
	dims  int
	http  *http.Client
	retry RetryPolicy

	mu       sync.Mutex
	maxBatch int
}

// fallbackMaxBatch is used when /info does not report max_client_batch_size.
const fallbackMaxBatch = 8

// New validates cfg and returns a Client. It does not touch the network.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("embed: BaseURL is required")
	}
	if cfg.Dimensions <= 0 {
		return nil, errors.New("embed: Dimensions must be > 0")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		base:     strings.TrimRight(cfg.BaseURL, "/"),
		dims:     cfg.Dimensions,
		http:     hc,
		retry:    cfg.Retry,
		maxBatch: max(cfg.MaxBatch, 0),
	}, nil
}

// Dimensions returns the expected vector size.
func (c *Client) Dimensions() int { return c.dims }

// MaxBatch returns the effective batch size, discovering it on first use.
func (c *Client) MaxBatch(ctx context.Context) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxBatch > 0 {
		return c.maxBatch, nil
	}
	var info struct {
		MaxClientBatchSize int `json:"max_client_batch_size"`
	}
	if err := c.do(ctx, http.MethodGet, "/info", nil, &info); err != nil {
		return 0, err
	}
	c.maxBatch = info.MaxClientBatchSize
	if c.maxBatch <= 0 {
		c.maxBatch = fallbackMaxBatch
	}
	return c.maxBatch, nil
}

// Embed returns one L2-normalized vector per text, in input order. Inputs
// larger than the TEI batch limit are split into sequential requests. It never
// retries.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	mb, err := c.MaxBatch(ctx)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += mb {
		end := min(start+mb, len(texts))
		vecs, err := c.embedChunk(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// EmbedBatch is Embed with the configured retry policy, for the publish path.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	attempts := max(c.retry.MaxAttempts, 1)
	delay := c.retry.Backoff
	var err error
	for i := 0; i < attempts; i++ {
		var vecs [][]float32
		vecs, err = c.Embed(ctx, texts)
		if err == nil || CodeOf(err) != CodeUpstreamUnavailable || i == attempts-1 {
			return vecs, err
		}
		select {
		case <-ctx.Done():
			return nil, mapTransport(ctx, ctx.Err())
		case <-time.After(delay):
		}
		delay *= 2
	}
	return nil, err
}

func (c *Client) embedChunk(ctx context.Context, texts []string) ([][]float32, error) {
	req := struct {
		Inputs   []string `json:"inputs"`
		Truncate bool     `json:"truncate"`
	}{texts, true}
	var raw [][]float32
	if err := c.do(ctx, http.MethodPost, "/embed", req, &raw); err != nil {
		return nil, err
	}
	if len(raw) != len(texts) {
		return nil, &Error{Code: CodeEmbeddingInvalid,
			Message: fmt.Sprintf("TEI returned %d vectors for %d inputs", len(raw), len(texts))}
	}
	for i, v := range raw {
		if err := c.checkAndNormalize(v); err != nil {
			return nil, &Error{Code: CodeEmbeddingInvalid, Message: fmt.Sprintf("vector %d: %v", i, err)}
		}
	}
	return raw, nil
}

func (c *Client) checkAndNormalize(v []float32) error {
	if len(v) != c.dims {
		return fmt.Errorf("dimension %d, expected %d", len(v), c.dims)
	}
	var sum float64
	for _, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return errors.New("contains NaN or Inf")
		}
		sum += f * f
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return errors.New("zero vector")
	}
	if math.Abs(norm-1) > 1e-3 {
		for i := range v {
			v[i] = float32(float64(v[i]) / norm)
		}
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return &Error{Code: CodeEmbeddingInvalid, Message: "encode request", Err: err}
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return &Error{Code: CodeUpstreamUnavailable, Message: "build request", Err: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return mapTransport(ctx, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return mapTransport(ctx, err)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return &Error{Code: CodeUpstreamUnavailable,
			Message: fmt.Sprintf("TEI %s %s: HTTP %d: %s", method, path, resp.StatusCode, snippet(data))}
	}
	if resp.StatusCode >= 400 {
		return &Error{Code: CodeEmbeddingInvalid,
			Message: fmt.Sprintf("TEI %s %s rejected: HTTP %d: %s", method, path, resp.StatusCode, snippet(data))}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &Error{Code: CodeEmbeddingInvalid, Message: "decode TEI response", Err: err}
	}
	return nil
}

func mapTransport(ctx context.Context, err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded),
		errors.As(err, &ne) && ne.Timeout():
		return &Error{Code: CodeUpstreamTimeout, Message: "TEI call timed out", Err: err}
	case errors.Is(err, context.Canceled):
		return err
	default:
		return &Error{Code: CodeUpstreamUnavailable, Message: "TEI call failed", Err: err}
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
