// SPDX-License-Identifier: Apache-2.0

package docx

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGolden(t *testing.T) {
	names, _ := filepath.Glob("testdata/*.docx")
	if len(names) != 8 {
		t.Fatalf("fixtures = %d, want 8", len(names))
	}
	for _, n := range names {
		base := filepath.Base(n)
		t.Run(base, func(t *testing.T) {
			doc, err := ParseBytes(fixture(t, base))
			if err != nil {
				t.Fatal(err)
			}
			got := marshal(t, doc)
			golden := strings.TrimSuffix(n, ".docx") + ".golden.json"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("output differs from %s (run with -update to regenerate)\n%s", golden, got)
			}
			again, _ := ParseBytes(fixture(t, base))
			if !bytes.Equal(marshal(t, again), got) {
				t.Error("parse is not deterministic")
			}
		})
	}
}

func paths(d *Document) [][]string {
	var out [][]string
	for _, s := range d.Sections {
		out = append(out, s.HeadingPath)
	}
	return out
}

func TestHeadingsFromOutlineLevelOnly(t *testing.T) {
	d, err := ParseBytes(fixture(t, "headings.docx"))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{},
		{"第一章 概述"},
		{"第一章 概述", "1.1 背景"},
		{"第二章 价格"},
		{"第二章 价格", "2.1 套餐"},
		{"第二章 价格", "2.1 套餐", "2.1.1 月费"},
		{"第二章 价格", "2.1 套餐", "2.1.2 直接覆盖"},
		{"附录"},
	}
	if !reflect.DeepEqual(paths(d), want) {
		t.Errorf("paths = %q\nwant %q", paths(d), want)
	}
	levels := []int{0, 1, 2, 1, 2, 3, 3, 1}
	for i, s := range d.Sections {
		if s.Level != levels[i] {
			t.Errorf("section %d level = %d, want %d", i, s.Level, levels[i])
		}
	}
	// the trap paragraph lands in 1.1 body
	if txt := d.Sections[2].Text(); !strings.Contains(txt, "Heading 1 trap") {
		t.Errorf("trap paragraph should be body of 1.1: %q", txt)
	}
	if d.Sections[0].SourceRef != "#0" || d.Sections[2].SourceRef != "第一章 概述 > 1.1 背景#2" {
		t.Errorf("source refs: %q %q", d.Sections[0].SourceRef, d.Sections[2].SourceRef)
	}
	codes := map[string]int{}
	for _, w := range d.Warnings {
		codes[w.Code]++
	}
	if codes[WarnStyleCycle] != 1 || codes[WarnStyleNotFound] != 1 || len(d.Warnings) != 2 {
		t.Errorf("warnings = %+v", d.Warnings)
	}
}

func TestTrackedChangesAcceptAll(t *testing.T) {
	d, err := ParseBytes(fixture(t, "tracked.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("tracked changes must not warn: %+v", d.Warnings)
	}
	if got := paths(d); !reflect.DeepEqual(got, [][]string{{"Plan Gold"}, {"Became a heading by style change"}}) {
		t.Fatalf("paths = %q", got)
	}
	var texts []string
	for _, b := range d.Sections[0].Blocks {
		texts = append(texts, b.Text)
	}
	want := []string{
		"Price is 12 yuan.",
		"Merged part one, part two.",
		"Alpha.",
		"Beta. Moved text.",
		"Inserted paragraph.",
		"Formatting changed only.",
		"Old properties were a heading; current ones are not.",
	}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("blocks = %q\nwant %q", texts, want)
	}
	tb := d.Sections[1].Blocks
	if len(tb) != 1 || !reflect.DeepEqual(tb[0].Table.Rows, [][]string{{"Item", "Qty"}, {"Added row", "3"}}) {
		t.Errorf("tracked table = %+v", tb)
	}
}

func TestInlineText(t *testing.T) {
	d, err := ParseBytes(fixture(t, "inline.docx"))
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, b := range d.Sections[0].Blocks {
		texts = append(texts, b.Text)
	}
	want := []string{
		"A\tB\nC\nD-E",
		"Page 7",
		"simple field result",
		"See the link text.",
		"Sym ©",
		"Before box after.",
		"Content control paragraph.",
	}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("blocks = %q\nwant %q", texts, want)
	}
}

