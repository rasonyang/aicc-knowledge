// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// Languages a Q&A mapping may name. AUTO (the default) lets the caller detect
// the language of each row.
const (
	QALanguageEN   = "EN"
	QALanguageZH   = "ZH"
	QALanguageAuto = "AUTO"
)

// Codes of Q&A import warnings.
const (
	CodeQARowIncomplete = "QA_ROW_INCOMPLETE"
)

// QASheet maps the question and answer columns of one sheet. Columns are
// found by header text (the joined header path, see the package comment).
// Unknown YAML fields are rejected.
type QASheet struct {
	Sheet string `yaml:"sheet" json:"sheet"`
	// HeaderStartRow is the first header row (optional, default 1).
	HeaderStartRow int `yaml:"headerStartRow,omitempty" json:"headerStartRow,omitempty"`
	// HeaderRows is the number of header rows (>= 1). Data starts below.
	HeaderRows int `yaml:"headerRows" json:"headerRows"`
	// Question and Answer are the header texts of the two required columns.
	Question string `yaml:"question" json:"question"`
	Answer   string `yaml:"answer" json:"answer"`
	// Alternates are header texts of columns holding other ways to ask the
	// question. A cell may hold one variant per line, or several variants
	// separated by the full-width semicolon "；" (see SplitVariants).
	Alternates []string `yaml:"alternates,omitempty" json:"alternates,omitempty"`
	// Language is EN, ZH or AUTO (default).
	Language string `yaml:"language,omitempty" json:"language,omitempty"`
}

// QAMappingFile is the content of a `<name>.qa.yaml` file.
type QAMappingFile struct {
	Sheets []QASheet `yaml:"sheets" json:"sheets"`
}

func (s QASheet) headerStart() int {
	if s.HeaderStartRow > 0 {
		return s.HeaderStartRow
	}
	return 1
}

// ParseQAMappingFile parses and validates a Q&A mapping file, rejecting
// unknown fields. Every problem found is listed in the returned *Error's
// *MappingError (code MAPPING_INVALID). The language is normalised: empty
// becomes AUTO and the value is upper-cased.
func ParseQAMappingFile(data []byte) (*QAMappingFile, error) {
	var f QAMappingFile
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.Strict(), yaml.DisallowUnknownField())
	if err := dec.Decode(&f); err != nil {
		return nil, &Error{Code: CodeMappingInvalid, Message: "Q&A mapping file YAML cannot be decoded", Err: err}
	}
	var iss []string
	add := func(format string, a ...any) { iss = append(iss, fmt.Sprintf(format, a...)) }
	if len(f.Sheets) == 0 {
		add("sheets must not be empty")
	}
	seen := map[string]bool{}
	for i := range f.Sheets {
		s := &f.Sheets[i]
		at := fmt.Sprintf("sheets[%d]", i)
		if strings.TrimSpace(s.Sheet) == "" {
			add("%s.sheet is required", at)
		} else if seen[s.Sheet] {
			add("%s.sheet %q is declared twice", at, s.Sheet)
		}
		seen[s.Sheet] = true
		if s.HeaderRows < 1 {
			add("%s.headerRows must be >= 1, got %d", at, s.HeaderRows)
		}
		if s.HeaderStartRow < 0 {
			add("%s.headerStartRow must be >= 1 when set, got %d", at, s.HeaderStartRow)
		}
		q, a := normSpace(s.Question), normSpace(s.Answer)
		if q == "" {
			add("%s.question is required", at)
		}
		if a == "" {
			add("%s.answer is required", at)
		}
		if q != "" && q == a {
			add("%s.question and answer name the same header %q", at, s.Question)
		}
		used := map[string]bool{q: true, a: true}
		for j, h := range s.Alternates {
			h = normSpace(h)
			switch {
			case h == "":
				add("%s.alternates[%d] is empty", at, j)
			case used[h]:
				add("%s.alternates[%d] %q repeats another column of the sheet", at, j, h)
			}
			used[h] = true
		}
		switch strings.ToUpper(strings.TrimSpace(s.Language)) {
		case "":
			s.Language = QALanguageAuto
		case QALanguageEN, QALanguageZH, QALanguageAuto:
			s.Language = strings.ToUpper(strings.TrimSpace(s.Language))
		default:
			add("%s.language %q must be EN, ZH or AUTO", at, s.Language)
		}
	}
	if len(iss) > 0 {
		return nil, &Error{Code: CodeMappingInvalid, Message: "Q&A mapping file is invalid", Err: &MappingError{Issues: iss}}
	}
	return &f, nil
}

