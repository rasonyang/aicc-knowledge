// SPDX-License-Identifier: Apache-2.0

// Command gen writes the .docx fixtures under internal/docx/testdata. It runs
// through `go generate ./internal/docx` and is deterministic: fixed zip
// timestamps and a fixed file order, so a regenerated fixture is byte-identical.
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const nsDecl = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"`

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

const docRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`

func build(styles, body string) []byte {
	parts := []struct{ name, data string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"word/_rels/document.xml.rels", docRels},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<w:document ` + nsDecl + `><w:body>` + body + `<w:sectPr/></w:body></w:document>`},
		{"word/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<w:styles ` + nsDecl + `>` + styles + `</w:styles>`},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
		must(err)
		_, err = w.Write([]byte(p.data))
		must(err)
	}
	must(zw.Close())
	return buf.Bytes()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func style(id, name, basedOn, outline string, def bool) string {
	s := `<w:style w:type="paragraph" w:styleId="` + id + `"`
	if def {
		s += ` w:default="1"`
	}
	s += `><w:name w:val="` + name + `"/>`
	if basedOn != "" {
		s += `<w:basedOn w:val="` + basedOn + `"/>`
	}
	if outline != "" {
		s += `<w:pPr><w:outlineLvl w:val="` + outline + `"/></w:pPr>`
	}
	return s + `</w:style>`
}

func p(styleID, runs string) string {
	if styleID == "" {
		return `<w:p>` + runs + `</w:p>`
	}
	return `<w:p><w:pPr><w:pStyle w:val="` + styleID + `"/></w:pPr>` + runs + `</w:p>`
}

func r(text string) string { return `<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r>` }

func tc(span, vmerge, inner string) string {
	pr := ""
	if span != "" {
		pr += `<w:gridSpan w:val="` + span + `"/>`
	}
	switch vmerge {
	case "restart":
		pr += `<w:vMerge w:val="restart"/>`
	case "continue":
		pr += `<w:vMerge/>`
	}
	return `<w:tc><w:tcPr>` + pr + `</w:tcPr>` + inner + `</w:tc>`
}

func tr(cells ...string) string {
	s := `<w:tr>`
	for _, c := range cells {
		s += c
	}
	return s + `</w:tr>`
}

func tbl(rows ...string) string {
	s := `<w:tbl><w:tblPr/>`
	for _, x := range rows {
		s += x
	}
	return s + `</w:tbl>`
}

func headings() []byte {
	styles := style("Normal", "Normal", "", "", true) +
		style("a3", "标题 1", "Normal", "0", false) +
		style("a4", "标题 2", "Normal", "1", false) +
		style("TrapH1", "Heading 1", "Normal", "", false) + // trap: name says heading, no outlineLvl
		style("BodyH2", "Heading 2", "a4", "9", false) + // outlineLvl 9 = body text
		style("Chapter", "章标题", "a3", "", false) + // inherits 0
		style("Section", "节标题", "a4", "", false) + // inherits 1
		style("SectionX", "节标题扩展", "Section", "", false) + // two hops: inherits 1
		style("SubSection", "小节", "a4", "2", false) + // overrides to 2
		style("CycA", "Cycle A", "CycB", "", false) +
		style("CycB", "Cycle B", "CycA", "", false)
	body := p("Normal", r("前言段落，位于第一个标题之前。")) +
		p("a3", r("第一章 概述")) +
		p("Normal", r("概述正文。")) +
		p("a4", r("1.1 背景")) +
		p("Normal", r("背景正文。")) +
		p("TrapH1", r("Heading 1 trap: styled like a heading but has no outlineLvl")) +
		p("Chapter", r("第二章 价格")) +
		p("SectionX", r("2.1 套餐")) +
		p("SubSection", r("2.1.1 月费")) +
		`<w:p><w:pPr><w:pStyle w:val="Normal"/><w:outlineLvl w:val="2"/></w:pPr>` + r("2.1.2 直接覆盖") + `</w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="a3"/><w:outlineLvl w:val="9"/></w:pPr>` + r("样式是标题 1，但直接大纲级别为 9，所以是正文") + `</w:p>` +
		p("BodyH2", r("BodyH2 style has outlineLvl 9: body text")) +
		p("CycA", r("cyclic basedOn chain: body text")) +
		p("Missing", r("undefined style: body text")) +
		p("a3", r("附录")) +
		p("Normal", r("附录正文。"))
	return build(styles, body)
}

