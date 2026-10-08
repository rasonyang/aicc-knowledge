// SPDX-License-Identifier: Apache-2.0

// Package xlsx reads .xlsx workbooks for two consumers: deterministic text
// extraction for Q&A generation (Extract) and typed structured-fact import
// driven by a per-sheet YAML mapping (ImportFacts).
//
// Conventions shared by both:
//
//   - Cached values only. A formula cell is read through the value Excel cached
//     in the file. The package never evaluates formulas (no CalcCellValue):
//     evaluation is not deterministic across engine versions and excelize's
//     calculation engine covers only part of Excel's function library, so a
//     computed number could silently differ from what the author saw. A formula
//     cell with no cached value yields the warning FORMULA_NO_CACHED_VALUE (an
//     error when the cell is a key or required fact column). An empty-string
//     cached result is indistinguishable from "no cache" through excelize and is
//     treated the same way.
//   - Merged cells: the value of the top-left cell applies to every cell of the
//     merged range, for headers and data alike.
//   - Hidden sheets are excluded (warning SHEET_HIDDEN_SKIPPED). Hidden rows are
//     excluded and every hidden row that holds content produces a
//     HIDDEN_ROW_SKIPPED warning. Header rows are read even when hidden; hidden
//     columns are not treated specially.
//   - Multi-row headers: a column's header path is its header-row cells (after
//     merge resolution, whitespace-normalised, empty cells dropped, consecutive
//     repeats collapsed) joined by " / ", e.g. "Price / Monthly".
//
// Output is deterministic and no I/O happens beyond reading the given bytes.
package xlsx

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Error codes returned as *Error.
const (
	CodeXLSXCorrupt       = "XLSX_CORRUPT"
	CodeUnsupportedFormat = "UNSUPPORTED_FORMAT"
	CodeMappingInvalid    = "MAPPING_INVALID"
)

// Warning and issue codes.
const (
	CodeFormulaNoCachedValue = "FORMULA_NO_CACHED_VALUE"
	CodeHiddenRowSkipped     = "HIDDEN_ROW_SKIPPED"
	CodeSheetHiddenSkipped   = "SHEET_HIDDEN_SKIPPED"
	CodeSheetUnreadable      = "SHEET_UNREADABLE"

	CodeSheetNotFound        = "SHEET_NOT_FOUND"
	CodeSheetHidden          = "SHEET_HIDDEN"
	CodeHeaderNotFound       = "HEADER_NOT_FOUND"
	CodeHeaderAmbiguous      = "HEADER_AMBIGUOUS"
	CodeCellTypeMismatch     = "CELL_TYPE_MISMATCH"
	CodeDateAmbiguous        = "DATE_AMBIGUOUS"
	CodeCellErrorValue       = "CELL_ERROR_VALUE"
	CodeRequiredMissing      = "REQUIRED_MISSING"
	CodeValidityRangeInvalid = "VALIDITY_RANGE_INVALID"
	CodeDuplicateKeyOverlap  = "DUPLICATE_KEY_OVERLAP"
)

// Error is a coded failure that stops processing of the whole file (or of the
// mapping). Per-cell problems are reported as Issue values instead.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the underlying cause, if any.
func (e *Error) Unwrap() error { return e.Err }

// CodeOf returns the code of err, or "" when err is not an *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Warning is a non-fatal observation. Location is "Sheet!A1" style.
type Warning struct {
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Location string `json:"location"`
	Row      int    `json:"row,omitempty"`
}

// Issue is a validation error found while importing facts. The caller rejects
// the whole import when any Issue exists.
type Issue struct {
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Location string `json:"location"`
	Column   string `json:"column,omitempty"`
	Row      int    `json:"row,omitempty"`
}

type warnSet struct {
	list []Warning
	seen map[string]bool
}

func (w *warnSet) add(x Warning) {
	k := x.Code + "\x00" + x.Location
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	if w.seen[k] {
		return
	}
	w.seen[k] = true
	w.list = append(w.list, x)
}

func (w *warnSet) out() []Warning {
	if w.list == nil {
		return []Warning{}
	}
	return w.list
}

type workbook struct {
	f        *excelize.File
	date1904 bool
}

func (wb *workbook) close() { _ = wb.f.Close() }

var oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

func openWorkbook(r io.ReaderAt, size int64) (*workbook, error) {
	var magic [8]byte
	if n, _ := r.ReadAt(magic[:], 0); n == 8 && bytes.Equal(magic[:], oleMagic) {
		return nil, &Error{Code: CodeUnsupportedFormat, Message: "OLE compound file (legacy .xls or encrypted workbook) is not supported"}
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, &Error{Code: CodeXLSXCorrupt, Message: "not a readable zip archive", Err: err}
	}
	hasWorkbook := false
	for _, zf := range zr.File {
		switch {
		case zf.Name == "xl/workbook.xml":
			hasWorkbook = true
		case zf.Name == "word/document.xml" || strings.HasPrefix(zf.Name, "ppt/"):
			return nil, &Error{Code: CodeUnsupportedFormat, Message: "archive is an Office file but not a spreadsheet"}
		}
	}
	if !hasWorkbook {
		return nil, &Error{Code: CodeXLSXCorrupt, Message: "xl/workbook.xml is missing"}
	}
	f, err := excelize.OpenReader(io.NewSectionReader(r, 0, size), excelize.Options{
		UnzipSizeLimit:    1 << 30,
		UnzipXMLSizeLimit: 256 << 20,
	})
	if err != nil {
		return nil, &Error{Code: CodeXLSXCorrupt, Message: "workbook cannot be opened", Err: err}
	}
	wb := &workbook{f: f}
	if props, err := f.GetWorkbookProps(); err == nil && props.Date1904 != nil {
		wb.date1904 = *props.Date1904
	}
	return wb, nil
}

//go:generate go run ./testdata/gen
