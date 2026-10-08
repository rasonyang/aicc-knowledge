// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from %s (run with -update to regenerate)\n%s", name, got)
	}
}

func sheetXML(t *testing.T, file, part string) string {
	t.Helper()
	b := fixture(t, file)
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == part {
			rc, _ := f.Open()
			defer rc.Close()
			d, _ := io.ReadAll(rc)
			return string(d)
		}
	}
	t.Fatalf("%s not in %s", part, file)
	return ""
}

func pricingMapping(t *testing.T) *Mapping {
	t.Helper()
	m, err := ParseMapping(fixture(t, "pricing.mapping.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFixtureFormulaCaching(t *testing.T) {
	x := sheetXML(t, "catalog.xlsx", "xl/worksheets/sheet3.xml")
	if !strings.Contains(x, `<c r="B2"><f>A2*2</f><v>4</v></c>`) {
		t.Errorf("B2 must carry a cached value: %s", x)
	}
	if !strings.Contains(x, `<c r="B3" t="str"><f>A3*2</f></c>`) {
		t.Errorf("B3 must have no cached value: %s", x)
	}
}

func TestExtractCatalogGolden(t *testing.T) {
	c, err := ExtractBytes(fixture(t, "catalog.xlsx"), ExtractOptions{ChunkRows: 3})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "catalog.extract.golden.json", marshal(t, c))
}

func TestExtractPricingTwoRowHeaderGolden(t *testing.T) {
	c, err := ExtractBytes(fixture(t, "pricing.xlsx"), ExtractOptions{HeaderRows: 2, ChunkRows: 2})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "pricing.extract.golden.json", marshal(t, c))
}

func TestExtractCatalogRules(t *testing.T) {
	c, err := ExtractBytes(fixture(t, "catalog.xlsx"), ExtractOptions{ChunkRows: 3})
	if err != nil {
		t.Fatal(err)
	}
	var plans, calc []Section
	for _, s := range c.Sections {
		switch s.HeadingPath[0] {
		case "Plans":
			plans = append(plans, s)
		case "Calc":
			calc = append(calc, s)
		case "Archive":
			t.Error("hidden sheet leaked into content")
		}
	}
	// Data rows 2,3,4,5,7,9 (6 is hidden, 8 is blank) -> 2 chunks of 3.
	if len(plans) != 2 || plans[0].SourceRef != "Plans!A2:E4" || plans[1].SourceRef != "Plans!A5:E9" {
		t.Fatalf("plans chunks = %+v", plans)
	}
	// Merged A2:A4 repeats "Mobile" on the rows below; header repeats per chunk.
	for _, want := range []string{"Category | Plan | Price | Discount | Start", "Mobile | Plus | 19.9 | 10% | 2024-01-01", "Mobile | Max | 39.9 | 5% | 2024-03-01"} {
		if !strings.Contains(plans[0].Text, want) {
			t.Errorf("chunk 1 lacks %q:\n%s", want, plans[0].Text)
		}
	}
	if !strings.HasPrefix(plans[1].Text, "Category | Plan | Price | Discount | Start\n") {
		t.Errorf("header not repeated:\n%s", plans[1].Text)
	}
	if strings.Contains(plans[0].Text+plans[1].Text, "retired") {
		t.Error("hidden row leaked")
	}
	if !strings.Contains(plans[1].Text, "Broadband | 1G | 199 |  | 2024-06-01") || !strings.Contains(plans[1].Text, "Other | Line with newline") {
		t.Errorf("chunk 2:\n%s", plans[1].Text)
	}
	if len(calc) != 1 || !strings.Contains(calc[0].Text, "2 | 4 | cached") || !strings.Contains(calc[0].Text, "3 |  | not cached") {
		t.Errorf("calc = %+v", calc)
	}
	counts := map[string]int{}
	locs := map[string]string{}
	for _, w := range c.Warnings {
		counts[w.Code]++
		locs[w.Code] = w.Location
	}
	if counts[CodeHiddenRowSkipped] != 1 || counts[CodeSheetHiddenSkipped] != 1 || counts[CodeFormulaNoCachedValue] != 1 || len(c.Warnings) != 3 {
		t.Errorf("warnings = %+v", c.Warnings)
	}
	if locs[CodeFormulaNoCachedValue] != "Calc!B3" {
		t.Errorf("formula warning location %q", locs[CodeFormulaNoCachedValue])
	}
	for _, w := range c.Warnings {
		if w.Code == CodeHiddenRowSkipped && w.Row != 6 {
			t.Errorf("hidden row warning row = %d", w.Row)
		}
	}
}

