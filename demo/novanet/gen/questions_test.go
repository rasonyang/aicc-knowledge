// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/goccy/go-yaml"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/docx"
	"github.com/rasonyang/aicc-knowledge/internal/products"
	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// The format of questions.yaml; README.md documents it for people.

type qRef struct {
	File    string `yaml:"file"`
	Section string `yaml:"section"`
	Fact    string `yaml:"fact,omitempty"`
}

type qFactLookup struct {
	Table string            `yaml:"table"`
	Key   map[string]string `yaml:"key"`
}

type question struct {
	ID          string       `yaml:"id"`
	Language    string       `yaml:"language"`
	Text        string       `yaml:"text"`
	Category    string       `yaml:"category"`
	Tags        []string     `yaml:"tags"`
	LiveExample bool         `yaml:"liveExample,omitempty"`
	Expect      []qRef       `yaml:"expect"`
	Avoid       []qRef       `yaml:"avoid,omitempty"`
	FactLookup  *qFactLookup `yaml:"factLookup,omitempty"`
	Note        string       `yaml:"note,omitempty"`
}

type questionFile struct {
	Questions []question `yaml:"questions"`
}

var (
	qCategories = []string{"COVERED", "PARTLY_COVERED", "NOT_COVERED", "OUTDATED_ONLY", "ACCOUNT_SPECIFIC"}
	qTags       = []string{"colloquial", "indirect", "asr_error", "mixed_language", "spoken_numbers", "near_duplicate", "deep_in_long_doc",
		"short_doc", "product_confusion", "unknown_model", "needs_fact_table", "table_lookup", "policy_change", "stale_source",
		"qa_workbook", "district", "multi_step"}
	// unguarded are the unknown-model questions the catalog cannot recognize;
	// their notes say so.
	unguarded = []string{"q-en-311", "q-zh-310"}
)

func loadQuestions(t testing.TB) []question {
	t.Helper()
	raw, err := os.ReadFile("../questions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var f questionFile
	if err := yaml.NewDecoder(bytes.NewReader(raw), yaml.Strict(), yaml.DisallowUnknownField()).Decode(&f); err != nil {
		t.Fatalf("questions.yaml: %v", err)
	}
	return f.Questions
}

// sectionRow is one section as internal/parse would store it.
type sectionRow struct {
	ref      string // the source_ref
	text     string // the body
	question string // Q&A rows: question and alternates
}

// sectionIndex builds the sections of the corpus with the same code and the
// same source_ref rule as internal/parse (object key + "#" + the parser's
// reference): docx sections, xlsx content chunks and Q&A rows.
func sectionIndex(t testing.TB) map[string][]sectionRow {
	t.Helper()
	dir := corpus(t)
	out := map[string][]sectionRow{}
	for key, b := range readAll(t, dir) {
		switch {
		case strings.HasSuffix(key, ".docx"):
			d, err := docx.ParseBytes(b)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range d.Sections {
				out[key] = append(out[key], sectionRow{ref: key + "#" + s.SourceRef, text: s.Text()})
			}
		case strings.HasSuffix(key, ".xlsx") && !strings.HasPrefix(key, "facts/"):
			var qf *xlsx.QAMappingFile
			if raw, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(key, ".xlsx")+".qa.yaml")); err == nil {
				if qf, err = xlsx.ParseQAMappingFile(raw); err != nil {
					t.Fatal(err)
				}
			}
			c, err := xlsx.ExtractBytes(b, xlsx.ExtractOptions{ExcludeSheets: qf.SheetNames()})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range c.Sections {
				out[key] = append(out[key], sectionRow{ref: key + "#" + s.SourceRef, text: s.Text})
			}
			if qf != nil {
				res, err := xlsx.ImportQABytes(b, qf)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range res.Rows {
					out[key] = append(out[key], sectionRow{ref: key + "#" + r.SourceRef, text: r.Answer, question: r.Question + "\n" + strings.Join(r.Alternates, "\n")})
				}
			}
		}
	}
	return out
}

