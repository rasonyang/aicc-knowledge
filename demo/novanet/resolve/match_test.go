// SPDX-License-Identifier: Apache-2.0

package main

import (
	"slices"
	"testing"
)

const pfx = "novanet/"

var items = []Item{
	{"a", pfx + "en/faq/plan-faq.docx#Plan FAQ > How much data?#4", "How much data does Plan 59 have?", "Plan 59 has 30 GB of data."},
	{"b", pfx + "en/faq/plan-faq.docx#Plan FAQ > How much data?#4", "Data on Plan 59?", "It includes 30 GB."},
	{"c", pfx + "en/faq/plan-faq.docx#Plan FAQ > How much data?#4", "What about roaming data?", "Roaming is 2 GB."},
	{"d", pfx + "en/faq/plan-faq.docx#Plan FAQ > How much data? Extra#5", "Other", "30 GB"},
	{"e", pfx + "zh/faq/x.xlsx#Quick answers!A5", "重置多久？", "按住重置键８秒。"},
}

func TestNormalize(t *testing.T) {
	if got, want := normalize(" ８ 秒　GB\n"), "8秒gb"; got != want {
		t.Fatalf("normalize = %q, want %q", got, want)
	}
}

func TestMatchSectionAndFact(t *testing.T) {
	e := Expectation{File: "en/faq/plan-faq.docx", Section: "Plan FAQ > How much data?", Fact: "30 gb"}
	got := Match(pfx, e, items)
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("Match = %v, want %v (c has another fact; d is another section whose name only shares a prefix)", got, want)
	}
	if len(got) != 2 {
		t.Fatalf("cardinality %d", len(got))
	}
}

func TestMatchWorkbookRowAndFullWidth(t *testing.T) {
	e := Expectation{File: "zh/faq/x.xlsx", Section: "Quick answers!A5", Fact: "8秒"}
	if got := Match(pfx, e, items); !slices.Equal(got, []string{"e"}) {
		t.Fatalf("Match = %v", got)
	}
}

func TestMatchEmptyFactTakesWholeSection(t *testing.T) {
	e := Expectation{File: "en/faq/plan-faq.docx", Section: "Plan FAQ > How much data?"}
	if got := Match(pfx, e, items); len(got) != 3 {
		t.Fatalf("Match = %v, want 3", got)
	}
}

func TestMatchPrefixMustBeRespected(t *testing.T) {
	e := Expectation{File: "en/faq/plan-faq.docx", Section: "Plan FAQ > How much data?", Fact: "gb"}
	if got := Match("other/", e, items); len(got) != 0 {
		t.Fatalf("Match = %v, want none", got)
	}
}

func TestResolveUnionsAndReportsUnresolved(t *testing.T) {
	exp := []Expectation{
		{File: "en/faq/plan-faq.docx", Section: "Plan FAQ > How much data?", Fact: "30 GB"},
		{File: "en/plans/missing.docx", Section: "X", Fact: "y"},
		{File: "en/faq/plan-faq.docx", Section: "Plan FAQ > How much data? Extra", Fact: "30 GB"},
	}
	r := Resolve(pfx, exp, items)
	if !slices.Equal(r.IDs, []string{"a", "b", "d"}) || len(r.Unresolved) != 1 {
		t.Fatalf("Resolve = %+v", r)
	}
	if got := Resolve(pfx, exp[1:2], items); len(got.IDs) != 0 || len(got.Unresolved) != 1 {
		t.Fatalf("gap Resolve = %+v", got)
	}
}

func TestGapKind(t *testing.T) {
	e := []Expectation{{File: "en/x.docx", Section: "S"}}
	cs := []sectionCandidate{{ID: "1", State: "REJECTED", SourceRef: pfx + "en/x.docx#S#2"}}
	if got := gapKind(pfx, e, cs, true); got != GapFactNotStated {
		t.Fatalf("published section = %s", got)
	}
	if got := gapKind(pfx, e, cs, false); got != GapOnlyRejected {
		t.Fatalf("only rejected = %s", got)
	}
	if got := gapKind(pfx, e, nil, false); got != GapNoCandidate {
		t.Fatalf("none = %s", got)
	}
}
