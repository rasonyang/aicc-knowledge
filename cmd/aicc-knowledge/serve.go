// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/config"
	"github.com/rasonyang/aicc-knowledge/internal/embed"
	"github.com/rasonyang/aicc-knowledge/internal/httpapi"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/search"
	"github.com/rasonyang/aicc-knowledge/internal/store"
)

func runServe(ctx context.Context, args []string, _, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "serve: takes no arguments; configure it with KB_* environment variables")
		return exitUsage
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireServe()
	}
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return exitFailure
	}
	if err := serve(ctx, cfg); err != nil {
		slog.Error("serve failed", "error", err)
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return exitFailure
	}
	return exitOK
}

func serve(ctx context.Context, cfg config.Config) error {
	prov, err := obs.Setup(ctx, obs.Options{
		ServiceName: cfg.ServiceName, Version: version, LogLevel: cfg.LogLevel,
		OTLPEndpoint: cfg.OTLPEndpoint, Dev: cfg.IsDev(),
	})
	if err != nil {
		return fmt.Errorf("set up observability: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = prov.Shutdown(sctx)
	}()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	applied, latest, err := st.MigrationVersions(ctx)
	if err != nil {
		return err
	}
	slog.Info("migrations applied", "version", applied, "latest", latest)

	searcher, err := newSearcher(cfg, prov.Metrics)
	if err != nil {
		return err
	}

	client := &http.Client{}
	checks := []httpapi.Check{
		{Name: "postgres", Fn: st.Ping},
		httpapi.HTTPHealthCheck("meilisearch", cfg.MeiliURL, "/health", client),
		httpapi.HTTPHealthCheck("tei", cfg.TEIURL, "/info", client),
	}
	apiSrv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.New(st, prov.Metrics, httpapi.Settings{
			ScopeKeys:        cfg.SearchScopeKeys,
			DefaultTimeoutMS: cfg.SearchDefaultTimeoutMS,
			MaxTimeoutMS:     cfg.SearchMaxTimeoutMS,
			Location:         cfg.Location(),
		}, searcher),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	opsSrv := &http.Server{
		Addr:              cfg.OpsAddr,
		Handler:           httpapi.OpsHandler(prov.MetricsHandler, checks, cfg.ReadyzTimeout),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 2)
	for name, srv := range map[string]*http.Server{"api": apiSrv, "ops": opsSrv} {
		go func() {
			slog.Info("listening", "listener", name, "addr", srv.Addr, "version", version)
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s listener: %w", name, err)
			}
		}()
	}

	var serveErr error
	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case serveErr = <-errc:
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(serveErr, apiSrv.Shutdown(sctx), opsSrv.Shutdown(sctx))
}

// newSearcher wires the query path: the query-side TEI (KB_TEI_URL, never the
// batch instance) and Meilisearch. Neither client retries, and no client-level
// timeout is set: the request budget is the context deadline.
func newSearcher(cfg config.Config, m *obs.Metrics) (*search.Searcher, error) {
	emb, err := embed.New(embed.Config{BaseURL: cfg.TEIURL, Dimensions: cfg.EmbeddingDimensions, HTTPClient: &http.Client{}})
	if err != nil {
		return nil, err
	}
	// Discover TEI's batch limit now, so the first query does not pay for it.
	// TEI may still be starting; the client retries the discovery on first use.
	wctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, _ = emb.MaxBatch(wctx)
	cancel()
	// No keep-alive to Meilisearch on the query path. Measured against the
	// compose stack: on a reused connection about every second search stalled
	// for ~40 ms (search p50 43 ms, p90 52 ms; the classic delayed-ACK stall on
	// a response sent in two small writes). With a fresh connection per search:
	// p50 3.6 ms, p90 4.1 ms. The handshake costs far less than the stall.
	ms, err := meili.New(meili.Config{BaseURL: cfg.MeiliURL, APIKey: cfg.MeiliAPIKey,
		HTTPClient: &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}})
	if err != nil {
		return nil, err
	}
	return &search.Searcher{
		Meili: ms, Embedder: emb, ScopeKeys: cfg.SearchScopeKeys,
		ThresholdEN: cfg.SearchThresholdEN, ThresholdZH: cfg.SearchThresholdZH, IndexPrefix: cfg.MeiliIndexPrefix, Metrics: m,
	}, nil
}
