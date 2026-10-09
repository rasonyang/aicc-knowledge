// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// FactRow is one validated data row.
type FactRow struct {
	// Row is the 1-based sheet row number.
	Row int `json:"row"`
	// SourceRef is the row as a range, e.g. "Pricing!A3:G3".
	SourceRef string `json:"sourceRef"`
	// Values holds every mapped column. STRING -> string, INTEGER -> int64,
	// DECIMAL -> json.Number (exact decimal text), DATE -> "yyyy-mm-dd",
	// BOOLEAN -> bool, empty optional cell -> nil.
	Values    map[string]any `json:"values"`
	Key       map[string]any `json:"key"`
	ValidFrom *string        `json:"validFrom"`
	ValidTo   *string        `json:"validTo"`
}

// FactsResult is the outcome of ImportFacts. When Issues is non-empty the
// caller must reject the whole import; Rows is then empty so a partial set can
// never be used by accident.
type FactsResult struct {
	Table    string    `json:"table"`
	Columns  []Column  `json:"columns"`
	Rows     []FactRow `json:"rows"`
	Issues   []Issue   `json:"issues"`
	Warnings []Warning `json:"warnings"`
}

// OK reports whether the import has no issues.
func (r *FactsResult) OK() bool { return len(r.Issues) == 0 }

// ImportFactsBytes is ImportFacts over an in-memory file.
func ImportFactsBytes(b []byte, m *Mapping) (*FactsResult, error) {
	return ImportFacts(bytes.NewReader(b), int64(len(b)), m)
}

