// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/docx"
	"github.com/rasonyang/aicc-knowledge/internal/generate"
	"github.com/rasonyang/aicc-knowledge/internal/products"
	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// generateInto runs every writer, in name order, as main does.
func generateInto(t testing.TB, dir, today string) {
	t.Helper()
	d, err := time.Parse(time.DateOnly, today)
	if err != nil {
		t.Fatal(err)
	}
	ws := slices.Clone(writers)
	slices.SortFunc(ws, func(a, b writer) int { return strings.Compare(a.name, b.name) })
	e := &env{out: dir, today: d}
	for _, w := range ws {
		if err := w.fn(e); err != nil {
			t.Fatalf("writer %s: %v", w.name, err)
		}
	}
}

var (
	corpusDir  string
	corpusOnce bool
)

// corpus generates the corpus once per test binary.
func corpus(t testing.TB) string {
	t.Helper()
	if !corpusOnce {
		dir, err := os.MkdirTemp("", "novanet-corpus-")
		if err != nil {
			t.Fatal(err)
		}
		generateInto(t, dir, "2026-10-27")
		corpusDir, corpusOnce = dir, true
	}
	return corpusDir
}

func TestMain(m *testing.M) {
	code := m.Run()
	if corpusDir != "" {
		os.RemoveAll(corpusDir)
	}
	os.Exit(code)
}