// sentenceHeadings is a document in the shape word processors export when
// whole intro and closing sentences carry an outline level, and the title is
// repeated as a lower-level heading.
func sentenceHeadings() []byte {
	styles := style("Normal", "Normal", "", "", true) +
		style("T1", "Title", "Normal", "0", false) +
		style("T2", "Sub", "Normal", "1", false) +
		style("T3", "Deep", "Normal", "2", false)
	body := p("T1", r("Acme Widget Guide")) +
		p("T3", r("Acme Widget Guide")) +
		p("T1", r("Welcome to the Acme Widget Guide. Please read the sections below before use.")) +
		p("T2", r("Setup")) +
		p("Normal", r("Plug the widget in and press the green button.")) +
		p("T3", r("这是一段很长的介绍文字，它被错误地设置了大纲级别，所以应该被当作正文处理而不是标题，因为它超过了四十个字符的上限")) +
		p("T2", r("如何清洁屏幕？")) +
		p("Normal", r("每周清洁一次即可。")) +
		p("T2", r("Does it work offline?")) +
		p("Normal", r("Yes.")) +
		p("T1", r("谢谢您的阅读！")) +
		p("T2", r("Version 2.0")) +
		p("Normal", r("Released last year."))
	return build(styles, body)
}

// stubs is a product FAQ in the shape that made the model invent facts: titles
// with no text, boilerplate lines under a title, and short question headings.
func stubs() []byte {
	styles := style("Normal", "Normal", "", "", true) +
		style("T1", "Title", "Normal", "0", false) +
		style("T2", "Sub", "Normal", "1", false)
	body := p("T1", r("Gadget Guide")) +
		p("Normal", r("Applicable products: Gadget One, Gadget Two")) +
		p("T2", r("Battery")) +
		p("Normal", r("The battery lasts about ten hours on a single charge when the screen brightness is at the default level.")) +
		p("T2", r("Does it support wireless charging?")) +
		p("Normal", r("No.")) +
		p("T2", r("Warranty")) +
		p("T2", r("Water resistance")) +
		p("Normal", r("The Gadget One has an IP67 rating, so it survives a short dip in fresh water.")) +
		p("T2", r("Q3 Colors")) +
		p("Normal", r("Black.")) +
		p("T2", r("是否支持无线充电？")) +
		p("Normal", r("不支持。")) +
		p("T2", r("Release notes")) +
		p("Normal", r("Version: 2.1"))
	return build(styles, body)
}

func tracked() []byte {
	styles := style("Normal", "Normal", "", "", true) + style("H1", "标题 1", "Normal", "0", false)
	ins := func(t string) string {
		return `<w:ins w:id="1" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:t>` + t + `</w:t></w:r></w:ins>`
	}
	del := func(t string) string {
		return `<w:del w:id="2" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:delText>` + t + `</w:delText></w:r></w:del>`
	}
	body := p("H1", r("Plan ")+ins("Gold")+del("Silver")) +
		p("Normal", r("Price is ")+del("10")+ins("12")+r(" yuan.")) +
		`<w:p><w:pPr><w:rPr><w:del w:id="3" w:author="a"/></w:rPr></w:pPr>` + r("Merged part one, ") + `</w:p>` +
		p("Normal", r("part two.")) +
		p("Normal", r("Alpha. ")+`<w:moveFrom w:id="4" w:author="a"><w:r><w:t>Moved text.</w:t></w:r></w:moveFrom>`) +
		p("Normal", r("Beta. ")+`<w:moveTo w:id="5" w:author="a"><w:r><w:t>Moved text.</w:t></w:r></w:moveTo>`) +
		`<w:p><w:pPr><w:rPr><w:ins w:id="6" w:author="a"/></w:rPr></w:pPr>` + ins("Inserted paragraph.") + `</w:p>` +
		`<w:p><w:pPr><w:rPr><w:del w:id="7" w:author="a"/></w:rPr></w:pPr>` + del("Wholly deleted paragraph.") + `</w:p>` +
		`<w:p><w:r><w:rPr><w:b/><w:rPrChange w:id="8" w:author="a"><w:rPr/></w:rPrChange></w:rPr><w:t>Formatting changed only.</w:t></w:r></w:p>` +
		// pPrChange records the OLD properties; they must not turn body into a heading or vice versa.
		`<w:p><w:pPr><w:pPrChange w:id="9" w:author="a"><w:pPr><w:outlineLvl w:val="0"/></w:pPr></w:pPrChange></w:pPr>` + r("Old properties were a heading; current ones are not.") + `</w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="H1"/><w:pPrChange w:id="10" w:author="a"><w:pPr><w:pStyle w:val="Normal"/></w:pPr></w:pPrChange></w:pPr>` + r("Became a heading by style change") + `</w:p>` +
		tbl(
			tr(tc("", "", p("", r("Item"))), tc("", "", p("", r("Qty")))),
			`<w:tr><w:trPr><w:del w:id="11" w:author="a"/></w:trPr>`+tc("", "", p("", del("Removed row")))+tc("", "", p("", del("0")))+`</w:tr>`,
			`<w:tr><w:trPr><w:ins w:id="12" w:author="a"/></w:trPr>`+tc("", "", p("", ins("Added row")))+tc("", "", p("", ins("3")))+`</w:tr>`,
		)
	return build(styles, body)
}

