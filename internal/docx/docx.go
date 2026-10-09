// SPDX-License-Identifier: Apache-2.0

// Package docx extracts an ordered list of sections from a .docx file using
// only the standard library (archive/zip and encoding/xml).
//
// Rules, all binding:
//
//   - A paragraph is a heading only when its resolved outline level says so:
//     a direct w:pPr/w:outlineLvl wins, otherwise the paragraph style's
//     w:outlineLvl, following w:basedOn chains (cycles are guarded). Style
//     names and ids are never consulted. Outline level 9 means body text.
//     Heading level = outlineLvl + 1.
//   - Heading text is checked by content as well: an outline-level paragraph
//     longer than 40 characters or ending in 。！.! is a sentence, not a
//     heading. It becomes body text of the current section with a
//     HEADING_DEMOTED warning. A short question (？ or ?) stays a heading.
//     Consecutive identical entries of a heading path collapse into one.
//   - The view is "accept all tracked changes": w:ins and w:moveTo content is
//     kept, w:del and w:moveFrom content is dropped, a deleted paragraph mark
//     merges the paragraph into the next one, formatting changes are ignored.
//   - Tables keep their structure. A cell spanning columns (w:gridSpan) or
//     rows (w:vMerge) repeats its text in every grid cell it covers, so every
//     row is addressable by grid column. Nested tables are flattened into the
//     parent cell text: rows separated by newline, cells by " | ".
//   - Footnotes, endnotes, comments, headers, footers, text boxes and drawings
//     are excluded. Field instructions (w:instrText) are skipped and field
//     results are kept.
//
// The package performs no I/O beyond reading the supplied bytes and its output
// is deterministic.
package docx

//go:generate go run ./testdata/gen

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Error codes returned by Parse.
const (
	CodeDOCXCorrupt       = "DOCX_CORRUPT"
	CodeUnsupportedFormat = "UNSUPPORTED_FORMAT"
)

// Warning codes.
const (
	WarnStyleCycle          = "STYLE_CYCLE"
	WarnStyleBasedOnMissing = "STYLE_BASED_ON_MISSING"
	WarnStyleNotFound       = "STYLE_NOT_FOUND"
	WarnStylesMissing       = "STYLES_MISSING"
	WarnInvalidOutlineLvl   = "INVALID_OUTLINE_LEVEL"
	WarnHeadingDemoted      = "HEADING_DEMOTED"
)

// Error is a coded parse failure.
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

// CodeOf returns the error code of err, or "" when err is not an *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Warning is a non-fatal observation made while parsing.
type Warning struct {
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Location string `json:"location"`
}

// BlockKind discriminates Block.
type BlockKind string

// Block kinds.
const (
	BlockParagraph BlockKind = "PARAGRAPH"
	BlockTable     BlockKind = "TABLE"
)

// Table is a table as rows of grid cells (merged cells repeat their text).
type Table struct {
	Rows [][]string `json:"rows"`
}

// Text renders the table as plain text: one line per row, cells joined by
// " | ", newlines inside a cell shown as "; ", literal pipes escaped.
func (t Table) Text() string {
	lines := make([]string, len(t.Rows))
	for i, row := range t.Rows {
		cells := make([]string, len(row))
		for j, c := range row {
			c = strings.ReplaceAll(c, "|", `\|`)
			c = strings.ReplaceAll(c, "\t", " ")
			c = strings.ReplaceAll(c, "\n", "; ")
			cells[j] = c
		}
		lines[i] = strings.Join(cells, " | ")
	}
	return strings.Join(lines, "\n")
}

// Block is a paragraph or a table inside a section body.
type Block struct {
	Kind  BlockKind `json:"kind"`
	Text  string    `json:"text"`
	Table *Table    `json:"table,omitempty"`
}

// Section is one heading and the body below it, up to the next heading.
// Ordinal 0 with an empty HeadingPath is the preamble.
type Section struct {
	Ordinal     int      `json:"ordinal"`
	HeadingPath []string `json:"headingPath"`
	Level       int      `json:"level"`
	Blocks      []Block  `json:"blocks"`
	SourceRef   string   `json:"sourceRef"`
}

// Text renders the body blocks as plain text, one block per line group.
func (s Section) Text() string {
	parts := make([]string, len(s.Blocks))
	for i, b := range s.Blocks {
		parts[i] = b.Text
	}
	return strings.Join(parts, "\n")
}

// Document is the parse result.
type Document struct {
	Sections []Section `json:"sections"`
	Warnings []Warning `json:"warnings"`
}

// maxPartBytes caps the decompressed size of any XML part read.
const maxPartBytes = 256 << 20

// ParseBytes parses a .docx held in memory.
func ParseBytes(b []byte) (*Document, error) {
	return Parse(bytes.NewReader(b), int64(len(b)))
}

// Parse parses a .docx from r.
func Parse(r io.ReaderAt, size int64) (*Document, error) {
	var magic [8]byte
	if n, _ := r.ReadAt(magic[:], 0); n == 8 && bytes.Equal(magic[:], []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
		return nil, &Error{Code: CodeUnsupportedFormat, Message: "OLE compound file (legacy .doc) is not supported"}
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, &Error{Code: CodeDOCXCorrupt, Message: "not a readable zip archive", Err: err}
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	docFile := files["word/document.xml"]
	if docFile == nil {
		for name := range files {
			if strings.HasPrefix(name, "xl/") || strings.HasPrefix(name, "ppt/") {
				return nil, &Error{Code: CodeUnsupportedFormat, Message: "archive is an Office file but not a word processing document"}
			}
		}
		return nil, &Error{Code: CodeDOCXCorrupt, Message: "word/document.xml is missing"}
	}
	docRoot, err := readTree(docFile)
	if err != nil {
		return nil, &Error{Code: CodeDOCXCorrupt, Message: "word/document.xml is malformed", Err: err}
	}
	b := newBuilder()
	if sf := files["word/styles.xml"]; sf != nil {
		root, err := readTree(sf)
		if err != nil {
			return nil, &Error{Code: CodeDOCXCorrupt, Message: "word/styles.xml is malformed", Err: err}
		}
		b.styles = loadStyles(root)
	} else {
		b.styles = loadStyles(nil)
		b.warn(WarnStylesMissing, "word/styles.xml is absent; only direct outline levels apply", "styles.xml")
	}
	body := docRoot.child("body")
	if body == nil {
		return nil, &Error{Code: CodeDOCXCorrupt, Message: "w:body is missing"}
	}
	b.walkBody(body)
	return b.finish(), nil
}
