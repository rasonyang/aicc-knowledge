// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

func clearKB(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "KB_") && !strings.HasPrefix(k, "KB_TEST_") {
			t.Setenv(k, "")
		}
	}
}

func invoke(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestEverySubcommandOfTheSpecIsRegistered(t *testing.T) {
	cmds := commands()
	for _, n := range []string{"serve", "scan", "parse", "generate", "export-review", "import-review",
		"publish", "rollback", "eval", "version", "create-api-key"} {
		if _, ok := cmds[n]; !ok {
			t.Errorf("subcommand %s missing", n)
		}
	}
	if len(cmds) != 11 {
		t.Errorf("subcommands = %d, want 11", len(cmds))
	}
	for name, c := range cmds {
		if c.run == nil || c.summary == "" {
			t.Errorf("subcommand %s is not implemented", name)
		}
	}
}

func TestVersionAndUsage(t *testing.T) {
	version = "v9.9.9-test"
	code, out, _ := invoke(t, "version")
	if code != 0 || out != "aicc-knowledge v9.9.9-test\n" {
		t.Fatalf("version = %d %q", code, out)
	}
	if code, _, stderr := invoke(t); code != exitUsage || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("no args = %d %q", code, stderr)
	}
	if code, _, stderr := invoke(t, "frobnicate"); code != exitUsage || !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Fatalf("unknown = %d %q", code, stderr)
	}
	code, out, _ = invoke(t, "help")
	if code != 0 || strings.Contains(out, "not implemented") || strings.Count(out, "\n  ") != 11 {
		t.Fatalf("help = %d, output:\n%s", code, out)
	}
}

func TestServeAndCreateAPIKeyFailFastNamingMissingConfig(t *testing.T) {
	clearKB(t)
	code, _, stderr := invoke(t, "serve")
	if code != exitFailure {
		t.Fatalf("serve exit = %d", code)
	}
	for _, k := range []string{"KB_DATABASE_URL", "KB_MEILI_URL", "KB_TEI_URL"} {
		if !strings.Contains(stderr, k) {
			t.Errorf("serve stderr does not name %s: %q", k, stderr)
		}
	}
	code, _, stderr = invoke(t, "create-api-key", "-name", "x")
	if code != exitFailure || !strings.Contains(stderr, "KB_DATABASE_URL") || strings.Contains(stderr, "KB_MEILI_URL") {
		t.Fatalf("create-api-key = %d %q", code, stderr)
	}
	if code, _, _ := invoke(t, "create-api-key"); code != exitUsage {
		t.Errorf("create-api-key without -name = %d, want usage", code)
	}
}

func TestCreateAPIKeyPrintsSecretOnceAndStoresOnlyTheDigest(t *testing.T) {
	clearKB(t)
	dsn := testdb.ScratchDSN(t, "cli")
	t.Setenv("KB_DATABASE_URL", dsn)
	code, stdout, stderr := invoke(t, "create-api-key", "-name", "aicc-prod")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	secret := strings.TrimSpace(stdout)
	if strings.Count(stdout, "\n") != 1 || len(secret) != 43 {
		t.Fatalf("stdout = %q, want exactly the secret on one line", stdout)
	}
	if strings.Contains(stderr, secret) {
		t.Error("the secret must not be repeated on stderr")
	}
	st, err := store.Open(context.Background(), dsn, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AuthenticateAPIKey(context.Background(), secret); err != nil {
		t.Fatalf("issued key does not authenticate: %v", err)
	}
	var n int
	if err := st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM api_keys WHERE name = 'aicc-prod'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("api_keys rows = %d, err = %v, want 1", n, err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitOK(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return string(b)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never became ready", url)
	return ""
}

// TestServeEndToEnd starts the real server against real PostgreSQL and
// Meilisearch, migrates a scratch database and talks to both listeners.
func TestServeEndToEnd(t *testing.T) {
	clearKB(t)
	meili, meiliKey := testdb.MeiliURL(t)
	tei := testdb.TEIURL()
	if tei == "" {
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer fake.Close()
		tei = fake.URL
		t.Log("KB_TEST_TEI_URL unset: TEI is a stand-in /health server")
	}
	apiAddr, opsAddr := freeAddr(t), freeAddr(t)
	t.Setenv("KB_DATABASE_URL", testdb.ScratchDSN(t, "serve"))
	t.Setenv("KB_MEILI_URL", meili)
	t.Setenv("KB_MEILI_API_KEY", meiliKey)
	t.Setenv("KB_TEI_URL", tei)
	t.Setenv("KB_HTTP_ADDR", apiAddr)
	t.Setenv("KB_OPS_ADDR", opsAddr)
	t.Setenv("KB_SEARCH_SCOPE_KEYS", "brand")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() { done <- run(ctx, []string{"serve"}, io.Discard, &stderr) }()

	if body := waitOK(t, "http://"+opsAddr+"/healthz"); body != "ok\n" {
		t.Fatalf("healthz = %q", body)
	}
	body := waitOK(t, "http://"+opsAddr+"/readyz")
	if !strings.Contains(body, "postgres: ok") || !strings.Contains(body, "meilisearch: ok") || !strings.Contains(body, "tei: ok") {
		t.Fatalf("readyz = %q", body)
	}
	resp, err := http.Post("http://"+apiAddr+"/v1/search", "application/json", strings.NewReader(`{"query":"q","language":"EN"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated search = %d, want 401", resp.StatusCode)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit = %d: %s", code, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down")
	}
}