// SheetNames returns the sheets the file reads, in declaration order.
func (f *QAMappingFile) SheetNames() []string {
	if f == nil {
		return nil
	}
	out := make([]string, len(f.Sheets))
	for i, s := range f.Sheets {
		out[i] = s.Sheet
	}
	return out
}

// SharedSheets returns the sheets claimed by both a facts mapping and a Q&A
// mapping, sorted.
func SharedSheets(qa *QAMappingFile, facts *MappingFile) []string {
	var out []string
	if qa == nil || facts == nil {
		return nil
	}
	for _, s := range qa.SheetNames() {
		if slices.Contains(facts.Sheets(), s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

// SplitVariants splits a cell of alternate questions: one variant per line,
// or variants separated by "；" (U+FF1B). Each variant is trimmed (inner
// white space collapsed) and empty ones are dropped. The ASCII semicolon is
// not a separator because it occurs inside questions.
func SplitVariants(cell string) []string {
	cell = strings.NewReplacer("\r\n", "\n", "\r", "\n", "；", "\n").Replace(cell)
	var out []string
	for _, v := range strings.Split(cell, "\n") {
		if v = normSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// QARow is one question and answer read from a sheet.
type QARow struct {
	Sheet string `json:"sheet"`
	// Row is the 1-based sheet row.
	Row int `json:"row"`
	// SourceRef is "<Sheet>!A<row>".
	SourceRef string `json:"sourceRef"`
	// Question has its white space collapsed to single spaces.
	Question string `json:"question"`
	// Alternates holds every variant of the alternate columns in column
	// order, without duplicates.
	Alternates []string `json:"alternates"`
	// Answer is the cell text as written (trimmed, line breaks kept).
	Answer string `json:"answer"`
	// Language is the mapping's EN, ZH or AUTO.
	Language string `json:"language"`
}

// QAResult is the outcome of ImportQA. When Issues is non-empty the caller
// rejects the import and Rows is empty.
type QAResult struct {
	Rows     []QARow   `json:"rows"`
	Issues   []Issue   `json:"issues"`
	Warnings []Warning `json:"warnings"`
}

// OK reports whether the import has no issues.
func (r *QAResult) OK() bool { return len(r.Issues) == 0 }

// ImportQABytes is ImportQA over an in-memory file.
func ImportQABytes(b []byte, f *QAMappingFile) (*QAResult, error) {
	return ImportQA(bytes.NewReader(b), int64(len(b)), f)
}

// ImportQA reads the question and answer rows of every mapped sheet.
// File-level problems are returned as *Error; problems of the sheets (missing
// sheet, missing or ambiguous header) are Issues.
//
// A row whose mapped cells are all empty is skipped silently. A hidden row
// with content is skipped with HIDDEN_ROW_SKIPPED, and a row with an empty
// question or an empty answer is skipped with QA_ROW_INCOMPLETE. A formula
// without a cached value counts as empty and is reported.
func ImportQA(r io.ReaderAt, size int64, f *QAMappingFile) (*QAResult, error) {
	wb, err := openWorkbook(r, size)
	if err != nil {
		return nil, err
	}
	defer wb.close()
	res := &QAResult{Rows: []QARow{}, Issues: []Issue{}}
	ws := &warnSet{}
	defer func() { res.Warnings = ws.out() }()
	issue := func(i Issue) { res.Issues = append(res.Issues, i) }
	sheets := wb.f.GetSheetList()

	for _, m := range f.Sheets {
		if !slices.Contains(sheets, m.Sheet) {
			issue(Issue{Code: CodeSheetNotFound, Detail: fmt.Sprintf("sheet %q does not exist", m.Sheet), Location: m.Sheet})
			continue
		}
		if vis, err := wb.f.GetSheetVisible(m.Sheet); err == nil && !vis {
			issue(Issue{Code: CodeSheetHidden, Detail: fmt.Sprintf("sheet %q is hidden and is excluded from import", m.Sheet), Location: m.Sheet})
			continue
		}
		g, err := newGrid(wb, m.Sheet)
		if err != nil {
			return nil, &Error{Code: CodeXLSXCorrupt, Message: "sheet cannot be read", Err: err}
		}
		importQASheet(g, m, ws, res)
	}
	if len(res.Issues) > 0 {
		res.Rows = []QARow{}
	}
	return res, nil
}

func importQASheet(g *grid, m QASheet, ws *warnSet, res *QAResult) {
	hs := m.headerStart()
	paths := g.headerPaths(hs, m.HeaderRows, ws)
	byHeader := map[string][]int{}
	for c := 1; c <= g.maxCol; c++ {
		if paths[c] != "" {
			byHeader[paths[c]] = append(byHeader[paths[c]], c)
		}
	}
	headerRange := g.rangeRef(hs, 1, hs+m.HeaderRows-1, max(g.maxCol, 1))
	find := func(role, header string) int {
		found := byHeader[normSpace(header)]
		switch len(found) {
		case 0:
			res.Issues = append(res.Issues, Issue{Code: CodeHeaderNotFound, Detail: fmt.Sprintf("%s header %q not found", role, header), Location: headerRange, Column: role})
		case 1:
			return found[0]
		default:
			res.Issues = append(res.Issues, Issue{Code: CodeHeaderAmbiguous, Detail: fmt.Sprintf("%s header %q matches %d columns", role, header, len(found)), Location: headerRange, Column: role})
		}
		return 0
	}
	qc, ac := find("question", m.Question), find("answer", m.Answer)
	var altCols []int
	for _, h := range m.Alternates {
		altCols = append(altCols, find("alternates", h))
	}
	if qc == 0 || ac == 0 || slices.Contains(altCols, 0) {
		return
	}

	text := func(row, col int) string {
		cd := g.at(row, col)
		if cd.noCache {
			ws.add(Warning{Code: CodeFormulaNoCachedValue, Detail: "formula has no cached value; read as empty", Location: g.sheet + "!" + cd.ref, Row: row})
		}
		return strings.TrimSpace(cd.display)
	}
	cols := append([]int{qc, ac}, altCols...)
	for row := hs + m.HeaderRows; row <= g.maxRow; row++ {
		any := false
		for _, c := range cols {
			if cd := g.at(row, c); !blank(cd) || cd.noCache {
				any = true
				break
			}
		}
		if !any {
			continue
		}
		ref := g.sheet + "!A" + fmt.Sprint(row)
		if !g.rowVisible(row) {
			ws.add(Warning{Code: CodeHiddenRowSkipped, Detail: fmt.Sprintf("hidden row %d excluded from Q&A import", row), Location: ref, Row: row})
			continue
		}
		q, a := normSpace(text(row, qc)), text(row, ac)
		if q == "" || a == "" {
			what := "question"
			if q != "" {
				what = "answer"
			}
			ws.add(Warning{Code: CodeQARowIncomplete, Detail: fmt.Sprintf("row %d has an empty %s and is skipped", row, what), Location: ref, Row: row})
			continue
		}
		alts := []string{}
		for _, c := range altCols {
			for _, v := range SplitVariants(text(row, c)) {
				if !slices.Contains(alts, v) {
					alts = append(alts, v)
				}
			}
		}
		res.Rows = append(res.Rows, QARow{Sheet: g.sheet, Row: row, SourceRef: ref, Question: q, Alternates: alts, Answer: a, Language: m.Language})
	}
}