func inline() []byte {
	styles := style("Normal", "Normal", "", "", true) + style("H1", "标题 1", "Normal", "0", false)
	body := p("H1", r("Inline features")) +
		`<w:p><w:r><w:t>A</w:t><w:tab/><w:t>B</w:t><w:br/><w:t>C</w:t><w:cr/><w:t>D</w:t><w:noBreakHyphen/><w:t>E</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Page </w:t></w:r><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> PAGE </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>7</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>` +
		`<w:p><w:fldSimple w:instr=" DATE "><w:r><w:t>simple field result</w:t></w:r></w:fldSimple></w:p>` +
		`<w:p><w:r><w:t xml:space="preserve">See </w:t></w:r><w:hyperlink w:anchor="x"><w:r><w:t>the link text</w:t></w:r></w:hyperlink><w:r><w:footnoteReference w:id="1"/></w:r><w:r><w:t>.</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>Sym </w:t></w:r><w:r><w:sym w:font="Symbol" w:char="F0B7"/></w:r><w:r><w:sym w:font="Arial" w:char="00A9"/></w:r></w:p>` +
		`<w:p><w:r><w:t>Before box </w:t></w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:r><w:drawing><w:txbxContent><w:p><w:r><w:t>box text</w:t></w:r></w:p></w:txbxContent></w:drawing></w:r></mc:Choice><mc:Fallback><w:r><w:pict><w:t>fallback text</w:t></w:pict></w:r></mc:Fallback></mc:AlternateContent><w:r><w:t>after.</w:t></w:r></w:p>` +
		`<w:sdt><w:sdtContent>` + p("", r("Content control paragraph.")) + `</w:sdtContent></w:sdt>`
	return build(styles, body)
}

func tables() []byte {
	styles := style("Normal", "Normal", "", "", true) + style("H1", "标题 1", "Normal", "0", false)
	merged := tbl(
		tr(tc("", "restart", p("", r("Plan"))), tc("2", "", p("", r("Price")))),
		tr(tc("", "continue", p("", r("ignored"))), tc("", "", p("", r("Monthly"))), tc("", "", p("", r("Yearly")))),
		tr(tc("", "", p("", r("Basic"))), tc("", "", p("", r("10"))), tc("", "", p("", r("100")))),
		tr(tc("", "", p("", r("Pro"))), tc("", "", p("", r("20"))+p("", r("(promo)"))), tc("", "", p("", r("a|b")))),
	)
	nested := tbl(
		tr(tc("", "", p("", r("Outer A"))),
			tc("", "", p("", r("Inner intro"))+tbl(
				tr(tc("", "", p("", r("x"))), tc("", "", p("", r("y")))),
				tr(tc("", "", p("", r("1"))), tc("", "", p("", r("2")))),
			))),
	)
	body := p("H1", r("Pricing")) + p("", r("Before table.")) + merged + p("", r("Between tables.")) + nested + p("", r("After tables."))
	return build(styles, body)
}

// callCenter builds a small realistic knowledge document: one heading and one
// paragraph per topic (billing, refunds, plan prices with figures). The text
// feeds the candidate-generation tests, including the ones against a real LLM.
func callCenter(titles, paragraphs []string) []byte {
	styles := style("Normal", "Normal", "", "", true) + style("Heading1", "heading 1", "Normal", "0", false)
	var body string
	for i, t := range titles {
		body += p("Heading1", r(t)) + p("Normal", r(paragraphs[i]))
	}
	return build(styles, body)
}

func faqEN() []byte {
	return callCenter(
		[]string{"Billing", "Refunds", "Plans and prices"},
		[]string{
			"Your bill is issued on the first day of each month and is payable within 15 days. A late payment is charged a late fee of 2 percent of the outstanding amount. You can pay by bank card, by bank transfer or at any service counter.",
			"A refund request must be made within 30 days of the purchase. An approved refund is returned to the original payment method within 5 to 7 business days. Service fees are not refundable.",
			"The Basic plan costs 9.90 dollars per month and includes 10 GB of data. The Plus plan costs 19.90 dollars per month and includes 50 GB of data. The Max plan costs 39.90 dollars per month and includes unlimited data.",
		})
}

func faqZH() []byte {
	return callCenter(
		[]string{"账单与缴费", "退款", "套餐价格"},
		[]string{
			"账单于每月1日出具，请在15天内缴清。逾期未缴的，按未缴金额的百分之二收取滞纳金。您可以通过银行卡、银行转账或任意营业厅柜台缴费。",
			"购买后三十天内可以申请退款。审核通过的退款会在五到七个工作日内退回原支付方式。服务费不予退还。",
			"基础套餐每月9.9元，包含10GB流量；加强套餐每月19.9元，包含50GB流量；旗舰套餐每月39.9元，流量不限量。",
		})
}

func main() {
	files := map[string][]byte{
		"headings.docx":  headings(),
		"tracked.docx":   tracked(),
		"sentences.docx": sentenceHeadings(),
		"stubs.docx":     stubs(),
		"inline.docx":    inline(),
		"tables.docx":    tables(),
		"faq_en.docx":    faqEN(),
		"faq_zh.docx":    faqZH(),
	}
	for name, data := range files {
		must(os.WriteFile(filepath.Join("testdata", name), data, 0o644))
	}
}