func TestExtractDefaults(t *testing.T) {
	c, err := ExtractBytes(fixture(t, "catalog.xlsx"), ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sections) != 2 { // Plans (one chunk of <=50) and Calc
		t.Errorf("sections = %d", len(c.Sections))
	}
}

func TestImportFactsPricingGolden(t *testing.T) {
	res, err := ImportFactsBytes(fixture(t, "pricing.xlsx"), pricingMapping(t))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "pricing.facts.golden.json", marshal(t, res))
}

func TestImportFactsPricingRules(t *testing.T) {
	res, err := ImportFactsBytes(fixture(t, "pricing.xlsx"), pricingMapping(t))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || len(res.Issues) != 0 {
		t.Fatalf("issues = %+v", res.Issues)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("rows = %d, want 5 (rows 3,4,5,6,9)", len(res.Rows))
	}
	var gotRows []int
	for _, r := range res.Rows {
		gotRows = append(gotRows, r.Row)
	}
	if !equalInts(gotRows, []int{3, 4, 5, 6, 9}) {
		t.Errorf("rows = %v", gotRows)
	}
	// Merged data cell A5:A6 gives row 6 the key "Pro".
	if res.Rows[3].Key["plan"] != "Pro" {
		t.Errorf("merged key = %v", res.Rows[3].Key)
	}
	// Exact decimals, cached formula value, text integer, text ISO date.
	if v := res.Rows[3].Values["monthly_price"]; v != json.Number("0.1") {
		t.Errorf("monthly = %#v", v)
	}
	if v := res.Rows[3].Values["yearly_price"]; v != json.Number("123456.789") {
		t.Errorf("yearly = %#v", v)
	}
	if v := res.Rows[0].Values["yearly_price"]; v != json.Number("99") {
		t.Errorf("cached formula = %#v", v)
	}
	if v := res.Rows[2].Values["data_gb"]; v != int64(100) {
		t.Errorf("text integer = %#v", v)
	}
	if s := res.Rows[0].ValidTo; s == nil || *s != "2024-07-01" {
		t.Errorf("text date = %v", s)
	}
	if s := res.Rows[0].ValidFrom; s == nil || *s != "2024-01-01" {
		t.Errorf("serial date = %v", s)
	}
	if res.Rows[1].ValidTo != nil {
		t.Errorf("open-ended validTo = %v", *res.Rows[1].ValidTo)
	}
	if res.Rows[0].SourceRef != "Pricing!A3:G3" {
		t.Errorf("sourceRef = %q", res.Rows[0].SourceRef)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != CodeHiddenRowSkipped || res.Warnings[0].Row != 7 {
		t.Errorf("warnings = %+v", res.Warnings)
	}
	if res.Columns[1].Unit != "CNY" || len(res.Columns) != 7 {
		t.Errorf("columns = %+v", res.Columns)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func errorMapping(sheet string) *Mapping {
	return &Mapping{
		Table: "t", Sheet: sheet, HeaderRows: 1,
		Columns: []Column{
			{Name: "plan", Header: "Plan", Type: TypeString},
			{Name: "price", Header: "Price", Type: TypeDecimal, Required: true},
			{Name: "valid_from", Header: "Valid From", Type: TypeDate},
			{Name: "valid_to", Header: "Valid To", Type: TypeDate},
			{Name: "remark", Header: "Remark", Type: TypeString},
		},
		KeyColumns: []string{"plan"}, ValidFrom: "valid_from", ValidTo: "valid_to",
	}
}

func TestImportFactsValidationErrors(t *testing.T) {
	cases := []struct {
		name     string
		sheet    string
		mutate   func(*Mapping)
		code     string
		count    int
		locs     []string
		warnings int
	}{
		{name: "type mismatch", sheet: "TypeMismatch", code: CodeCellTypeMismatch, count: 1, locs: []string{"TypeMismatch!B2"}},
		{name: "duplicate overlapping key", sheet: "DupOverlap", code: CodeDuplicateKeyOverlap, count: 1, locs: []string{"DupOverlap!A3"}},
		{name: "required missing", sheet: "RequiredMissing", code: CodeRequiredMissing, count: 2, locs: []string{"RequiredMissing!B2", "RequiredMissing!A3"}},
		{name: "validTo not after validFrom", sheet: "BadRange", code: CodeValidityRangeInvalid, count: 2, locs: []string{"BadRange!D2", "BadRange!D3"}},
		{name: "ambiguous text date", sheet: "AmbiguousDate", code: CodeDateAmbiguous, count: 1, locs: []string{"AmbiguousDate!C2"}},
		{name: "required formula without cache", sheet: "NoCache", code: CodeFormulaNoCachedValue, count: 1, locs: []string{"NoCache!B2"}, warnings: 1},
		{name: "hidden sheet", sheet: "Secret", code: CodeSheetHidden, count: 1, locs: []string{"Secret"}},
		{name: "missing sheet", sheet: "Nope", code: CodeSheetNotFound, count: 1, locs: []string{"Nope"}},
		{name: "missing header", sheet: "TypeMismatch", mutate: func(m *Mapping) { m.Columns[1].Header = "Cost" }, code: CodeHeaderNotFound, count: 1, locs: []string{"TypeMismatch!A1:E1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := errorMapping(c.sheet)
			if c.mutate != nil {
				c.mutate(m)
			}
			res, err := ImportFactsBytes(fixture(t, "errors.xlsx"), m)
			if err != nil {
				t.Fatal(err)
			}
			if res.OK() {
				t.Fatal("expected issues")
			}
			if len(res.Issues) != c.count {
				t.Fatalf("issues = %+v, want %d", res.Issues, c.count)
			}
			for i, is := range res.Issues {
				if is.Code != c.code {
					t.Errorf("issue %d code = %s, want %s", i, is.Code, c.code)
				}
				if is.Location != c.locs[i] {
					t.Errorf("issue %d location = %s, want %s", i, is.Location, c.locs[i])
				}
			}
			if len(res.Rows) != 0 {
				t.Errorf("rows must be empty when issues exist, got %d", len(res.Rows))
			}
			if len(res.Warnings) != c.warnings {
				t.Errorf("warnings = %+v, want %d", res.Warnings, c.warnings)
			}
		})
	}
}

func TestImportFactsOptionalUncachedFormulaIsWarningOnly(t *testing.T) {
	m := errorMapping("NoCache")
	m.Columns[1].Required = false
	m.Columns[1].Type = TypeDecimal
	res, err := ImportFactsBytes(fixture(t, "errors.xlsx"), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 0 || len(res.Rows) != 3 || len(res.Warnings) != 2 {
		t.Fatalf("issues=%+v rows=%d warnings=%+v", res.Issues, len(res.Rows), res.Warnings)
	}
	if res.Rows[0].Values["price"] != nil || res.Rows[1].Values["remark"] != nil {
		t.Errorf("uncached cells must import as null: %+v", res.Rows[:2])
	}
}

func TestImportFactsAdjacentPeriodsAreNotOverlap(t *testing.T) {
	m := errorMapping("DupOverlap")
	res, err := ImportFactsBytes(fixture(t, "errors.xlsx"), m)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range res.Issues {
		if strings.Contains(is.Location, "A4") || strings.Contains(is.Location, "A5") {
			t.Errorf("adjacent periods for key C flagged: %+v", is)
		}
	}
}

func TestFileLevelErrors(t *testing.T) {
	good := fixture(t, "catalog.xlsx")
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
	cases := []struct {
		name string
		in   []byte
		code string
	}{
		{"legacy xls", fixture(t, "legacy.xls"), CodeUnsupportedFormat},
		{"word archive", zipWith(map[string]string{"word/document.xml": "<x/>"}), CodeUnsupportedFormat},
		{"not a zip", []byte("plain text, not a workbook"), CodeXLSXCorrupt},
		{"empty", nil, CodeXLSXCorrupt},
		{"truncated", good[:len(good)/2], CodeXLSXCorrupt},
		{"no workbook part", zipWith(map[string]string{"[Content_Types].xml": "<x/>"}), CodeXLSXCorrupt},
		{"garbage workbook part", zipWith(map[string]string{"xl/workbook.xml": "<workbook"}), CodeXLSXCorrupt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ExtractBytes(c.in, ExtractOptions{}); CodeOf(err) != c.code {
				t.Errorf("Extract code = %q (%v), want %q", CodeOf(err), err, c.code)
			}
			if _, err := ImportFactsBytes(c.in, errorMapping("X")); CodeOf(err) != c.code {
				t.Errorf("ImportFacts code = %q (%v), want %q", CodeOf(err), err, c.code)
			}
		})
	}
}

func TestMappingParsing(t *testing.T) {
	m := pricingMapping(t)
	if m.Table != "plan_prices" || len(m.Columns) != 7 || m.HeaderRows != 2 || m.Columns[1].Header != "Price / Monthly" || !m.Columns[1].Required {
		t.Errorf("mapping = %+v", m)
	}
	const base = "table: t\nsheet: S\nheaderRows: 1\ncolumns:\n  - {name: a, header: A, type: STRING}\nkeyColumns: [a]\n"
	if _, err := ParseMapping([]byte(base)); err != nil {
		t.Fatalf("base mapping: %v", err)
	}
	cases := []struct {
		name   string
		yaml   string
		issues int
	}{
		{"unknown field", base + "bogus: 1\n", 0},
		{"unknown column field", strings.Replace(base, "type: STRING}", "type: STRING, scale: 2}", 1), 0},
		{"bad table name", strings.Replace(base, "table: t", "table: PlanPrices", 1), 1},
		{"bad column name", strings.Replace(base, "name: a,", "name: camelCase,", 1), 2}, // name + keyColumns entry unknown
		{"bad type", strings.Replace(base, "STRING", "FLOAT", 1), 1},
		{"headerRows zero", strings.Replace(base, "headerRows: 1", "headerRows: 0", 1), 1},
		{"key not a column", strings.Replace(base, "keyColumns: [a]", "keyColumns: [zz]", 1), 1},
		{"no key", strings.Replace(base, "keyColumns: [a]", "keyColumns: []", 1), 1},
		{"validFrom not a date", base + "validFrom: a\n", 1},
		{"validFrom unknown", base + "validFrom: nope\n", 1},
		{"dataStartRow inside header", base + "dataStartRow: 1\n", 1},
		{"duplicate column", strings.Replace(base, "{name: a, header: A, type: STRING}", "{name: a, header: A, type: STRING}\n  - {name: a, header: B, type: STRING}", 1), 1},
		{"not yaml mapping", "- just\n- a list\n", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseMapping([]byte(c.yaml))
			if CodeOf(err) != CodeMappingInvalid {
				t.Fatalf("err = %v", err)
			}
			if c.issues > 0 {
				var me *MappingError
				if !asMappingError(err, &me) || len(me.Issues) != c.issues {
					t.Errorf("issues = %v, want %d", err, c.issues)
				}
			}
		})
	}
}

func asMappingError(err error, target **MappingError) bool {
	for err != nil {
		if me, ok := err.(*MappingError); ok {
			*target = me
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestValueHelpers(t *testing.T) {
	dec := map[string]string{
		"0.10000000000000001": "0.1", "1E-05": "0.00001", "19.90": "19.9", "-0": "0", "123456.789": "123456.789", "100": "100",
	}
	for in, want := range dec {
		got, ok := numberText(in)
		if !ok || got != want {
			t.Errorf("numberText(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for in, want := range map[string]string{"+19.90": "19.9", "007.50": "7.5", ".5": "0.5", "-0.0": "0", "5.": "5", "100": "100"} {
		if got := canonicalDecimal(in); got != want {
			t.Errorf("canonicalDecimal(%q) = %q want %q", in, got, want)
		}
	}
	for _, c := range []struct {
		id     int
		custom string
		want   bool
	}{
		{14, "", true}, {22, "", true}, {0, "", false}, {10, "", false}, {2, "", false},
		{164, "yyyy-mm-dd", true}, {164, `0.0 "m"`, false}, {164, "[$-409]d-mmm", true},
		{164, "0.00E+00", false}, {164, `#,##0.00 [$CNY]`, false}, {164, "hh:mm:ss", true},
	} {
		if got := isDateFormat(c.id, c.custom); got != c.want {
			t.Errorf("isDateFormat(%d,%q) = %v", c.id, c.custom, got)
		}
	}
}

func TestConvertTextForms(t *testing.T) {
	g := &grid{wb: &workbook{}}
	text := func(s string) cellData { return cellData{raw: s, display: s, typ: 5} } // CellTypeInlineString
	cases := []struct {
		in   string
		typ  ColumnType
		want any
		code string
	}{
		{"2024-02-29", TypeDate, "2024-02-29", ""},
		{"2024/3/1", TypeDate, "2024-03-01", ""},
		{"2024年3月1日", TypeDate, "2024-03-01", ""},
		{"2023-02-29", TypeDate, nil, CodeCellTypeMismatch},
		{"01/02/2024", TypeDate, nil, CodeDateAmbiguous},
		{"1.2.24", TypeDate, nil, CodeDateAmbiguous},
		{"next week", TypeDate, nil, CodeCellTypeMismatch},
		{" 42 ", TypeInteger, int64(42), ""},
		{"4.2", TypeInteger, nil, CodeCellTypeMismatch},
		{"1,000", TypeInteger, nil, CodeCellTypeMismatch},
		{"12.50", TypeDecimal, json.Number("12.5"), ""},
		{"1,000.5", TypeDecimal, nil, CodeCellTypeMismatch},
		{"是", TypeBoolean, true, ""},
		{"No", TypeBoolean, false, ""},
		{"maybe", TypeBoolean, nil, CodeCellTypeMismatch},
	}
	for _, c := range cases {
		v, code, _ := g.convert(text(c.in), c.typ)
		if code != c.code || v != c.want {
			t.Errorf("convert(%q,%s) = %#v,%q want %#v,%q", c.in, c.typ, v, code, c.want, c.code)
		}
	}
}

// TestRealConsumer opens the fixtures with officecli, an independent
// implementation, when it is installed.
func TestRealConsumer(t *testing.T) {
	bin, err := exec.LookPath("officecli")
	if err != nil {
		t.Skip("officecli not installed")
	}
	path, _ := filepath.Abs("testdata/catalog.xlsx")
	out, err := exec.Command(bin, "view", path, "text").CombinedOutput()
	if err != nil {
		t.Skipf("officecli could not read fixture: %v\n%s", err, out)
	}
	for _, w := range []string{"Broadband", "Basic", "Calc"} {
		if !strings.Contains(string(out), w) {
			t.Errorf("officecli output lacks %q:\n%s", w, out)
		}
	}
}

func TestParseMappingFile(t *testing.T) {
	inner := string(fixture(t, "pricing.mapping.yaml"))
	var wrapped strings.Builder
	wrapped.WriteString("tables:\n")
	for _, line := range strings.Split(strings.TrimSpace(inner), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "table:") {
			wrapped.WriteString("  - " + line + "\n")
		} else {
			wrapped.WriteString("    " + line + "\n")
		}
	}
	mf, err := ParseMappingFile([]byte(wrapped.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(mf.Tables) != 1 || mf.Tables[0].Table != "plan_prices" || len(mf.Tables[0].Columns) != 7 {
		t.Fatalf("tables = %+v", mf.Tables)
	}
	if got := mf.Sheets(); len(got) != 1 || got[0] != "Pricing" {
		t.Errorf("sheets = %v", got)
	}

	bad := map[string]string{
		"empty":         "",
		"no tables":     "tables: []\n",
		"unknown field": "tables:\n  - table: t\n    sheet: S\n    headerRows: 1\n    bogus: 1\n",
		"top unknown":   "other: 1\n",
		"invalid table": "tables:\n  - table: Bad Name\n    sheet: S\n    headerRows: 1\n",
		"duplicate":     strings.Replace(wrapped.String(), "keyColumns: [plan]", "keyColumns: [plan]", 1) + strings.TrimPrefix(wrapped.String(), "tables:\n"),
	}
	for name, doc := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMappingFile([]byte(doc))
			if CodeOf(err) != CodeMappingInvalid {
				t.Fatalf("err = %v, want MAPPING_INVALID", err)
			}
		})
	}
	var me *MappingError
	_, err = ParseMappingFile([]byte(bad["duplicate"]))
	if !errors.As(err, &me) || len(me.Issues) != 1 || !strings.Contains(me.Issues[0], "declared twice") {
		t.Errorf("duplicate issues = %+v", me)
	}
}

func TestExtractExcludeSheets(t *testing.T) {
	c, err := ExtractBytes(fixture(t, "catalog.xlsx"), ExtractOptions{ExcludeSheets: []string{"Plans", "Archive"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sections) != 1 || c.Sections[0].HeadingPath[0] != "Calc" || c.Sections[0].Ordinal != 1 {
		t.Fatalf("sections = %+v", c.Sections)
	}
	// Excluded sheets are not warned about: the caller handles them.
	if len(c.Warnings) != 1 || c.Warnings[0].Code != CodeFormulaNoCachedValue {
		t.Errorf("warnings = %+v", c.Warnings)
	}
}
