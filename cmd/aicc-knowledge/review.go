// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/rasonyang/aicc-knowledge/internal/config"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/review"
)

// runExportReview writes the candidates awaiting review to an Excel workbook.
func runExportReview(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export-review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "path of the .xlsx workbook to write (required)")
	states := fs.String("state", string(domain.CandidatePendingReview), "comma-separated candidate states to export")
	language := fs.String("language", "", "export only this language: EN or ZH (default both)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 || *out == "" || !strings.HasSuffix(strings.ToLower(*out), ".xlsx") {
		fmt.Fprintln(stderr, "export-review: needs -out <path>.xlsx and takes no arguments")
		return exitUsage
	}
	lang, err := review.LanguageOf(*language)
	if err != nil {
		fmt.Fprintf(stderr, "export-review: %v\n", err)
		return exitUsage
	}
	var list []domain.CandidateState
	for _, s := range strings.Split(*states, ",") {
		if s = strings.ToUpper(strings.TrimSpace(s)); s != "" {
			list = append(list, domain.CandidateState(s))
		}
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireDatabase()
	}
	if err != nil {
		fmt.Fprintf(stderr, "export-review: %v\n", err)
		return exitFailure
	}
	st, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "export-review: %v\n", err)
		return exitFailure
	}
	defer st.Close()
	f, n, err := review.Export(ctx, st, review.ExportOptions{States: list, Language: lang})
	if err != nil {
		fmt.Fprintf(stderr, "export-review: %v\n", err)
		return exitFailure
	}
	defer f.Close()
	if err := f.SaveAs(*out); err != nil {
		fmt.Fprintf(stderr, "export-review: write %s: %v\n", *out, err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "exported=%d out=%s\n", n, *out)
	return exitOK
}

// runImportReview applies the decisions of a reviewed workbook. It exits 1
// when any row was refused (STALE, NOT_PENDING, invalid, ...), 0 otherwise.
func runImportReview(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import-review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "path of the reviewed .xlsx workbook (required)")
	reviewer := fs.String("reviewer", "", "name recorded in the audit trail (required)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 || *in == "" || strings.TrimSpace(*reviewer) == "" {
		fmt.Fprintln(stderr, "import-review: needs -in <path>.xlsx and -reviewer <name> and takes no arguments")
		return exitUsage
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireDatabase()
	}
	if err != nil {
		fmt.Fprintf(stderr, "import-review: %v\n", err)
		return exitFailure
	}
	prov, shutdown, err := setupObs(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "import-review: %v\n", err)
		return exitFailure
	}
	defer shutdown()
	st, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "import-review: %v\n", err)
		return exitFailure
	}
	defer st.Close()

	sum, err := review.Import(ctx, st, *in, review.ImportOptions{Reviewer: *reviewer, Limits: limitsOf(cfg), Metrics: prov.Metrics})
	if err != nil {
		var fe *review.FileError
		if errors.As(err, &fe) {
			fmt.Fprintf(stderr, "import-review: %v\n", err)
		} else {
			fmt.Fprintf(stderr, "import-review: %v (nothing was applied)\n", err)
		}
		return exitFailure
	}
	errs := sum.Errors()
	fmt.Fprintf(stdout, "rows=%d approved=%d rejected=%d edited=%d skipped=%d errors=%d\n", sum.Rows,
		sum.Counts[review.OutcomeApproved], sum.Counts[review.OutcomeRejected], sum.Counts[review.OutcomeEdited],
		sum.Counts[review.OutcomeSkipped], len(errs))
	for _, r := range errs {
		fmt.Fprintf(stdout, "error row=%d id=%s code=%s detail=%q\n", r.Row, r.ID, r.Outcome, r.Detail)
	}
	if len(errs) > 0 {
		return exitFailure
	}
	return exitOK
}
