// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Check is one dependency probe of /readyz.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

// OpsHandler serves the unauthenticated ops listener: /metrics, /healthz and
// /readyz. It is not part of the API contract and must stay off public
// networks.
func OpsHandler(metrics http.Handler, checks []Check, timeout time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, http.StatusOK, "ok\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		ready := true
		for _, c := range checks {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			err := c.Fn(ctx)
			cancel()
			if err != nil {
				ready = false
				fmt.Fprintf(&b, "%s: %s\n", c.Name, oneLine(err.Error()))
				continue
			}
			fmt.Fprintf(&b, "%s: ok\n", c.Name)
		}
		if !ready {
			writeText(w, http.StatusServiceUnavailable, "ready: no\n"+b.String())
			return
		}
		writeText(w, http.StatusOK, "ready: yes\n"+b.String())
	})
	return mux
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// HTTPHealthCheck probes GET {baseURL}{path} and expects 200.
//
// Meilisearch is probed at /health. TEI is probed at /info, not /health: TEI's
// /health runs an inference and queues behind in-flight embeddings, so under
// load it took 15 s while /info answered in 5 ms, and /readyz would flap
// exactly when the service is busiest.
func HTTPHealthCheck(name, baseURL, path string, client *http.Client) Check {
	return Check{Name: name, Fn: func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("unreachable: %w", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s returned %d", path, resp.StatusCode)
		}
		return nil
	}}
}
