// SPDX-License-Identifier: Apache-2.0

package products

import (
	"regexp"
	"strings"
)

// The model-token rule is the one internal/candidate uses to keep model names
// out of "figures": a letter-first token with one or two digits and up to two
// trailing letters (K5, A2, ZQ 3S, Pro-2). A token of up to five letters
// glued to its digits, or joined by a hyphen, is a model in any case. With a
// space between family and digits only capitals count ("ZQ 3S"), because
// ordinary text is full of "in 2 days" and "Step 3"; a lower-case family word
// followed by a space counts when the catalog knows that family (zq 9).
var (
	compactModel = `[A-Za-z]{1,5}-?\d{1,2}[A-Za-z]{0,2}`
	spacedModel  = `[A-Z]{1,5} \d{1,2}[A-Za-z]{0,2}`
	anyModelRe   = regexp.MustCompile(`\b(?:` + compactModel + `|` + spacedModel + `)\b`)
	fullModelRe  = regexp.MustCompile(`^(?:` + compactModel + `|` + spacedModel + `)$`)
	familyRe     = regexp.MustCompile(`\b([A-Za-z]{1,5}) \d{1,2}[A-Za-z]{0,2}\b`)
)

// LooksLikeModel reports whether token, as a whole, is a model-like token.
func LooksLikeModel(token string) bool { return fullModelRe.MatchString(token) }

// FindModelTokens returns the model-like tokens of text, in order. A token
// that is merely a lower-case word and a number is not one here; the catalog
// adds those for the families it knows.
func FindModelTokens(text string) []string {
	var out []string
	for _, t := range modelTokens(string(prepare(text)), nil) {
		out = append(out, t.text)
	}
	return out
}

type modelToken struct {
	text  string
	start int // byte offset in the searched string
}

// modelTokens finds the tokens of s (already prepared), plus the lower-case
// family-and-number tokens of the given families.
func modelTokens(s string, families map[string]bool) []modelToken {
	var out []modelToken
	add := func(loc []int) {
		for _, o := range out {
			if loc[0] < o.start+len(o.text) && o.start < loc[1] {
				return
			}
		}
		out = append(out, modelToken{text: s[loc[0]:loc[1]], start: loc[0]})
	}
	for _, loc := range anyModelRe.FindAllStringIndex(s, -1) {
		add(loc)
	}
	for _, m := range familyRe.FindAllStringSubmatchIndex(s, -1) {
		if families[strings.ToLower(s[m[2]:m[3]])] {
			add(m[:2])
		}
	}
	// Order of appearance.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].start < out[j-1].start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