func TestTables(t *testing.T) {
	d, err := ParseBytes(fixture(t, "tables.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sections) != 1 {
		t.Fatalf("sections = %d", len(d.Sections))
	}
	bl := d.Sections[0].Blocks
	if len(bl) != 5 {
		t.Fatalf("blocks = %d", len(bl))
	}
	wantRows := [][]string{
		{"Plan", "Price", "Price"},
		{"Plan", "Monthly", "Yearly"},
		{"Basic", "10", "100"},
		{"Pro", "20\n(promo)", "a|b"},
	}
	if !reflect.DeepEqual(bl[1].Table.Rows, wantRows) {
		t.Errorf("rows = %q", bl[1].Table.Rows)
	}
	wantText := "Plan | Price | Price\nPlan | Monthly | Yearly\nBasic | 10 | 100\nPro | 20; (promo) | a\\|b"
	if bl[1].Text != wantText {
		t.Errorf("text = %q", bl[1].Text)
	}
	if got := bl[3].Table.Rows; !reflect.DeepEqual(got, [][]string{{"Outer A", "Inner intro\nx | y\n1 | 2"}}) {
		t.Errorf("nested rows = %q", got)
	}
}

func TestErrors(t *testing.T) {
	ole := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 600)...)
	zipWith := func(files map[string]string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for n, c := range files {
			w, _ := zw.Create(n)
			_, _ = w.Write([]byte(c))
		}
		_ = zw.Close()
		return buf.Bytes()
	}
	good := fixture(t, "headings.docx")
	cases := []struct {
		name string
		in   []byte
		code string
	}{
		{"ole doc", ole, CodeUnsupportedFormat},
		{"xlsx archive", zipWith(map[string]string{"xl/workbook.xml": "<x/>"}), CodeUnsupportedFormat},
		{"not a zip", []byte("hello world, definitely not a zip"), CodeDOCXCorrupt},
		{"empty", nil, CodeDOCXCorrupt},
		{"truncated zip", good[:len(good)/2], CodeDOCXCorrupt},
		{"no document.xml", zipWith(map[string]string{"[Content_Types].xml": "<x/>"}), CodeDOCXCorrupt},
		{"malformed document.xml", zipWith(map[string]string{"word/document.xml": "<w:document><w:body>"}), CodeDOCXCorrupt},
		{"malformed styles.xml", zipWith(map[string]string{"word/document.xml": `<document><body/></document>`, "word/styles.xml": "<styles"}), CodeDOCXCorrupt},
		{"no body", zipWith(map[string]string{"word/document.xml": `<document/>`}), CodeDOCXCorrupt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := ParseBytes(c.in)
			if err == nil || d != nil {
				t.Fatalf("want error, got doc=%v err=%v", d, err)
			}
			if got := CodeOf(err); got != c.code {
				t.Errorf("code = %q, want %q (%v)", got, c.code, err)
			}
		})
	}
}

func TestSentenceLikeHeadingsAreDemotedAndDuplicatesCollapse(t *testing.T) {
	d, err := ParseBytes(fixture(t, "sentences.docx"))
	if err != nil {
		t.Fatal(err)
	}
	got := paths(d)
	wantPaths := [][]string{
		{"Acme Widget Guide"},
		{"Acme Widget Guide"}, // the repeated level-3 title: same path, collapsed
		{"Acme Widget Guide", "Setup"},
		{"Acme Widget Guide", "如何清洁屏幕？"},
		{"Acme Widget Guide", "Does it work offline?"},
		{"Acme Widget Guide", "Version 2.0"},
	}
	// "谢谢您的阅读！" ends the level-1 chain: it is demoted, so it joins the body of the last section.
	if !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("paths = %q\nwant %q", got, wantPaths)
	}
	if len(d.Sections) != 6 {
		t.Fatalf("sections = %d, want 6", len(d.Sections))
	}
	if d.Sections[1].SourceRef != "Acme Widget Guide#2" {
		t.Errorf("source ref = %q", d.Sections[0].SourceRef)
	}
	if txt := d.Sections[1].Text(); !strings.Contains(txt, "Welcome to the Acme Widget Guide.") {
		t.Errorf("the intro sentence should be body text of the title section: %q", txt)
	}
	if txt := d.Sections[2].Text(); !strings.Contains(txt, "这是一段很长的介绍文字") || !strings.Contains(txt, "green button") {
		t.Errorf("the long paragraph should be body of Setup: %q", txt)
	}
	if txt := d.Sections[4].Text(); !strings.Contains(txt, "谢谢您的阅读！") || !strings.Contains(txt, "Yes.") {
		t.Errorf("the closing sentence should be body text: %q", txt)
	}
	if txt := d.Sections[3].Text(); txt != "每周清洁一次即可。" {
		t.Errorf("question heading body = %q", txt)
	}
	n := 0
	for _, w := range d.Warnings {
		if w.Code == WarnHeadingDemoted {
			n++
		}
	}
	if n != 3 || len(d.Warnings) != 3 {
		t.Errorf("warnings = %+v, want 3 HEADING_DEMOTED", d.Warnings)
	}
}

func TestMissingStylesWarnsAndKeepsDirectOutline(t *testing.T) {
	doc := `<w:document xmlns:w="x"><w:body><w:p><w:pPr><w:outlineLvl w:val="0"/></w:pPr><w:r><w:t>Head</w:t></w:r></w:p><w:p><w:r><w:t>Body</w:t></w:r></w:p></w:body></w:document>`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(doc))
	_ = zw.Close()
	d, err := ParseBytes(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sections) != 1 || d.Sections[0].HeadingPath[0] != "Head" || len(d.Sections[0].Blocks) != 1 {
		t.Errorf("sections = %+v", d.Sections)
	}
	if len(d.Warnings) != 1 || d.Warnings[0].Code != WarnStylesMissing {
		t.Errorf("warnings = %+v", d.Warnings)
	}
}

// TestRealConsumer cross-checks the extracted text against officecli, a
// separate implementation, when it is installed.
func TestRealConsumer(t *testing.T) {
	bin, err := exec.LookPath("officecli")
	if err != nil {
		t.Skip("officecli not installed")
	}
	path, _ := filepath.Abs("testdata/tracked.docx")
	out, err := exec.Command(bin, "view", path, "text").CombinedOutput()
	if err != nil {
		t.Skipf("officecli could not open fixture: %v\n%s", err, out)
	}
	// Opening the file proves the fixture is a well-formed docx. Paragraphs
	// whose accepted-changes text is unambiguous must appear verbatim in the
	// consumer's view. (officecli keeps a deleted paragraph mark's halves
	// separate and counts the deleted table row, so those are not compared.)
	for _, w := range []string{"Plan Gold", "Price is 12 yuan.", "Beta. Moved text.", "Inserted paragraph.", "Formatting changed only."} {
		if !strings.Contains(string(out), w) {
			t.Errorf("officecli text lacks %q:\n%s", w, out)
		}
	}
}
