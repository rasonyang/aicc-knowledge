// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/rasonyang/aicc-knowledge/internal/config"
	"github.com/rasonyang/aicc-knowledge/internal/store"
)

// runCreateAPIKey issues a bearer key. The secret goes to stdout exactly once;
// the database keeps only its SHA-256 digest.
func runCreateAPIKey(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-api-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "label for the key, for example the caller's name (required)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *name == "" {
		fmt.Fprintln(stderr, "create-api-key: -name is required")
		return exitUsage
	}
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireDatabase()
	}
	if err != nil {
		fmt.Fprintf(stderr, "create-api-key: %v\n", err)
		return exitFailure
	}
	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		fmt.Fprintf(stderr, "create-api-key: %v\n", err)
		return exitFailure
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		fmt.Fprintf(stderr, "create-api-key: %v\n", err)
		return exitFailure
	}
	secret, err := st.IssueAPIKey(ctx, *name)
	if err != nil {
		fmt.Fprintf(stderr, "create-api-key: %v\n", err)
		return exitFailure
	}
	fmt.Fprintln(stderr, "API key created. This is the only time the secret is shown.")
	fmt.Fprintln(stdout, secret)
	return exitOK
}
