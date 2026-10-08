// SPDX-License-Identifier: Apache-2.0

package docx

import (
	"fmt"
	"strconv"
	"strings"
)

type pathEntry struct {
	level int
	text  string
}

type builder struct {
	styles   *styleSet
	warnings []Warning
	seenWarn map[string]bool
	sections []*Section
	stack    []pathEntry
	cur      *Section
	ordinal  int
	paraIdx  int
}

func newBuilder() *builder {
	return &builder{seenWarn: map[string]bool{}}
}

func (b *builder) warn(code, detail, loc string) {
	k := code + "\x00" + detail + "\x00" + loc
	if b.seenWarn[k] {
		return
	}
	b.seenWarn[k] = true
	b.warnings = append(b.warnings, Warning{Code: code, Detail: detail, Location: loc})
}

func (b *builder) finish() *Document {
	d := &Document{Sections: make([]Section, 0, len(b.sections)), Warnings: b.warnings}
	for _, s := range b.sections {
		d.Sections = append(d.Sections, *s)
	}
	if d.Warnings == nil {
		d.Warnings = []Warning{}
	}
	return d
}

func (n *node) kidsOrNil() []*node {
	if n == nil {
		return nil
	}
	return n.kids
}

// blocks unwraps structural containers and returns the p / tbl nodes in order,
// honouring tracked changes at block level.
func blocks(kids []*node) []*node {
	var out []*node
	for _, k := range kids {
		switch k.name {
		case "p", "tbl":
			out = append(out, k)
		case "sdt":
			out = append(out, blocks(k.child("sdtContent").kidsOrNil())...)
		case "ins", "moveTo", "customXml", "smartTag":
			out = append(out, blocks(k.kids)...)
		case "AlternateContent":
			out = append(out, blocks(k.child("Choice").kidsOrNil())...)
		}
	}
	return out
}

func (b *builder) walkBody(body *node) {
	carry := ""
	carryHas := false
	for _, blk := range blocks(body.kids) {
		switch blk.name {
		case "tbl":
			if carryHas {
				b.addParagraph(carry)
				carry, carryHas = "", false
			}
			rows := b.tableRows(blk)
			if len(rows) > 0 {
				t := &Table{Rows: rows}
				b.add(Block{Kind: BlockTable, Text: t.Text(), Table: t})
			}
		case "p":
			b.paraIdx++
			txt, markGone := paragraphText(blk)
			txt = carry + txt
			if markGone {
				carry, carryHas = txt, true
				continue
			}
			carry, carryHas = "", false
			b.paragraph(blk, txt)
		}
	}
	if carryHas {
		b.addParagraph(carry)
	}
}

func (b *builder) headingLevel(p *node) int {
	ppr := p.child("pPr")
	loc := "body#p" + strconv.Itoa(b.paraIdx)
	if ol := ppr.child("outlineLvl"); ol != nil {
		v, _ := ol.attr("val")
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 9 {
			if n == 9 {
				return 0
			}
			return n + 1
		}
		b.warn(WarnInvalidOutlineLvl, "paragraph outlineLvl "+strconv.Quote(v)+" is not 0..9; falling back to style", loc)
	}
	sid, _ := ppr.child("pStyle").attr("val")
	n := b.styles.outline(sid, b)
	if n < 0 || n == 9 {
		return 0
	}
	return n + 1
}

func (b *builder) paragraph(p *node, txt string) {
	lvl := b.headingLevel(p)
	if lvl > 0 {
		if h := cleanHeading(txt); h != "" {
			b.startSection(lvl, h)
			return
		}
	}
	b.addParagraph(txt)
}

func (b *builder) startSection(level int, text string) {
	for len(b.stack) > 0 && b.stack[len(b.stack)-1].level >= level {
		b.stack = b.stack[:len(b.stack)-1]
	}
	b.stack = append(b.stack, pathEntry{level, text})
	b.ordinal++
	path := make([]string, len(b.stack))
	for i, e := range b.stack {
		path[i] = e.text
	}
	b.cur = &Section{
		Ordinal:     b.ordinal,
		HeadingPath: path,
		Level:       level,
		Blocks:      []Block{},
		SourceRef:   fmt.Sprintf("%s#%d", strings.Join(path, " > "), b.ordinal),
	}
	b.sections = append(b.sections, b.cur)
}

func (b *builder) addParagraph(txt string) {
	txt = strings.TrimSpace(txt)
	if txt == "" {
		return
	}
	b.add(Block{Kind: BlockParagraph, Text: txt})
}

func (b *builder) add(bl Block) {
	if b.cur == nil {
		b.cur = &Section{HeadingPath: []string{}, Blocks: []Block{}, SourceRef: "#0"}
		b.sections = append(b.sections, b.cur)
	}
	b.cur.Blocks = append(b.cur.Blocks, bl)
}

