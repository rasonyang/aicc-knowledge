// SPDX-License-Identifier: Apache-2.0

// Package httpapi serves the authenticated read API (generated ServerInterface
// from docs/openapi.json) and the unauthenticated ops listener.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/rasonyang/aicc-knowledge/internal/api"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/facts"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/search"
	"github.com/rasonyang/aicc-knowledge/internal/store"
)

const (
	maxBodyBytes    = 1 << 20
	maxQueryRunes   = 1000
	maxTimeoutMsAPI = 60000 // the contract's hard upper bound
	defaultTopK     = 3
)

// Settings are the request-validation limits taken from configuration.
type Settings struct {
	ScopeKeys        []string
	DefaultTimeoutMS int
	MaxTimeoutMS     int
	// Location is the zone whose current date is the default `at` of a facts
	// lookup; nil means UTC.
	Location *time.Location
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

// Server implements api.ServerInterface.
type Server struct {
	store    *store.Store
	metrics  *obs.Metrics
	settings Settings
	searcher Searcher
}

// Searcher runs a FAQ search. *search.Searcher implements it.
type Searcher interface {
	Search(ctx context.Context, req search.Request) (search.Response, error)
}

var _ api.ServerInterface = (*Server)(nil)

// New builds the handler of the authenticated API listener.
func New(st *store.Store, metrics *obs.Metrics, settings Settings, searcher Searcher) http.Handler {
	r := newRouter(&Server{store: st, metrics: metrics, settings: settings, searcher: searcher})
	return otelhttp.NewHandler(r, "aicc-knowledge", otelhttp.WithSpanNameFormatter(
		func(_ string, r *http.Request) string { return r.Method + " " + r.URL.Path }))
}

// newRouter mounts the generated routes behind the auth middleware. The
// route-coverage gate test walks it.
func newRouter(s *Server) chi.Router {
	r := chi.NewRouter()
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such route", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed for this route", nil)
	})
	r.Use(s.recordMetrics)
	api.HandlerWithOptions(s, api.ChiServerOptions{
		BaseRouter:       r,
		Middlewares:      []api.MiddlewareFunc{s.authenticate},
		ErrorHandlerFunc: writeParamError,
	})
	return r
}

func writeParamError(w http.ResponseWriter, _ *http.Request, err error) {
	writeError(w, http.StatusBadRequest, CodeValidationFailed, err.Error(), nil)
}

// statusRecorder captures the status code for the metrics middleware.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) recordMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		s.metrics.ObserveHTTP(r.Context(), r.Method, route, rec.status, time.Since(start))
	})
}