// ImportFacts reads the mapped sheet into typed rows. File-level problems
// (unsupported or corrupt file, invalid mapping) are returned as *Error;
// everything about the sheet's content is reported in FactsResult.Issues.
func ImportFacts(r io.ReaderAt, size int64, m *Mapping) (*FactsResult, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	wb, err := openWorkbook(r, size)
	if err != nil {
		return nil, err
	}
	defer wb.close()

	res := &FactsResult{Table: m.Table, Columns: append([]Column(nil), m.Columns...), Rows: []FactRow{}, Issues: []Issue{}}
	ws := &warnSet{}
	defer func() { res.Warnings = ws.out() }()
	issue := func(i Issue) { res.Issues = append(res.Issues, i) }

	idx := -1
	for i, s := range wb.f.GetSheetList() {
		if s == m.Sheet {
			idx = i
		}
	}
	if idx < 0 {
		issue(Issue{Code: CodeSheetNotFound, Detail: fmt.Sprintf("sheet %q does not exist", m.Sheet), Location: m.Sheet})
		return res, nil
	}
	if vis, err := wb.f.GetSheetVisible(m.Sheet); err == nil && !vis {
		issue(Issue{Code: CodeSheetHidden, Detail: fmt.Sprintf("sheet %q is hidden and is excluded from import", m.Sheet), Location: m.Sheet})
		return res, nil
	}
	g, err := newGrid(wb, m.Sheet)
	if err != nil {
		return nil, &Error{Code: CodeXLSXCorrupt, Message: "sheet cannot be read", Err: err}
	}

	// Locate columns by header path.
	hs := m.headerStart()
	paths := g.headerPaths(hs, m.HeaderRows, ws)
	byHeader := map[string][]int{}
	for c := 1; c <= g.maxCol; c++ {
		if paths[c] != "" {
			byHeader[paths[c]] = append(byHeader[paths[c]], c)
		}
	}
	colIdx := make([]int, len(m.Columns))
	headerRange := g.rangeRef(hs, 1, hs+m.HeaderRows-1, max(g.maxCol, 1))
	for i, c := range m.Columns {
		found := byHeader[normSpace(c.Header)]
		switch len(found) {
		case 0:
			issue(Issue{Code: CodeHeaderNotFound, Detail: fmt.Sprintf("header %q not found", c.Header), Location: headerRange, Column: c.Name})
		case 1:
			colIdx[i] = found[0]
		default:
			issue(Issue{Code: CodeHeaderAmbiguous, Detail: fmt.Sprintf("header %q matches %d columns", c.Header, len(found)), Location: headerRange, Column: c.Name})
		}
	}
	if len(res.Issues) > 0 {
		return res, nil
	}

	isKey := map[string]bool{}
	for _, k := range m.KeyColumns {
		isKey[k] = true
	}
	maxMapped := 0
	for _, c := range colIdx {
		maxMapped = max(maxMapped, c)
	}
	dataStart := m.DataStartRow
	if dataStart == 0 {
		dataStart = hs + m.HeaderRows
	}

	type accepted struct {
		row      int
		from, to string
	}
	groups := map[string][]accepted{}
	var okRows []FactRow
	var okKeys []string

	for row := dataStart; row <= g.maxRow; row++ {
		hasContent := false
		for _, c := range colIdx {
			cd := g.at(row, c)
			if cd.noCache || !blank(cd) {
				hasContent = true
				break
			}
		}
		if !hasContent {
			continue
		}
		rowRef := g.rangeRef(row, 1, row, maxMapped)
		if !g.rowVisible(row) {
			ws.add(Warning{Code: CodeHiddenRowSkipped, Detail: fmt.Sprintf("hidden row %d excluded from import", row), Location: rowRef, Row: row})
			continue
		}
		values := make(map[string]any, len(m.Columns))
		rowOK := true
		for i, c := range m.Columns {
			cd := g.at(row, colIdx[i])
			loc := m.Sheet + "!" + cd.ref
			required := c.Required || isKey[c.Name]
			switch {
			case cd.noCache:
				if required {
					issue(Issue{Code: CodeFormulaNoCachedValue, Detail: "key or required cell holds a formula with no cached value", Location: loc, Column: c.Name, Row: row})
					rowOK = false
				} else {
					ws.add(Warning{Code: CodeFormulaNoCachedValue, Detail: "formula has no cached value; imported as null", Location: loc, Row: row})
				}
				values[c.Name] = nil
			case blank(cd):
				if required {
					cl, _ := cellName(colIdx[i], row)
					issue(Issue{Code: CodeRequiredMissing, Detail: "required value is empty", Location: m.Sheet + "!" + cl, Column: c.Name, Row: row})
					rowOK = false
				}
				values[c.Name] = nil
			default:
				v, code, detail := g.convert(cd, c.Type)
				if code != "" {
					issue(Issue{Code: code, Detail: detail, Location: loc, Column: c.Name, Row: row})
					rowOK = false
					break
				}
				values[c.Name] = v
			}
		}
		if !rowOK {
			continue
		}
		fr := FactRow{Row: row, SourceRef: rowRef, Values: values, Key: map[string]any{}}
		keyVals := make([]any, len(m.KeyColumns))
		for i, k := range m.KeyColumns {
			fr.Key[k] = values[k]
			keyVals[i] = values[k]
		}
		kb, _ := json.Marshal(keyVals)
		var from, to string
		if m.ValidFrom != "" {
			if s, ok := values[m.ValidFrom].(string); ok {
				fr.ValidFrom, from = &s, s
			}
		}
		if m.ValidTo != "" {
			if s, ok := values[m.ValidTo].(string); ok {
				fr.ValidTo, to = &s, s
			}
		}
		if from != "" && to != "" && to <= from {
			ci := indexOf(m, m.ValidTo)
			ref, _ := cellName(colIdx[ci], row)
			issue(Issue{Code: CodeValidityRangeInvalid, Detail: fmt.Sprintf("validTo %s is not after validFrom %s", to, from), Location: m.Sheet + "!" + ref, Column: m.ValidTo, Row: row})
			continue
		}
		okRows = append(okRows, fr)
		okKeys = append(okKeys, string(kb))
	}

	// Overlap check, in row order, against earlier accepted rows of the same key.
	for i, fr := range okRows {
		a := accepted{row: fr.Row}
		if fr.ValidFrom != nil {
			a.from = *fr.ValidFrom
		}
		a.to = "\xff"
		if fr.ValidTo != nil {
			a.to = *fr.ValidTo
		}
		clash := 0
		for _, b := range groups[okKeys[i]] {
			if a.from < b.to && b.from < a.to {
				clash = b.row
				break
			}
		}
		groups[okKeys[i]] = append(groups[okKeys[i]], a)
		if clash != 0 {
			ci := indexOf(m, m.KeyColumns[0])
			ref, _ := cellName(colIdx[ci], fr.Row)
			issue(Issue{Code: CodeDuplicateKeyOverlap, Detail: fmt.Sprintf("key %s has overlapping validity with row %d", strings.TrimSpace(string(mustJSON(fr.Key))), clash), Location: m.Sheet + "!" + ref, Column: m.KeyColumns[0], Row: fr.Row})
		}
	}

	if len(res.Issues) == 0 {
		res.Rows = okRows
	}
	return res, nil
}

func blank(c cellData) bool {
	return c.empty() || (c.isText() && strings.TrimSpace(c.raw) == "")
}

func indexOf(m *Mapping, name string) int {
	for i, c := range m.Columns {
		if c.Name == name {
			return i
		}
	}
	return 0
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
