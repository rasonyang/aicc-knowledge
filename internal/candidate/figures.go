// SPDX-License-Identifier: Apache-2.0

package candidate

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
)

// Flags returns the flags a candidate carries, computed from its text:
// CONTAINS_FIGURES when the question, an alternate question or the answer
// states a figure. The result is never nil.
func Flags(question string, alternates []string, answer string) []string {
	flags := []string{}
	if ContainsFigures(question) || ContainsFigures(answer) || anyFigures(alternates) {
		flags = append(flags, string(domain.FlagContainsFigures))
	}
	return flags
}

func anyFigures(list []string) bool {
	for _, s := range list {
		if ContainsFigures(s) {
			return true
		}
	}
	return false
}

// ContainsFigures reports whether text states a number a reviewer must check
// against the source. The rule errs toward flagging:
//
//   - any Unicode number character (ASCII, full-width and other digits,
//     fractions such as ½, circled and Roman numerals);
//   - a percent sign (% % ‰) or any currency symbol (Unicode category Sc);
//   - an English number word from two upward (two, twenty, hundred, million,
//     dozen, ...) or percent/dollar/cent/yuan/euro; "one" alone is not
//     flagged because it is mostly a pronoun;
//   - a Chinese numeral used as a quantity (see chineseQuantity).
//
// Letter-first model tokens are not figures (see modelToken): X5, A2, ZQ 3,
// ZQ 3S. Digit-first tokens (4K, 60fps, 128GB, 1999元) and plain numbers are.
func ContainsFigures(text string) bool {
	text = modelToken.ReplaceAllString(text, " ")
	for _, r := range text {
		if unicode.IsNumber(r) || unicode.Is(unicode.Sc, r) || r == '%' || r == '％' || r == '‰' {
			return true
		}
	}
	if englishFigure.MatchString(text) {
		return true
	}
	return chineseQuantity(text)
}

// modelToken matches a letter-first product model: capital letters, an
// optional space or hyphen, one or two digits and up to two letters (X5, A2,
// ZQ 3, ZQ 3S, Pro-2), or a capitalised word, an optional hyphen and the
// digits (Pro-2, Max5). A capitalised word followed by a space and a number
// ("Open 24 hours", "Step 3") is not a model. The match must end at a word
// boundary, so X500 and ZQ 3000 are not models.
var modelToken = regexp.MustCompile(`\b(?:[A-Z]{1,5}[ -]?|[A-Z][a-z]{1,8}-?)\d{1,2}[A-Za-z]{0,2}\b`)

var englishFigure = regexp.MustCompile(`(?i)\b(?:two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety|hundred|thousand|million|billion|dozen|percent|dollars?|cents?|euros?|yuan|rmb|usd|cny)\b`)

const numeralRunes = "零〇一二三四五六七八九十百千万亿两壹贰叁肆伍陆柒捌玖拾佰仟"

// quantityUnits are what follows a lone numeral when it counts something.
// The bare 分 is handled separately because 十分 means "very".
var quantityUnits = []string{
	"个", "元", "块", "角", "毛", "天", "日", "号", "月", "年", "周", "星期", "次", "条", "位", "名", "人", "件", "秒", "小时",
	"倍", "折", "成", "度", "公里", "米", "台", "套", "张", "份", "笔", "项", "种", "级", "档", "期", "岁", "季", "遍", "回",
	"趟", "层", "间", "家", "只", "部", "本", "辆", "克", "斤", "吨", "兆", "美元", "分钟", "页", "场", "批", "组", "类", "步",
	"户", "课", "门", "封", "通", "段", "站", "点钟",
}

// quantityDeny lists words whose numeral is not a quantity, matched at the
// start of the numeral run.
var quantityDeny = []string{
	"一些", "一般", "统一", "一下", "一起", "一定", "一样", "一切", "一旦", "一直", "一律", "一会", "一点", "万一", "一概",
	"一向", "一再", "一并", "一经", "一味", "一度", "一贯", "一时", "一面", "一方面", "一次性", "一同", "一道", "十分", "十足",
	"百货", "百科", "千万不", "千万别", "千万要", "千万小心", "万能", "万物", "万分",
}

// nonQuantityBefore are characters that turn a following numeral into a word
// part or an ordinal, not a quantity.
const nonQuantityBefore = "第同统唯单惟不"

var fractionWord = regexp.MustCompile(`分之[` + numeralRunes + `]`)

// chineseQuantity implements the Chinese half of ContainsFigures. It scans
// maximal runs of numeral characters:
//
//   - 分之 followed by a numeral (百分之五, 三分之一) is always a figure;
//   - a run is skipped when it starts one of quantityDeny (一些, 一般, 统一,
//     十分, ...) or follows a character of nonQuantityBefore (第一, 同一, 唯一);
//   - a run of two or more numeral characters (三十, 五十九, 一千二百, 十万) is
//     a figure;
//   - a lone numeral is a figure when a counting unit follows (一次, 三折,
//     两个月, 五分钟, 十块).
func chineseQuantity(text string) bool {
	if fractionWord.MatchString(text) {
		return true
	}
	rs := []rune(text)
	for i := 0; i < len(rs); {
		if !isNumeral(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isNumeral(rs[j]) {
			j++
		}
		run, rest := string(rs[i:j]), string(rs[j:])
		before := rune(0)
		if i > 0 {
			before = rs[i-1]
		}
		i = j
		if before != 0 && strings.ContainsRune(nonQuantityBefore, before) {
			continue
		}
		full := run + rest
		if denied(full) {
			continue
		}
		if len([]rune(run)) >= 2 {
			return true
		}
		for _, u := range quantityUnits {
			if strings.HasPrefix(rest, u) {
				return true
			}
		}
		if strings.HasPrefix(rest, "分") && run != "十" {
			return true
		}
	}
	return false
}

func isNumeral(r rune) bool { return strings.ContainsRune(numeralRunes, r) }

func denied(s string) bool {
	for _, d := range quantityDeny {
		if strings.HasPrefix(s, d) {
			return true
		}
	}
	return false
}
