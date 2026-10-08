// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseQAMappingFile(t *testing.T) {
	good := `
sheets:
  - sheet: Support FAQ
    headerRows: 1
    question: Customer question
    answer: Reply
    alternates: [Variants, More variants]
    language: zh
  - sheet: Second
    headerStartRow: 3
    headerRows: 2
    question: Top / Question
    answer: Top / Answer
`
	f, err := ParseQAMappingFile([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Sheets) != 2 || f.Sheets[0].Language != "ZH" || f.Sheets[1].Language != "AUTO" || f.Sheets[1].headerStart() != 3 || f.Sheets[0].headerStart() != 1 {
		t.Errorf("parsed = %+v", f)
	}
	if got := f.SheetNames(); !slices.Equal(got, []string{"Support FAQ", "Second"}) {
		t.Errorf("SheetNames = %v", got)
	}

	bad := []struct{ name, yaml, want string }{
		{"empty", "sheets: []", "sheets must not be empty"},
		{"unknown field", "sheets:\n  - sheet: S\n    headerRows: 1\n    question: Q\n    answer: A\n    extra: 1", "extra"},
		{"unknown top-level field", "sheets:\n  - {sheet: S, headerRows: 1, question: Q, answer: A}\nfoo: 1", "foo"},
		{"no sheet", "sheets:\n  - {headerRows: 1, question: Q, answer: A}", "sheet is required"},
		{"header rows", "sheets:\n  - {sheet: S, question: Q, answer: A}", "headerRows must be >= 1"},
		{"no question", "sheets:\n  - {sheet: S, headerRows: 1, answer: A}", "question is required"},
		{"no answer", "sheets:\n  - {sheet: S, headerRows: 1, question: Q}", "answer is required"},
		{"same header", "sheets:\n  - {sheet: S, headerRows: 1, question: Q, answer: Q}", "same header"},
		{"alternate repeats", "sheets:\n  - {sheet: S, headerRows: 1, question: Q, answer: A, alternates: [Q]}", "repeats another column"},
		{"empty alternate", "sheets:\n  - {sheet: S, headerRows: 1, question: Q, answer: A, alternates: ['']}", "alternates[0] is empty"},
		{"language", "sheets:\n  - {sheet: S, headerRows: 1, question: Q, answer: A, language: FR}", "must be EN, ZH or AUTO"},
		{"duplicate sheet", "sheets:\n  - {sheet: S, headerRows: 1, question: Q, answer: A}\n  - {sheet: S, headerRows: 1, question: Q, answer: A}", "declared twice"},
		{"not yaml", "sheets: [", ""},
	}
	for _, c := range bad {
		_, err := ParseQAMappingFile([]byte(c.yaml))
		if err == nil || CodeOf(err) != CodeMappingInvalid {
			t.Errorf("%s: err = %v, want MAPPING_INVALID", c.name, err)
			continue
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v lacks %q", c.name, err, c.want)
		}
	}
	// Several problems are all reported.
	_, err = ParseQAMappingFile([]byte("sheets:\n  - {sheet: S, headerRows: 0, language: FR}"))
	var me *MappingError
	var xe *Error
	if !errors.As(err, &xe) || !errors.As(xe.Err, &me) || len(me.Issues) < 4 {
		t.Errorf("issues = %+v", me)
	}
}