// resolve finds the one section a reference names.
func resolve(idx map[string][]sectionRow, r qRef) (sectionRow, string) {
	rows, ok := idx[r.File]
	if !ok {
		return sectionRow{}, "no such file " + r.File
	}
	want := r.File + "#" + r.Section
	var hit []sectionRow
	for _, s := range rows {
		if s.ref == want || strings.HasPrefix(s.ref, want+"#") {
			hit = append(hit, s)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], ""
	case 0:
		var have []string
		for _, s := range rows {
			have = append(have, strings.TrimPrefix(s.ref, r.File+"#"))
		}
		return sectionRow{}, "no section " + r.Section + " in " + r.File + "; it has " + strings.Join(have, " | ")
	}
	return sectionRow{}, "section " + r.Section + " in " + r.File + " is not unique"
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func TestQuestions(t *testing.T) {
	qs := loadQuestions(t)
	idx := sectionIndex(t)

	catRaw, err := os.ReadFile(filepath.Join(corpus(t), "products.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := products.Parse(catRaw)
	if err != nil {
		t.Fatal(err)
	}

	ids := map[string]bool{}
	byCat := map[string]int{}
	byLang := map[string]int{}
	tagCount := map[string]int{}
	live := map[string]int{} // language/group -> count
	fact := 0
	for _, q := range qs {
		fail := func(f string, a ...any) { t.Errorf(q.ID+": "+f, a...) }
		if ids[q.ID] || q.ID == "" {
			fail("id is empty or repeated")
		}
		ids[q.ID] = true
		if q.Language != "EN" && q.Language != "ZH" {
			fail("language %q", q.Language)
		}
		if !slices.Contains(qCategories, q.Category) {
			fail("category %q", q.Category)
		}
		byCat[q.Category]++
		byLang[q.Language]++
		if strings.TrimSpace(q.Text) == "" {
			fail("empty text")
		}
		mixed := slices.Contains(q.Tags, "mixed_language")
		switch {
		case q.Language == "ZH" && !hasHan(q.Text):
			fail("a ZH question without Chinese characters")
		case q.Language == "EN" && hasHan(q.Text) && !mixed:
			fail("an EN question with Chinese characters is not tagged mixed_language")
		}
		if d, ok := candidate.DetectLanguage(q.Text); ok && !mixed && string(d) != q.Language {
			fail("text reads as %s, language is %s", d, q.Language)
		}
		if len(q.Tags) == 0 {
			fail("no style tags")
		}
		for _, tg := range q.Tags {
			if !slices.Contains(qTags, tg) {
				fail("unknown tag %q", tg)
			}
			tagCount[tg]++
		}
		if (q.FactLookup != nil) != slices.Contains(q.Tags, "needs_fact_table") {
			fail("needs_fact_table and factLookup must go together")
		}
		if q.FactLookup != nil {
			fact++
			if !slices.Contains([]string{"plans", "topups", "roaming_packs"}, q.FactLookup.Table) || len(q.FactLookup.Key) != 1 {
				fail("factLookup %+v", q.FactLookup)
			}
		}

		switch q.Category {
		case "COVERED", "PARTLY_COVERED", "OUTDATED_ONLY":
			if len(q.Expect) == 0 {
				fail("%s needs at least one expected section", q.Category)
			}
		default:
			if len(q.Expect) != 0 {
				fail("%s must expect NO_MATCH", q.Category)
			}
		}
		if q.Category == "OUTDATED_ONLY" && q.Note == "" {
			fail("an OUTDATED_ONLY label needs a note")
		}
		if q.LiveExample {
			g := "HIT"
			switch q.Category {
			case "NOT_COVERED", "ACCOUNT_SPECIFIC":
				g = "NO_MATCH"
			case "OUTDATED_ONLY":
				g = "OUTDATED"
			}
			if q.Category == "PARTLY_COVERED" {
				g = "PARTLY"
			}
			live[q.Language+"/"+g]++
		}

		prefix := strings.ToLower(q.Language) + "/"
		for _, e := range q.Expect {
			if !strings.HasPrefix(e.File, prefix) {
				fail("%s is not a %s document", e.File, q.Language)
			}
			if e.Fact == "" {
				fail("%s#%s has no fact", e.File, e.Section)
			}
			s, msg := resolve(idx, e)
			if msg != "" {
				fail("%s", msg)
				continue
			}
			if !strings.Contains(s.text, e.Fact) {
				fail("fact %q is not in %s#%s: %q", e.Fact, e.File, e.Section, s.text)
			}
		}
		for _, a := range q.Avoid {
			if _, msg := resolve(idx, a); msg != "" {
				fail("avoid: %s", msg)
			}
			if slices.ContainsFunc(q.Expect, func(e qRef) bool { return e.File == a.File && e.Section == a.Section }) {
				fail("avoid lists an expected section")
			}
		}

		// The guard must not throw away an expected hit: when the question
		// names products, an expected document is either generic or about one
		// of them (or a compatible one).
		ex := cat.Extract(q.Text)
		if slices.Contains(q.Tags, "unknown_model") {
			if got := len(ex.UnknownModels) > 0; got == slices.Contains(unguarded, q.ID) {
				fail("unknown_model: guard detects it = %v, expected %v (%v)", got, !slices.Contains(unguarded, q.ID), ex)
			}
		} else if len(ex.UnknownModels) > 0 {
			fail("names an unknown model %v but is not tagged unknown_model", ex.UnknownModels)
		}
		if len(ex.IDs) > 0 && len(ex.UnknownModels) == 0 {
			allowed := cat.Expand(ex.IDs)
			for _, e := range q.Expect {
				s, msg := resolve(idx, e)
				if msg != "" {
					continue
				}
				// What a candidate question could be: the heading of the
				// section, or the Q&A row's own questions.
				question := e.Section
				if i := strings.LastIndex(question, " > "); i >= 0 {
					question = question[i+3:]
				}
				var alts []string
				if s.question != "" {
					parts := strings.Split(s.question, "\n")
					question, alts = parts[0], parts[1:]
				}
				tags := cat.DocumentProducts(question, alts, e.File)
				if len(tags) > 0 && !slices.ContainsFunc(tags, func(p string) bool { return allowed[p] }) {
					fail("the guard would drop the expected %s#%s: it is about %v, the question names %v", e.File, e.Section, tags, ex.IDs)
				}
			}
		}
	}

	t.Logf("%d questions: categories %v, languages %v", len(qs), byCat, byLang)
	t.Logf("tags %v", tagCount)
	t.Logf("live examples %v", live)
	if len(qs) < 60 {
		t.Errorf("%d questions, want at least 60", len(qs))
	}
	for _, c := range qCategories {
		if byCat[c] < 4 {
			t.Errorf("category %s has %d questions", c, byCat[c])
		}
	}
	for _, l := range []string{"EN", "ZH"} {
		if n := byLang[l]; n*100 < len(qs)*40 {
			t.Errorf("language %s has %d of %d questions", l, n, len(qs))
		}
		for _, g := range []string{"HIT", "NO_MATCH", "OUTDATED"} {
			if n := live[l+"/"+g]; n < 3 || n > 4 {
				t.Errorf("%s/%s has %d live examples, want 3 or 4", l, g, n)
			}
		}
	}
	if fact < 6 {
		t.Errorf("%d questions that need a fact table", fact)
	}
	for _, tg := range qTags {
		if tagCount[tg] == 0 {
			t.Errorf("no question has the tag %s", tg)
		}
	}
}
