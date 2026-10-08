// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/config"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/embed"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/publish"
	"github.com/rasonyang/aicc-knowledge/internal/store"
)

// newPublisher wires the offline path. Documents are embedded by the batch TEI
// (KB_TEI_BATCH_URL, default KB_TEI_URL) with retries, in small serial batches.
// The caller closes the store.
func newPublisher(ctx context.Context, cfg config.Config, m *obs.Metrics) (*publish.Publisher, *store.Store, error) {
	st, err := openStore(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	emb, err := embed.New(embed.Config{
		BaseURL: cfg.TEIBatchURL, Dimensions: cfg.EmbeddingDimensions,
		HTTPClient: &http.Client{Timeout: 2 * time.Minute},
		Retry:      embed.RetryPolicy{MaxAttempts: 4, Backoff: time.Second},
	})
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	ms, err := meili.New(meili.Config{BaseURL: cfg.MeiliURL, APIKey: cfg.MeiliAPIKey})
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return &publish.Publisher{
		Store: st, Meili: ms, Embedder: emb, Dimensions: cfg.EmbeddingDimensions,
		ScopeKeys: cfg.SearchScopeKeys, ScopePathKeys: cfg.ScopePathKeys(), S3Prefix: cfg.S3Prefix,
		RetainIndexes: cfg.PublishRetainIndexes, IndexPrefix: cfg.MeiliIndexPrefix, Metrics: m,
	}, st, nil
}

func languagesOf(s string, allowAll bool) ([]domain.Language, error) {
	switch strings.ToUpper(s) {
	case "ALL":
		if allowAll {
			return []domain.Language{domain.LanguageEN, domain.LanguageZH}, nil
		}
	case "EN":
		return []domain.Language{domain.LanguageEN}, nil
	case "ZH":
		return []domain.Language{domain.LanguageZH}, nil
	}
	if allowAll {
		return nil, fmt.Errorf("-language must be EN, ZH or ALL, got %q", s)
	}
	return nil, fmt.Errorf("-language must be EN or ZH, got %q", s)
}

// runPublish builds the approved candidates of a language into a new index and
// swaps it in. It exits 1 when any language failed.
func runPublish(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	language := fs.String("language", "", "EN, ZH or ALL (required)")
	allowEmpty := fs.Bool("allow-empty", false, "allow publishing an empty index over a live one that has documents")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	langs, err := languagesOf(*language, true)
	if err != nil || fs.NArg() > 0 {
		if err == nil {
			err = errors.New("takes no arguments")
		}
		fmt.Fprintf(stderr, "publish: %v\n", err)
		return exitUsage
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequirePublish()
	}
	if err != nil {
		fmt.Fprintf(stderr, "publish: %v\n", err)
		return exitFailure
	}
	prov, shutdown, err := setupObs(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "publish: %v\n", err)
		return exitFailure
	}
	defer shutdown()
	p, st, err := newPublisher(ctx, cfg, prov.Metrics)
	if err != nil {
		fmt.Fprintf(stderr, "publish: %v\n", err)
		return exitFailure
	}
	defer st.Close()

	code := exitOK
	for _, lang := range langs {
		res, err := p.Publish(ctx, lang, publish.PublishOptions{AllowEmpty: *allowEmpty})
		if err != nil {
			fmt.Fprintf(stdout, "language=%s outcome=FAILED code=%s publication=%s\n", lang, publish.CodeOf(err), idOrDash(res.PublicationID))
			fmt.Fprintf(stderr, "publish %s: %v\n", lang, err)
			code = exitFailure
			continue
		}
		sup := "-"
		if res.Superseded != nil {
			sup = res.Superseded.String()
		}
		fmt.Fprintf(stdout, "language=%s outcome=%s publication=%s items=%d superseded=%s pruned=%d duration=%s\n",
			lang, res.Outcome, idOrDash(res.PublicationID), res.Items, sup, len(res.Pruned), res.Duration.Round(time.Millisecond))
	}
	return code
}

func idOrDash(id uuid.UUID) string {
	if id == uuid.Nil {
		return "-"
	}
	return id.String()
}

// runRollback makes a SUPERSEDED publication LIVE again.
func runRollback(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.SetOutput(stderr)
	language := fs.String("language", "", "EN or ZH (required)")
	to := fs.String("to", "", "publication id to roll back to (default: the most recently superseded)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	langs, err := languagesOf(*language, false)
	var target *uuid.UUID
	if err == nil && *to != "" {
		id, perr := uuid.Parse(*to)
		if perr != nil {
			err = fmt.Errorf("-to %q is not a publication id", *to)
		}
		target = &id
	}
	if err != nil || fs.NArg() > 0 {
		if err == nil {
			err = errors.New("takes no arguments")
		}
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return exitUsage
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequirePublish()
	}
	if err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return exitFailure
	}
	prov, shutdown, err := setupObs(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return exitFailure
	}
	defer shutdown()
	p, st, err := newPublisher(ctx, cfg, prov.Metrics)
	if err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return exitFailure
	}
	defer st.Close()

	res, err := p.Rollback(ctx, langs[0], target)
	if err != nil {
		fmt.Fprintf(stdout, "language=%s outcome=FAILED code=%s\n", langs[0], publish.CodeOf(err))
		fmt.Fprintf(stderr, "rollback %s: %v\n", langs[0], err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "language=%s outcome=LIVE publication=%s superseded=%s items=%d rebuilt=%t pruned=%d duration=%s\n",
		langs[0], res.To, res.From, res.Items, res.Rebuilt, len(res.Pruned), res.Duration.Round(time.Millisecond))
	return exitOK
}
