// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"unicode"
)

// Item is a published FAQ item as the resolver sees it.
type Item struct {
	CandidateID string
	SourceRef   string
	Question    string
	Answer      string
}

// Expectation is one {file, section, fact} entry of questions.yaml.
type Expectation struct {
	File    string
	Section string
	Fact    string
}

// normalize folds full-width ASCII to half width, lowercases and drops all
// white space, so that a fact matches however the answer spaces or cases it.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0x3000:
			continue
		case r >= 0xFF01 && r <= 0xFF5E:
			r = r - 0xFF01 + 0x21
		}
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// sourceMatches reports whether a stored source_ref belongs to the section: it
// equals prefix+file+"#"+section or continues it with "#" (the ordinal).
func sourceMatches(prefix string, e Expectation, sourceRef string) bool {
	want := prefix + e.File + "#" + e.Section
	return sourceRef == want || strings.HasPrefix(sourceRef, want+"#")
}

// Match returns the candidate ids of the items that satisfy the expectation:
// same section, and the fact occurs in the answer or the question after
// normalization. An empty fact accepts every item of the section.
func Match(prefix string, e Expectation, items []Item) []string {
	fact := normalize(e.Fact)
	var ids []string
	for _, it := range items {
		if !sourceMatches(prefix, e, it.SourceRef) {
			continue
		}
		if fact != "" && !strings.Contains(normalize(it.Answer), fact) && !strings.Contains(normalize(it.Question), fact) {
			continue
		}
		ids = append(ids, it.CandidateID)
	}
	return ids
}

// SectionItems returns the items that come from the section, whatever they say.
func SectionItems(prefix string, e Expectation, items []Item) []Item {
	var out []Item
	for _, it := range items {
		if sourceMatches(prefix, e, it.SourceRef) {
			out = append(out, it)
		}
	}
	return out
}

// Resolution is the outcome for one question.
type Resolution struct {
	IDs []string
	// Unresolved lists the expectations that matched nothing.
	Unresolved []Expectation
}

// Resolve unions the ids of all expectations. A question with expectations is
// resolved when at least one expectation matched; the others are reported as
// unresolved but do not exclude it. A question with none resolved and some
// expected is a gap.
func Resolve(prefix string, expect []Expectation, items []Item) Resolution {
	var r Resolution
	seen := map[string]bool{}
	for _, e := range expect {
		ids := Match(prefix, e, items)
		if len(ids) == 0 {
			r.Unresolved = append(r.Unresolved, e)
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				r.IDs = append(r.IDs, id)
			}
		}
	}
	return r
}

// Gap kinds.
const (
	GapNoCandidate   = "NO_CANDIDATE"
	GapOnlyRejected  = "ONLY_REJECTED"
	GapFactNotStated = "FACT_NOT_STATED"
)

// sectionCandidate is a candidate of any state that came from a section.
type sectionCandidate struct {
	ID, State, Question, Answer, SourceRef string
}

// gapKind explains why no expectation resolved. published says whether the
// live publication holds an item of any expected section.
func gapKind(prefix string, exp []Expectation, cands []sectionCandidate, published bool) string {
	if published {
		return GapFactNotStated
	}
	any := false
	for _, e := range exp {
		for _, c := range cands {
			if sourceMatches(prefix, e, c.SourceRef) {
				any = true
			}
		}
	}
	if any {
		return GapOnlyRejected
	}
	return GapNoCandidate
}
