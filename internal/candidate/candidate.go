// SPDX-License-Identifier: Apache-2.0

// Package candidate holds the pure rules about a candidate Q&A: how its text
// is normalised, how its content hash is computed, which flags it carries and
// whether it is fit to be shown to a reviewer. Generation, export and import
// all use these functions, so the three can never disagree about what a
// candidate "is".
package candidate

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
)

// Content is everything the content hash covers. The text fields must already
// be Clean.
type Content struct {
	Language           domain.Language
	Question           string
	AlternateQuestions []string
	Answer             string
	SourceRef          string
	FileVersionID      uuid.UUID
}

// hashDomain versions the canonical encoding. Changing the encoding means
// changing this tag, which makes every older hash mismatch (and so every
// outstanding review export STALE) instead of silently colliding.
const hashDomain = "aicc-knowledge/candidate-content/v1"

// ContentHash is the SHA-256 of the canonical encoding of the content: the
// domain tag, then language, question, the alternate questions (a count, then
// each in order), answer, source ref and file version id, every string
// prefixed by its byte length as a big-endian uint64 so no field can bleed
// into the next. It is the one hash function of the service; the database
// column candidates.content_hash, the hidden export column and the import
// check all hold this value.
func ContentHash(c Content) [32]byte {
	h := sha256.New()
	var n [8]byte
	str := func(s string) {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	str(hashDomain)
	str(string(c.Language))
	str(c.Question)
	binary.BigEndian.PutUint64(n[:], uint64(len(c.AlternateQuestions)))
	h.Write(n[:])
	for _, a := range c.AlternateQuestions {
		str(a)
	}
	str(c.Answer)
	str(c.SourceRef)
	str(c.FileVersionID.String())
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Clean trims s and collapses every run of white space (newlines included)
// into one space. It is idempotent, which is what makes an export followed by
// an import reproduce the stored text exactly.
func Clean(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// Normalize is the key two questions are compared by: Unicode compatibility
// folded (full-width to ASCII), lower-cased, with white space and punctuation
// removed.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKC.String(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// DetectLanguage reports the dominant script of text. It is ZH when there are
// at least zhMinHan Han characters (a sentence with that many is Chinese
// however many Latin product names it carries), or when Han characters are at
// at least 4 and at least 15% of the letters (Han plus Latin letters), or when they make up at
// least 40% of the text's weight, where each Latin word counts as two
// characters. Otherwise it is EN. ok is false when the text has no letters.
func DetectLanguage(text string) (lang domain.Language, ok bool) {
	han, words, latin := 0, 0, 0
	inWord := false
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
			inWord = false
		case r < 0x250 && unicode.IsLetter(r):
			if !inWord {
				words++
			}
			latin++
			inWord = true
		default:
			inWord = false
		}
	}
	if han == 0 && words == 0 {
		return "", false
	}
	if han >= zhMinHan || (han >= 4 && float64(han)/float64(han+latin) >= 0.15) || float64(han)/float64(han+2*words) >= 0.4 {
		return domain.LanguageZH, true
	}
	return domain.LanguageEN, true
}

// zhMinHan is the number of Han characters that makes a text Chinese.
const zhMinHan = 6
