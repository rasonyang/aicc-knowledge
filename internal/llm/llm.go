// SPDX-License-Identifier: Apache-2.0

// Package llm is a small client for an OpenAI-compatible chat-completions
// endpoint (POST {base}/chat/completions) that asks for JSON-schema-constrained
// output. It serves the offline candidate generation only; no request path of
// the service calls it.
//
// The server's grammar enforcement is not trusted: the caller passes a
// validator that checks the content against the schema in Go, and a content
// that fails it is an LLM_OUTPUT_INVALID error. Transport failures (timeouts,
// connection errors, HTTP 429 and 5xx) are retried a bounded number of times
// with exponential backoff; other 4xx answers are not retried.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ErrorCode is a machine-readable LLM failure identifier.
type ErrorCode string

// Error codes.
const (
	// CodeUpstreamTimeout: every attempt ran out of time.
	CodeUpstreamTimeout ErrorCode = "UPSTREAM_TIMEOUT"
	// CodeUpstreamUnavailable: the endpoint could not be reached, kept
	// answering 5xx or 429, or rejected the request with another 4xx.
	CodeUpstreamUnavailable ErrorCode = "UPSTREAM_UNAVAILABLE"
	// CodeOutputInvalid: the answer was received but is not JSON that conforms
	// to the requested schema (or was cut off).
	CodeOutputInvalid ErrorCode = "LLM_OUTPUT_INVALID"
)

// Error is a coded LLM failure.
type Error struct {
	Code ErrorCode
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	s := string(e.Code) + ": " + e.Msg
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *Error) Unwrap() error { return e.Err }

// Is matches any *Error with the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrUpstreamTimeout     = &Error{Code: CodeUpstreamTimeout}
	ErrUpstreamUnavailable = &Error{Code: CodeUpstreamUnavailable}
	ErrOutputInvalid       = &Error{Code: CodeOutputInvalid}
)

// Config configures a Client.
type Config struct {
	// BaseURL is the API root including the version path, e.g.
	// http://host:8080/v1; "/chat/completions" is appended.
	BaseURL string
	// APIKey is sent as a Bearer token only when non-empty.
	APIKey string
	Model  string
	// Timeout bounds one HTTP attempt.
	Timeout     time.Duration
	Temperature float64
	// Seed is sent when non-nil (best-effort determinism).
	Seed *int64
	// MaxTokens bounds the completion; 0 means DefaultMaxTokens.
	MaxTokens int
	// Retries is the number of extra attempts after a retryable failure
	// (default DefaultRetries; negative means none).
	Retries int
	// Backoff is the wait before the first retry, doubled each time
	// (default DefaultBackoff).
	Backoff time.Duration
	// HTTPClient overrides the transport (tests).
	HTTPClient *http.Client
}

// Defaults.
const (
	DefaultMaxTokens = 2048
	DefaultRetries   = 2
	DefaultBackoff   = 500 * time.Millisecond
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Request is one structured-output completion.
type Request struct {
	Messages []Message
	// SchemaName and Schema describe the JSON the model must return
	// (sent as response_format json_schema, strict).
	SchemaName string
	Schema     map[string]any
}

// Client calls the endpoint.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates cfg and returns a client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("llm: BaseURL and Model are required")
	}
	if cfg.Timeout <= 0 {
		return nil, errors.New("llm: Timeout must be positive")
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.Retries == 0 {
		cfg.Retries = DefaultRetries
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = DefaultBackoff
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{cfg: cfg, http: hc}, nil
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

type responseFormat struct {
	Type       string `json:"type"`
	JSONSchema struct {
		Name   string         `json:"name"`
		Strict bool           `json:"strict"`
		Schema map[string]any `json:"schema"`
	} `json:"json_schema"`
}

type chatBody struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	Temperature    float64        `json:"temperature"`
	Seed           *int64         `json:"seed,omitempty"`
	MaxTokens      int            `json:"max_tokens"`
	ResponseFormat responseFormat `json:"response_format"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// CompleteJSON sends the request and returns the content of the first choice,
// after validate accepted it (validate may be nil). The returned error is a
// *Error, or the context's error when ctx ended.
func (c *Client) CompleteJSON(ctx context.Context, req Request, validate func(content []byte) error) (string, error) {
	var rf responseFormat
	rf.Type = "json_schema"
	rf.JSONSchema.Name, rf.JSONSchema.Strict, rf.JSONSchema.Schema = req.SchemaName, true, req.Schema
	payload, err := json.Marshal(chatBody{
		Model: c.cfg.Model, Messages: req.Messages, Temperature: c.cfg.Temperature, Seed: c.cfg.Seed,
		MaxTokens: c.cfg.MaxTokens, ResponseFormat: rf,
	})
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}

	var last *Error
	backoff := c.cfg.Backoff
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		content, retry, err := c.attempt(ctx, payload)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err == nil {
			if validate != nil {
				if verr := validate([]byte(content)); verr != nil {
					return "", &Error{Code: CodeOutputInvalid, Msg: "content does not conform to the schema", Err: verr}
				}
			}
			return content, nil
		}
		last = err
		if !retry {
			break
		}
	}
	return "", last
}

// attempt makes one HTTP call. retry says whether a failure is worth another
// attempt.
func (c *Client) attempt(ctx context.Context, payload []byte) (content string, retry bool, _ *Error) {
	actx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", false, &Error{Code: CodeUpstreamUnavailable, Msg: "build request", Err: err}
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	if c.cfg.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		return "", true, transportError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", true, transportError(err)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return "", true, &Error{Code: CodeUpstreamUnavailable, Msg: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, snippet(body))}
	case resp.StatusCode >= 400:
		return "", false, &Error{Code: CodeUpstreamUnavailable, Msg: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, snippet(body))}
	}
	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil || len(cr.Choices) == 0 {
		return "", false, &Error{Code: CodeOutputInvalid, Msg: "response has no choices: " + snippet(body), Err: err}
	}
	ch := cr.Choices[0]
	if ch.FinishReason == "length" {
		return "", false, &Error{Code: CodeOutputInvalid, Msg: "completion was cut off at the token limit"}
	}
	return ch.Message.Content, false, nil
}

func transportError(err error) *Error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &Error{Code: CodeUpstreamTimeout, Msg: "no answer in time", Err: err}
	}
	return &Error{Code: CodeUpstreamUnavailable, Msg: "request failed", Err: err}
}

func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "..."
	}
	return s
}
