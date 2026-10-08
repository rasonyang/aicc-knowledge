// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/httpapi"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/search"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

type env struct {
	srv   *httptest.Server
	store *store.Store
	key   string
	obs   *obs.Providers
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.ScratchDSN(t, "http"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := st.IssueAPIKey(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := obs.Setup(ctx, obs.Options{ServiceName: "test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })
	h := httpapi.New(st, p.Metrics, httpapi.Settings{
		ScopeKeys: []string{"brand", "channel"}, DefaultTimeoutMS: 2000, MaxTimeoutMS: 5000,
	}, unusedSearcher())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{srv: srv, store: st, key: key, obs: p}
}

type errBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Params  map[string]any `json:"params"`
	} `json:"error"`
}

func (e *env) do(t *testing.T, method, path, auth, body string) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// expectError asserts status, the envelope shape and the code.
func expectError(t *testing.T, resp *http.Response, raw []byte, status int, code string) errBody {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d; body %s", resp.StatusCode, status, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if len(top) != 1 || top["error"] == nil {
		t.Fatalf("envelope must have exactly one key \"error\": %s", raw)
	}
	var e errBody
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Error.Code != code {
		t.Fatalf("code = %q, want %q; body %s", e.Error.Code, code, raw)
	}
	if e.Error.Message == "" {
		t.Error("message is empty")
	}
	return e
}

func TestUnauthorized(t *testing.T) {
	e := newEnv(t)
	body := `{"query":"hello","language":"EN"}`
	for name, auth := range map[string]string{
		"no header":    "",
		"bad key":      "Bearer not-a-real-key",
		"wrong scheme": "Basic " + e.key,
		"empty bearer": "Bearer ",
		"key only":     e.key,
	} {
		for _, path := range []string{"/v1/search", "/v1/facts/prices/lookup"} {
			t.Run(name+" "+path, func(t *testing.T) {
				resp, raw := e.do(t, "POST", path, auth, body)
				expectError(t, resp, raw, 401, "UNAUTHORIZED")
				if resp.Header.Get("WWW-Authenticate") == "" {
					t.Error("missing WWW-Authenticate")
				}
			})
		}
	}
}

func TestRevokedKeyIsUnauthorized(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.Pool.Exec(context.Background(), `UPDATE api_keys SET revoked_at = now()`); err != nil {
		t.Fatal(err)
	}
	resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key, `{"query":"x","language":"EN"}`)
	expectError(t, resp, raw, 401, "UNAUTHORIZED")
}

func TestSearchValidation(t *testing.T) {
	e := newEnv(t)
	auth := "Bearer " + e.key
	cases := []struct {
		name, body, field string
	}{
		{"topK zero", `{"query":"q","language":"EN","topK":0}`, "topK"},
		{"topK four", `{"query":"q","language":"EN","topK":4}`, "topK"},
		{"topK negative", `{"query":"q","language":"EN","topK":-1}`, "topK"},
		{"empty query", `{"query":"","language":"EN"}`, "query"},
		{"blank query", `{"query":"   ","language":"EN"}`, "query"},
		{"missing query", `{"language":"EN"}`, "query"},
		{"missing language", `{"query":"q"}`, "language"},
		{"unknown language", `{"query":"q","language":"FR"}`, "language"},
		{"lowercase language", `{"query":"q","language":"en"}`, "language"},
		{"timeout zero", `{"query":"q","language":"EN","timeoutMs":0}`, "timeoutMs"},
		{"timeout over contract", `{"query":"q","language":"EN","timeoutMs":60001}`, "timeoutMs"},
		{"query too long", `{"query":"` + strings.Repeat("a", 1001) + `","language":"EN"}`, "query"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := e.do(t, "POST", "/v1/search", auth, tc.body)
			got := expectError(t, resp, raw, 422, "VALIDATION_FAILED")
			if got.Error.Params["field"] != tc.field {
				t.Errorf("params.field = %v, want %s", got.Error.Params["field"], tc.field)
			}
		})
	}
	for name, body := range map[string]string{
		"not json":      `{"query":`,
		"wrong type":    `{"query":5,"language":"EN"}`,
		"unknown field": `{"query":"q","language":"EN","limit":2}`,
		"trailing data": `{"query":"q","language":"EN"} {}`,
		"empty body":    ``,
	} {
		t.Run(name, func(t *testing.T) {
			resp, raw := e.do(t, "POST", "/v1/search", auth, body)
			expectError(t, resp, raw, 400, "VALIDATION_FAILED")
		})
	}
}

