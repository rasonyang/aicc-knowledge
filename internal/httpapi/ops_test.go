// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/httpapi"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

// teiURL returns a real TEI when KB_TEST_TEI_URL is set. Otherwise it starts a
// stand-in that only answers GET /info, which is all /readyz touches, and
// logs that it did. TEI is the one dependency that may be faked in tests.
func teiURL(t *testing.T) string {
	t.Helper()
	if u := testdb.TEIURL(); u != "" {
		return u
	}
	t.Log("KB_TEST_TEI_URL unset: using a stand-in /info server for TEI")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func deadURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

func opsServer(t *testing.T, st *store.Store, meili, tei string) *httptest.Server {
	t.Helper()
	p, err := obs.Setup(context.Background(), obs.Options{ServiceName: "ops-test", LogLevel: "error", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	client := &http.Client{}
	checks := []httpapi.Check{
		{Name: "postgres", Fn: st.Ping},
		httpapi.HTTPHealthCheck("meilisearch", meili, "/health", client),
		httpapi.HTTPHealthCheck("tei", tei, "/info", client),
	}
	srv := httptest.NewServer(httpapi.OpsHandler(p.MetricsHandler, checks, 1500*time.Millisecond))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.ScratchDSN(t, "ops"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestHealthz(t *testing.T) {
	st := openStore(t)
	// /healthz must not depend on anything: point every dependency at a dead port.
	srv := opsServer(t, st, deadURL(t), deadURL(t))
	code, body := get(t, srv.URL+"/healthz")
	if code != 200 || body != "ok\n" {
		t.Fatalf("/healthz = %d %q", code, body)
	}
}

func TestReadyzAllDependenciesUp(t *testing.T) {
	st := openStore(t)
	meili, _ := testdb.MeiliURL(t)
	srv := opsServer(t, st, meili, teiURL(t))
	code, body := get(t, srv.URL+"/readyz")
	if code != 200 {
		t.Fatalf("/readyz = %d %q", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	want := []string{"ready: yes", "postgres: ok", "meilisearch: ok", "tei: ok"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q, want %d", lines, len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestReadyzNamesTheFailingDependency(t *testing.T) {
	meili, _ := testdb.MeiliURL(t)
	cases := []struct {
		name string
		prep func(t *testing.T) (st *store.Store, meiliURL, tei string)
		fail string
	}{
		{"meilisearch down", func(t *testing.T) (*store.Store, string, string) {
			return openStore(t), deadURL(t), teiURL(t)
		}, "meilisearch"},
		{"tei down", func(t *testing.T) (*store.Store, string, string) {
			return openStore(t), meili, deadURL(t)
		}, "tei"},
		{"postgres down", func(t *testing.T) (*store.Store, string, string) {
			st := openStore(t)
			st.Close()
			return st, meili, teiURL(t)
		}, "postgres"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, m, tei := tc.prep(t)
			srv := opsServer(t, st, m, tei)
			code, body := get(t, srv.URL+"/readyz")
			if code != 503 {
				t.Fatalf("/readyz = %d %q, want 503", code, body)
			}
			lines := strings.Split(strings.TrimSpace(body), "\n")
			if len(lines) != 4 || lines[0] != "ready: no" {
				t.Fatalf("body = %q", body)
			}
			failing := 0
			for _, l := range lines[1:] {
				name, val, ok := strings.Cut(l, ": ")
				if !ok {
					t.Fatalf("line %q is not name: value", l)
				}
				if val != "ok" {
					failing++
					if name != tc.fail {
						t.Errorf("unexpected failing dependency %q", name)
					}
				} else if name == tc.fail {
					t.Errorf("%s should be failing", name)
				}
			}
			if failing != 1 {
				t.Errorf("failing dependencies = %d, want exactly 1", failing)
			}
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	st := openStore(t)
	srv := opsServer(t, st, deadURL(t), deadURL(t))
	code, body := get(t, srv.URL+"/metrics")
	if code != 200 || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("/metrics = %d, body lacks go_goroutines", code)
	}
}