func readAll(t testing.TB, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDeterministic(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()
	generateInto(t, a, "2026-10-27")
	generateInto(t, b, "2026-10-27")
	generateInto(t, c, "2026-12-01")
	fa, fb, fc := readAll(t, a), readAll(t, b), readAll(t, c)
	if len(fa) < 250 {
		t.Fatalf("only %d files", len(fa))
	}
	if len(fa) != len(fb) {
		t.Fatalf("two runs wrote %d and %d files", len(fa), len(fb))
	}
	for k, v := range fa {
		if !bytes.Equal(v, fb[k]) {
			t.Errorf("%s differs between two runs with the same -today", k)
		}
	}
	if len(fa) != len(fc) {
		t.Fatalf("a different -today changed the file list: %d vs %d", len(fa), len(fc))
	}
	changed := 0
	for k, v := range fa {
		if !bytes.Equal(v, fc[k]) {
			if !strings.HasPrefix(k, "facts/") {
				t.Errorf("%s changed with -today but is not a fact table", k)
			}
			changed++
		}
	}
	if changed == 0 {
		t.Error("-today did not change any fact table")
	}
}

// docxFiles returns the generated .docx objects and their parse results.
func docxFiles(t testing.TB) map[string]*docx.Document {
	t.Helper()
	dir := corpus(t)
	out := map[string]*docx.Document{}
	for k, b := range readAll(t, dir) {
		if !strings.HasSuffix(k, ".docx") {
			continue
		}
		d, err := docx.ParseBytes(b)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		out[k] = d
	}
	return out
}

func TestDocxParseWithoutProblems(t *testing.T) {
	docs := docxFiles(t)
	src := map[string]string{}
	for _, d := range nvDocs {
		src[d.key] = d.src
	}
	if len(docs) != len(src) {
		t.Fatalf("%d parsed docx for %d sources", len(docs), len(src))
	}
	headings := 0
	for k, d := range docs {
		if len(d.Warnings) > 0 {
			t.Errorf("%s: warnings %+v", k, d.Warnings)
		}
		want := 0
		for _, b := range nvParse(src[k]) {
			if strings.HasPrefix(b.kind, "H") {
				want++
				if n := utf8.RuneCountInString(b.text); n > 40 {
					t.Errorf("%s: heading %q has %d characters, over the 40 that still read as a heading", k, b.text, n)
				}
			}
		}
		if len(d.Sections) != want {
			t.Errorf("%s: %d sections, %d headings written", k, len(d.Sections), want)
			continue
		}
		headings += want
		if d.Sections[0].Ordinal != 1 || len(d.Sections[0].HeadingPath) != 1 {
			t.Errorf("%s: the first section is not the title: %+v", k, d.Sections[0].HeadingPath)
		}
		seen := map[string]bool{}
		for _, s := range d.Sections {
			ref := strings.Join(s.HeadingPath, " > ")
			if seen[ref] {
				t.Errorf("%s: heading path %q is not unique", k, ref)
			}
			seen[ref] = true
			if n := utf8.RuneCountInString(s.Text()); n > generate.MaxSectionChars {
				t.Errorf("%s: section %q has %d characters, over %d", k, ref, n, generate.MaxSectionChars)
			}
		}
	}
	t.Logf("%d docx, %d sections", len(docs), headings)
}

func TestSpotHeadings(t *testing.T) {
	docs := docxFiles(t)
	check := func(key string, want ...string) {
		t.Helper()
		d := docs[key]
		if d == nil {
			t.Fatalf("no %s", key)
		}
		var got []string
		for _, s := range d.Sections {
			got = append(got, s.HeadingPath[len(s.HeadingPath)-1])
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("%s: heading %q missing, have %q", key, w, got)
			}
		}
	}
	check("en/faq/plan-faq.docx", "Plan FAQ", "How much data does Plan 59 include?", "How much data does Plan 59 Plus include?")
	check("zh/policies/合约与提前解约政策_2026版.docx", "合约与提前解约政策（第三版）", "冷静期", "搬家移机")
	check("en/policies/contract-and-early-termination-policy-v2.docx", "Renewal gift", "Cooling-off period")
	for _, k := range []string{"en/policies/contract-and-early-termination-policy-v3.docx"} {
		for _, s := range docs[k].Sections {
			if s.HeadingPath[len(s.HeadingPath)-1] == "Renewal gift" {
				t.Errorf("%s still has the renewal gift section that version 3 dropped", k)
			}
		}
	}
	// The four heading flavors all occur.
	flavors := map[uint32]int{}
	for _, d := range nvDocs {
		flavors[nvHash(d.key)%uint32(len(nvFlavors))]++
	}
	if len(flavors) != len(nvFlavors) {
		t.Errorf("heading flavors in use: %v", flavors)
	}
	// Long handbooks: many sections.
	for _, k := range []string{"en/handbooks/customer-service-handbook.docx", "zh/handbooks/客户服务手册_2026.docx"} {
		if n := len(docs[k].Sections); n < 30 {
			t.Errorf("%s has %d sections", k, n)
		}
	}
	// Short documents: one section, the whole document.
	if n := len(docs["en/short/customer-care-hours.docx"].Sections); n != 1 {
		t.Errorf("short document has %d sections", n)
	}
}

func TestCorpusShape(t *testing.T) {
	files := readAll(t, corpus(t))
	count := map[string]int{}
	for k := range files {
		lang := strings.SplitN(k, "/", 2)[0]
		switch {
		case k == "products.yaml":
			count["catalog"]++
		case strings.HasSuffix(k, ".docx"):
			count[lang+" docx"]++
		case strings.HasSuffix(k, ".xlsx"):
			count[lang+" xlsx"]++
		case strings.HasSuffix(k, ".yaml"):
			count[lang+" yaml"]++
		}
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%-10s %d", k, count[k])
	}
	if count["catalog"] != 1 {
		t.Error("products.yaml must be at the root")
	}
	if count["zh docx"] < 120 || count["en docx"] < 120 {
		t.Errorf("too few documents: %v", count)
	}
	total := 0
	for _, n := range count {
		total += n
	}
	if total < 250 || total > 400 {
		t.Errorf("%d files; the corpus should have about 250 to 350", total)
	}
	if _, ok := files["facts/plans.xlsx"]; !ok {
		t.Error("the plans fact table is not part of the generated corpus")
	}
}

var (
	urlRe   = regexp.MustCompile(`(?i)https?://|www\.|@[a-z0-9-]+\.|\.(com|net|org|cn|io)\b`)
	phoneRe = regexp.MustCompile(`\d{7,}|\d{3}[- ]\d{4}`)
)

