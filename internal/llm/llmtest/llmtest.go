// SPDX-License-Identifier: Apache-2.0

// Package llmtest is a scripted OpenAI-compatible chat-completions server for
// tests. The LLM is the one dependency tests may fake (PostgreSQL and
// Meilisearch never are): it is non-deterministic, slow and, in the cloud,
// costs money. Real-model checks live in tests gated on KB_TEST_LLM_URL.
package llmtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/llm"
)

// Call is one request the server received.
type Call struct {
	// N is the 1-based sequence number of the call.
	N        int
	Messages []llm.Message
}

// User returns the content of the first user message (the section text).
func (c Call) User() string {
	for _, m := range c.Messages {
		if m.Role == llm.RoleUser {
			return m.Content
		}
	}
	return ""
}

// Last returns the content of the last message.
func (c Call) Last() string { return c.Messages[len(c.Messages)-1].Content }

// Handler maps a call to the content of the assistant message. Return a
// Fail to answer with an HTTP error instead.
type Handler func(c Call) (content string, fail *Fail)

// Fail makes the server answer with this HTTP status.
type Fail struct{ Status int }

// Server is the fake endpoint.
type Server struct {
	*httptest.Server
	mu    sync.Mutex
	calls []Call
}

// New starts the server; it is closed when the test ends.
func New(t testing.TB, h Handler) *Server {
	t.Helper()
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		call := Call{N: len(s.calls) + 1, Messages: body.Messages}
		s.calls = append(s.calls, call)
		s.mu.Unlock()
		content, fail := h(call)
		if fail != nil {
			http.Error(w, "scripted failure", fail.Status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(s.Close)
	return s
}

// Calls returns the requests received so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Client returns a client for the server with a fast retry backoff.
func (s *Server) Client(t testing.TB) *llm.Client {
	t.Helper()
	c, err := llm.New(llm.Config{BaseURL: s.URL + "/v1", Model: "fake-model", Timeout: 10 * time.Second, Backoff: time.Millisecond, Retries: -1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Cand is one candidate of a scripted answer.
type Cand struct {
	Question string
	Alts     []string
	Answer   string
	Language string
}

// Reply renders candidates as the JSON content the model is asked for.
func Reply(cands ...Cand) string {
	type item struct {
		Question           string   `json:"question"`
		AlternateQuestions []string `json:"alternateQuestions"`
		Answer             string   `json:"answer"`
		Language           string   `json:"language"`
	}
	items := make([]item, 0, len(cands))
	for _, c := range cands {
		alts := c.Alts
		if alts == nil {
			alts = []string{}
		}
		items = append(items, item{c.Question, alts, c.Answer, c.Language})
	}
	b, _ := json.Marshal(map[string]any{"candidates": items})
	return string(b)
}