// paragraphText returns the accepted text of p and whether its paragraph mark
// is a tracked deletion (the paragraph then merges into the next one).
func paragraphText(p *node) (string, bool) {
	gone := false
	if rpr := p.child("pPr").child("rPr"); rpr != nil {
		if rpr.child("del") != nil || rpr.child("moveFrom") != nil {
			gone = true
		}
	}
	var sb strings.Builder
	inline(&sb, p.kids)
	return sb.String(), gone
}

func inline(sb *strings.Builder, kids []*node) {
	for _, k := range kids {
		switch k.name {
		case "r":
			run(sb, k)
		case "hyperlink", "smartTag", "ins", "moveTo", "fldSimple", "customXml", "dir", "bdo":
			inline(sb, k.kids)
		case "sdt":
			inline(sb, k.child("sdtContent").kidsOrNil())
		case "AlternateContent":
			inline(sb, k.child("Choice").kidsOrNil())
		}
		// del, moveFrom, pPr, proofErr, bookmarks, ranges: nothing to emit.
	}
}

func run(sb *strings.Builder, r *node) {
	for _, k := range r.kids {
		switch k.name {
		case "t":
			sb.WriteString(k.text)
		case "tab", "ptab":
			sb.WriteByte('\t')
		case "br", "cr":
			sb.WriteByte('\n')
		case "noBreakHyphen":
			sb.WriteByte('-')
		case "sym":
			// Best effort: private-use code points (symbol fonts) have no
			// portable Unicode meaning and are dropped.
			if v, _ := k.attr("char"); v != "" {
				if n, err := strconv.ParseUint(v, 16, 32); err == nil && !(n >= 0xE000 && n <= 0xF8FF) && n > 0 {
					sb.WriteRune(rune(n))
				}
			}
		}
		// delText, instrText, fldChar, drawing, pict, footnoteReference: skipped.
	}
}

// tableRows renders tbl as grid rows. Spanned and vertically merged cells repeat
// the text of the cell that owns the merge.
func (b *builder) tableRows(tbl *node) [][]string {
	var rows [][]string
	var vtext []string
	for _, tr := range tableTrs(tbl.kids) {
		if tr.child("trPr").child("del") != nil {
			continue
		}
		var row []string
		for _, tc := range tableTcs(tr.kids) {
			span := 1
			if v, ok := tc.child("tcPr").child("gridSpan").attr("val"); ok {
				if n, err := strconv.Atoi(v); err == nil && n > 1 {
					span = n
				}
			}
			var txt string
			if vm := tc.child("tcPr").child("vMerge"); vm != nil {
				if v, _ := vm.attr("val"); v != "restart" {
					if col := len(row); col < len(vtext) {
						txt = vtext[col]
					}
				} else {
					txt = b.cellText(tc)
				}
			} else {
				txt = b.cellText(tc)
			}
			for i := 0; i < span; i++ {
				col := len(row)
				for len(vtext) <= col {
					vtext = append(vtext, "")
				}
				vtext[col] = txt
				row = append(row, txt)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func tableTrs(kids []*node) []*node { return unwrap(kids, "tr") }
func tableTcs(kids []*node) []*node { return unwrap(kids, "tc") }

func unwrap(kids []*node, want string) []*node {
	var out []*node
	for _, k := range kids {
		switch k.name {
		case want:
			out = append(out, k)
		case "sdt":
			out = append(out, unwrap(k.child("sdtContent").kidsOrNil(), want)...)
		case "ins", "moveTo", "customXml":
			out = append(out, unwrap(k.kids, want)...)
		}
	}
	return out
}

// cellText joins the cell's paragraphs with newlines; a nested table is
// flattened in place as lines of " | "-joined cells.
func (b *builder) cellText(tc *node) string {
	var parts []string
	carry := ""
	carryHas := false
	flush := func() {
		if carryHas {
			if s := strings.TrimSpace(carry); s != "" {
				parts = append(parts, s)
			}
			carry, carryHas = "", false
		}
	}
	for _, blk := range blocks(tc.kids) {
		switch blk.name {
		case "p":
			txt, gone := paragraphText(blk)
			carry += txt
			carryHas = true
			if !gone {
				flush()
			}
		case "tbl":
			flush()
			nested := b.tableRows(blk)
			if len(nested) > 0 {
				lines := make([]string, len(nested))
				for i, r := range nested {
					lines[i] = strings.Join(r, " | ")
				}
				parts = append(parts, strings.Join(lines, "\n"))
			}
		}
	}
	flush()
	return strings.Join(parts, "\n")
}