func TestUnknownScopeKey(t *testing.T) {
	e := newEnv(t)
	resp, raw := e.do(t, "POST", "/v1/search", "Bearer "+e.key,
		`{"query":"q","language":"EN","scope":{"brand":"acme","region":"eu","tier":"gold"}}`)
	got := expectError(t, resp, raw, 422, "UNKNOWN_SCOPE_KEY")
	keys, _ := got.Error.Params["keys"].([]any)
	if len(keys) != 2 || keys[0] != "region" || keys[1] != "tier" {
		t.Errorf("params.keys = %v, want exactly [region tier]", keys)
	}
	allowed, _ := got.Error.Params["allowedKeys"].([]any)
	if len(allowed) != 2 {
		t.Errorf("params.allowedKeys = %v, want the 2 configured keys", allowed)
	}
}

// unusedSearcher is wired into servers whose tests never reach the search
// stage (validation, auth, facts). Its dependencies are unreachable on purpose.
func unusedSearcher() httpapi.Searcher {
	m, _ := meili.New(meili.Config{BaseURL: "http://127.0.0.1:1"})
	return &search.Searcher{Meili: m, Embedder: failingEmbedder{}, ScopeKeys: []string{"brand", "channel"},
		ThresholdEN: 0.75, ThresholdZH: 0.75}
}

type failingEmbedder struct{}

func (failingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("test: embedder must not be called")
}

func TestFactsLookup(t *testing.T) {
	e := newEnv(t)
	auth := "Bearer " + e.key
	resp, raw := e.do(t, "POST", "/v1/facts/prices/lookup", auth, `{"key":{"sku":"A1"},"at":"2026-10-08"}`)
	got := expectError(t, resp, raw, 404, "FACT_TABLE_NOT_FOUND")
	if got.Error.Params["table"] != "prices" {
		t.Errorf("params.table = %v", got.Error.Params["table"])
	}
	resp, raw = e.do(t, "POST", "/v1/facts/prices/lookup", auth, `{"key":{}}`)
	expectError(t, resp, raw, 422, "VALIDATION_FAILED")
	resp, raw = e.do(t, "POST", "/v1/facts/prices/lookup", auth, `{}`)
	expectError(t, resp, raw, 422, "VALIDATION_FAILED")
	resp, raw = e.do(t, "POST", "/v1/facts/prices/lookup", auth, `{"key":{"sku":"A1"},"at":"yesterday"}`)
	expectError(t, resp, raw, 400, "VALIDATION_FAILED")
}

func TestRouterErrorsUseTheEnvelope(t *testing.T) {
	e := newEnv(t)
	auth := "Bearer " + e.key
	resp, raw := e.do(t, "GET", "/v1/nope", auth, "")
	expectError(t, resp, raw, 404, "NOT_FOUND")
	resp, raw = e.do(t, "GET", "/", "", "")
	expectError(t, resp, raw, 404, "NOT_FOUND")
	resp, raw = e.do(t, "GET", "/v1/search", auth, "")
	expectError(t, resp, raw, 405, "METHOD_NOT_ALLOWED")
	resp, raw = e.do(t, "PUT", "/v1/facts/prices/lookup", "", "")
	expectError(t, resp, raw, 405, "METHOD_NOT_ALLOWED")
}

func TestOpsEndpointsAreNotOnTheAPIListener(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/metrics", "/healthz", "/readyz"} {
		resp, raw := e.do(t, "GET", p, "", "")
		expectError(t, resp, raw, 404, "NOT_FOUND")
	}
}

func TestRequestMetricsUseTheRoutePattern(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/v1/search", "Bearer "+e.key, `{"query":"q","language":"FR"}`)
	e.do(t, "POST", "/v1/facts/abc/lookup", "Bearer "+e.key, `{"key":{"a":"b"}}`)
	e.do(t, "POST", "/v1/search", "", `{}`)
	rec := httptest.NewRecorder()
	e.obs.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	text := rec.Body.String()
	for _, want := range []string{
		`route="/v1/search"`, `route="/v1/facts/{table}/lookup"`, `code="422"`, `code="404"`, `code="401"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
	if strings.Contains(text, `route="/v1/facts/abc/lookup"`) {
		t.Error("metrics must not use the raw path as a label")
	}
}