// authenticate requires a bearer API key. Every generated route sits behind
// it, because every operation in the contract declares apiKeyBearer.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, secret, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		secret = strings.TrimSpace(secret)
		if !ok || !strings.EqualFold(scheme, "Bearer") || secret == "" {
			unauthorized(w, "missing bearer API key")
			return
		}
		switch err := s.store.AuthenticateAPIKey(r.Context(), secret); {
		case err == nil:
			next.ServeHTTP(w, r)
		case errors.Is(err, store.ErrInvalidAPIKey):
			unauthorized(w, "unknown API key")
		default:
			slog.ErrorContext(r.Context(), "authenticate", "error", err)
			writeError(w, http.StatusServiceUnavailable, CodeStorageDown, "the database is unavailable", nil)
		}
	})
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="aicc-knowledge"`)
	writeError(w, http.StatusUnauthorized, CodeUnauthorized, msg, nil)
}

// decodeBody reads a JSON body strictly: unknown fields and trailing data are
// malformed requests.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	dec.UseNumber() // untyped values (facts keys) keep their exact digits
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidationFailed, "request body is not valid for this operation: "+err.Error(), nil)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeValidationFailed, "request body has trailing data", nil)
		return false
	}
	return true
}

func invalid(w http.ResponseWriter, field, rule, msg string) {
	writeError(w, http.StatusUnprocessableEntity, CodeValidationFailed, msg, map[string]any{"field": field, "rule": rule})
}

// SearchFaq implements POST /v1/search.
//
// Order of decisions: body shape (400), field rules (422 VALIDATION_FAILED),
// scope keys (422 UNKNOWN_SCOPE_KEY), then the search itself. The budget is
// min(timeoutMs or the default, the configured maximum) and covers embedding
// and search (504 UPSTREAM_TIMEOUT). TEI or Meilisearch down is 503
// UPSTREAM_UNAVAILABLE, and a language whose live index does not exist is 503
// INDEX_UNAVAILABLE. There is no per-request database lookup of the LIVE
// publication: see package search.
func (s *Server) SearchFaq(w http.ResponseWriter, r *http.Request) {
	var req api.SearchRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		invalid(w, "query", "required", "query must not be empty")
		return
	}
	if utf8.RuneCountInString(req.Query) > maxQueryRunes {
		invalid(w, "query", "maxLength", fmt.Sprintf("query must be at most %d characters", maxQueryRunes))
		return
	}
	if !domain.Language(req.Language).Valid() {
		invalid(w, "language", "enum", "language must be EN or ZH")
		return
	}
	topK := defaultTopK
	if req.TopK != nil {
		topK = *req.TopK
		if topK < 1 || topK > 3 {
			invalid(w, "topK", "range", "topK must be between 1 and 3")
			return
		}
	}
	timeoutMS := s.settings.DefaultTimeoutMS
	if req.TimeoutMs != nil {
		if *req.TimeoutMs < 1 || *req.TimeoutMs > maxTimeoutMsAPI {
			invalid(w, "timeoutMs", "range", fmt.Sprintf("timeoutMs must be between 1 and %d", maxTimeoutMsAPI))
			return
		}
		timeoutMS = *req.TimeoutMs
	}
	timeoutMS = min(timeoutMS, s.settings.MaxTimeoutMS)
	if req.Scope != nil {
		var unknown []string
		for k := range *req.Scope {
			if !slices.Contains(s.settings.ScopeKeys, k) {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			slices.Sort(unknown)
			writeError(w, http.StatusUnprocessableEntity, CodeUnknownScopeKey,
				"scope contains keys that are not configured: "+strings.Join(unknown, ", "),
				map[string]any{"keys": unknown, "allowedKeys": s.allowedScopeKeys()})
			return
		}
	}

	var scope map[string]string
	if req.Scope != nil {
		scope = *req.Scope
	}
	resp, err := s.searcher.Search(r.Context(), search.Request{
		Query: req.Query, Language: domain.Language(req.Language), Scope: scope, TopK: topK,
		Timeout: time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		s.writeSearchError(w, r, domain.Language(req.Language), err)
		return
	}
	items := make([]api.SearchItem, 0, len(resp.Items))
	for _, it := range resp.Items {
		items = append(items, api.SearchItem{ID: it.ID, Question: it.Question, Answer: it.Answer,
			SourceRef: it.SourceRef, Score: float32(it.Score)})
	}
	writeJSON(w, http.StatusOK, api.SearchResponse{
		Status: api.SearchStatus(resp.Status),
		Items:  items,
		LatencyMs: api.SearchLatencyMs{
			Embedding: ms(resp.Latency.Embedding), Search: ms(resp.Latency.Search), Total: ms(resp.Latency.Total),
		},
	})
}

func ms(d time.Duration) float32 { return float32(d.Microseconds()) / 1000 }

// writeSearchError maps the searcher's coded errors to the contract.
func (s *Server) writeSearchError(w http.ResponseWriter, r *http.Request, lang domain.Language, err error) {
	switch search.CodeOf(err) {
	case search.CodeIndexUnavailable:
		writeError(w, http.StatusServiceUnavailable, CodeIndexUnavailable,
			"no live index for this language", map[string]any{"language": string(lang)})
	case search.CodeUpstreamTimeout:
		writeError(w, http.StatusGatewayTimeout, CodeUpstreamTimeout, "the search did not finish within the request timeout", nil)
	case search.CodeUpstreamUnavailable:
		slog.ErrorContext(r.Context(), "search dependency unavailable", "error", err)
		writeError(w, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "the embedding service or the search index is unavailable", nil)
	case search.CodeUnknownScopeKey:
		writeError(w, http.StatusUnprocessableEntity, CodeUnknownScopeKey, "scope contains keys that are not configured",
			map[string]any{"allowedKeys": s.allowedScopeKeys()})
	default:
		if r.Context().Err() != nil {
			return // the caller went away; nobody reads the answer
		}
		slog.ErrorContext(r.Context(), "search failed", "error", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "the search failed unexpectedly", nil)
	}
}

func (s *Server) allowedScopeKeys() []string {
	if s.settings.ScopeKeys == nil {
		return []string{}
	}
	return s.settings.ScopeKeys
}

// factsLookupResponse is the wire form of api.FactsLookupResponse with the
// nullable fields always present: NOT_FOUND carries explicit nulls, as the
// contract declares them nullable (the generated struct omits nil fields).
type factsLookupResponse struct {
	Status    api.FactsLookupStatus `json:"status"`
	Row       map[string]any        `json:"row"`
	SourceRef *string               `json:"sourceRef"`
	ValidFrom *string               `json:"validFrom"`
	ValidTo   *string               `json:"validTo"`
}

// today returns the current date in the configured zone as a UTC date.
func (s *Server) today() time.Time {
	now := time.Now()
	if s.settings.Now != nil {
		now = s.settings.Now()
	}
	loc := s.settings.Location
	if loc == nil {
		loc = time.UTC
	}
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.Format(time.DateOnly)
	return &v
}

// LookupFact implements POST /v1/facts/{table}/lookup.
//
// Order of decisions: body shape (400), a non-empty key (422), unknown table
// (404 FACT_TABLE_NOT_FOUND), a table without a valid import (503
// FACT_TABLE_UNAVAILABLE), then the key against the table's key columns and
// types (422 VALIDATION_FAILED with params missing, unknown and invalid).
// Without `at` the date is today in KB_TIMEZONE.
func (s *Server) LookupFact(w http.ResponseWriter, r *http.Request, table string) {
	ctx := r.Context()
	var req api.FactsLookupRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.Key) == 0 {
		s.metrics.ObserveFactsLookup(ctx, "INVALID")
		invalid(w, "key", "required", "key must contain at least one column value")
		return
	}
	at := s.today()
	if req.At != nil {
		at = req.At.Time
	}
	m, err := facts.Lookup(ctx, s.store, table, req.Key, at)
	var unavailable *facts.UnavailableError
	var keyErr *facts.KeyError
	switch {
	case errors.Is(err, facts.ErrTableNotFound):
		s.metrics.ObserveFactsLookup(ctx, "TABLE_NOT_FOUND")
		writeError(w, http.StatusNotFound, CodeFactTableNotFound, "no fact table is registered under this name",
			map[string]any{"table": table})
	case errors.As(err, &unavailable):
		s.metrics.ObserveFactsLookup(ctx, "TABLE_UNAVAILABLE")
		writeError(w, http.StatusServiceUnavailable, CodeFactTableUnavailable,
			"the fact table has no valid import; its source workbook or mapping was removed, replaced or failed to import",
			map[string]any{"table": table, "reason": unavailable.Code})
	case errors.As(err, &keyErr):
		s.metrics.ObserveFactsLookup(ctx, "INVALID")
		inv := make([]facts.KeyInvalid, 0, len(keyErr.Invalid))
		inv = append(inv, keyErr.Invalid...)
		writeError(w, http.StatusUnprocessableEntity, CodeValidationFailed, keyErr.Error(), map[string]any{
			"field": "key", "rule": "keyColumns",
			"keyColumns": keyErr.KeyColumns, "missing": nonNil(keyErr.Missing), "unknown": nonNil(keyErr.Unknown), "invalid": inv,
		})
	case errors.Is(err, facts.ErrAmbiguous):
		s.metrics.ObserveFactsLookup(ctx, "ERROR")
		slog.ErrorContext(ctx, "facts lookup matched more than one row", "table", table, "error", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "the fact table holds more than one row for this key and date", nil)
	case err != nil:
		s.metrics.ObserveFactsLookup(ctx, "ERROR")
		slog.ErrorContext(ctx, "facts lookup", "table", table, "error", err)
		writeError(w, http.StatusServiceUnavailable, CodeStorageDown, "the database is unavailable", nil)
	case m == nil:
		s.metrics.ObserveFactsLookup(ctx, "NOT_FOUND")
		writeJSON(w, http.StatusOK, factsLookupResponse{Status: api.FactsLookupStatusNOTFOUND})
	default:
		s.metrics.ObserveFactsLookup(ctx, "FOUND")
		writeJSON(w, http.StatusOK, factsLookupResponse{
			Status: api.FactsLookupStatusFOUND, Row: m.Row, SourceRef: &m.SourceRef,
			ValidFrom: formatDate(m.ValidFrom), ValidTo: formatDate(m.ValidTo),
		})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
