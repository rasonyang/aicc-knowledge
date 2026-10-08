// SPDX-License-Identifier: Apache-2.0

// Command gen writes the .xlsx fixtures under internal/xlsx/testdata. It runs
// through `go generate ./internal/xlsx`. Workbooks are built with excelize and
// then rewritten with fixed zip timestamps and file order so the output is
// byte-reproducible. Formula cells written by excelize carry no cached value;
// cache patches add a <v> for the few formulas that need one.
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/xuri/excelize/v2"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// patch maps a sheet part name to the cell refs that get a cached numeric value.
type patch map[string]map[string]string

// finish serialises f, injects cached formula values and rewrites the archive
// deterministically.
func finish(f *excelize.File, name string, p patch) {
	buf, err := f.WriteToBuffer()
	must(err)
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	must(err)
	names := make([]string, 0, len(zr.File))
	byName := map[string]*zip.File{}
	for _, zf := range zr.File {
		names = append(names, zf.Name)
		byName[zf.Name] = zf
	}
	sort.Strings(names)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, n := range names {
		rc, err := byName[n].Open()
		must(err)
		data, err := io.ReadAll(rc)
		must(err)
		rc.Close()
		for ref, v := range p[n] {
			re := regexp.MustCompile(`<c r="` + ref + `" t="str">(<f>[^<]*</f>)</c>`)
			if !re.Match(data) {
				must(fmt.Errorf("%s: formula cell %s not found for cache patch", n, ref))
			}
			data = re.ReplaceAll(data, []byte(`<c r="`+ref+`">${1}<v>`+v+`</v></c>`))
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
		must(err)
		_, err = w.Write(data)
		must(err)
	}
	must(zw.Close())
	must(os.WriteFile(filepath.Join("testdata", name), out.Bytes(), 0o644))
}

func set(f *excelize.File, sheet, ref string, v any) { must(f.SetCellValue(sheet, ref, v)) }

func row(f *excelize.File, sheet string, r int, vals ...any) {
	for i, v := range vals {
		if v == nil {
			continue
		}
		ref, err := excelize.CoordinatesToCellName(i+1, r)
		must(err)
		set(f, sheet, ref, v)
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func dateStyle(f *excelize.File) int {
	s, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	must(err)
	return s
}

func isoStyle(f *excelize.File) int {
	fmtCode := "yyyy-mm-dd"
	s, err := f.NewStyle(&excelize.Style{CustomNumFmt: &fmtCode})
	must(err)
	return s
}

func catalog() {
	f := excelize.NewFile()
	must(f.SetSheetName("Sheet1", "Plans"))
	row(f, "Plans", 1, "Category", "Plan", "Price", "Discount", "Start")
	row(f, "Plans", 2, "Mobile", "Basic", 9.9, 0.15, day(2024, 1, 1))
	row(f, "Plans", 3, nil, "Plus", 19.9, 0.1, day(2024, 1, 1))
	row(f, "Plans", 4, nil, "Max", 39.9, 0.05, day(2024, 3, 1))
	row(f, "Plans", 5, "Broadband", "100M", 79, nil, day(2024, 1, 1))
	row(f, "Plans", 6, nil, "500M (retired)", 129, nil, day(2023, 1, 1))
	row(f, "Plans", 7, nil, "1G", 199, nil, day(2024, 6, 1))
	row(f, "Plans", 9, "Other", "Line with\nnewline", 1, nil, nil)
	must(f.MergeCell("Plans", "A2", "A4"))
	must(f.MergeCell("Plans", "A5", "A7"))
	must(f.SetRowVisible("Plans", 6, false))
	pct, err := f.NewStyle(&excelize.Style{NumFmt: 9})
	must(err)
	must(f.SetCellStyle("Plans", "D2", "D4", pct))
	must(f.SetCellStyle("Plans", "E2", "E7", isoStyle(f)))

	_, err = f.NewSheet("Archive")
	must(err)
	row(f, "Archive", 1, "Old", "Value")
	row(f, "Archive", 2, "x", 1)
	must(f.SetSheetVisible("Archive", false))

	_, err = f.NewSheet("Calc")
	must(err)
	row(f, "Calc", 1, "x", "double", "note")
	row(f, "Calc", 2, 2, nil, "cached")
	must(f.SetCellFormula("Calc", "B2", "A2*2"))
	row(f, "Calc", 3, 3, nil, "not cached")
	must(f.SetCellFormula("Calc", "B3", "A3*2"))
	finish(f, "catalog.xlsx", patch{"xl/worksheets/sheet3.xml": {"B2": "4"}})
}

func pricing() {
	f := excelize.NewFile()
	const s = "Pricing"
	must(f.SetSheetName("Sheet1", s))
	row(f, s, 1, "Plan", "Price", nil, "Data (GB)", "Unlimited", "Validity")
	row(f, s, 2, nil, "Monthly", "Yearly", nil, nil, "From", "To")
	for _, m := range [][2]string{{"A1", "A2"}, {"B1", "C1"}, {"D1", "D2"}, {"E1", "E2"}, {"F1", "G1"}} {
		must(f.MergeCell(s, m[0], m[1]))
	}
	row(f, s, 3, "Basic", 9.9, nil, 10, false, day(2024, 1, 1), "2024-07-01")
	must(f.SetCellFormula(s, "C3", "B3*10"))
	row(f, s, 4, "Basic", 8.9, 89, 10, false, day(2024, 7, 1), nil)
	row(f, s, 5, "Pro", 19.99, 199.9, "100", false, day(2024, 1, 1), day(2024, 7, 1))
	row(f, s, 6, nil, 0.1, 123456.789, 100, false, day(2024, 7, 1), nil)
	must(f.MergeCell(s, "A5", "A6"))
	row(f, s, 7, "Secret", 1, 1, 1, false, day(2024, 1, 1), nil)
	must(f.SetRowVisible(s, 7, false))
	row(f, s, 9, "Max", 0.1, 1234567.891, 500, true, day(2024, 1, 1), nil)
	must(f.SetCellStyle(s, "F3", "G9", isoStyle(f)))
	finish(f, "pricing.xlsx", patch{"xl/worksheets/sheet1.xml": {"C3": "99"}})
}

func errorsBook() {
	f := excelize.NewFile()
	hdr := func(sheet string) {
		row(f, sheet, 1, "Plan", "Price", "Valid From", "Valid To", "Remark")
	}
	add := func(sheet string) {
		_, err := f.NewSheet(sheet)
		must(err)
		hdr(sheet)
		must(f.SetCellStyle(sheet, "C2", "D20", dateStyle(f)))
	}
	must(f.SetSheetName("Sheet1", "TypeMismatch"))
	hdr("TypeMismatch")
	must(f.SetCellStyle("TypeMismatch", "C2", "D20", dateStyle(f)))
	row(f, "TypeMismatch", 2, "A", "abc", day(2024, 1, 1))
	row(f, "TypeMismatch", 3, "B", 12.5, day(2024, 1, 1))

	add("DupOverlap")
	row(f, "DupOverlap", 2, "A", 10, day(2024, 1, 1), day(2024, 12, 31))
	row(f, "DupOverlap", 3, "A", 11, day(2024, 6, 1), nil)
	row(f, "DupOverlap", 4, "C", 5, day(2024, 1, 1), day(2024, 7, 1))
	row(f, "DupOverlap", 5, "C", 6, day(2024, 7, 1), nil)

	add("RequiredMissing")
	row(f, "RequiredMissing", 2, "A", nil, day(2024, 1, 1))
	row(f, "RequiredMissing", 3, nil, 3, day(2024, 1, 1))
	row(f, "RequiredMissing", 4, "B", 4, day(2024, 1, 1))

	add("BadRange")
	row(f, "BadRange", 2, "A", 1, day(2024, 5, 1), day(2024, 5, 1))
	row(f, "BadRange", 3, "B", 2, day(2024, 5, 1), day(2024, 4, 1))
	row(f, "BadRange", 4, "C", 3, day(2024, 5, 1), day(2024, 6, 1))

	add("AmbiguousDate")
	row(f, "AmbiguousDate", 2, "A", 1, "01/02/2024")
	row(f, "AmbiguousDate", 3, "B", 2, "2024-03-01")
	row(f, "AmbiguousDate", 4, "C", 3, "2024/3/1")

	add("NoCache")
	row(f, "NoCache", 2, "A", nil, day(2024, 1, 1))
	must(f.SetCellFormula("NoCache", "B2", "1+1"))
	row(f, "NoCache", 3, "B", 2, day(2024, 1, 1))
	must(f.SetCellFormula("NoCache", "E3", `"x"&"y"`))
	row(f, "NoCache", 4, "C", 3, day(2024, 1, 1))

	add("Secret")
	row(f, "Secret", 2, "A", 1, day(2024, 1, 1))
	must(f.SetSheetVisible("Secret", false))
	finish(f, "errors.xlsx", nil)
}

// qa is a workbook of curated question and answer columns: an English sheet
// (short, empty, hidden, multi-line and over-long answers), a Chinese sheet
// with a short answer, and a notes sheet that stays ordinary content.
func qa() {
	f := excelize.NewFile()
	must(f.SetSheetName("Sheet1", "FAQ"))
	row(f, "FAQ", 1, "Question", "Answer", "Variants")
	row(f, "FAQ", 2, "Does the X1 support wireless charging?", "Yes, it supports wireless charging.", "wireless charging?\ncan I charge it wirelessly?")
	row(f, "FAQ", 3, "How long is the warranty?", "The warranty lasts 2 years.", "warranty period；how long does the warranty last")
	row(f, "FAQ", 4, "Hidden question?", "Hidden answer.")
	row(f, "FAQ", 5, "Is there a data cap?", nil)
	row(f, "FAQ", 6, "How do I reset the device?", "Press and hold the power button for 10 seconds.\n- Wait for the light to blink twice.\n- Release the button.\nThe device restarts.")
	row(f, "FAQ", 7, "Which colors can I choose?", "You can choose from black, white, red, blue, green, yellow, orange, purple, pink, grey, silver and gold, and every color is available in all sizes and in both the standard and the limited edition, with free engraving on request for orders placed online.")
	must(f.SetRowVisible("FAQ", 4, false))

	_, err := f.NewSheet("常见问题")
	must(err)
	row(f, "常见问题", 1, "问题", "回答", "其他问法")
	row(f, "常见问题", 2, "是否支持无线充电？", "不支持。", "能无线充电吗；支持无线充吗")

	_, err = f.NewSheet("Notes")
	must(err)
	row(f, "Notes", 1, "Topic", "Detail")
	row(f, "Notes", 2, "Shipping", "Orders ship within three business days after the payment is confirmed.")
	row(f, "Notes", 3, "Returns", "Unused items can be returned within thirty days of delivery.")
	finish(f, "qa.xlsx", nil)
}

func main() {
	qa()
	catalog()
	pricing()
	errorsBook()
	// A tiny OLE-signature file stands in for a legacy .xls.
	must(os.WriteFile(filepath.Join("testdata", "legacy.xls"), append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 504)...), 0o644))
}
