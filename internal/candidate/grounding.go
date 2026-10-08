// SPDX-License-Identifier: Apache-2.0

package candidate

import (
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// CodeUngroundedFigure marks an answer that states a number or a model number
// the source text does not contain: the model made it up.
const CodeUngroundedFigure = "UNGROUNDED_FIGURE"

var numberToken = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// Figures returns the figures of text in a normalised form, in order of
// appearance without duplicates: numbers (ASCII or full-width digits, with
// the thousands separators removed, so "1,999" and "1999" agree) and
// letter-first model tokens (X5, ZQ 3S, Pro-2), lower-cased with the space or
// hyphen removed ("zq3s"). The digits inside a model token are part of the
// model, not a separate number.
func Figures(text string) []string {
	models, numbers := figures(text)
	return append(models, numbers...)
}

func squash(s string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.ToLower(norm.NFKC.String(s)))
}

func figures(text string) (models, numbers []string) {
	text = norm.NFKC.String(text)
	add := func(list []string, s string) []string {
		if slices.Contains(list, s) {
			return list
		}
		return append(list, s)
	}
	for _, m := range modelToken.FindAllString(text, -1) {
		models = add(models, squash(m))
	}
	for _, n := range numberToken.FindAllString(modelToken.ReplaceAllString(text, " "), -1) {
		numbers = add(numbers, strings.TrimRight(strings.ReplaceAll(n, ",", ""), "."))
	}
	return models, numbers
}

// CheckGrounded reports a violation when the answer contains a figure (see
// Figures) that the source does not. Numbers are compared as whole numbers
// (30 is not in 130). A model token is looked for in the source ignoring
// case, spaces and hyphens, so "ZQ3S" is grounded by "zq 3s". It is a
// deterministic guard against invented facts; it cannot tell whether a figure
// is attached to the right thing, so the reviewer still reads every
// candidate. Spelled-out numbers (五十, fifty) are not compared.
func CheckGrounded(answer, source string) (Violation, bool) {
	_, have := figures(source)
	flat := squash(source)
	models, numbers := figures(answer)
	var missing []string
	for _, m := range models {
		if !strings.Contains(flat, m) {
			missing = append(missing, m)
		}
	}
	for _, n := range numbers {
		if !slices.Contains(have, n) {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return Violation{}, false
	}
	return Violation{Code: CodeUngroundedFigure, Field: "answer", Detail: "not in the source text: " + strings.Join(missing, ", ")}, true
}
