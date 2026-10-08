// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

var (
	intRe       = regexp.MustCompile(`^[+-]?\d+$`)
	decimalRe   = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)$`)
	isoDateRe   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	slashDateRe = regexp.MustCompile(`^(\d{4})/(\d{1,2})/(\d{1,2})$`)
	cjkDateRe   = regexp.MustCompile(`^(\d{4})年(\d{1,2})月(\d{1,2})日$`)
	ambigDateRe = regexp.MustCompile(`^\d{1,4}[/.-]\d{1,2}[/.-]\d{1,4}$`)
)

// canonicalDecimal renders a decimal string without trailing fractional zeros,
// a leading plus sign or a negative zero. It works on the text, so no binary
// floating point is involved.
func canonicalDecimal(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	s = strings.TrimLeft(s, "0")
	if s == "" || strings.HasPrefix(s, ".") {
		s = "0" + s
	}
	if neg && s != "0" {
		s = "-" + s
	}
	return s
}

// numberText turns the raw XML text of a numeric cell into plain decimal text.
// Excel writes doubles with up to 17 significant digits ("0.10000000000000001");
// the shortest representation that round-trips the double is the value the
// author typed.
func numberText(raw string) (string, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return "", false
	}
	return canonicalDecimal(strconv.FormatFloat(f, 'f', -1, 64)), true
}

func validDate(y, m, d string) (string, bool) {
	yy, _ := strconv.Atoi(y)
	mm, _ := strconv.Atoi(m)
	dd, _ := strconv.Atoi(d)
	t := time.Date(yy, time.Month(mm), dd, 0, 0, 0, 0, time.UTC)
	if t.Year() != yy || int(t.Month()) != mm || t.Day() != dd {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

// convert turns a non-blank cell into the typed value. On failure it returns a
// non-empty code and a detail.
func (g *grid) convert(cd cellData, typ ColumnType) (any, string, string) {
	if cd.isError() {
		return nil, CodeCellErrorValue, "cell holds the error value " + cd.display
	}
	mismatch := func(want string) (any, string, string) {
		return nil, CodeCellTypeMismatch, "expected " + want + ", found " + strconvQuote(cd.display)
	}
	text := strings.TrimSpace(cd.display)
	switch typ {
	case TypeString:
		return text, "", ""
	case TypeInteger:
		if cd.isNumber() && !cd.isDate {
			if s, ok := numberText(cd.raw); ok && !strings.Contains(s, ".") {
				if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= -(1<<53) && n <= 1<<53 {
					return n, "", ""
				}
			}
		} else if cd.isText() && intRe.MatchString(text) {
			if n, err := strconv.ParseInt(strings.TrimPrefix(text, "+"), 10, 64); err == nil {
				return n, "", ""
			}
		}
		return mismatch("an integer")
	case TypeDecimal:
		if cd.isNumber() && !cd.isDate {
			if s, ok := numberText(cd.raw); ok {
				return json.Number(s), "", ""
			}
		} else if cd.isText() && decimalRe.MatchString(text) {
			return json.Number(canonicalDecimal(text)), "", ""
		}
		return mismatch("a decimal number")
	case TypeBoolean:
		switch {
		case cd.typ == excelize.CellTypeBool || (cd.isNumber() && !cd.isDate):
			switch strings.TrimSpace(cd.raw) {
			case "1":
				return true, "", ""
			case "0":
				return false, "", ""
			}
		case cd.isText():
			switch strings.ToLower(text) {
			case "true", "yes", "是":
				return true, "", ""
			case "false", "no", "否":
				return false, "", ""
			}
		}
		return mismatch("a boolean")
	case TypeDate:
		return g.convertDate(cd, text)
	}
	return mismatch("a known type")
}

func (g *grid) convertDate(cd cellData, text string) (any, string, string) {
	bad := func(code, why string) (any, string, string) {
		return nil, code, why + ", found " + strconvQuote(cd.display)
	}
	switch {
	case cd.typ == excelize.CellTypeDate:
		for _, layout := range []string{"2006-01-02T15:04:05Z", "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(cd.raw)); err == nil {
				if t.Hour()|t.Minute()|t.Second() == 0 {
					return t.Format("2006-01-02"), "", ""
				}
				break
			}
		}
		return bad(CodeCellTypeMismatch, "expected a date without time of day")
	case cd.isNumber():
		if !cd.isDate {
			return bad(CodeCellTypeMismatch, "expected a date; the number has no date format")
		}
		if s, ok := g.serialToDate(cd.raw); ok {
			return s, "", ""
		}
		return bad(CodeCellTypeMismatch, "expected a date without time of day")
	case cd.isText():
		for _, re := range []*regexp.Regexp{isoDateRe, slashDateRe, cjkDateRe} {
			if m := re.FindStringSubmatch(text); m != nil {
				if s, ok := validDate(m[1], m[2], m[3]); ok {
					return s, "", ""
				}
				return bad(CodeCellTypeMismatch, "expected a real calendar date")
			}
		}
		if ambigDateRe.MatchString(text) {
			return bad(CodeDateAmbiguous, "ambiguous text date (use yyyy-mm-dd)")
		}
	}
	return bad(CodeCellTypeMismatch, "expected a date")
}

func strconvQuote(s string) string { return strconv.Quote(s) }
