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
	"github.com/rasonyang/aicc-knowledge/internal/s3store"
	"github.com/rasonyang/aicc-knowledge/internal/scan"
	"github.com/rasonyang/aicc-knowledge/internal/store"
)

// runScan runs migrations, then one full scan (or, with -watch, a scan every
// -interval until interrupted), and prints a summary per scan on stdout. It
// exits 0 when every scan finished without error, 1 otherwise.
func runScan(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	watch := fs.Bool("watch", false, "keep scanning every -interval until interrupted")
	interval := fs.Duration("interval", 5*time.Minute, "time between scans with -watch")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 || *interval <= 0 {
		fmt.Fprintln(stderr, "scan: takes no arguments and needs a positive -interval")
		return exitUsage
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireScan()
	}
	if err != nil {
		fmt.Fprintf(stderr, "scan: %v\n", err)
		return exitFailure
	}
	if err := runScanLoop(ctx, cfg, *watch, *interval, stdout); err != nil {
		fmt.Fprintf(stderr, "scan: %v\n", err)
		return exitFailure
	}
	return exitOK
}

func runScanLoop(ctx context.Context, cfg config.Config, watch bool, interval time.Duration, stdout io.Writer) error {
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
	client, err := s3store.New(ctx, s3store.Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket, Prefix: cfg.S3Prefix,
		UsePathStyle: cfg.S3UsePathStyle, AccessKeyID: cfg.S3AccessKeyID, SecretAccessKey: cfg.S3SecretAccessKey,
	})
	if err != nil {
		return err
	}
	sc := &scan.Scanner{Store: st, S3: client, Metrics: prov.Metrics, Log: slog.Default(), MaxObjectBytes: cfg.S3MaxObjectBytes}

	for {
		sum, err := sc.Run(ctx)
		printSummary(stdout, sum)
		switch {
		case err != nil && !watch:
			return err
		case err != nil:
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("scan failed", "error", err)
		case sum.Errors > 0 && !watch:
			return fmt.Errorf("%d object(s) failed; see the log", sum.Errors)
		}
		if !watch {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

func printSummary(w io.Writer, s scan.Summary) {
	fmt.Fprintf(w, "seen=%d new_versions=%d unchanged=%d metadata_only=%d removed=%d unsupported=%d ignored=%d oversize=%d errors=%d jobs_enqueued=%d stale_candidates=%d bytes_downloaded=%d\n",
		s.Seen, s.NewVersions, s.Unchanged, s.MetadataOnly, s.Removed, s.Unsupported, s.Ignored, s.Oversize, s.Errors,
		s.JobsEnqueued, s.StaleCandidates, s.BytesDownloaded)
}
