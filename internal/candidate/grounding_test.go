// SPDX-License-Identifier: Apache-2.0

package candidate

import (
	"slices"
	"testing"
)

func TestFigures(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"", nil},
		{"无数字", nil},
		{"售价 1999 元", []string{"1999"}},
		{"售价１９９９元", []string{"1999"}},
		{"1,999 yuan and 3.5 kg", []string{"1999", "3.5"}},
		{"It ends in 2024.", []string{"2024"}},
		{"ZQ 3S 支持 4K", []string{"zq3s", "4"}},
		{"K5 and Pro-2 and A2", []string{"k5", "pro2", "a2"}},
		{"30 days, 30 days", []string{"30"}},
	}
	for _, c := range cases {
		if got := Figures(c.text); !slices.Equal(got, c.want) {
			t.Errorf("Figures(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestCheckGrounded(t *testing.T) {
	cases := []struct {
		name, answer, source string
		ungrounded           bool
	}{
		{"same number", "售价 1999 元。", "该产品售价 1999 元，含税。", false},
		{"full-width in source", "售价 1999 元。", "该产品售价１９９９元。", false},
		{"full-width in answer", "售价１９９９元。", "price 1999", false},
		{"thousands separator", "Costs 1999 yuan.", "Costs 1,999 yuan.", false},
		{"invented number", "质保 24 个月。", "该产品提供质保服务。", true},
		{"changed number", "Costs 299 dollars.", "Costs 399 dollars.", true},
		{"number is not a substring match", "Takes 30 days.", "Takes 130 days.", true},
		{"no figures", "支持外接显示器。", "支持外接显示器，无需适配器。", false},
		{"model grounded", "ZQ 3S 支持外接显示器。", "zq 3s 支持外接显示器。", false},
		{"model grounded with spacing", "ZQ3S 支持。", "ZQ 3S 支持。", false},
		{"invented model", "X6 支持防水。", "K5 支持防水。", true},
		{"model digit is not a bare number", "支持 5 个。", "K5 支持防水。", true},
		{"4K grounded", "支持 4K 录制。", "支持 4K 录制，60fps。", false},
		{"invented fps", "支持 4K 120fps 录制。", "支持 4K 录制，60fps。", true},
	}
	for _, c := range cases {
		v, bad := CheckGrounded(c.answer, c.source)
		if bad != c.ungrounded {
			t.Errorf("%s: CheckGrounded(%q, %q) = %v, %v", c.name, c.answer, c.source, v, bad)
		}
		if bad && v.Code != "UNGROUNDED_FIGURE" {
			t.Errorf("%s: code %q", c.name, v.Code)
		}
	}
}
