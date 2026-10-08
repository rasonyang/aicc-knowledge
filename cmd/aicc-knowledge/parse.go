// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/config"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/parse"
	"github.com/rasonyang/aicc-knowledge/internal/s3store"
	"github.com/rasonyang/aicc-knowledge/internal/store"
)

// runParse runs migrations, then claims PARSE jobs until none remains (or,
// with -watch, keeps polling every -interval until interrupted) and prints a
// summary per pass on stdout. With -retry-failed it first resets every
// current PARSE_FAILED version to DISCOVERED and re-arms its job; PARSED,
// UNSUPPORTED and REMOVED versions are never touched. It exits 0 when every
// job succeeded or was skipped, 1 when any version failed to parse or any job
// hit an infrastructure error.
func runParse(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	fs.SetOutput(stderr)
	watch := fs.Bool("watch", false, "keep polling for parse jobs every -interval until interrupted")
	interval := fs.Duration("interval", 10*time.Second, "time between polls with -watch")
	retry := fs.Bool("retry-failed", false, "first reset every current PARSE_FAILED version to DISCOVERED and re-queue its job")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 || *interval <= 0 {
		fmt.Fprintln(stderr, "parse: takes no arguments and needs a positive -interval")
		return exitUsage
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireParse()
	}
	if err != nil {
		fmt.Fprintf(stderr, "parse: %v\n", err)
		return exitFailure
	}
	ok, err := runParseLoop(ctx, cfg, *watch, *retry, *interval, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "parse: %v\n", err)
		return exitFailure
	}
	if !ok {
		return exitFailure
	}
	return exitOK
}

// runParseLoop reports whether every job of the (last) pass was clean.
func runParseLoop(ctx context.Context, cfg config.Config, watch, retry bool, interval time.Duration, stdout io.Writer) (bool, error) {
	prov, err := obs.Setup(ctx, obs.Options{
		ServiceName: cfg.ServiceName, Version: version, LogLevel: cfg.LogLevel,
		OTLPEndpoint: cfg.OTLPEndpoint, Dev: cfg.IsDev(),
	})
	if err != nil {
		return false, fmt.Errorf("set up observability: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = prov.Shutdown(sctx)
	}()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		return false, err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return false, err
	}
	client, err := s3store.New(ctx, s3store.Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket, Prefix: cfg.S3Prefix,
		UsePathStyle: cfg.S3UsePathStyle, AccessKeyID: cfg.S3AccessKeyID, SecretAccessKey: cfg.S3SecretAccessKey,
	})
	if err != nil {
		return false, err
	}
	w := &parse.Worker{Store: st, S3: client, Metrics: prov.Metrics, Log: slog.Default(), MaxObjectBytes: cfg.S3MaxObjectBytes}

	if retry {
		rs, err := w.RetryFailed(ctx)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(stdout, "retried_versions=%d jobs_queued=%d\n", rs.Versions, rs.JobsQueued)
	}
	for {
		sum, err := w.Run(ctx)
		fmt.Fprintf(stdout, "claimed=%d parsed=%d parse_failed=%d skipped=%d errors=%d\n",
			sum.Claimed, sum.Parsed, sum.Failed, sum.Skipped, sum.Errors)
		clean := sum.Failed == 0 && sum.Errors == 0
		switch {
		case err != nil && ctx.Err() != nil:
			return clean, nil
		case err != nil && !watch:
			return false, err
		case err != nil:
			slog.Error("parse pass failed", "error", err)
		case !watch:
			return clean, nil
		}
		select {
		case <-ctx.Done():
			return clean, nil
		case <-time.After(interval):
		}
	}
}
