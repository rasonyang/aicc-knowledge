// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// This file holds the shared machinery of the document corpus: a small markup
// that the content files are written in, a deterministic .docx writer and a
// deterministic .xlsx writer. The markup, one item per line:
//
//	# Title            heading level 1 (outline level 0)
//	## Heading         heading level 2
//	### Heading        heading level 3
//	| a | b |          a table row (consecutive rows form one table)
//	~ Text             a bold pseudo heading that has no outline level, so the
//	                   parser reads it as body text (the "looks like a heading" trap)
//	anything else      a paragraph
//
// Blank lines are ignored.

func init() { register("docs", writeDocs) }

type nvDoc struct {
	key string
	src string
}

var (
	nvDocs  []nvDoc
	nvBooks []nvBook
)

// nvAdd registers one .docx under its object key.
func nvAdd(key, src string) { nvDocs = append(nvDocs, nvDoc{key, src}) }

// nvPair registers the English and the Chinese version of one document under
// en/<base>.docx and zh/<base>.docx.
func nvPair(base, en, zh string) {
	nvAdd("en/"+base+".docx", en)
	nvAdd("zh/"+base+".docx", zh)
}

// nvPairNamed is nvPair for documents whose Chinese file name is Chinese, as
// real file servers have them.
func nvPairNamed(enBase, zhBase, en, zh string) {
	nvAdd("en/"+enBase+".docx", en)
	nvAdd("zh/"+zhBase+".docx", zh)
}

// nvSheet is one worksheet of a workbook: rows[0] is the header row.
type nvSheet struct {
	name string
	rows [][]string
}

// nvBook is one .xlsx file. When qa is set the book is a Q&A workbook and a
// .qa.yaml mapping is written next to it.
type nvBook struct {
	key    string
	sheets []nvSheet
	qa     *xlsx.QAMappingFile
}

func nvAddBook(b nvBook) { nvBooks = append(nvBooks, b) }

// ---------------------------------------------------------------- markup

type nvBlock struct {
	kind  string // H1 H2 H3 P TABLE FAKE
	text  string
	table [][]string
}

func nvParse(src string) []nvBlock {
	var out []nvBlock
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "### "):
			out = append(out, nvBlock{kind: "H3", text: line[4:]})
		case strings.HasPrefix(line, "## "):
			out = append(out, nvBlock{kind: "H2", text: line[3:]})
		case strings.HasPrefix(line, "# "):
			out = append(out, nvBlock{kind: "H1", text: line[2:]})
		case strings.HasPrefix(line, "~ "):
			out = append(out, nvBlock{kind: "FAKE", text: line[2:]})
		case strings.HasPrefix(line, "|"):
			row := strings.Split(strings.Trim(line, "|"), "|")
			for i := range row {
				row[i] = strings.TrimSpace(row[i])
			}
			if n := len(out); n > 0 && out[n-1].kind == "TABLE" {
				out[n-1].table = append(out[n-1].table, row)
			} else {
				out = append(out, nvBlock{kind: "TABLE", table: [][]string{row}})
			}
		default:
			out = append(out, nvBlock{kind: "P", text: line})
		}
	}
	return out
}

// ---------------------------------------------------------------- docx

const nvNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

const nvContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`

const nvRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

const nvDocRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`

func nvStyle(id, name, basedOn string, outline int, def bool) string {
	s := `<w:style w:type="paragraph" w:styleId="` + id + `"`
	if def {
		s += ` w:default="1"`
	}
	s += `><w:name w:val="` + name + `"/>`
	if basedOn != "" {
		s += `<w:basedOn w:val="` + basedOn + `"/>`
	}
	if outline >= 0 {
		s += fmt.Sprintf(`<w:pPr><w:outlineLvl w:val="%d"/></w:pPr>`, outline)
	}
	return s + `</w:style>`
}

// nvFlavors are the ways real documents mark their headings. They differ in
// style ids and names, and in whether the outline level sits on the style, is
// inherited through basedOn, or is set directly on the paragraph. The parser
// must find the headings in every one of them.
type nvFlavor struct {
	styles string
	// headStyle[i] is the paragraph style of heading level i+1; "" means the
	// outline level is written directly on the paragraph.
	headStyle [3]string
}

var nvFlavors = []nvFlavor{
	{ // English built-in names, outline on the style
		styles: nvStyle("Normal", "Normal", "", -1, true) + nvStyle("Heading1", "heading 1", "Normal", 0, false) +
			nvStyle("Heading2", "heading 2", "Normal", 1, false) + nvStyle("Heading3", "heading 3", "Normal", 2, false) +
			nvStyle("Heading4", "heading 4", "Normal", -1, false),
		headStyle: [3]string{"Heading1", "Heading2", "Heading3"},
	},
	{ // Chinese built-in names and numeric ids
		styles: nvStyle("a", "Normal", "", -1, true) + nvStyle("1", "标题 1", "a", 0, false) +
			nvStyle("2", "标题 2", "a", 1, false) + nvStyle("3", "标题 3", "a", 2, false) +
			nvStyle("4", "标题 4", "a", -1, false),
		headStyle: [3]string{"1", "2", "3"},
	},
	{ // outline level written directly on every heading paragraph
		styles:    nvStyle("Normal", "Normal", "", -1, true) + nvStyle("Heading4", "heading 4", "Normal", -1, false),
		headStyle: [3]string{"", "", ""},
	},
	{ // house styles that inherit the outline level through basedOn
		styles: nvStyle("Normal", "Normal", "", -1, true) + nvStyle("Chapter", "章标题", "Normal", 0, false) +
			nvStyle("Part", "节标题", "Normal", 1, false) + nvStyle("PartX", "节标题扩展", "Part", -1, false) +
			nvStyle("Sub", "小节", "PartX", 2, false) + nvStyle("Heading4", "heading 4", "Normal", -1, false),
		headStyle: [3]string{"Chapter", "PartX", "Sub"},
	},
}

