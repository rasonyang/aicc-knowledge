// SPDX-License-Identifier: Apache-2.0

// Command gen writes the NovaNet demo corpus: the .docx and .xlsx source files
// of a fictional carrier, their .facts.yaml and .qa.yaml mappings and the
// products.yaml catalog, laid out as they go under KB_S3_PREFIX. Every word is
// invented for the demo. The output is deterministic for a given -today, so a
// regenerated file is byte-identical and scan sees no change.
//
//	go run ./demo/novanet/gen -out /path/to/corpus -today 2026-10-27
//
// -today anchors the validity dates of the fact tables (a price that ended
// yesterday and one that starts today); shift it to the day of a live test.
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// env is what every writer gets.
type env struct {
	out   string    // root of the corpus; object keys are relative to it
	today time.Time // the service's "today" for the validity dates
}

type writer struct {
	name string
	fn   func(*env) error
}

// writers is filled by the init functions of the files in this package, one
// per part of the corpus, so each part lives in its own file.
var writers []writer

func register(name string, fn func(*env) error) {
	writers = append(writers, writer{name, fn})
}

func main() {
	out := flag.String("out", "", "output directory (required); it must be empty or absent")
	today := flag.String("today", time.Now().Format(time.DateOnly), "anchor date yyyy-mm-dd for the fact tables' validity")
	flag.Parse()
	if *out == "" {
		fail("-out is required")
	}
	t, err := time.Parse(time.DateOnly, *today)
	if err != nil {
		fail("-today: " + err.Error())
	}
	if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 {
		fail(*out + " is not empty")
	}
	e := &env{out: *out, today: t}
	slices.SortFunc(writers, func(a, b writer) int { return strings.Compare(a.name, b.name) })
	for _, w := range writers {
		if err := w.fn(e); err != nil {
			fail(w.name + ": " + err.Error())
		}
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "gen: "+msg)
	os.Exit(1)
}
