// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/xuri/excelize/v2"
)

type mergeRange struct{ r1, c1, r2, c2 int }

// cellData is one resolved cell. For a merged cell it is the top-left cell.
type cellData struct {
	ref     string // A1 reference of the cell the value came from
	raw     string
	display string
	typ     excelize.CellType
	isDate  bool
	noCache bool // formula without a cached value
}

func (c cellData) empty() bool { return c.raw == "" }

func (c cellData) isError() bool { return c.typ == excelize.CellTypeError }

// isText reports whether the stored value is a string (not a number/bool/date).
func (c cellData) isText() bool {
	switch c.typ {
	case excelize.CellTypeInlineString, excelize.CellTypeSharedString, excelize.CellTypeFormula:
		return true
	}
	return false
}

func (c cellData) isNumber() bool {
	return c.typ == excelize.CellTypeNumber || c.typ == excelize.CellTypeUnset
}

type grid struct {
	wb     *workbook
	sheet  string
	maxRow int
	maxCol int
	merges []mergeRange
	cache  map[[2]int]cellData
	styles map[int]bool
}

func newGrid(wb *workbook, sheet string) (*grid, error) {
	rows, err := wb.f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	g := &grid{wb: wb, sheet: sheet, cache: map[[2]int]cellData{}, styles: map[int]bool{}}
	g.maxRow = len(rows)
	for _, r := range rows {
		if len(r) > g.maxCol {
			g.maxCol = len(r)
		}
	}
	if dim, err := wb.f.GetSheetDimension(sheet); err == nil && dim != "" {
		parts := strings.Split(dim, ":")
		if c, _, err := excelize.CellNameToCoordinates(parts[len(parts)-1]); err == nil && c > g.maxCol && c <= 16384 {
			g.maxCol = c
		}
	}
	merges, err := wb.f.GetMergeCells(sheet, true)
	if err != nil {
		return nil, err
	}
	for _, m := range merges {
		c1, r1, e1 := excelize.CellNameToCoordinates(m.GetStartAxis())
		c2, r2, e2 := excelize.CellNameToCoordinates(m.GetEndAxis())
		if e1 != nil || e2 != nil {
			continue
		}
		g.merges = append(g.merges, mergeRange{r1, c1, r2, c2})
		// A merged range extends the grid only up to its owner's data: columns
		// beyond the used width still need headers/values when the owner is inside.
		if r1 <= g.maxRow && c1 <= g.maxCol {
			if r2 > g.maxRow {
				g.maxRow = r2
			}
			if c2 > g.maxCol {
				g.maxCol = c2
			}
		}
	}
	return g, nil
}

func (g *grid) origin(row, col int) (int, int) {
	for _, m := range g.merges {
		if row >= m.r1 && row <= m.r2 && col >= m.c1 && col <= m.c2 {
			return m.r1, m.c1
		}
	}
	return row, col
}

func (g *grid) at(row, col int) cellData {
	row, col = g.origin(row, col)
	key := [2]int{row, col}
	if c, ok := g.cache[key]; ok {
		return c
	}
	ref, _ := excelize.CoordinatesToCellName(col, row)
	c := cellData{ref: ref}
	f := g.wb.f
	c.raw, _ = f.GetCellValue(g.sheet, ref, excelize.Options{RawCellValue: true})
	if c.raw == "" {
		if fm, _ := f.GetCellFormula(g.sheet, ref); fm != "" {
			c.noCache = true
		}
		g.cache[key] = c
		return c
	}
	c.display, _ = f.GetCellValue(g.sheet, ref)
	c.typ, _ = f.GetCellType(g.sheet, ref)
	if c.isNumber() {
		c.isDate = g.dateStyle(ref)
	}
	g.cache[key] = c
	return c
}

func (g *grid) dateStyle(ref string) bool {
	sid, err := g.wb.f.GetCellStyle(g.sheet, ref)
	if err != nil {
		return false
	}
	if v, ok := g.styles[sid]; ok {
		return v
	}
	st, err := g.wb.f.GetStyle(sid)
	v := false
	if err == nil && st != nil {
		custom := ""
		if st.CustomNumFmt != nil {
			custom = *st.CustomNumFmt
		}
		v = isDateFormat(st.NumFmt, custom)
	}
	g.styles[sid] = v
	return v
}

func (g *grid) rowVisible(row int) bool {
	v, err := g.wb.f.GetRowVisible(g.sheet, row)
	return err != nil || v
}

// rowHasContent reports whether any cell of the row holds a value or a formula.
func (g *grid) rowHasContent(row int) bool {
	for c := 1; c <= g.maxCol; c++ {
		cd := g.at(row, c)
		if !cd.empty() || cd.noCache {
			return true
		}
	}
	return false
}

func (g *grid) loc(row, col int) string {
	ref, _ := excelize.CoordinatesToCellName(col, row)
	return g.sheet + "!" + ref
}

func (g *grid) rangeRef(r1, c1, r2, c2 int) string {
	a, _ := excelize.CoordinatesToCellName(c1, r1)
	b, _ := excelize.CoordinatesToCellName(c2, r2)
	return g.sheet + "!" + a + ":" + b
}

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// headerPaths returns the joined header path of every column 1..maxCol
// (index 0 unused). Uncached formulas in header cells are reported to ws.
func (g *grid) headerPaths(start, n int, ws *warnSet) []string {
	out := make([]string, g.maxCol+1)
	for c := 1; c <= g.maxCol; c++ {
		var parts []string
		for r := start; r < start+n; r++ {
			cd := g.at(r, c)
			if cd.noCache {
				ws.add(Warning{Code: CodeFormulaNoCachedValue, Detail: "header cell holds a formula with no cached value", Location: g.sheet + "!" + cd.ref})
			}
			p := normSpace(cd.display)
			if p == "" {
				continue
			}
			if len(parts) > 0 && parts[len(parts)-1] == p {
				continue
			}
			parts = append(parts, p)
		}
		out[c] = strings.Join(parts, " / ")
	}
	return out
}

func isDateFormat(id int, custom string) bool {
	switch {
	case id >= 14 && id <= 22, id >= 27 && id <= 36, id >= 45 && id <= 47, id >= 50 && id <= 58:
		return true
	}
	if custom == "" {
		return false
	}
	var sb strings.Builder
	inQuote, inBracket, esc := false, false, false
	for _, r := range custom {
		switch {
		case esc:
			esc = false
		case r == '\\' || r == '_' || r == '*':
			esc = true
		case r == '"':
			inQuote = !inQuote
		case inQuote:
		case r == '[':
			inBracket = true
		case r == ']':
			inBracket = false
		case inBracket:
		default:
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.ContainsAny(sb.String(), "ymdhs")
}

// serialToDate converts an Excel serial to an ISO date. ok is false when the
// serial has a time-of-day part or is out of range.
func (g *grid) serialToDate(raw string) (string, bool) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v != float64(int64(v)) {
		return "", false
	}
	t, err := excelize.ExcelDateToTime(v, g.wb.date1904)
	if err != nil {
		return "", false
	}
	return t.In(time.UTC).Format("2006-01-02"), true
}

func cellName(col, row int) (string, error) { return excelize.CoordinatesToCellName(col, row) }