func TestSplitVariants(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"one question", []string{"one question"}},
		{"first\nsecond\r\nthird", []string{"first", "second", "third"}},
		{"支持吗；能用吗； 可以吗 ；", []string{"支持吗", "能用吗", "可以吗"}},
		{"a; b", []string{"a; b"}}, // the ASCII semicolon stays inside the variant
		{"x\n\n y   z ", []string{"x", "y z"}},
	}
	for _, c := range cases {
		if got := SplitVariants(c.in); !slices.Equal(got, c.want) {
			t.Errorf("SplitVariants(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSharedSheets(t *testing.T) {
	qa, _ := ParseQAMappingFile([]byte("sheets:\n  - {sheet: A, headerRows: 1, question: Q, answer: R}\n  - {sheet: B, headerRows: 1, question: Q, answer: R}"))
	facts := &MappingFile{Tables: []Mapping{{Table: "t", Sheet: "B"}, {Table: "u", Sheet: "C"}}}
	if got := SharedSheets(qa, facts); !slices.Equal(got, []string{"B"}) {
		t.Errorf("SharedSheets = %v", got)
	}
	if SharedSheets(qa, nil) != nil || SharedSheets(nil, facts) != nil {
		t.Error("nil mapping shares nothing")
	}
}

func qaBook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "Support")
	rows := [][]any{
		{"Notes about this sheet", nil, nil}, // 1: a banner above the header
		{"Question", "Answer", "Variants"},   // 2
		{"Does it support wireless charging?", "Yes.", "wireless charging?\ncan I charge wirelessly?"}, // 3
		{"Is there a warranty?", "Two years.", "warranty period；how long is the warranty"},             // 4
		{"Hidden question?", "Hidden answer.", nil},                                                    // 5 (hidden)
		{"No answer yet?", nil, nil},                                                                   // 6
		{nil, "Orphan answer", nil},                                                                    // 7
		{nil, nil, nil},                                                                                // 8 blank
		{"  Spaced   question ?  ", "Line one\nLine two", "Is there a warranty?"},                      // 9
	}
	for r, vals := range rows {
		for c, v := range vals {
			if v != nil {
				ref, _ := excelize.CoordinatesToCellName(c+1, r+1)
				_ = f.SetCellValue("Support", ref, v)
			}
		}
	}
	if err := f.SetRowVisible("Support", 5, false); err != nil {
		t.Fatal(err)
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportQARows(t *testing.T) {
	mf, err := ParseQAMappingFile([]byte("sheets:\n  - sheet: Support\n    headerStartRow: 2\n    headerRows: 1\n    question: Question\n    answer: Answer\n    alternates: [Variants]\n    language: EN"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ImportQABytes(qaBook(t), mf)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || len(res.Rows) != 3 {
		t.Fatalf("result = %+v", res)
	}
	r0, r1, r2 := res.Rows[0], res.Rows[1], res.Rows[2]
	if r0.Row != 3 || r0.SourceRef != "Support!A3" || r0.Question != "Does it support wireless charging?" || r0.Answer != "Yes." ||
		!slices.Equal(r0.Alternates, []string{"wireless charging?", "can I charge wirelessly?"}) || r0.Language != "EN" {
		t.Errorf("row 3 = %+v", r0)
	}
	if !slices.Equal(r1.Alternates, []string{"warranty period", "how long is the warranty"}) {
		t.Errorf("row 4 alternates = %q", r1.Alternates)
	}
	if r2.Row != 9 || r2.Question != "Spaced question ?" || r2.Answer != "Line one\nLine two" || !slices.Equal(r2.Alternates, []string{"Is there a warranty?"}) {
		t.Errorf("row 9 = %+v", r2)
	}
	codes := map[string]string{}
	for _, w := range res.Warnings {
		codes[w.Location] = w.Code
	}
	want := map[string]string{"Support!A5": CodeHiddenRowSkipped, "Support!A6": CodeQARowIncomplete, "Support!A7": CodeQARowIncomplete}
	if len(res.Warnings) != 3 || codes["Support!A5"] != want["Support!A5"] || codes["Support!A6"] != want["Support!A6"] || codes["Support!A7"] != want["Support!A7"] {
		t.Errorf("warnings = %+v", res.Warnings)
	}
}

func TestImportQAIssues(t *testing.T) {
	data := qaBook(t)
	cases := []struct {
		name, yaml, code string
	}{
		{"sheet missing", "sheets:\n  - {sheet: Nope, headerRows: 1, question: Question, answer: Answer}", CodeSheetNotFound},
		{"question header missing", "sheets:\n  - {sheet: Support, headerStartRow: 2, headerRows: 1, question: Missing, answer: Answer}", CodeHeaderNotFound},
		{"answer header missing", "sheets:\n  - {sheet: Support, headerStartRow: 2, headerRows: 1, question: Question, answer: Missing}", CodeHeaderNotFound},
		{"alternate header missing", "sheets:\n  - {sheet: Support, headerStartRow: 2, headerRows: 1, question: Question, answer: Answer, alternates: [Gone]}", CodeHeaderNotFound},
		{"header on the wrong row", "sheets:\n  - {sheet: Support, headerRows: 1, question: Question, answer: Answer}", CodeHeaderNotFound},
	}
	for _, c := range cases {
		mf, err := ParseQAMappingFile([]byte(c.yaml))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		res, err := ImportQABytes(data, mf)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.OK() || len(res.Rows) != 0 || res.Issues[0].Code != c.code {
			t.Errorf("%s: result = %+v, want issue %s and no rows", c.name, res, c.code)
		}
	}
}

func TestImportQAMergedHeaderAndAmbiguity(t *testing.T) {
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "Merged")
	set := func(ref string, v any) { _ = f.SetCellValue("Merged", ref, v) }
	set("A1", "Group")
	_ = f.MergeCell("Merged", "A1", "B1")
	set("A2", "Ask")
	set("B2", "Say")
	set("A3", "q1?")
	set("B3", "a1")
	set("A4", "Dup")
	set("B4", "Dup")
	buf, _ := f.WriteToBuffer()
	mf, _ := ParseQAMappingFile([]byte("sheets:\n  - {sheet: Merged, headerRows: 2, question: Group / Ask, answer: Group / Say}"))
	res, err := ImportQABytes(buf.Bytes(), mf)
	if err != nil || !res.OK() || len(res.Rows) != 2 || res.Rows[0].Question != "q1?" || res.Rows[0].Answer != "a1" {
		t.Fatalf("merged header: %+v %v", res, err)
	}
	// Two columns with the same header text are ambiguous.
	mf, _ = ParseQAMappingFile([]byte("sheets:\n  - {sheet: Merged, headerRows: 1, question: Group, answer: Ask}"))
	res, _ = ImportQABytes(buf.Bytes(), mf)
	if res.OK() || res.Issues[0].Code != CodeHeaderAmbiguous {
		t.Errorf("expected HEADER_AMBIGUOUS: %+v", res)
	}
}
