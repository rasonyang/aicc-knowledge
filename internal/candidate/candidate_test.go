// SPDX-License-Identifier: Apache-2.0

package candidate

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
)

func TestContainsFiguresTable(t *testing.T) {
	flagged := []string{
		"The plan costs 59 yuan.", "It takes 30 days.", "五十九元", "三十天", "百分之五", "三分之一", "２４ hours", "5%", "５％",
		"$9.99 per month", "¥59", "€5", "one hundred dollars", "twenty-four hours", "about a dozen", "两个月", "每月一次", "一千二百元",
		"十块钱", "二十四小时", "三折", "五分钟", "一万", "十万", "Tier ①", "½ price", "共三个步骤", "退款需要七天",
	}
	for _, s := range flagged {
		if !ContainsFigures(s) {
			t.Errorf("ContainsFigures(%q) = false, want true", s)
		}
	}
	plain := []string{
		"", "Refunds go back to the original payment method.", "No one is available right now.", "One of our agents will call you.",
		"一些客户会收到短信", "一般情况下会退回原支付方式", "统一使用客服热线", "请稍等一下", "我们一起处理", "一定会处理",
		"同样的方式", "唯一的方式", "第一步，打开应用", "万一失败请联系我们", "十分感谢", "千万不要告诉别人", "百货商店",
		"同一天", "一样的价格表", "一直在线", "不一样", "这是一次性的操作",
	}
	for _, s := range plain {
		if ContainsFigures(s) {
			t.Errorf("ContainsFigures(%q) = true, want false", s)
		}
	}
}

