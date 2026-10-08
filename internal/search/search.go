// SPDX-License-Identifier: Apache-2.0

// Package search is the one implementation of a FAQ search: embed the query,
// run a pure-vector Meilisearch search on the live index of the language, and
// decide HIT or NO_MATCH. The HTTP handler and the in-process `eval` call the
// same Searcher, so the latency eval measures is the latency AICC sees (minus
// HTTP).
//
// The live index uid (faq_en, faq_zh) only ever holds published content: the
// publish and rollback code is the only writer, and it swaps a fully built,
// verified index in. The searcher therefore does not ask PostgreSQL whether a
// publication is LIVE; a missing index is INDEX_UNAVAILABLE.
package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/embed"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
)

// Code is a coded error. The values are members of the contract's ErrorCode enum.
type Code string

const (
	CodeUpstreamTimeout     Code = "UPSTREAM_TIMEOUT"
	CodeUpstreamUnavailable Code = "UPSTREAM_UNAVAILABLE"
	CodeIndexUnavailable    Code = "INDEX_UNAVAILABLE"
	CodeUnknownScopeKey     Code = "UNKNOWN_SCOPE_KEY"
	CodeInternal            Code = "INTERNAL"
)

// Error is the only error type Search returns, apart from a bare
// context.Canceled when the caller went away.
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

// Embedder embeds query text. *embed.Client satisfies it.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Status is the outcome of a search that ran.
type Status string

const (
	StatusHit     Status = "HIT"
	StatusNoMatch Status = "NO_MATCH"
)

// overfetch is the least number of hits asked of Meilisearch. Its vector search
// is approximate, and a small limit combined with a filter sometimes returned a
// lower-ranked document instead of the best one (limit 1: 4 wrong in 450 live
// queries; limits 3 to 20: none, see docs/research/02-meilisearch.md section 8).
// The searcher therefore asks for more and keeps the first topK.
const overfetch = 10

// Searcher runs searches. It is safe for concurrent use.
type Searcher struct {
	Meili    *meili.Client
	Embedder Embedder
	// ScopeKeys are the keys a request may filter on.
	ScopeKeys []string
	// ThresholdEN and ThresholdZH are the NO_MATCH score thresholds, in (0.5, 1].
	ThresholdEN, ThresholdZH float64
	// IndexPrefix is "faq_" unless a test needs a private namespace.
	IndexPrefix string
	// Metrics may be nil (eval).
	Metrics *obs.Metrics
}

// Request is one search.
type Request struct {
	Query    string
	Language domain.Language
	Scope    map[string]string
	TopK     int
	// Timeout is the whole budget: embedding and search. Zero means none.
	Timeout time.Duration
	// Threshold overrides the language's threshold when non-nil. 0 disables the
	// gate. Eval uses it to sweep thresholds; the HTTP handler never sets it.
	Threshold *float64
}

// Item is one result.
type Item struct {
	ID        string
	Question  string
	Answer    string
	SourceRef string
	Score     float64
}

// Latency is the per-stage wall-clock time of one search, measured with the
// monotonic clock.
type Latency struct {
	Embedding, Search, Total time.Duration
}

// Response is the answer to a search that ran.
type Response struct {
	Status  Status
	Items   []Item
	Latency Latency
}

// ThresholdFor returns the configured threshold of a language.
func (s *Searcher) ThresholdFor(lang domain.Language) float64 {
	if lang == domain.LanguageZH {
		return s.ThresholdZH
	}
	return s.ThresholdEN
}

func (s *Searcher) liveUID(lang domain.Language) string {
	prefix := s.IndexPrefix
	if prefix == "" {
		prefix = "faq_"
	}
	return meili.LiveUID(prefix, string(lang))
}

// Search runs one search. On failure it returns an *Error and the metrics (when
// configured) record the code as the status.
func (s *Searcher) Search(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	resp, err := s.search(ctx, req, start)
	resp.Latency.Total = time.Since(start)
	if s.Metrics != nil {
		status := string(resp.Status)
		if err != nil {
			status = string(CodeOf(err))
			if status == "" {
				status = "CANCELED"
			}
		}
		s.Metrics.ObserveSearch(ctx, string(req.Language), status, obs.SearchStages{
			Embedding: resp.Latency.Embedding, Search: resp.Latency.Search, Total: resp.Latency.Total})
	}
	return resp, err
}

func (s *Searcher) search(ctx context.Context, req Request, start time.Time) (Response, error) {
	var resp Response
	filter, err := meili.BuildFilter(s.ScopeKeys, req.Scope)
	if err != nil {
		return resp, &Error{Code: CodeUnknownScopeKey, Message: "scope key is not configured", Err: err}
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	topK := req.TopK
	if topK < 1 {
		topK = 3
	}
	threshold := s.ThresholdFor(req.Language)
	if req.Threshold != nil {
		threshold = *req.Threshold
	}

	t0 := time.Now()
	vecs, err := s.Embedder.Embed(ctx, []string{req.Query})
	resp.Latency.Embedding = time.Since(t0)
	if err != nil {
		return resp, s.wrap(ctx, "embed the query", err)
	}
	if len(vecs) != 1 {
		return resp, &Error{Code: CodeInternal, Message: fmt.Sprintf("embedder returned %d vectors for one query", len(vecs))}
	}

	t1 := time.Now()
	res, err := s.Meili.VectorSearch(ctx, s.liveUID(req.Language), vecs[0], max(topK, overfetch), filter, 0)
	resp.Latency.Search = time.Since(t1)
	if err != nil {
		return resp, s.wrap(ctx, "search the live index", err)
	}

	// The threshold is applied here, not by Meilisearch: a rankingScoreThreshold
	// that fewer than limit hits clear sends Meilisearch down a much slower
	// path.
	hits := slices.Clone(res.Hits)
	slices.SortStableFunc(hits, func(a, b meili.Hit) int { return cmp.Compare(b.Score, a.Score) })
	hits = slices.DeleteFunc(hits, func(h meili.Hit) bool { return h.Score < threshold })
	hits = hits[:min(len(hits), topK)]
	resp.Items = make([]Item, 0, len(hits))
	for _, h := range hits {
		resp.Items = append(resp.Items, Item{ID: h.ID, Question: h.Question, Answer: h.Answer, SourceRef: h.SourceRef, Score: h.Score})
	}
	resp.Status = StatusNoMatch
	if len(resp.Items) > 0 {
		resp.Status = StatusHit
	}
	return resp, nil
}

// wrap maps an embed or meili failure to this package's codes. An expired
// deadline wins over whatever error it provoked.
func (s *Searcher) wrap(ctx context.Context, what string, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: CodeUpstreamTimeout, Message: "the request budget ran out while trying to " + what, Err: err}
	}
	switch {
	case embed.CodeOf(err) == embed.CodeUpstreamTimeout, meili.CodeOf(err) == meili.CodeUpstreamTimeout:
		return &Error{Code: CodeUpstreamTimeout, Message: "timeout while trying to " + what, Err: err}
	case embed.CodeOf(err) == embed.CodeUpstreamUnavailable, embed.CodeOf(err) == embed.CodeEmbeddingInvalid,
		meili.CodeOf(err) == meili.CodeUpstreamUnavailable:
		return &Error{Code: CodeUpstreamUnavailable, Message: "dependency unavailable while trying to " + what, Err: err}
	case meili.CodeOf(err) == meili.CodeIndexUnavailable:
		return &Error{Code: CodeIndexUnavailable, Message: "no live index for the language", Err: err}
	}
	return &Error{Code: CodeInternal, Message: "unexpected failure while trying to " + what, Err: err}
}