// TestNothingRealLooking keeps URLs, e-mail addresses and phone numbers out of
// every document.
func TestNothingRealLooking(t *testing.T) {
	for k, d := range docxFiles(t) {
		for _, s := range d.Sections {
			text := strings.Join(s.HeadingPath, " ") + "\n" + s.Text()
			if m := urlRe.FindString(text); m != "" {
				t.Errorf("%s: %q looks like a URL or address", k, m)
			}
			if m := phoneRe.FindString(text); m != "" {
				t.Errorf("%s: %q looks like a phone number", k, m)
			}
		}
	}
	for _, b := range nvBooks {
		for _, s := range b.sheets {
			for _, row := range s.rows {
				for _, c := range row {
					if urlRe.MatchString(c) || phoneRe.MatchString(c) {
						t.Errorf("%s: %q looks real", b.key, c)
					}
				}
			}
		}
	}
}

func TestQAWorkbooks(t *testing.T) {
	dir := corpus(t)
	lim := candidate.Limits{MaxAnswerEN: 240, MaxAnswerZH: 90}
	n := 0
	for _, b := range nvBooks {
		if b.qa == nil {
			continue
		}
		n++
		raw, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(b.key, ".xlsx")+".qa.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		qf, err := xlsx.ParseQAMappingFile(raw)
		if err != nil {
			t.Fatalf("%s: %v", b.key, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, b.key))
		if err != nil {
			t.Fatal(err)
		}
		res, err := xlsx.ImportQABytes(data, qf)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Issues) > 0 || len(res.Warnings) > 0 {
			t.Errorf("%s: issues %+v warnings %+v", b.key, res.Issues, res.Warnings)
		}
		if len(res.Rows) < 25 {
			t.Errorf("%s: %d Q&A rows", b.key, len(res.Rows))
		}
		for _, r := range res.Rows {
			lang := qf.Sheets[0].Language
			v := candidate.Validate(candidate.Draft{Language: lang, Question: r.Question, AlternateQuestions: r.Alternates, Answer: r.Answer}, lim)
			if !v.OK() {
				t.Errorf("%s row %d: %v", b.key, r.Row, v.Violations)
			}
			if len(r.Alternates) == 0 {
				t.Errorf("%s row %d has no alternate phrasings", b.key, r.Row)
			}
			if d, ok := candidate.DetectLanguage(r.Question + " " + r.Answer); !ok || string(d) != lang {
				t.Errorf("%s row %d: language detects as %v, mapping says %s", b.key, r.Row, d, lang)
			}
		}
		// The sheets the mapping does not cover are content.
		c, err := xlsx.ExtractBytes(data, xlsx.ExtractOptions{ExcludeSheets: qf.SheetNames()})
		if err != nil || len(c.Sections) == 0 || len(c.Warnings) > 0 {
			t.Errorf("%s: content %v %v", b.key, c, err)
		}
	}
	if n != 2 {
		t.Errorf("%d Q&A workbooks, want one per language", n)
	}
}

func TestWorkbooksExtract(t *testing.T) {
	dir := corpus(t)
	for _, b := range nvBooks {
		if b.qa != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, b.key))
		if err != nil {
			t.Fatal(err)
		}
		c, err := xlsx.ExtractBytes(data, xlsx.ExtractOptions{})
		if err != nil {
			t.Fatalf("%s: %v", b.key, err)
		}
		if len(c.Warnings) > 0 || len(c.Sections) != len(b.sheets) {
			t.Errorf("%s: %d sections for %d sheets, warnings %+v", b.key, len(c.Sections), len(b.sheets), c.Warnings)
		}
	}
}

