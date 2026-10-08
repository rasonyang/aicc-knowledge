// SPDX-License-Identifier: Apache-2.0

// Package products holds the product catalog: which products a business sells,
// the names callers use for them, and which products are interchangeable for
// support questions. It is pure (no I/O) so that publish, search and eval share
// one definition of "which products does this text name".
//
// Why it exists: a vector search happily answers a question about model A with
// the FAQ of model B, because the two read almost the same. The catalog lets
// the searcher drop such hits (see internal/search): the document side is
// tagged with product ids at publish time, the query side is extracted here at
// search time, with the same code.
//
// Matching works on a canonical form: Unicode NFKC (full-width becomes
// half-width), lower case, hyphen and underscore read as a space, a Chinese
// numeral directly after a Latin letter turned into digits ("ZQ三S" reads
// "ZQ 3S"), and every white space removed. Aliases are matched longest first
// with ASCII alphanumeric boundaries, so "ZQ 3" is not found inside "ZQ 3S"
// and "Nova" is not found in "Nova K2" when both are known.
package products

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"golang.org/x/text/unicode/norm"
)

// CodeCatalogInvalid is the parse error code of a catalog that does not
// validate. It is stored in file_versions.parse_error_code.
const CodeCatalogInvalid = "CATALOG_INVALID"

// CatalogFileName is the one catalog object, at the root of KB_S3_PREFIX.
const CatalogFileName = "products.yaml"

// Error is returned by Parse and FromJSON. Issues lists every problem found.
type Error struct {
	Code   string
	Issues []string
}

func (e *Error) Error() string { return e.Code + ": " + strings.Join(e.Issues, "; ") }

// Product is one catalog entry.
type Product struct {
	// ID is stable: [a-z0-9-], starting with a letter or digit.
	ID string `yaml:"id" json:"id"`
	// Names are the aliases callers and documents use.
	Names []string `yaml:"names" json:"names"`
	// CompatibleWith lists products whose questions this product's documents
	// may answer. The relation is symmetric whichever side declares it.
	CompatibleWith []string `yaml:"compatibleWith,omitempty" json:"compatibleWith,omitempty"`
}

