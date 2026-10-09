// SPDX-License-Identifier: Apache-2.0

// Command kb-bench measures the latency of TEI embedding and reranking on the
// host it runs on, with synthetic text only. It is a separate tool so the
// product binary stays lean and the benchmark can be cross-compiled and
// copied to a target server on its own. scripts/bench/bench.sh drives it and
// starts the TEI containers.
//
//	kb-bench host    --out DIR [--image IMG]...
//	kb-bench wait    --url URL [--timeout 30m]
//	kb-bench measure --kind embed|rerank --url URL --model ID --out DIR
//	kb-bench summary --out DIR
//	kb-bench compare DIR_A DIR_B
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kb-bench:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kb-bench host|wait|measure|summary|compare ...")
	}
	ctx := context.Background()
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	switch cmd {
	case "host":
		out := fs.String("out", "", "result directory")
		var images listFlag
		fs.Var(&images, "image", "docker image to record the digest of (repeatable)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return err
		}
		return writeJSON(filepath.Join(*out, "host.json"), CollectHost(images))
	case "wait":
		url := fs.String("url", "", "TEI base URL")
		timeout := fs.Duration("timeout", 30*time.Minute, "give up after")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		d, err := NewTEI(*url).WaitReady(ctx, *timeout)
		if err != nil {
			return err
		}
		fmt.Printf("%.1f\n", d.Seconds())
		return nil
	case "measure":
		o := MeasureOpts{}
		fs.StringVar(&o.Kind, "kind", "embed", "embed or rerank")
		fs.StringVar(&o.URL, "url", "", "TEI base URL")
		fs.StringVar(&o.Model, "model", "", "model id, for the record")
		fs.StringVar(&o.Image, "image", "", "image, for the record")
		fs.StringVar(&o.OutDir, "out", "", "result directory")
		fs.Float64Var(&o.StartupSec, "startup-sec", 0, "seconds the container took to answer /info")
		fs.IntVar(&o.N, "n", 0, "sample size per series (default 200 embed, 100 rerank)")
		fs.IntVar(&o.ConcN, "conc-n", 0, "requests per concurrency level (default 100 embed, 40 rerank)")
		conc := fs.String("concurrency", "2,4", "comma-separated concurrency levels, empty for none")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if o.URL == "" || o.OutDir == "" || o.Model == "" {
			return fmt.Errorf("measure needs --url, --model and --out")
		}
		if o.N == 0 {
			o.N = map[string]int{"embed": 200, "rerank": 100}[o.Kind]
		}
		if o.ConcN == 0 {
			o.ConcN = map[string]int{"embed": 100, "rerank": 40}[o.Kind]
		}
		for _, f := range strings.Split(*conc, ",") {
			if f = strings.TrimSpace(f); f != "" {
				n, err := strconv.Atoi(f)
				if err != nil || n < 1 {
					return fmt.Errorf("bad --concurrency %q", f)
				}
				o.Concurrency = append(o.Concurrency, n)
			}
		}
		if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
			return err
		}
		_, err := Measure(ctx, o)
		return err
	case "summary":
		out := fs.String("out", "", "result directory")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		s, err := Summary(*out)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*out, "summary.md"), []byte(s), 0o644); err != nil {
			return err
		}
		fmt.Print(s)
		return nil
	case "compare":
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: kb-bench compare DIR_A DIR_B")
		}
		s, err := Compare(fs.Arg(0), fs.Arg(1))
		if err != nil {
			return err
		}
		fmt.Print(s)
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}
