// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/config"
	"github.com/rasonyang/aicc-knowledge/internal/generate"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/store"
)

// runGenerate runs migrations, then claims GENERATE jobs until none remains
// (or, with -watch, keeps polling every -interval until interrupted) and
// prints a summary per pass on stdout. -version queues one file version
// first (an already generated version is skipped). It exits 0 when no job hit
// an infrastructure error, 1 otherwise.
func runGenerate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	watch := fs.Bool("watch", false, "keep polling for generate jobs every -interval until interrupted")
	interval := fs.Duration("interval", 10*time.Second, "time between polls with -watch")
	versionID := fs.String("version", "", "queue the GENERATE job of this file version id first")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 || *interval <= 0 {
		fmt.Fprintln(stderr, "generate: takes no arguments and needs a positive -interval")
		return exitUsage
	}
	var version uuid.UUID
	if *versionID != "" {
		v, err := uuid.Parse(*versionID)
		if err != nil {
			fmt.Fprintf(stderr, "generate: -version %q is not a UUID\n", *versionID)
			return exitUsage
		}
		version = v
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireGenerate()
	}
	if err != nil {
		fmt.Fprintf(stderr, "generate: %v\n", err)
		return exitFailure
	}
	ok, err := runGenerateLoop(ctx, cfg, *watch, version, *interval, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "generate: %v\n", err)
		return exitFailure
	}
	if !ok {
		return exitFailure
	}
	return exitOK
}

// setupObs starts logging and metrics for a subcommand. The returned function
// shuts them down.
func setupObs(ctx context.Context, cfg config.Config) (*obs.Providers, func(), error) {
	prov, err := obs.Setup(ctx, obs.Options{
		ServiceName: cfg.ServiceName, Version: version, LogLevel: cfg.LogLevel,
		OTLPEndpoint: cfg.OTLPEndpoint, Dev: cfg.IsDev(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("set up observability: %w", err)
	}
	return prov, func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = prov.Shutdown(sctx)
	}, nil
}

// openStore opens the database and applies migrations.
func openStore(ctx context.Context, cfg config.Config) (*store.Store, error) {
	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func limitsOf(cfg config.Config) candidate.Limits {
	return candidate.Limits{MaxAnswerEN: cfg.GenerateMaxAnswerCharsEN, MaxAnswerZH: cfg.GenerateMaxAnswerCharsZH}
}

// runGenerateLoop reports whether every job of the (last) pass was clean.
func runGenerateLoop(ctx context.Context, cfg config.Config, watch bool, version uuid.UUID, interval time.Duration, stdout io.Writer) (bool, error) {
	prov, shutdown, err := setupObs(ctx, cfg)
	if err != nil {
		return false, err
	}
	defer shutdown()
	st, err := openStore(ctx, cfg)
	if err != nil {
		return false, err
	}
	defer st.Close()
	client, err := llm.New(llm.Config{
		BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel, Timeout: cfg.LLMTimeout,
		Temperature: cfg.LLMTemperature, Seed: cfg.LLMSeed,
	})
	if err != nil {
		return false, err
	}
	w := &generate.Worker{Store: st, LLM: client, Limits: limitsOf(cfg), Metrics: prov.Metrics, Log: slog.Default()}

	if version != uuid.Nil {
		queued, err := w.Enqueue(ctx, version)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(stdout, "version=%s job_queued=%t\n", version, queued)
	}
	for {
		sum, err := w.Run(ctx)
		perSection := time.Duration(0)
		if sum.Sections > 0 {
			perSection = sum.LLMTime / time.Duration(sum.Sections)
		}
		fmt.Fprintf(stdout, "claimed=%d generated=%d skipped=%d errors=%d sections=%d candidates=%d warnings=%d duplicates=%d llm_seconds=%.1f seconds_per_section=%.1f prompt_version=%s model=%s\n",
			sum.Claimed, sum.Generated, sum.Skipped, sum.Errors, sum.Sections, sum.Candidates, sum.Warnings, sum.Duplicates,
			sum.LLMTime.Seconds(), perSection.Seconds(), generate.PromptVersion, client.Model())
		clean := sum.Errors == 0
		switch {
		case err != nil && ctx.Err() != nil:
			return clean, nil
		case err != nil && !watch:
			return false, err
		case err != nil:
			slog.Error("generate pass failed", "error", err)
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