func nvEsc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func nvRun(text string, bold bool) string {
	rpr := ""
	if bold {
		rpr = `<w:rPr><w:b/></w:rPr>`
	}
	return `<w:r>` + rpr + `<w:t xml:space="preserve">` + nvEsc(text) + `</w:t></w:r>`
}

func nvHash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// nvDocx renders markup as a .docx. The flavor is chosen from the key, so it
// is stable.
func nvDocx(key, src string) []byte {
	fl := nvFlavors[nvHash(key)%uint32(len(nvFlavors))]
	var body strings.Builder
	for _, b := range nvParse(src) {
		switch b.kind {
		case "H1", "H2", "H3":
			lvl := int(b.kind[1] - '1')
			if st := fl.headStyle[lvl]; st != "" {
				body.WriteString(`<w:p><w:pPr><w:pStyle w:val="` + st + `"/></w:pPr>` + nvRun(b.text, false) + `</w:p>`)
			} else {
				body.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:outlineLvl w:val="%d"/></w:pPr>`, lvl) + nvRun(b.text, true) + `</w:p>`)
			}
		case "FAKE":
			body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading4"/></w:pPr>` + nvRun(b.text, true) + `</w:p>`)
		case "P":
			body.WriteString(`<w:p>` + nvRun(b.text, false) + `</w:p>`)
		case "TABLE":
			body.WriteString(`<w:tbl><w:tblPr/>`)
			for _, row := range b.table {
				body.WriteString(`<w:tr>`)
				for _, c := range row {
					body.WriteString(`<w:tc><w:tcPr/><w:p>` + nvRun(c, false) + `</w:p></w:tc>`)
				}
				body.WriteString(`</w:tr>`)
			}
			body.WriteString(`</w:tbl>`)
		}
	}
	head := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	parts := []struct{ name, data string }{
		{"[Content_Types].xml", nvContentTypes},
		{"_rels/.rels", nvRootRels},
		{"word/_rels/document.xml.rels", nvDocRels},
		{"word/document.xml", head + `<w:document ` + nvNS + `><w:body>` + body.String() + `<w:sectPr/></w:body></w:document>`},
		{"word/styles.xml", head + `<w:styles ` + nvNS + `>` + fl.styles + `</w:styles>`},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err == nil {
			_, err = w.Write([]byte(p.data))
		}
		if err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------- xlsx

func nvXlsx(b nvBook) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	fixed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if err := f.SetDocProps(&excelize.DocProperties{Creator: "NovaNet", LastModifiedBy: "NovaNet", Created: fixed, Modified: fixed}); err != nil {
		return nil, err
	}
	for i, s := range b.sheets {
		if i == 0 {
			if err := f.SetSheetName("Sheet1", s.name); err != nil {
				return nil, err
			}
		} else if _, err := f.NewSheet(s.name); err != nil {
			return nil, err
		}
		for r, row := range s.rows {
			for c, v := range row {
				if v == "" {
					continue
				}
				ref, _ := excelize.CoordinatesToCellName(c+1, r+1)
				if err := f.SetCellStr(s.name, ref, v); err != nil {
					return nil, err
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------- writer

func nvWrite(out, key string, data []byte) error {
	p := filepath.Join(out, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func writeDocs(e *env) error {
	seen := map[string]bool{}
	docs := slices.Clone(nvDocs)
	slices.SortFunc(docs, func(a, b nvDoc) int { return strings.Compare(a.key, b.key) })
	for _, d := range docs {
		if seen[d.key] {
			return fmt.Errorf("duplicate key %s", d.key)
		}
		seen[d.key] = true
		if err := nvWrite(e.out, d.key, nvDocx(d.key, d.src)); err != nil {
			return err
		}
	}
	books := slices.Clone(nvBooks)
	slices.SortFunc(books, func(a, b nvBook) int { return strings.Compare(a.key, b.key) })
	for _, b := range books {
		if seen[b.key] {
			return fmt.Errorf("duplicate key %s", b.key)
		}
		seen[b.key] = true
		data, err := nvXlsx(b)
		if err != nil {
			return fmt.Errorf("%s: %w", b.key, err)
		}
		if err := nvWrite(e.out, b.key, data); err != nil {
			return err
		}
		if b.qa != nil {
			y, err := yaml.Marshal(b.qa)
			if err != nil {
				return err
			}
			mk := strings.TrimSuffix(b.key, ".xlsx") + ".qa.yaml"
			if err := nvWrite(e.out, mk, append([]byte("# SPDX-License-Identifier: Apache-2.0\n"), y...)); err != nil {
				return err
			}
		}
	}
	return nil
}