// File is the YAML document.
type File struct {
	Products []Product `yaml:"products" json:"products"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// minAliasRunes is the shortest canonical alias: one character would match
// inside nearly any text.
const minAliasRunes = 2

// Catalog is a validated, immutable catalog. It is safe for concurrent use.
type Catalog struct {
	products []Product
	byAlias  map[string]string // canonical alias -> product id
	lengths  []int             // distinct alias lengths in runes, longest first
	compat   map[string][]string
	families map[string]bool // leading Latin letters of every alias, lower case
	ids      map[string]bool
}

// Parse decodes and validates a catalog YAML file. Unknown fields are
// rejected. The error is an *Error with code CATALOG_INVALID.
func Parse(data []byte) (*Catalog, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.Strict(), yaml.DisallowUnknownField())
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, &Error{Code: CodeCatalogInvalid, Issues: []string{"YAML cannot be decoded: " + err.Error()}}
	}
	return New(f.Products)
}

// FromJSON rebuilds a catalog from the JSON that MarshalJSON wrote.
func FromJSON(raw []byte) (*Catalog, error) {
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, &Error{Code: CodeCatalogInvalid, Issues: []string{"stored catalog cannot be decoded: " + err.Error()}}
	}
	return New(f.Products)
}

// MarshalJSON writes the validated products, which FromJSON reads back.
func (c *Catalog) MarshalJSON() ([]byte, error) {
	return json.Marshal(File{Products: c.products})
}

// Products returns a copy of the catalog's products in declaration order.
func (c *Catalog) Products() []Product { return slices.Clone(c.products) }

// Has reports whether id is a product of the catalog.
func (c *Catalog) Has(id string) bool { return c.ids[id] }

// New validates products and builds the matcher.
func New(products []Product) (*Catalog, error) {
	var iss []string
	add := func(f string, a ...any) { iss = append(iss, fmt.Sprintf(f, a...)) }
	if len(products) == 0 {
		add("products must not be empty")
	}
	c := &Catalog{byAlias: map[string]string{}, compat: map[string][]string{}, families: map[string]bool{}, ids: map[string]bool{}}
	for i, p := range products {
		switch {
		case !idRe.MatchString(p.ID):
			add("products[%d].id %q must match [a-z0-9-] and start with a letter or digit (at most 64 characters)", i, p.ID)
		case c.ids[p.ID]:
			add("products[%d].id %q is duplicated", i, p.ID)
		default:
			c.ids[p.ID] = true
		}
		if len(p.Names) == 0 {
			add("products[%d] (%s): names must not be empty", i, p.ID)
		}
		own := map[string]bool{}
		for j, n := range p.Names {
			key := Normalize(n)
			switch {
			case utf8.RuneCountInString(key) < minAliasRunes:
				add("products[%d].names[%d] %q is too short once normalized (at least %d characters)", i, j, n, minAliasRunes)
			case own[key]:
				// The same product spelled twice (full-width, hyphen): harmless.
			default:
				own[key] = true
				if other, taken := c.byAlias[key]; taken {
					add("products[%d].names[%d] %q normalizes to %q, which product %q already names", i, j, n, key, other)
					continue
				}
				c.byAlias[key] = p.ID
			}
		}
	}
	for i, p := range products {
		for _, other := range p.CompatibleWith {
			switch {
			case other == p.ID:
				add("products[%d] (%s): compatibleWith lists itself", i, p.ID)
			case !c.ids[other]:
				add("products[%d] (%s): compatibleWith names unknown product %q", i, p.ID, other)
			default:
				c.compat[p.ID] = appendUnique(c.compat[p.ID], other)
				c.compat[other] = appendUnique(c.compat[other], p.ID)
			}
		}
	}
	if len(iss) > 0 {
		return nil, &Error{Code: CodeCatalogInvalid, Issues: iss}
	}
	seenLen := map[int]bool{}
	for alias := range c.byAlias {
		n := utf8.RuneCountInString(alias)
		if !seenLen[n] {
			seenLen[n] = true
			c.lengths = append(c.lengths, n)
		}
	}
	for _, p := range products {
		for _, n := range p.Names {
			// The family of an alias is its leading Latin word ("zq" in
			// "ZQ 3", "nova" in "Nova K2"); aliases that do not start with a
			// Latin letter have none.
			if f := leadingLetters(string(lowerRunes(prepare(n)))); f != "" {
				c.families[f] = true
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(c.lengths)))
	c.products = slices.Clone(products)
	return c, nil
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

func leadingLetters(s string) string {
	end := 0
	for end < len(s) && s[end] >= 'a' && s[end] <= 'z' {
		end++
	}
	return s[:end]
}

// Expand returns ids plus every product compatible with one of them.
func (c *Catalog) Expand(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids)*2)
	for _, id := range ids {
		out[id] = true
		for _, o := range c.compat[id] {
			out[o] = true
		}
	}
	return out
}

// Extraction is what a text names.
type Extraction struct {
	// IDs are the products named, in order of first appearance, without repeats.
	IDs []string
	// UnknownModels are model-like tokens (see FindModelTokens) that no alias of
	// the catalog covers, in order of appearance.
	UnknownModels []string
}

// Extract finds the products a text names and the model-like tokens it uses
// that the catalog does not know.
func (c *Catalog) Extract(text string) Extraction {
	cased := prepare(text)
	lower := lowerRunes(cased)
	var stripped []rune
	var pos []int          // stripped index -> index in lower
	var spaceBefore []bool // a separator preceded the rune
	sep := false
	for i, r := range lower {
		if unicode.IsSpace(r) || r == '-' || r == '_' {
			sep = true
			continue
		}
		stripped = append(stripped, r)
		pos = append(pos, i)
		spaceBefore = append(spaceBefore, sep)
		sep = false
	}
	n := len(stripped)
	leftOK := func(i int) bool {
		return i == 0 || spaceBefore[i] || !asciiAlnum(stripped[i-1]) || !asciiAlnum(stripped[i])
	}
	rightOK := func(j int) bool {
		return j >= n || spaceBefore[j] || !asciiAlnum(stripped[j-1]) || !asciiAlnum(stripped[j])
	}

	var ex Extraction
	type span struct{ from, to int }
	var covered []span
	record := func(id string, i, j int) {
		if !slices.Contains(ex.IDs, id) {
			ex.IDs = append(ex.IDs, id)
		}
		covered = append(covered, span{pos[i], pos[j-1] + 1})
	}
	// find returns the id and length of the longest alias at stripped[i:].
	find := func(i int, prefix string) (string, int) {
		for _, L := range c.lengths {
			pl := utf8.RuneCountInString(prefix)
			if L <= pl || i+L-pl > n {
				continue
			}
			id, ok := c.byAlias[prefix+string(stripped[i:i+L-pl])]
			if !ok || !rightOK(i+L-pl) {
				continue
			}
			return id, L - pl
		}
		return "", 0
	}
	for i := 0; i < n; {
		id, l := "", 0
		if leftOK(i) {
			id, l = find(i, "")
		}
		if id == "" {
			i++
			continue
		}
		record(id, i, i+l)
		j := i + l
		// "ZQ 3/3S": after a list separator the family word of the alias just
		// matched may be implied.
		family := leadingLetters(string(stripped[i:j]))
		for family != "" && j < n && isListSeparator(stripped[j]) && j+1 < n {
			id2, l2 := find(j+1, family)
			if id2 == "" {
				break
			}
			record(id2, j+1, j+1+l2)
			j += 1 + l2
		}
		i = j
	}

	for _, tok := range modelTokens(string(cased), c.families) {
		from := utf8.RuneCountInString(string(cased)[:tok.start])
		to := from + utf8.RuneCountInString(tok.text)
		hit := false
		for _, s := range covered {
			if from < s.to && s.from < to {
				hit = true
				break
			}
		}
		// R2 only concerns a family the catalog knows: the token's own leading
		// letters ("zq" in "ZQ 9") or the Latin word right before it ("nova"
		// in "Nova K9"). Other model-like tokens (iOS17, USB3, mp4) are not
		// the catalog's business.
		if !hit && c.knownFamily(string(cased), tok) {
			ex.UnknownModels = append(ex.UnknownModels, tok.text)
		}
	}
	return ex
}

func (c *Catalog) knownFamily(s string, tok modelToken) bool {
	if c.families[strings.ToLower(leadingLetters(strings.ToLower(tok.text)))] {
		return true
	}
	before := strings.TrimRight(s[:tok.start], " -_")
	if len(before) == len(s[:tok.start]) {
		return false // glued to the previous character: not a separate word
	}
	i := len(before)
	for i > 0 && isASCIILetter(rune(before[i-1])) {
		i--
	}
	return i < len(before) && c.families[strings.ToLower(before[i:])]
}

// DocumentProducts returns the products a FAQ document is about: those its
// question and alternate questions name; when they name none, those named by
// the file name of its source (directories do not count); else nil, which
// means the document is generic.
func (c *Catalog) DocumentProducts(question string, alternates []string, objectKey string) []string {
	var ids []string
	for _, s := range append([]string{question}, alternates...) {
		for _, id := range c.Extract(s).IDs {
			ids = appendUnique(ids, id)
		}
	}
	if len(ids) > 0 {
		return ids
	}
	base := objectKey
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return slices.Clone(c.Extract(base).IDs)
}

func isListSeparator(r rune) bool {
	switch r {
	case '/', '、', '和', '与', '及', ',', '，', '或':
		return true
	}
	return false
}

func asciiAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z'
}

// Normalize returns the canonical form of text: NFKC, lower case, Chinese
// numerals after a Latin letter as digits, hyphen, underscore and every white
// space removed.
func Normalize(text string) string {
	var b strings.Builder
	for _, r := range lowerRunes(prepare(text)) {
		if unicode.IsSpace(r) || r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func lowerRunes(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// prepare applies NFKC and the numeral conversion; case and separators are
// kept so that the model-token rule can read them.
func prepare(text string) []rune {
	rs := []rune(norm.NFKC.String(text))
	out := make([]rune, 0, len(rs)+4)
	for i := 0; i < len(rs); {
		r := rs[i]
		if numeralValue(r) > 0 && len(out) > 0 && isASCIILetter(out[len(out)-1]) {
			j := i
			for j < len(rs) && numeralValue(rs[j]) > 0 {
				j++
			}
			if v, ok := parseNumeral(rs[i:j]); ok && !(j-i == 1 && rs[i] == '一' && j < len(rs) && unicode.Is(unicode.Han, rs[j])) {
				out = append(out, ' ')
				out = append(out, []rune(fmt.Sprint(v))...)
				i = j
				continue
			}
		}
		out = append(out, r)
		i++
	}
	return out
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

// numeralValue is the value of 一..九 and 十 (10), else 0.
func numeralValue(r rune) int {
	if i := strings.IndexRune("一二三四五六七八九十", r); i >= 0 {
		return i/3 + 1 // each rune is 3 bytes
	}
	return 0
}

// parseNumeral reads 1 to 99 in Chinese numerals: 三, 十, 十二, 二十, 二十一.
func parseNumeral(rs []rune) (int, bool) {
	switch len(rs) {
	case 1:
		return numeralValue(rs[0]), true
	case 2:
		switch {
		case rs[0] == '十' && rs[1] != '十':
			return 10 + numeralValue(rs[1]), true
		case rs[1] == '十' && rs[0] != '十':
			return numeralValue(rs[0]) * 10, true
		}
	case 3:
		if rs[1] == '十' && rs[0] != '十' && rs[2] != '十' {
			return numeralValue(rs[0])*10 + numeralValue(rs[2]), true
		}
	}
	return 0, false
}