func TestCatalog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(corpus(t), "products.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := products.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(s string) []string { return cat.Extract(s).IDs }
	unknown := func(s string) []string { return cat.Extract(s).UnknownModels }
	eq := func(got []string, want ...string) bool { return slices.Equal(got, want) }

	cases := []struct {
		text string
		want []string
	}{
		{"How much data does Plan 59 include?", []string{"plan-59"}},
		{"How much data does Plan 59 Plus include?", []string{"plan-59-plus"}},
		{"plan 59 plus", []string{"plan-59-plus"}},
		{"59元套餐有多少流量", []string{"plan-59"}},
		{"59元Plus套餐有多少流量", []string{"plan-59-plus"}},
		{"五十九元Plus套餐", []string{"plan-59-plus"}},
		{"光纤500和光纤1000有什么区别", []string{"fiber-500", "fiber-1000"}},
		{"光纤五百的上行速率", []string{"fiber-500"}},
		{"光纤一千的路由器", []string{"fiber-1000"}},
		{"Fiber 500 vs Fiber 1000", []string{"fiber-500", "fiber-1000"}},
		{"How do I reset the R5?", []string{"r5"}},
		{"How do I reset the R5 Pro?", []string{"r5-pro"}},
		{"R五 Pro怎么复位", []string{"r5-pro"}},
		{"T2 Max有什么不同", []string{"t2-max"}},
		{"敬老套餐能开eSIM吗", []string{"senior-care"}},
		{"基础套餐几个G", []string{"basic-plan"}},
		{"加油包30多少钱", []string{"booster-30"}},
		{"what's the weather", nil},
	}
	for _, c := range cases {
		if got := ids(c.text); !eq(got, c.want...) {
			t.Errorf("Extract(%q) = %v, want %v", c.text, got, c.want)
		}
		if u := unknown(c.text); len(u) > 0 {
			t.Errorf("Extract(%q) reports unknown models %v", c.text, u)
		}
	}
	for _, q := range []string{"How much is Plan 69?", "Plan 79 的流量是多少", "my R7 router has no signal", "G3光猫怎么装", "How do I pair the M2 extender?", "T3电视盒", "Fiber 5"} {
		if u := unknown(q); len(u) == 0 {
			t.Errorf("%q should name an unknown model", q)
		}
	}
	// Documented limits of the guard.
	for _, q := range []string{"光线五百多少钱", "69元套餐多少钱", "Fiber 800 price", "Is Booster 20 available?"} {
		if u := unknown(q); len(u) != 0 || len(ids(q)) != 0 {
			t.Errorf("%q is documented as not guarded but extracts %v %v", q, ids(q), u)
		}
	}
	// Compatibility.
	exp := cat.Expand([]string{"m1"})
	if !exp["r5"] || !exp["r5-pro"] || exp["r3"] {
		t.Errorf("M1 compatibility: %v", exp)
	}
	// A document is tagged by its file name when its question names nothing.
	if got := cat.DocumentProducts("What is included?", nil, "en/plans/plan-59-plus-sheet.docx"); !eq(got, "plan-59-plus") {
		t.Errorf("tags of the Plan 59 Plus sheet: %v", got)
	}
	if got := cat.DocumentProducts("该怎么办？", nil, "zh/plans/59元Plus套餐资费说明.docx"); !eq(got, "plan-59-plus") {
		t.Errorf("tags of the zh Plan 59 Plus sheet: %v", got)
	}
	if got := cat.DocumentProducts("Cooling-off period", nil, "en/policies/contract-and-early-termination-policy-v3.docx"); got != nil {
		t.Errorf("a policy file name must not tag products: %v", got)
	}
	// Every plan of plans.go is a product with its Chinese name.
	for _, p := range plans {
		if got := ids(p.NameZH); len(got) != 1 {
			t.Errorf("%s: zh name %q extracts %v", p.Key, p.NameZH, got)
		}
		en := p.NameEN
		if p.Key == "Basic" {
			en = "Basic Plan" // "Basic" alone is an ordinary word, so it is not an alias
		}
		if got := ids(en); len(got) != 1 {
			t.Errorf("%s: name %q extracts %v", p.Key, en, got)
		}
	}
}
