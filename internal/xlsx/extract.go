// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Default extraction parameters used when ExtractOptions fields are <= 0.
const (
	DefaultHeaderRows = 1
	DefaultChunkRows  = 50
)

// ExtractOptions tunes Extract.
type ExtractOptions struct {
	// HeaderRows is how many rows at the top of every sheet form the header
	// (default 1). Their joined paths are repeated in every chunk.
	HeaderRows int `json:"headerRows"`
	// ChunkRows is the maximum number of data rows per section (default 50).
	ChunkRows int `json:"chunkRows"`
	// ExcludeSheets names sheets that are skipped entirely, without a
	// warning: the caller handles them elsewhere (sheets covered by a facts
	// mapping are facts-only). Ordinals count only the sheets that remain.
	ExcludeSheets []string `json:"excludeSheets,omitempty"`
}

// Section is one chunk of one sheet.
type Section struct {
	Ordinal int `json:"ordinal"`
	// HeadingPath is [sheet name].
	HeadingPath []string `json:"headingPath"`
	// Text is the header line followed by one line per data row, cells joined
	// by " | ".
	Text string `json:"text"`
	// FirstRow and LastRow are the first and last data row of the chunk.
	FirstRow int `json:"firstRow"`
	LastRow  int `json:"lastRow"`
	// SourceRef is the data range, e.g. "Plans!A2:D9".
	SourceRef string `json:"sourceRef"`
}

// Content is the result of Extract.
type Content struct {
	Sections []Section `json:"sections"`
	Warnings []Warning `json:"warnings"`
}

// ExtractBytes is Extract over an in-memory file.
func ExtractBytes(b []byte, opts ExtractOptions) (*Content, error) {
	return Extract(bytes.NewReader(b), int64(len(b)), opts)
}

// Extract renders every visible sheet as chunked text sections.
func Extract(r io.ReaderAt, size int64, opts ExtractOptions) (*Content, error) {
	if opts.HeaderRows <= 0 {
		opts.HeaderRows = DefaultHeaderRows
	}
	if opts.ChunkRows <= 0 {
		opts.ChunkRows = DefaultChunkRows
	}
	wb, err := openWorkbook(r, size)
	if err != nil {
		return nil, err
	}
	defer wb.close()

	ws := &warnSet{}
	out := &Content{Sections: []Section{}}
	for _, sheet := range wb.f.GetSheetList() {
		if slices.Contains(opts.ExcludeSheets, sheet) {
			continue
		}
		if vis, err := wb.f.GetSheetVisible(sheet); err == nil && !vis {
			ws.add(Warning{Code: CodeSheetHiddenSkipped, Detail: "hidden sheet excluded", Location: sheet})
			continue
		}
		g, err := newGrid(wb, sheet)
		if err != nil {
			ws.add(Warning{Code: CodeSheetUnreadable, Detail: err.Error(), Location: sheet})
			continue
		}
		extractSheet(g, opts, ws, out)
	}
	out.Warnings = ws.out()
	return out, nil
}

func renderLine(cells []string) string {
	for i, c := range cells {
		c = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(c)
		cells[i] = normSpace(c)
	}
	return strings.Join(cells, " | ")
}

func extractSheet(g *grid, opts ExtractOptions, ws *warnSet, out *Content) {
	if g.maxRow == 0 || g.maxCol == 0 {
		return
	}
	headers := g.headerPaths(1, opts.HeaderRows, ws)
	headerLine := renderLine(append([]string(nil), headers[1:]...))

	var lines []string
	first, last := 0, 0
	flush := func() {
		if len(lines) == 0 {
			return
		}
		out.Sections = append(out.Sections, Section{
			Ordinal:     len(out.Sections) + 1,
			HeadingPath: []string{g.sheet},
			Text:        headerLine + "\n" + strings.Join(lines, "\n"),
			FirstRow:    first,
			LastRow:     last,
			SourceRef:   g.rangeRef(first, 1, last, g.maxCol),
		})
		lines = nil
	}
	for row := opts.HeaderRows + 1; row <= g.maxRow; row++ {
		if !g.rowHasContent(row) {
			continue
		}
		if !g.rowVisible(row) {
			ws.add(Warning{Code: CodeHiddenRowSkipped, Detail: fmt.Sprintf("hidden row %d excluded", row), Location: g.rangeRef(row, 1, row, g.maxCol), Row: row})
			continue
		}
		cells := make([]string, g.maxCol)
		for c := 1; c <= g.maxCol; c++ {
			cd := g.at(row, c)
			if cd.noCache {
				ws.add(Warning{Code: CodeFormulaNoCachedValue, Detail: "formula has no cached value; rendered empty", Location: g.sheet + "!" + cd.ref})
			}
			cells[c-1] = cd.display
		}
		if len(lines) == 0 {
			first = row
		}
		last = row
		lines = append(lines, renderLine(cells))
		if len(lines) == opts.ChunkRows {
			flush()
		}
	}
	flush()
}
