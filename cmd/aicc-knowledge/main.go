// SPDX-License-Identifier: Apache-2.0

// Command aicc-knowledge is the single binary of the service: the HTTP server
// (serve) and the admin operations that run beside it.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	_ "time/tzdata" // KB_TIMEZONE must resolve without a system zoneinfo (scratch images)
)

// version is stamped at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

type command struct {
	summary string
	run     func(ctx context.Context, args []string, stdout, stderr io.Writer) int
}

func commands() map[string]command {
	return map[string]command{
		"serve":          {summary: "run the HTTP API and the ops listener", run: runServe},
		"version":        {summary: "print the version", run: runVersion},
		"create-api-key": {summary: "issue a bearer API key (the secret is printed once)", run: runCreateAPIKey},
		"scan":           {summary: "scan S3 for new, changed and deleted files", run: runScan},
		"parse":          {summary: "parse discovered file versions into sections and facts", run: runParse},
		"generate":       {summary: "generate candidate Q&A with the LLM", run: runGenerate},
		"export-review":  {summary: "export candidates to a review workbook", run: runExportReview},
		"import-review":  {summary: "import a reviewed workbook", run: runImportReview},
		"publish":        {summary: "build and swap in a new index from approved rows", run: runPublish},
		"rollback":       {summary: "swap the previous publication back in", run: runRollback},
		"eval":           {summary: "measure recall, NO_MATCH precision and latency", run: runEval},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches one invocation and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		usage(stdout)
		return exitOK
	}
	if name == "--version" || name == "-version" {
		name = "version"
	}
	cmd, ok := commands()[name]
	if !ok {
		fmt.Fprintf(stderr, "aicc-knowledge: unknown command %q\n\n", name)
		usage(stderr)
		return exitUsage
	}
	return cmd.run(ctx, args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: aicc-knowledge <command> [flags]")
	fmt.Fprintln(w)
	cmds := commands()
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-14s %s\n", n, cmds[n].summary)
	}
}

func runVersion(_ context.Context, _ []string, stdout, _ io.Writer) int {
	fmt.Fprintf(stdout, "aicc-knowledge %s\n", version)
	return exitOK
}
