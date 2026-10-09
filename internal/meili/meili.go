// SPDX-License-Identifier: Apache-2.0

// Package meili wraps the Meilisearch operations that versioned publication
// needs: build a brand-new staging index, check its tasks, swap it with the
// live index, and search it with a caller-supplied vector.
//
// It talks to Meilisearch over plain net/http with typed structs rather than
// meilisearch-go. The package needs ten endpoints, task polling with coded
// failures (research caveat C6), and a search body whose exact fields matter
// (caveat C4); owning the wire format keeps all of that visible and keeps the
// SDK's retry and timeout behaviour out of the query path.
//
// Writes are asynchronous. Every write method returns the Meilisearch task uid
// and the caller must WaitTask it: a swap with a missing index answers 202 and
// fails later, and a failed document batch leaves the target index untouched.
// Publish must wait for every build task before it enqueues the swap, and mark
// a publication LIVE only after the swap task succeeded.
package meili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Code is a coded error, SCREAMING_SNAKE like every other code in the service.
type Code string

const (
	// CodeUpstreamTimeout: context deadline or HTTP client timeout.
	CodeUpstreamTimeout Code = "UPSTREAM_TIMEOUT"
	// CodeUpstreamUnavailable: connection failure or 5xx.
	CodeUpstreamUnavailable Code = "UPSTREAM_UNAVAILABLE"
	// CodeIndexUnavailable: Meilisearch answered index_not_found.
	CodeIndexUnavailable Code = "INDEX_UNAVAILABLE"
	// CodeIndexTaskFailed: a task finished failed or canceled. MeiliCode holds
	// Meilisearch's own error code (invalid_vector_dimensions, index_not_found...).
	CodeIndexTaskFailed Code = "INDEX_TASK_FAILED"
	// CodeIndexRequestRejected: Meilisearch answered another 4xx, for example
	// invalid_search_filter or invalid_vector_dimensions on a search.
	CodeIndexRequestRejected Code = "INDEX_REQUEST_REJECTED"
	// CodeUnknownScopeKey: the filter builder refused a key outside its allowlist.
	CodeUnknownScopeKey Code = "UNKNOWN_SCOPE_KEY"
)

// Error is the only error type this package returns, apart from a bare
// context.Canceled when the caller cancels.
type Error struct {
	Code      Code
	MeiliCode string // Meilisearch's error code, when it supplied one
	TaskUID   int64  // set for CodeIndexTaskFailed
	Message   string
	Err       error
}

