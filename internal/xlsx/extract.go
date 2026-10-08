// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// Default extraction parameters used when ExtractOptions fields are <= 0.
const (
	DefaultHeaderRows = 1
	DefaultChunkRows  = 50
	// DefaultMaxChunkChars bounds the text of one section, in characters. It
	// equals the most text generate sends to the LLM (generate.MaxSectionChars;
	// a test keeps the two equal), so a chunk is never cut downstream.
	DefaultMaxChunkChars = 6000
)

// ExtractOptions tunes Extract.
type ExtractOptions struct {
	// HeaderRows is how many rows at the top of every sheet form the header
	// (default 1). Their joined paths are repeated in every chunk.
	HeaderRows int `json:"headerRows"`
	// ChunkRows is the maximum number of data rows per section (default 50).
	ChunkRows int `json:"chunkRows"`
	// MaxChunkChars is the maximum text length of a section in characters,
	// header line included (default DefaultMaxChunkChars). A chunk is closed
	// early when the next row would not fit, so long rows give short chunks.
	// A single row that cannot fit even alone is cut, with a
	// SECTION_TRUNCATED warning.
	MaxChunkChars int `json:"maxChunkChars"`
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
	if opts.MaxChunkChars <= 0 {
		opts.MaxChunkChars = DefaultMaxChunkChars
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
	// The header line is repeated in every chunk, so it may take at most half
	// of the budget; the rest is for rows.
	if n := utf8.RuneCountInString(headerLine); n > opts.MaxChunkChars/2 {
		headerLine = truncateRunes(headerLine, opts.MaxChunkChars/2)
		ws.add(Warning{Code: CodeSectionTruncated, Detail: fmt.Sprintf("header line of %d characters cut to %d", n, opts.MaxChunkChars/2), Location: g.rangeRef(1, 1, opts.HeaderRows, g.maxCol)})
	}
	headerChars := utf8.RuneCountInString(headerLine)

	var lines []string
	used := headerChars // characters of the chunk being built, header included
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
		lines, used = nil, headerChars
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
		line := renderLine(cells)
		n := utf8.RuneCountInString(line)
		if room := opts.MaxChunkChars - headerChars - 1; n > room {
			// Not even alone in a chunk: cut it, loudly.
			ws.add(Warning{Code: CodeSectionTruncated, Detail: fmt.Sprintf("row %d has %d characters, cut to %d", row, n, room), Location: g.rangeRef(row, 1, row, g.maxCol), Row: row})
			line, n = truncateRunes(line, room), room
		}
		if len(lines) > 0 && used+1+n > opts.MaxChunkChars {
			flush()
		}
		if len(lines) == 0 {
			first = row
		}
		last = row
		lines = append(lines, line)
		used += 1 + n
		if len(lines) == opts.ChunkRows {
			flush()
		}
	}
	flush()
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
