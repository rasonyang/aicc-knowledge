// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/config"
	"github.com/rasonyang/aicc-knowledge/internal/eval"
)

// runEval measures recall@3, NO_MATCH precision and per-stage latency over a
// question CSV. It runs in process (the same search code as the HTTP handler)
// unless -url is given. It exits 0 when the run completed, even if some
// questions failed; the report counts those errors.
func runEval(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "question CSV: question,language,expectedIds,scope (required)")
	language := fs.String("language", "", "evaluate only EN or ZH (default both)")
	url := fs.String("url", "", "base URL of a running service; default is in process (needs KB_MEILI_URL and KB_TEI_URL)")
	apiKey := fs.String("api-key", "", "bearer API key for -url")
	asJSON := fs.Bool("json", false, "print JSON instead of text")
	sweep := fs.Bool("sweep", false, "re-run with the NO_MATCH threshold disabled and report thresholds 0.50 to 0.95 (in process only)")
	minPrecision := fs.Float64("min-precision", 0.9, "NO_MATCH precision the sweep's best row must reach")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 || *in == "" {
		fmt.Fprintln(stderr, "eval: needs -in <questions.csv> and takes no arguments")
		return exitUsage
	}
	if *sweep && *url != "" {
		fmt.Fprintln(stderr, "eval: -sweep needs the in-process search (the threshold cannot be overridden over HTTP); drop -url")
		return exitUsage
	}
	if *url != "" && *apiKey == "" {
		fmt.Fprintln(stderr, "eval: -url needs -api-key")
		return exitUsage
	}
	filter, err := languagesOf(orDefault(*language, "ALL"), true)
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return exitUsage
	}
	f, err := os.Open(*in)
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return exitFailure
	}
	all, err := eval.ReadCSV(f)
	f.Close()
	if err != nil {
		fmt.Fprintf(stderr, "eval: %s: %v\n", *in, err)
		return exitFailure
	}
	var qs []eval.Question
	for _, q := range all {
		for _, l := range filter {
			if q.Language == l {
				qs = append(qs, q)
			}
		}
	}
	if len(qs) == 0 {
		fmt.Fprintf(stderr, "eval: no questions in %s for language %s\n", *in, orDefault(*language, "ALL"))
		return exitFailure
	}

	cfg, err := config.Load()
	if err == nil && *url == "" {
		err = cfg.RequireEval()
	}
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return exitFailure
	}
	var runner eval.Runner
	if *url != "" {
		runner = eval.HTTP{BaseURL: *url, APIKey: *apiKey}
	} else {
		s, err := newSearcher(cfg, nil)
		if err != nil {
			fmt.Fprintf(stderr, "eval: %v\n", err)
			return exitFailure
		}
		runner = eval.InProcess{Searcher: s, Timeout: time.Duration(cfg.SearchMaxTimeoutMS) * time.Millisecond}
	}

	var threshold *float64
	if *sweep {
		zero := 0.0
		threshold = &zero
	}
	outs := eval.Run(ctx, runner, qs, threshold)
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "eval: interrupted")
		return exitFailure
	}
	var reps []eval.Report
	if *sweep {
		reps = eval.SweepReports(outs, *minPrecision)
	} else {
		reps = eval.Reports(outs)
	}
	if *asJSON {
		if err := eval.WriteJSON(stdout, reps, *minPrecision); err != nil {
			fmt.Fprintf(stderr, "eval: %v\n", err)
			return exitFailure
		}
	} else {
		eval.WriteText(stdout, reps, *minPrecision)
	}
	for _, o := range outs {
		if o.Err != nil {
			fmt.Fprintf(stderr, "eval: line %d (%s %q): %v\n", o.Question.Line, o.Question.Language, o.Question.Text, o.Err)
		}
	}
	return exitOK
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