func (e *Error) Error() string {
	s := string(e.Code)
	if e.MeiliCode != "" {
		s += " (" + e.MeiliCode + ")"
	}
	s += ": " + e.Message
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
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

// EmbedderName is the one embedder every index declares.
const EmbedderName = "default"

// Document is the shape of one searchable Q&A entry.
//
// ID is the primary key. Meilisearch accepts ids of letters, digits, hyphens
// and underscores up to 511 bytes, so a UUID string is fine. Vectors carries
// the embedding under "default"; use SetVector. Scope values are strings and
// every configured scope key is filterable as scope.<key>.
type Document struct {
	ID                 string            `json:"id"`
	Question           string            `json:"question"`
	AlternateQuestions []string          `json:"alternateQuestions"`
	Answer             string            `json:"answer"`
	SourceRef          string            `json:"sourceRef"`
	Scope              map[string]string `json:"scope,omitempty"`
	PublicationID      string            `json:"publicationId"`
	// Products are the catalog product ids the document is about; empty means
	// generic. The searcher's guard reads them from the hits.
	Products []string               `json:"products"`
	Vectors  map[string][][]float32 `json:"_vectors"`
}

// SetVector stores v as the document's only userProvided "default" embedding.
func (d *Document) SetVector(v []float32) { d.SetVectors([][]float32{v}) }

// SetVectors stores vs as the document's userProvided "default" embeddings, one
// per phrasing of the question.
func (d *Document) SetVectors(vs [][]float32) {
	d.Vectors = map[string][][]float32{EmbedderName: vs}
}

// Embedder is a Meilisearch embedder definition.
type Embedder struct {
	Source     string `json:"source"`
	Dimensions int    `json:"dimensions"`
}

// Settings is the subset of index settings this service configures.
type Settings struct {
	Embedders            map[string]Embedder `json:"embedders"`
	SearchableAttributes []string            `json:"searchableAttributes"`
	FilterableAttributes []string            `json:"filterableAttributes"`
	DisplayedAttributes  []string            `json:"displayedAttributes"`
}

// DefaultSettings returns the settings of a publication index: a userProvided
// embedder of the given dimensions, question/alternateQuestions/answer
// searchable, and scope.<key> filterable for each scope key. Keys must be
// plain identifiers (see ValidScopeKey).
func DefaultSettings(dimensions int, scopeKeys []string) (Settings, error) {
	if dimensions <= 0 {
		return Settings{}, errors.New("meili: dimensions must be > 0")
	}
	filterable := make([]string, 0, len(scopeKeys))
	for _, k := range scopeKeys {
		if !ValidScopeKey(k) {
			return Settings{}, &Error{Code: CodeUnknownScopeKey, Message: fmt.Sprintf("scope key %q is not a plain identifier", k)}
		}
		filterable = append(filterable, "scope."+k)
	}
	return Settings{
		Embedders:            map[string]Embedder{EmbedderName: {Source: "userProvided", Dimensions: dimensions}},
		SearchableAttributes: []string{"question", "alternateQuestions", "answer"},
		FilterableAttributes: filterable,
		DisplayedAttributes: []string{"id", "question", "alternateQuestions", "answer", "sourceRef",
			"scope", "publicationId", "products"},
	}, nil
}

var scopeKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidScopeKey reports whether k is safe to use as an unquoted filter
// attribute name (scope.<k>).
func ValidScopeKey(k string) bool { return scopeKeyRe.MatchString(k) }

// BuildFilter turns a scope map into a Meilisearch filter expression:
// `(scope.k = "v" OR scope.k NOT EXISTS) AND (scope.k2 = "v2" OR scope.k2 NOT
// EXISTS)`, keys sorted for determinism. A document that does not carry a scope
// key is global for that key and matches whatever value the caller asks for.
// Every key
// must be in allowed and be a plain identifier, otherwise it returns a
// CodeUnknownScopeKey error; values are double-quoted with `\` and `"`
// escaped. An empty scope yields "".
func BuildFilter(allowed []string, scope map[string]string) (string, error) {
	if len(scope) == 0 {
		return "", nil
	}
	ok := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		ok[k] = true
	}
	keys := make([]string, 0, len(scope))
	for k := range scope {
		if !ok[k] || !ValidScopeKey(k) {
			return "", &Error{Code: CodeUnknownScopeKey, Message: fmt.Sprintf("scope key %q is not allowed", k)}
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, "(scope."+k+" = "+quoteFilterValue(scope[k])+" OR scope."+k+" NOT EXISTS)")
	}
	return strings.Join(parts, " AND "), nil
}

var filterEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func quoteFilterValue(v string) string { return `"` + filterEscaper.Replace(v) + `"` }

// Config configures a Client.
type Config struct {
	BaseURL      string
	APIKey       string        // may be empty for an unsecured dev instance
	HTTPClient   *http.Client  // nil = a client with a 30s timeout
	PollInterval time.Duration // WaitTask polling period, default 25ms
}

// Client talks to one Meilisearch instance. It is safe for concurrent use and
// never retries.
type Client struct {
	base string
	key  string
	http *http.Client
	poll time.Duration
}

// New validates cfg and returns a Client. It does not touch the network.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("meili: BaseURL is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = 25 * time.Millisecond
	}
	return &Client{base: strings.TrimRight(cfg.BaseURL, "/"), key: cfg.APIKey, http: hc, poll: poll}, nil
}

// CreateIndex enqueues index creation and returns the task uid.
func (c *Client) CreateIndex(ctx context.Context, uid, primaryKey string) (int64, error) {
	return c.enqueue(ctx, http.MethodPost, "/indexes", map[string]string{"uid": uid, "primaryKey": primaryKey})
}

// ConfigureIndex enqueues a settings update and returns the task uid.
func (c *Client) ConfigureIndex(ctx context.Context, uid string, s Settings) (int64, error) {
	return c.enqueue(ctx, http.MethodPatch, "/indexes/"+url.PathEscape(uid)+"/settings", s)
}

