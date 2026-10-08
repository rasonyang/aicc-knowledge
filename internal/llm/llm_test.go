// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func reply(content, finish string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": finish}}})
	return string(b)
}

func newClient(t *testing.T, h http.HandlerFunc, mut func(*Config)) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := Config{BaseURL: srv.URL + "/v1/", Model: "m", Timeout: 2 * time.Second, Backoff: time.Millisecond}
	if mut != nil {
		mut(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

var req = Request{
	Messages:   []Message{{RoleSystem, "s"}, {RoleUser, "u"}},
	SchemaName: "out", Schema: map[string]any{"type": "object"},
}

func TestRequestShapeAndAuthHeader(t *testing.T) {
	var got struct {
		Path, Auth string
		Body       map[string]any
	}
	seed := int64(7)
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		got.Path, got.Auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got.Body)
		_, _ = w.Write([]byte(reply(`{"ok":true}`, "stop")))
	}, func(c *Config) { c.APIKey, c.Seed, c.Temperature = "secret", &seed, 0 })
	out, err := c.CompleteJSON(context.Background(), req, nil)
	if err != nil || out != `{"ok":true}` {
		t.Fatalf("%q %v", out, err)
	}
	if got.Path != "/v1/chat/completions" || got.Auth != "Bearer secret" {
		t.Errorf("path %q auth %q", got.Path, got.Auth)
	}
	rf := got.Body["response_format"].(map[string]any)
	js := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["name"] != "out" || js["strict"] != true || got.Body["model"] != "m" ||
		got.Body["seed"] != float64(7) || got.Body["temperature"] != float64(0) || len(got.Body["messages"].([]any)) != 2 {
		t.Errorf("body = %v", got.Body)
	}
}

func TestNoAuthHeaderWithoutKeyAndNoSeedWhenUnset(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if _, has := r.Header["Authorization"]; has {
			t.Error("Authorization sent without a key")
		}
		if _, has := b["seed"]; has {
			t.Error("seed sent when unset")
		}
		_, _ = w.Write([]byte(reply(`{}`, "stop")))
	}, nil)
	if _, err := c.CompleteJSON(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRetriesServerErrorsThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(reply(`{}`, "stop")))
	}, nil)
	if _, err := c.CompleteJSON(context.Background(), req, nil); err != nil || calls.Load() != 3 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestPersistentServerErrorIsUnavailableAfterBoundedRetries(t *testing.T) {
	var calls atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "down", http.StatusBadGateway)
	}, nil)
	_, err := c.CompleteJSON(context.Background(), req, nil)
	if !errors.Is(err, ErrUpstreamUnavailable) || calls.Load() != 3 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestClientErrorIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad model", http.StatusBadRequest)
	}, nil)
	_, err := c.CompleteJSON(context.Background(), req, nil)
	if !errors.Is(err, ErrUpstreamUnavailable) || calls.Load() != 1 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestTimeoutIsCodedAndRetried(t *testing.T) {
	var calls atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}, func(c *Config) { c.Timeout = 30 * time.Millisecond; c.Retries = 1 })
	_, err := c.CompleteJSON(context.Background(), req, nil)
	if !errors.Is(err, ErrUpstreamTimeout) || calls.Load() != 2 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestConnectionRefusedIsUnavailable(t *testing.T) {
	c, srv := newClient(t, func(w http.ResponseWriter, r *http.Request) {}, func(c *Config) { c.Retries = -1 })
	srv.Close()
	if _, err := c.CompleteJSON(context.Background(), req, nil); !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("err %v", err)
	}
}

func TestOutputInvalid(t *testing.T) {
	cases := map[string]string{
		"validator rejects": reply(`{"x":1}`, "stop"),
		"cut off":           reply(`{"x":`, "length"),
		"no choices":        `{"choices":[]}`,
		"not json body":     `<html>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(body)) }, nil)
			_, err := c.CompleteJSON(context.Background(), req, func(b []byte) error { return errors.New("nope") })
			if !errors.Is(err, ErrOutputInvalid) || calls.Load() != 1 {
				t.Fatalf("err %v calls %d", err, calls.Load())
			}
		})
	}
}

func TestContextCancelReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	}, nil)
	if _, err := c.CompleteJSON(ctx, req, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	for _, cfg := range []Config{{Model: "m", Timeout: time.Second}, {BaseURL: "http://x"}, {BaseURL: "http://x", Model: "m"}} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}
