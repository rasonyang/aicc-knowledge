// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/httpapi"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
)

// factsMapping declares three tables over the Pricing sheet: plan_prices keyed
// by a STRING, plan_by_yearly keyed by a DECIMAL and plan_by_gb keyed by an
// INTEGER plus a DATE.
func factsMapping(t *testing.T) []byte {
	base := string(parsetest.PricingMapping(t))
	one := strings.TrimPrefix(base, "tables:\n")
	byYearly := strings.Replace(strings.Replace(one, "plan_prices", "plan_by_yearly", 1), "keyColumns: [plan]", "keyColumns: [yearly_price]", 1)
	byGB := strings.Replace(strings.Replace(one, "plan_prices", "plan_by_gb", 1), "keyColumns: [plan]", "keyColumns: [data_gb, valid_from]", 1)
	return []byte(base + byYearly + byGB)
}

type factsEnv struct {
	*env
	pe *parsetest.Env
}

// newFactsEnv serves the real API over a database that the real scan and parse
// workers fill from the real S3 server. The clock is 2024-06-30T20:00:00Z.
func newFactsEnv(t *testing.T, loc *time.Location) *factsEnv {
	t.Helper()
	ctx := context.Background()
	pe := parsetest.New(t)
	key, err := pe.Store.IssueAPIKey(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := obs.Setup(ctx, obs.Options{ServiceName: "test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })
	srv := httptest.NewServer(httpapi.New(pe.Store, p.Metrics, httpapi.Settings{
		DefaultTimeoutMS: 2000, MaxTimeoutMS: 5000, Location: loc,
		Now: func() time.Time { return time.Date(2024, 6, 30, 20, 0, 0, 0, time.UTC) },
	}, unusedSearcher()))
	t.Cleanup(srv.Close)
	return &factsEnv{env: &env{srv: srv, store: pe.Store, key: key, obs: p}, pe: pe}
}

type lookupBody struct {
	Status    string         `json:"status"`
	Row       map[string]any `json:"row"`
	SourceRef *string        `json:"sourceRef"`
	ValidFrom *string        `json:"validFrom"`
	ValidTo   *string        `json:"validTo"`
}

func (e *factsEnv) lookup(t *testing.T, table, body string) (int, []byte) {
	t.Helper()
	resp, raw := e.do(t, "POST", "/v1/facts/"+table+"/lookup", "Bearer "+e.key, body)
	return resp.StatusCode, raw
}

func (e *factsEnv) found(t *testing.T, table, body string) lookupBody {
	t.Helper()
	code, raw := e.lookup(t, table, body)
	if code != 200 {
		t.Fatalf("%s %s = %d %s", table, body, code, raw)
	}
	var lb lookupBody
	if err := json.Unmarshal(raw, &lb); err != nil {
		t.Fatal(err)
	}
	return lb
}

func TestFactsLookupRequiresAnAPIKey(t *testing.T) {
	e := newEnv(t)
	body := `{"key":{"plan":"Basic"}}`
	for name, auth := range map[string]string{"none": "", "unknown key": "Bearer nope", "wrong scheme": "Basic " + e.key} {
		resp, raw := e.do(t, "POST", "/v1/facts/plan_prices/lookup", auth, body)
		expectError(t, resp, raw, 401, "UNAUTHORIZED")
		_ = name
	}
}

func TestFactsLookupAgainstImportedWorkbook(t *testing.T) {
	e := newFactsEnv(t, time.UTC)
	e.pe.PutFixture("pricing.xlsx", "xlsx/pricing.xlsx")
	e.pe.Put("pricing.facts.yaml", factsMapping(t))
	if ps := e.pe.ScanParse(); ps.Parsed != 2 {
		t.Fatalf("parse = %+v", ps)
	}
	wbKey := e.pe.ObjectKey("pricing.xlsx")

	t.Run("FOUND in the first validity period", func(t *testing.T) {
		code, raw := e.lookup(t, "plan_prices", `{"key":{"plan":"Basic"},"at":"2024-03-01"}`)
		if code != 200 {
			t.Fatalf("%d %s", code, raw)
		}
		var top map[string]any
		if err := json.Unmarshal(raw, &top); err != nil || len(top) != 5 {
			t.Fatalf("keys = %v (%v), want exactly status,row,sourceRef,validFrom,validTo", top, err)
		}
		var lb lookupBody
		_ = json.Unmarshal(raw, &lb)
		if lb.Status != "FOUND" || lb.SourceRef == nil || *lb.SourceRef != wbKey+"#Pricing!A3:G3" ||
			lb.ValidFrom == nil || *lb.ValidFrom != "2024-01-01" || lb.ValidTo == nil || *lb.ValidTo != "2024-07-01" {
			t.Fatalf("body = %s", raw)
		}
		// Typed values: numbers and booleans are JSON numbers and booleans.
		if lb.Row["plan"] != "Basic" || lb.Row["monthly_price"] != 9.9 || lb.Row["yearly_price"] != float64(99) ||
			lb.Row["data_gb"] != float64(10) || lb.Row["unlimited"] != false ||
			lb.Row["valid_from"] != "2024-01-01" || lb.Row["valid_to"] != "2024-07-01" || len(lb.Row) != 7 {
			t.Errorf("row = %v", lb.Row)
		}
	})
	t.Run("FOUND in the other period, open-ended validTo is null", func(t *testing.T) {
		code, raw := e.lookup(t, "plan_prices", `{"key":{"plan":"Basic"},"at":"2024-07-01"}`)
		var top map[string]any
		_ = json.Unmarshal(raw, &top)
		if code != 200 || top["status"] != "FOUND" || top["validFrom"] != "2024-07-01" {
			t.Fatalf("%d %s", code, raw)
		}
		if v, present := top["validTo"]; !present || v != nil {
			t.Errorf("validTo = %v (present %v), want an explicit null", v, present)
		}
		if top["row"].(map[string]any)["monthly_price"] != 8.9 {
			t.Errorf("row = %v", top["row"])
		}
	})
	t.Run("half-open edge: validTo itself belongs to the next period", func(t *testing.T) {
		if lb := e.found(t, "plan_prices", `{"key":{"plan":"Pro"},"at":"2024-06-30"}`); lb.Row["monthly_price"] != 19.99 {
			t.Errorf("2024-06-30 = %v", lb.Row)
		}
		if lb := e.found(t, "plan_prices", `{"key":{"plan":"Pro"},"at":"2024-07-01"}`); lb.Row["monthly_price"] != 0.1 {
			t.Errorf("2024-07-01 = %v", lb.Row)
		}
	})
	t.Run("NOT_FOUND carries explicit nulls", func(t *testing.T) {
		for _, body := range []string{`{"key":{"plan":"Nope"},"at":"2024-03-01"}`, `{"key":{"plan":"Basic"},"at":"2023-12-31"}`} {
			code, raw := e.lookup(t, "plan_prices", body)
			var top map[string]any
			_ = json.Unmarshal(raw, &top)
			if code != 200 || top["status"] != "NOT_FOUND" || len(top) != 5 {
				t.Fatalf("%s -> %d %s", body, code, raw)
			}
			for _, k := range []string{"row", "sourceRef", "validFrom", "validTo"} {
				if v, present := top[k]; !present || v != nil {
					t.Errorf("%s = %v (present %v), want null", k, v, present)
				}
			}
		}
	})
	t.Run("the default date is today in KB_TIMEZONE", func(t *testing.T) {
		// 2024-06-30T20:00Z: still June 30 in UTC, already July 1 in Shanghai.
		if lb := e.found(t, "plan_prices", `{"key":{"plan":"Basic"}}`); lb.Row["monthly_price"] != 9.9 {
			t.Errorf("UTC default = %v", lb.Row)
		}
		sh, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		e2 := newFactsEnvOver(t, e, sh)
		if lb := e2.found(t, "plan_prices", `{"key":{"plan":"Basic"}}`); lb.Row["monthly_price"] != 8.9 {
			t.Errorf("Shanghai default = %v", lb.Row)
		}
	})
	t.Run("422 for keys that do not fit", func(t *testing.T) {
		type tc struct {
			name, table, body string
			missing, unknown  []string
			invalid           []string // columns
		}
		for _, c := range []tc{
			{"missing", "plan_prices", `{"key":{"other":"x"}}`, []string{"plan"}, []string{"other"}, nil},
			{"extra", "plan_prices", `{"key":{"plan":"Basic","extra":1}}`, nil, []string{"extra"}, nil},
			{"non-key column", "plan_prices", `{"key":{"plan":"Basic","monthly_price":9.9}}`, nil, []string{"monthly_price"}, nil},
			{"number for STRING", "plan_prices", `{"key":{"plan":5}}`, nil, nil, []string{"plan"}},
			{"null", "plan_prices", `{"key":{"plan":null}}`, nil, nil, []string{"plan"}},
			{"partial composite", "plan_by_gb", `{"key":{"data_gb":10}}`, []string{"valid_from"}, nil, nil},
			{"bad integer", "plan_by_gb", `{"key":{"data_gb":10.5,"valid_from":"2024-01-01"}}`, nil, nil, []string{"data_gb"}},
			{"bad date", "plan_by_gb", `{"key":{"data_gb":10,"valid_from":"2024-1-1"}}`, nil, nil, []string{"valid_from"}},
			{"bad decimal", "plan_by_yearly", `{"key":{"yearly_price":"99,00"}}`, nil, nil, []string{"yearly_price"}},
		} {
			code, raw := e.lookup(t, c.table, c.body)
			var eb errBody
			_ = json.Unmarshal(raw, &eb)
			if code != 422 || eb.Error.Code != "VALIDATION_FAILED" || eb.Error.Message == "" {
				t.Errorf("%s: %d %s", c.name, code, raw)
				continue
			}
			strs := func(k string) []string {
				out := []string{}
				for _, v := range eb.Error.Params[k].([]any) {
					out = append(out, v.(string))
				}
				return out
			}
			var invalid []string
			for _, v := range eb.Error.Params["invalid"].([]any) {
				invalid = append(invalid, v.(map[string]any)["column"].(string))
			}
			if !slices.Equal(strs("missing"), append([]string{}, c.missing...)) || !slices.Equal(strs("unknown"), append([]string{}, c.unknown...)) ||
				!slices.Equal(invalid, c.invalid) {
				t.Errorf("%s: params = %v", c.name, eb.Error.Params)
			}
		}
		code, raw := e.lookup(t, "plan_prices", `{"key":{}}`)
		if code != 422 {
			t.Errorf("empty key: %d %s", code, raw)
		}
	})
	t.Run("DECIMAL, INTEGER and DATE keys are coerced and compared by value", func(t *testing.T) {
		for _, body := range []string{
			`{"key":{"yearly_price":99}}`, `{"key":{"yearly_price":99.0}}`, `{"key":{"yearly_price":"99"}}`, `{"key":{"yearly_price":"99.00"}}`,
		} {
			if lb := e.found(t, "plan_by_yearly", body+""); lb.Status != "FOUND" || lb.Row["plan"] != "Basic" || lb.Row["monthly_price"] != 9.9 {
				t.Errorf("%s -> %+v", body, lb)
			}
		}
		if lb := e.found(t, "plan_by_yearly", `{"key":{"yearly_price":"199.90"},"at":"2024-03-01"}`); lb.Row["plan"] != "Pro" {
			t.Errorf("199.90 -> %+v", lb)
		}
		if lb := e.found(t, "plan_by_yearly", `{"key":{"yearly_price":"123456.789"},"at":"2024-08-01"}`); lb.Row["plan"] != "Pro" {
			t.Errorf("123456.789 -> %+v", lb)
		}
		for _, body := range []string{
			`{"key":{"data_gb":10,"valid_from":"2024-07-01"},"at":"2024-08-01"}`, `{"key":{"data_gb":"10","valid_from":"2024-07-01"},"at":"2024-08-01"}`,
		} {
			if lb := e.found(t, "plan_by_gb", body); lb.Status != "FOUND" || lb.Row["monthly_price"] != 8.9 {
				t.Errorf("%s -> %+v", body, lb)
			}
		}
		if lb := e.found(t, "plan_by_gb", `{"key":{"data_gb":10,"valid_from":"2024-07-01"},"at":"2024-03-01"}`); lb.Status != "NOT_FOUND" {
			t.Errorf("key valid only from July, asked in March -> %+v", lb)
		}
	})
	t.Run("unknown table is 404 FACT_TABLE_NOT_FOUND", func(t *testing.T) {
		resp, raw := e.do(t, "POST", "/v1/facts/nope/lookup", "Bearer "+e.key, `{"key":{"plan":"Basic"}}`)
		got := expectError(t, resp, raw, 404, "FACT_TABLE_NOT_FOUND")
		if got.Error.Params["table"] != "nope" {
			t.Errorf("params = %v", got.Error.Params)
		}
	})

	// The workbook changes to one with an invalid row: 503, the old rows are not served.
	e.pe.Put("pricing.xlsx", parsetest.EditPricing(t, func(f *excelize.File) {
		if err := f.SetCellValue("Pricing", "B3", "not a price"); err != nil {
			t.Fatal(err)
		}
	}))
	e.pe.ScanParse()
	for _, table := range []string{"plan_prices", "plan_by_yearly", "plan_by_gb"} {
		resp, raw := e.do(t, "POST", "/v1/facts/"+table+"/lookup", "Bearer "+e.key, `{"key":{"plan":"Basic"},"at":"2024-03-01"}`)
		got := expectError(t, resp, raw, 503, "FACT_TABLE_UNAVAILABLE")
		if got.Error.Params["table"] != table || got.Error.Params["reason"] != "FACTS_INVALID" {
			t.Errorf("%s params = %v", table, got.Error.Params)
		}
	}
	// 503 wins over a key that would be a 422: the table cannot be served at all.
	resp, raw := e.do(t, "POST", "/v1/facts/plan_prices/lookup", "Bearer "+e.key, `{"key":{"bogus":1}}`)
	expectError(t, resp, raw, 503, "FACT_TABLE_UNAVAILABLE")

	// The fixed workbook brings it back, with the new values only.
	e.pe.Put("pricing.xlsx", parsetest.EditPricing(t, func(f *excelize.File) {
		if err := f.SetCellValue("Pricing", "B3", 7.5); err != nil {
			t.Fatal(err)
		}
	}))
	e.pe.ScanParse()
	if lb := e.found(t, "plan_prices", `{"key":{"plan":"Basic"},"at":"2024-03-01"}`); lb.Status != "FOUND" || lb.Row["monthly_price"] != 7.5 {
		t.Fatalf("after the fix: %+v", lb)
	}

	// Removing the mapping makes the tables unavailable again.
	e.pe.S3.Delete("pricing.facts.yaml")
	e.pe.ScanParse()
	resp, raw = e.do(t, "POST", "/v1/facts/plan_prices/lookup", "Bearer "+e.key, `{"key":{"plan":"Basic"}}`)
	got := expectError(t, resp, raw, 503, "FACT_TABLE_UNAVAILABLE")
	if got.Error.Params["reason"] != "SOURCE_REMOVED" {
		t.Errorf("params = %v", got.Error.Params)
	}

	// Lookup outcomes are counted.
	rec := httptest.NewRecorder()
	e.obs.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	text := rec.Body.String()
	for _, want := range []string{
		obs.MetricFactsLookupsTotal + `{status="FOUND"`, obs.MetricFactsLookupsTotal + `{status="NOT_FOUND"`,
		obs.MetricFactsLookupsTotal + `{status="INVALID"`, obs.MetricFactsLookupsTotal + `{status="TABLE_NOT_FOUND"`,
		obs.MetricFactsLookupsTotal + `{status="TABLE_UNAVAILABLE"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
}

// newFactsEnvOver serves the same database and API key under another time zone.
func newFactsEnvOver(t *testing.T, base *factsEnv, loc *time.Location) *factsEnv {
	t.Helper()
	srv := httptest.NewServer(httpapi.New(base.pe.Store, base.obs.Metrics, httpapi.Settings{
		DefaultTimeoutMS: 2000, MaxTimeoutMS: 5000, Location: loc,
		Now: func() time.Time { return time.Date(2024, 6, 30, 20, 0, 0, 0, time.UTC) },
	}, unusedSearcher()))
	t.Cleanup(srv.Close)
	return &factsEnv{env: &env{srv: srv, store: base.store, key: base.key, obs: base.obs}, pe: base.pe}
}