// AddDocuments enqueues docs in chunks of batchSize (all at once when
// batchSize <= 0) and returns one task uid per chunk. A failed chunk fails
// only itself, so the caller must WaitTasks all of them.
func (c *Client) AddDocuments(ctx context.Context, uid string, docs []Document, batchSize int) ([]int64, error) {
	if batchSize <= 0 {
		batchSize = max(len(docs), 1)
	}
	var tasks []int64
	for start := 0; start < len(docs); start += batchSize {
		end := min(start+batchSize, len(docs))
		t, err := c.enqueue(ctx, http.MethodPost, "/indexes/"+url.PathEscape(uid)+"/documents", docs[start:end])
		if err != nil {
			return tasks, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// DeleteIndex enqueues index deletion and returns the task uid. The task
// fails with index_not_found when the index does not exist.
func (c *Client) DeleteIndex(ctx context.Context, uid string) (int64, error) {
	return c.enqueue(ctx, http.MethodDelete, "/indexes/"+url.PathEscape(uid), nil)
}

// Swap enqueues an atomic swap of two indexes (documents, settings and
// embedders travel together) and returns the task uid. A missing index only
// fails the task, so WaitTask it. Calling Swap again is the rollback.
func (c *Client) Swap(ctx context.Context, a, b string) (int64, error) {
	body := []map[string][]string{{"indexes": {a, b}}}
	return c.enqueue(ctx, http.MethodPost, "/swap-indexes", body)
}

// SwapRename enqueues an atomic rename of from to the uid to, which must not
// exist (Meilisearch fails the task with index_already_exists otherwise). It is
// the first-publication path: no live index to swap with, and no window in which
// an empty live index exists. Wait for the task.
func (c *Client) SwapRename(ctx context.Context, from, to string) (int64, error) {
	body := []map[string]any{{"indexes": []string{from, to}, "rename": true}}
	return c.enqueue(ctx, http.MethodPost, "/swap-indexes", body)
}

// LiveUID is the uid of a language's live index: prefix + lowercase language
// code ("faq_" + "EN" gives faq_en).
func LiveUID(prefix, language string) string { return prefix + strings.ToLower(language) }

// DocumentIDs returns the primary keys of every document in the index.
func (c *Client) DocumentIDs(ctx context.Context, uid string) ([]string, error) {
	const page = 1000
	ids := []string{}
	for offset := 0; ; offset += page {
		var r struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
			Total int `json:"total"`
		}
		path := fmt.Sprintf("/indexes/%s/documents?fields=id&limit=%d&offset=%d", url.PathEscape(uid), page, offset)
		if err := c.do(ctx, http.MethodGet, path, nil, &r); err != nil {
			return nil, err
		}
		for _, d := range r.Results {
			ids = append(ids, d.ID)
		}
		if len(r.Results) < page {
			return ids, nil
		}
	}
}

// IndexExists reports whether the index exists right now (after any pending
// create task has been waited for).
func (c *Client) IndexExists(ctx context.Context, uid string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/indexes/"+url.PathEscape(uid), nil, nil)
	if CodeOf(err) == CodeIndexUnavailable {
		return false, nil
	}
	return err == nil, err
}

// DocumentCount returns numberOfDocuments from the index stats.
func (c *Client) DocumentCount(ctx context.Context, uid string) (int64, error) {
	var st struct {
		NumberOfDocuments int64 `json:"numberOfDocuments"`
	}
	if err := c.do(ctx, http.MethodGet, "/indexes/"+url.PathEscape(uid)+"/stats", nil, &st); err != nil {
		return 0, err
	}
	return st.NumberOfDocuments, nil
}

// ListIndexUIDs returns the uids of up to 1000 indexes.
func (c *Client) ListIndexUIDs(ctx context.Context) ([]string, error) {
	var r struct {
		Results []struct {
			UID string `json:"uid"`
		} `json:"results"`
	}
	if err := c.do(ctx, http.MethodGet, "/indexes?limit=1000", nil, &r); err != nil {
		return nil, err
	}
	out := make([]string, len(r.Results))
	for i, x := range r.Results {
		out[i] = x.UID
	}
	return out, nil
}

// Task is the terminal state of a Meilisearch task.
type Task struct {
	UID     int64           `json:"uid"`
	Status  string          `json:"status"`
	Type    string          `json:"type"`
	Details json.RawMessage `json:"details"`
	Error   *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"type"`
	} `json:"error"`
}

// WaitTask polls until the task is terminal. It returns the task when it
// succeeded, and a CodeIndexTaskFailed error carrying Meilisearch's error code
// and message when it failed or was canceled. The context bounds the wait.
func (c *Client) WaitTask(ctx context.Context, uid int64) (*Task, error) {
	for {
		var t Task
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/tasks/%d", uid), nil, &t); err != nil {
			return nil, err
		}
		switch t.Status {
		case "succeeded":
			return &t, nil
		case "failed", "canceled":
			e := &Error{Code: CodeIndexTaskFailed, TaskUID: uid,
				Message: fmt.Sprintf("task %d (%s) %s", uid, t.Type, t.Status)}
			if t.Error != nil {
				e.MeiliCode = t.Error.Code
				e.Message += ": " + t.Error.Message
			}
			return &t, e
		}
		select {
		case <-ctx.Done():
			return nil, mapTransport(ctx, ctx.Err())
		case <-time.After(c.poll):
		}
	}
}

// WaitTasks waits for every task in order and returns the first failure.
func (c *Client) WaitTasks(ctx context.Context, uids []int64) error {
	var first error
	for _, u := range uids {
		if _, err := c.WaitTask(ctx, u); err != nil && first == nil {
			first = err
			if CodeOf(err) != CodeIndexTaskFailed {
				return err // transport problem: stop polling
			}
		}
	}
	return first
}

// Hit is one search result.
type Hit struct {
	ID                 string            `json:"id"`
	Question           string            `json:"question"`
	AlternateQuestions []string          `json:"alternateQuestions"`
	Answer             string            `json:"answer"`
	SourceRef          string            `json:"sourceRef"`
	Scope              map[string]string `json:"scope"`
	PublicationID      string            `json:"publicationId"`
	Products           []string          `json:"products"`
	Score              float64           `json:"_rankingScore"`
}

// SearchResult is a search answer. ProcessingTimeMs is Meilisearch's own
// processing time, separate from the caller's wall-clock search latency.
type SearchResult struct {
	Hits             []Hit
	ProcessingTimeMs int64
}

// VectorSearch runs a pure-vector search (research caveat C4): the caller's
// vector, hybrid {embedder: default, semanticRatio: 1.0}, empty q, scores
// shown, and rankingScoreThreshold set when threshold > 0 (0 omits it). The
// vector score is (1+cos)/2, so an orthogonal vector scores 0.5; a threshold
// must be calibrated above that. An empty hit list means NO_MATCH. filter
// comes from BuildFilter ("" = none).
//
// The search service does not send a threshold: Meilisearch takes a much
// slower path when fewer than limit hits clear it, so the searcher fetches
// the hits and applies the threshold itself.
func (c *Client) VectorSearch(ctx context.Context, uid string, vector []float32, limit int, filter string, threshold float64) (*SearchResult, error) {
	body := map[string]any{
		"q":                "",
		"vector":           vector,
		"hybrid":           map[string]any{"embedder": EmbedderName, "semanticRatio": 1.0},
		"limit":            limit,
		"showRankingScore": true,
	}
	if threshold > 0 {
		body["rankingScoreThreshold"] = threshold
	}
	if filter != "" {
		body["filter"] = filter
	}
	var r struct {
		Hits             []Hit `json:"hits"`
		ProcessingTimeMs int64 `json:"processingTimeMs"`
	}
	if err := c.do(ctx, http.MethodPost, "/indexes/"+url.PathEscape(uid)+"/search", body, &r); err != nil {
		return nil, err
	}
	if r.Hits == nil {
		r.Hits = []Hit{}
	}
	return &SearchResult{Hits: r.Hits, ProcessingTimeMs: r.ProcessingTimeMs}, nil
}

func (c *Client) enqueue(ctx context.Context, method, path string, body any) (int64, error) {
	var r struct {
		TaskUID int64 `json:"taskUid"`
	}
	if err := c.do(ctx, method, path, body, &r); err != nil {
		return 0, err
	}
	return r.TaskUID, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return &Error{Code: CodeIndexRequestRejected, Message: "encode request", Err: err}
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
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
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
	if resp.StatusCode >= 400 {
		var me struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(data, &me)
		e := &Error{MeiliCode: me.Code, Message: fmt.Sprintf("Meilisearch %s %s: HTTP %d: %s", method, path, resp.StatusCode, me.Message)}
		switch {
		case me.Code == "index_not_found":
			e.Code = CodeIndexUnavailable
		case resp.StatusCode >= 500:
			e.Code = CodeUpstreamUnavailable
		default:
			e.Code = CodeIndexRequestRejected
		}
		return e
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return &Error{Code: CodeUpstreamUnavailable, Message: "decode Meilisearch response", Err: err}
		}
	}
	return nil
}

func mapTransport(ctx context.Context, err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded),
		errors.As(err, &ne) && ne.Timeout():
		return &Error{Code: CodeUpstreamTimeout, Message: "Meilisearch call timed out", Err: err}
	case errors.Is(err, context.Canceled):
		return err
	default:
		return &Error{Code: CodeUpstreamUnavailable, Message: "Meilisearch call failed", Err: err}
	}
}