func TestContainsFiguresModelNames(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"ZQ 3 售价 1999 元", true},
		{"ZQ 3S 支持外接显示器吗", false},
		{"支持 4K 录制", true},
		{"K5 支持防水吗", false},
		{"A2 和 ZQ 3 有什么区别", false},
		{"Does the ZQ 3S support an external display?", false},
		{"Does the Pro-2 fit?", false},
		{"60fps 录制", true},
		{"128GB 版本", true},
		{"售价 1999元", true},
		{"The K5 costs 300 dollars", true},
		{"It takes 30 days.", true},
		{"Open 24 hours", true},
		{"Step 3 is next", true},
		{"版本 K5 与 X6 都有", false},
		{"K5 supports 4K", true},
		{"ZQ 3S 有 2 个颜色", true},
	}
	for _, c := range cases {
		if got := ContainsFigures(c.text); got != c.want {
			t.Errorf("ContainsFigures(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestFlagsCoverQuestionAlternatesAndAnswer(t *testing.T) {
	if got := Flags("How do I reset it?", []string{"Reset steps?"}, "Open settings."); len(got) != 0 || got == nil {
		t.Errorf("no figures: %v", got)
	}
	for name, got := range map[string][]string{
		"answer":    Flags("q?", nil, "It is 5 dollars."),
		"question":  Flags("Is it 5 dollars?", nil, "Yes."),
		"alternate": Flags("q?", []string{"Costs 5?"}, "Yes."),
	} {
		if len(got) != 1 || got[0] != "CONTAINS_FIGURES" {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestContentHashIsStableAndSensitiveToEveryField(t *testing.T) {
	v := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	base := Content{Language: domain.LanguageEN, Question: "q", AlternateQuestions: []string{"a", "b"}, Answer: "x", SourceRef: "k#1", FileVersionID: v}
	h := ContentHash(base)
	if h != ContentHash(base) {
		t.Fatal("hash is not deterministic")
	}
	mut := map[string]func(c *Content){
		"language": func(c *Content) { c.Language = domain.LanguageZH },
		"question": func(c *Content) { c.Question = "q2" },
		"alts":     func(c *Content) { c.AlternateQuestions = []string{"a"} },
		"altorder": func(c *Content) { c.AlternateQuestions = []string{"b", "a"} },
		"answer":   func(c *Content) { c.Answer = "y" },
		"source":   func(c *Content) { c.SourceRef = "k#2" },
		"version":  func(c *Content) { c.FileVersionID = uuid.MustParse("22222222-2222-2222-2222-222222222222") },
		// Field boundaries: moving a byte from one field to the next changes the hash.
		"boundary": func(c *Content) { c.Question, c.Answer = "qx", "" },
	}
	seen := map[[32]byte]string{h: "base"}
	for name, f := range mut {
		c := base
		c.AlternateQuestions = slices.Clone(base.AlternateQuestions)
		f(&c)
		got := ContentHash(c)
		if other, dup := seen[got]; dup {
			t.Errorf("%s collides with %s", name, other)
		}
		seen[got] = name
	}
	if len(seen) != len(mut)+1 {
		t.Errorf("distinct hashes = %d, want %d", len(seen), len(mut)+1)
	}
}

func TestCleanIsIdempotentAndCollapsesWhitespace(t *testing.T) {
	for in, want := range map[string]string{
		"  a \n b\t\tc  ": "a b c", "": "", "\n\n": "", "x": "x", "全角　空格": "全角 空格",
	} {
		if got := Clean(in); got != want || Clean(got) != got {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeFoldsCaseWidthPunctuationWhitespace(t *testing.T) {
	same := [][]string{
		{"How do I get a refund?", "how  do i get a REFUND", "How do I get a refund？", "ＨＯＷ ｄｏ Ｉ ｇｅｔ ａ refund!"},
		{"如何申请退款？", "如何申请退款", "如何 申请退款!"},
	}
	for _, g := range same {
		for _, s := range g[1:] {
			if Normalize(g[0]) != Normalize(s) {
				t.Errorf("Normalize(%q) != Normalize(%q)", g[0], s)
			}
		}
	}
	if Normalize("退款多久到账") == Normalize("如何申请退款") {
		t.Error("different questions normalise equal")
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		text string
		want domain.Language
		ok   bool
	}{
		{"Refunds take five days.", domain.LanguageEN, true},
		{"退款需要五个工作日。", domain.LanguageZH, true},
		{"VoLTE 套餐每月包含流量", domain.LanguageZH, true},
		{"Our Basic plan (基础版) costs less.", domain.LanguageEN, true},
		{"2024 - 59", "", false},
		{"", "", false},
		// Chinese text with many Latin product names is still Chinese.
		{"ZQ 3S 的 SteadyMode 防抖支持 4K 吗", domain.LanguageZH, true},
		{"Alpha Pro Max 和 Beta Lite 都支持 WiFi 与 Bluetooth 连接吗", domain.LanguageZH, true},
		{"支持 USB-C Gen2 SuperSpeed Plus 快充 吗", domain.LanguageZH, true},
		{"是的，Model X1 Ultra 支持 WiFi6", domain.LanguageZH, true},
		// True English stays English, even with a little Chinese.
		{"The Alpha Pro supports 4K video and SteadyMode stabilisation.", domain.LanguageEN, true},
		{"Our Basic plan (基础版) costs less than the Premium plan and includes support.", domain.LanguageEN, true},
		{"Yes, the Beta Lite works with the 无 sticker only.", domain.LanguageEN, true},
	}
	for _, c := range cases {
		got, ok := DetectLanguage(c.text)
		if got != c.want || ok != c.ok {
			t.Errorf("DetectLanguage(%q) = %q,%v want %q,%v", c.text, got, ok, c.want, c.ok)
		}
	}
}

var lim = Limits{MaxAnswerEN: 100, MaxAnswerZH: 30}

func codes(r Result) []string {
	var out []string
	for _, v := range r.Violations {
		out = append(out, v.Code+":"+v.Field)
	}
	slices.Sort(out)
	return out
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name string
		d    Draft
		want []string
	}{
		{"ok EN", Draft{"EN", "How long do refunds take?", nil, "Refunds take five days."}, nil},
		{"ok ZH", Draft{"zh", "退款多久到账？", nil, "退款五个工作日内到账。"}, nil},
		{"empty question", Draft{"EN", "  ", nil, "Fine."}, []string{"EMPTY_QUESTION:question"}},
		{"empty answer", Draft{"EN", "Q?", nil, "\n"}, []string{"EMPTY_ANSWER:answer"}},
		{"EN too long", Draft{"EN", "Q?", nil, strings.Repeat("word ", 30)}, []string{"ANSWER_TOO_LONG:answer"}},
		{"ZH limit is lower", Draft{"ZH", "Q？", nil, strings.Repeat("退款", 20)}, []string{"ANSWER_TOO_LONG:answer"}},
		{"EN limit does not apply to ZH", Draft{"EN", "Q?", nil, strings.Repeat("a", 100)}, nil},
		{"multiline answer", Draft{"EN", "Q?", nil, "First.\nSecond."}, []string{"MULTILINE:answer"}},
		{"bullets", Draft{"EN", "Q?", nil, "- one thing - another"}, []string{"MARKDOWN:answer"}},
		{"bold", Draft{"EN", "Q?", nil, "Use **Settings** now."}, []string{"MARKDOWN:answer"}},
		{"link", Draft{"EN", "Q?", nil, "See [the page](http://x.example/a)."}, []string{"MARKDOWN:answer", "URL:answer"}},
		{"url", Draft{"EN", "Q?", nil, "Go to www.example.com today."}, []string{"URL:answer"}},
		{"bare domain", Draft{"EN", "Q?", nil, "Visit example.com to pay."}, []string{"URL:answer"}},
		{"markdown question", Draft{"EN", "# What?", nil, "Fine."}, []string{"MARKDOWN:question"}},
		{"bad language", Draft{"FR", "Q?", nil, "Fine."}, []string{"BAD_LANGUAGE:language"}},
		{"language mismatch", Draft{"EN", "Q?", nil, "退款需要五个工作日。"}, []string{"LANGUAGE_MISMATCH:answer"}},
		{"digits only answer has no script", Draft{"ZH", "几天？", nil, "30"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Validate(c.d, lim)
			got := codes(r)
			want := slices.Clone(c.want)
			slices.Sort(want)
			if !slices.Equal(got, want) || r.OK() != (len(want) == 0) {
				t.Fatalf("violations = %v, want %v", got, want)
			}
		})
	}
}

func TestValidateNormalisesAlternates(t *testing.T) {
	r := Validate(Draft{
		Language: "EN", Question: "  How do I get a refund?\n",
		AlternateQuestions: []string{
			"how do i get a refund", "", "Refund steps?", "refund steps", "Get my money back?", "Cancel and refund?", "Fourth one?",
			strings.Repeat("x", 300), "See http://a.example now?",
		},
		Answer: "Open the app.",
	}, lim)
	if !r.OK() {
		t.Fatal(r.Violations)
	}
	want := []string{"Refund steps?", "Get my money back?", "Cancel and refund?"}
	if r.Question != "How do I get a refund?" || !slices.Equal(r.AlternateQuestions, want) || len(r.AlternateQuestions) != MaxAlternates {
		t.Fatalf("question %q alternates %v", r.Question, r.AlternateQuestions)
	}
	if r := Validate(Draft{Language: "EN", Question: "Q?", Answer: "A."}, lim); r.AlternateQuestions == nil || len(r.AlternateQuestions) != 0 {
		t.Errorf("alternates = %#v, want empty non-nil", r.AlternateQuestions)
	}
}
