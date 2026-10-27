// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

var boundedReq = Request{
	Messages:   []Message{{RoleSystem, "s"}, {RoleUser, "u"}},
	SchemaName: "out",
	Schema: map[string]any{"type": "object", "properties": map[string]any{
		"a": map[string]any{"type": "string", "maxLength": 10},
		"b": map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 5}},
	}},
}

// A server that rejects maxLength in strict mode (HTTP 400) must not fail the
// request: the client retries once without the bounds and remembers.
func TestServerRejectingStringBoundsIsDegradedNotFailed(t *testing.T) {
	var calls, bounded atomic.Int32
	var logs bytes.Buffer
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "maxLength") {
			bounded.Add(1)
			http.Error(w, `{"error":{"message":"'maxLength' is not permitted"}}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(reply(`{}`, "stop")))
	}, func(c *Config) { c.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
	for i := 0; i < 2; i++ {
		if _, err := c.CompleteJSON(context.Background(), boundedReq, nil); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	// First request: bounded (400) + degraded; second: degraded only.
	if calls.Load() != 3 || bounded.Load() != 1 {
		t.Fatalf("calls %d, bounded %d, want 3 and 1", calls.Load(), bounded.Load())
	}
	if !c.StringBoundsOff() || strings.Count(logs.String(), "LLM_STRING_BOUNDS_UNSUPPORTED") != 1 {
		t.Fatalf("bounds off %v, log %q: want off and exactly one warning", c.StringBoundsOff(), logs.String())
	}
	if !HasStringBounds(boundedReq.Schema) {
		t.Error("the caller's schema was modified")
	}
}

// A 400 with another cause (a prompt over the context size) must not switch
// the bounds off: the stripped request is refused too.
func TestBadRequestOfBothFormsKeepsTheBoundsAndFails(t *testing.T) {
	var calls, bounded atomic.Int32
	var logs bytes.Buffer
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "maxLength") {
			bounded.Add(1)
		}
		http.Error(w, "prompt exceeds the context size", http.StatusBadRequest)
	}, func(c *Config) { c.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
	for i := 0; i < 2; i++ {
		_, err := c.CompleteJSON(context.Background(), boundedReq, nil)
		if !errors.Is(err, ErrUpstreamUnavailable) {
			t.Fatalf("request %d: err %v", i, err)
		}
	}
	// Each request: bounded (400), then stripped (400). Both times bounded first.
	if calls.Load() != 4 || bounded.Load() != 2 {
		t.Fatalf("calls %d, bounded %d, want 4 and 2", calls.Load(), bounded.Load())
	}
	if c.StringBoundsOff() || logs.Len() != 0 {
		t.Fatalf("bounds off %v, log %q: a 400 for both forms must change nothing", c.StringBoundsOff(), logs.String())
	}
}

func TestRequestMaxTokensLowersButNeverRaisesTheCeiling(t *testing.T) {
	var got []float64
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = append(got, b["max_tokens"].(float64))
		_, _ = w.Write([]byte(reply(`{}`, "stop")))
	}, func(c *Config) { c.MaxTokens = 500 })
	for _, n := range []int{0, 100, 9000} {
		r := req
		r.MaxTokens = n
		if _, err := c.CompleteJSON(context.Background(), r, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 3 || got[0] != 500 || got[1] != 100 || got[2] != 500 {
		t.Fatalf("max_tokens = %v, want [500 100 500]", got)
	}
}

func TestCutOffCompletionIsFlagged(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(reply(`{"a`, "length"))) }, nil)
	_, err := c.CompleteJSON(context.Background(), req, nil)
	var le *Error
	if !errors.As(err, &le) || !le.CutOff || le.Code != CodeOutputInvalid {
		t.Fatalf("err %v", err)
	}
}
