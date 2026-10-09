// SPDX-License-Identifier: Apache-2.0

package products

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const syntheticCatalog = `
products:
  - id: zq-3
    names: ["ZQ 3", "ZQ三", "zq-3 standard"]
    compatibleWith: [zq-3s]
  - id: zq-3s
    names: ["ZQ 3S", "ZQ三S"]
  - id: zq-ultra
    names: ["ZQ Ultra"]
  - id: nova
    names: ["Nova"]
  - id: nova-k2
    names: ["Nova K2", "Ｎｏｖａ　Ｋ２"]
  - id: lumo-air
    names: ["Lumo Air", "露米Air"]
`

func mustCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Parse([]byte(syntheticCatalog))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ZQ 3", "zq3"},
		{"ZQ-3", "zq3"},
		{"zq_3", "zq3"},
		{"ＺＱ　３Ｓ", "zq3s"},
		{"ZQ三S", "zq3s"},
		{"zq三s", "zq3s"},
		{"ZQ十", "zq10"},
		{"ZQ十二", "zq12"},
		{"ZQ二十", "zq20"},
		{"ZQ二十一", "zq21"},
		{"ZQ 一下", "zq一下"}, // a lone 一 before Han text is a word, not a number
		{"ZQ一下", "zq一下"},
		{"三号", "三号"}, // not after a Latin letter
		{"  Nova   K2 ", "novak2"},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtract(t *testing.T) {
	c := mustCatalog(t)
	for _, tc := range []struct {
		name, in string
		ids      []string
		unknown  []string
	}{
		{"plain", "How long is the ZQ Ultra battery?", []string{"zq-ultra"}, nil},
		{"full width", "ＺＱ　Ｕｌｔｒａ的电池", []string{"zq-ultra"}, nil},
		{"chinese numeral", "ZQ三的价格是多少", []string{"zq-3"}, nil},
		{"chinese numeral with suffix", "zq三s怎么充电", []string{"zq-3s"}, nil},
		{"longest match zq 3s", "ZQ 3S怎么充电", []string{"zq-3s"}, nil},
		{"longest match zq 3", "ZQ 3怎么充电", []string{"zq-3"}, nil},
		{"hyphen spelling", "zq-3 standard 的价格", []string{"zq-3"}, nil},
		{"family word alone", "Nova 的保修", []string{"nova"}, nil},
		{"longer alias wins over family word", "Nova K2 的保修", []string{"nova-k2"}, nil},
		{"full width alias", "ＮＯＶＡ　Ｋ２的保修", []string{"nova-k2"}, nil},
		{"left boundary", "myzq3 is great", nil, nil},
		{"right boundary", "zq3x is great", nil, []string{"zq3x"}},
		{"digits boundary", "ZQ 35 price", nil, []string{"ZQ 35"}},
		{"list with slash", "ZQ 3/ZQ Ultra 区别", []string{"zq-3", "zq-ultra"}, nil},
		{"list with dunhao", "ZQ 3、ZQ 3S、ZQ Ultra", []string{"zq-3", "zq-3s", "zq-ultra"}, nil},
		{"list with he", "ZQ Ultra和Nova K2哪个好", []string{"zq-ultra", "nova-k2"}, nil},
		{"shorthand list", "ZQ 3/3S 的区别", []string{"zq-3", "zq-3s"}, nil},
		{"unknown model", "ZQ 9 多少钱", nil, []string{"ZQ 9"}},
		{"unknown model next to known", "ZQ Ultra 和 ZQ 9", []string{"zq-ultra"}, []string{"ZQ 9"}},
		{"unknown compact of a known family", "zq9怎么样", nil, []string{"zq9"}},
		{"unknown sibling after a family word", "Nova K9 防水吗", []string{"nova"}, []string{"K9"}},
		{"phone compatibility", "iPhone 15 能连 ZQ 3 吗", []string{"zq-3"}, nil},
		{"covered token is not unknown", "Nova K2怎么样", []string{"nova-k2"}, nil},
		{"unknown sibling of a known family", "Nova K3怎么样", []string{"nova"}, []string{"K3"}},
		{"latin lower case with space of a known family", "zq 9 多少钱", nil, []string{"zq 9"}},
		{"nothing", "怎么开发票", nil, nil},
		{"mp4", "支持 mp4 吗", nil, nil},
		{"wifi6", "支持wifi6吗", nil, nil},
		{"usb3", "有USB3接口吗", nil, nil},
		{"h265", "支持H265吗", nil, nil},
		{"ios17", "iOS17能用吗", nil, nil},
		{"android 14", "Android 14 能用吗", nil, nil},
		{"4k", "支持4K吗", nil, nil},
		{"not a model: number words", "in 2 days", nil, nil},
		{"not a model: long number", "Call 4000 now", nil, nil},
		{"mixed alias", "露米Air怎么样", []string{"lumo-air"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Extract(tc.in)
			if !slices.Equal(got.IDs, tc.ids) || len(got.IDs) != len(tc.ids) {
				t.Errorf("IDs = %v, want %v", got.IDs, tc.ids)
			}
			if !slices.Equal(got.UnknownModels, tc.unknown) || len(got.UnknownModels) != len(tc.unknown) {
				t.Errorf("UnknownModels = %v, want %v", got.UnknownModels, tc.unknown)
			}
		})
	}
}

func TestExpandIsSymmetric(t *testing.T) {
	c := mustCatalog(t)
	for _, tc := range []struct {
		in   []string
		want []string
	}{
		{[]string{"zq-3"}, []string{"zq-3", "zq-3s"}},
		{[]string{"zq-3s"}, []string{"zq-3", "zq-3s"}}, // declared only on zq-3
		{[]string{"zq-ultra"}, []string{"zq-ultra"}},
		{nil, nil},
	} {
		got := c.Expand(tc.in)
		var keys []string
		for k := range got {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, tc.want) || len(keys) != len(tc.want) {
			t.Errorf("Expand(%v) = %v, want %v", tc.in, keys, tc.want)
		}
	}
}

func TestFindModelTokens(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"ZQ 3S and K5", []string{"ZQ 3S", "K5"}},
		{"Pro-2 is out", []string{"Pro-2"}},
		{"Open 24 hours", nil},
		{"Step 3", nil},
		{"ZQ 3000", nil},
		{"4K 60fps", nil},
		{"zq9", []string{"zq9"}},
	} {
		got := FindModelTokens(tc.in)
		if !slices.Equal(got, tc.want) || len(got) != len(tc.want) {
			t.Errorf("FindModelTokens(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if !LooksLikeModel("ZQ 3S") || !LooksLikeModel("k2") || LooksLikeModel("4K") || LooksLikeModel("hello") {
		t.Error("LooksLikeModel disagrees with the rule")
	}
}

func TestParseValidation(t *testing.T) {
	ok := func(s string) string { return "products:\n" + s }
	for _, tc := range []struct {
		name, yaml string
		issue      string // substring of one reported issue
	}{
		{"empty file", "", "products must not be empty"},
		{"empty list", "products: []", "products must not be empty"},
		{"unknown field", ok("  - id: a-1\n    names: [A1]\n    colour: red\n"), ""},
		{"bad id", ok("  - id: Bad_Id\n    names: [Alpha]\n"), "id"},
		{"duplicate id", ok("  - id: a\n    names: [Alpha]\n  - id: a\n    names: [Beta]\n"), "duplicated"},
		{"no names", ok("  - id: a\n    names: []\n"), "names"},
		{"blank name", ok("  - id: a\n    names: [\"  \"]\n"), "names"},
		{"duplicate normalized name across products", ok("  - id: a\n    names: [\"ZQ 3\"]\n  - id: b\n    names: [\"ｚｑ-3\"]\n"), "already names"},
		{"unknown compatible", ok("  - id: a\n    names: [Alpha]\n    compatibleWith: [nope]\n"), "unknown product"},
		{"self compatible", ok("  - id: a\n    names: [Alpha]\n    compatibleWith: [a]\n"), "itself"},
		{"not yaml", "products: [", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			var pe *Error
			if !errors.As(err, &pe) || pe.Code != CodeCatalogInvalid {
				t.Fatalf("err = %v, want a CATALOG_INVALID *Error", err)
			}
			if tc.issue != "" && !strings.Contains(err.Error(), tc.issue) {
				t.Errorf("error %q does not mention %q", err, tc.issue)
			}
		})
	}
}

func TestParseReportsEveryIssue(t *testing.T) {
	_, err := Parse([]byte("products:\n  - id: a\n    names: [Alpha]\n    compatibleWith: [x]\n  - id: a\n    names: [Alpha]\n"))
	var pe *Error
	if !errors.As(err, &pe) || len(pe.Issues) < 3 {
		t.Fatalf("want at least 3 issues (duplicate id, duplicate name, unknown compat), got %v", err)
	}
}

func TestRoundTripJSON(t *testing.T) {
	c := mustCatalog(t)
	raw, err := c.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, b := c.Extract("ZQ 3/3S 和 Nova K2"), back.Extract("ZQ 3/3S 和 Nova K2")
	if !slices.Equal(a.IDs, b.IDs) || len(a.IDs) != 3 {
		t.Errorf("round trip changed extraction: %v vs %v", a.IDs, b.IDs)
	}
}

func TestDocumentProducts(t *testing.T) {
	c := mustCatalog(t)
	for _, tc := range []struct {
		name, q string
		alts    []string
		key     string
		want    []string
	}{
		{"question wins", "ZQ Ultra多久充满", nil, "kb/zq-3-faq.docx", []string{"zq-ultra"}},
		{"alternates add", "多久充满", []string{"ZQ 3S多久充满"}, "kb/faq.docx", []string{"zq-3s"}},
		{"file name fallback", "多久充满", nil, "kb/ZQ三_常见问题.docx", []string{"zq-3"}},
		{"generic", "怎么开发票", nil, "kb/faq.docx", nil},
		{"directories are not names", "怎么开发票", nil, "kb/zq-ultra/faq.docx", nil},
	} {
		got := c.DocumentProducts(tc.q, tc.alts, tc.key)
		if !slices.Equal(got, tc.want) || len(got) != len(tc.want) {
			t.Errorf("%s: DocumentProducts = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func BenchmarkExtract(b *testing.B) {
	c, err := Parse([]byte(syntheticCatalog))
	if err != nil {
		b.Fatal(err)
	}
	q := "我想问一下ZQ三s和ZQ Ultra的电池续航有什么区别，还有Nova K2的保修期多久"
	for b.Loop() {
		c.Extract(q)
	}
}

const bareAliasCatalog = `
products:
  - id: zq-series
    names: ["ZQ", "ZQ 系列"]
  - id: zq-3
    names: ["ZQ 3"]
  - id: zq-3s
    names: ["ZQ 3S"]
  - id: zq-ultra
    names: ["ZQ Ultra"]
  - id: nova
    names: ["Nova"]
  - id: nova-k2
    names: ["Nova K2"]
`

// A bare family alias must not swallow the model after it (R2).
func TestExtractBareFamilyAlias(t *testing.T) {
	c, err := Parse([]byte(bareAliasCatalog))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, in string
		ids      []string
		unknown  []string
	}{
		{"unknown number", "ZQ 5 电池能用多久", nil, []string{"ZQ 5"}},
		{"unknown compact", "zq5电池能用多久", nil, []string{"zq5"}},
		{"unknown hyphen", "ZQ-5 电池能用多久", nil, []string{"ZQ-5"}},
		{"unknown chinese numeral", "ZQ五电池能用多久", nil, []string{"ZQ 5"}},
		{"unknown full width", "ＺＱ　５防水吗", nil, []string{"ZQ 5"}},
		{"unknown with letters", "ZQ 5S 防水吗", nil, []string{"ZQ 5S"}},
		{"unknown with word", "ZQ 5 Pro 防水吗", nil, []string{"ZQ 5 Pro"}},
		{"unknown with glued word", "ZQ 9Air 防水吗", nil, []string{"ZQ 9Air"}},
		{"other family", "Nova 7 多久保修", nil, []string{"Nova 7"}},
		{"unknown then english words", "Is the ZQ 5 waterproof?", nil, []string{"ZQ 5"}},
		{"unknown letters then english words", "Is the ZQ 5S waterproof?", nil, []string{"ZQ 5S"}},
		{"unknown with word then english", "Is the ZQ 5 Pro waterproof?", nil, []string{"ZQ 5 Pro"}},
		{"unknown next to known", "ZQ Ultra 和 ZQ 5 区别", []string{"zq-ultra"}, []string{"ZQ 5"}},
		{"known 3", "ZQ 3 防水吗", []string{"zq-3"}, nil},
		{"known 3S", "ZQ 3S 防水吗", []string{"zq-3s"}, nil},
		{"known chinese numeral", "ZQ三S 防水吗", []string{"zq-3s"}, nil},
		{"known Ultra", "ZQ Ultra 防水吗", []string{"zq-ultra"}, nil},
		{"series alias", "ZQ 系列 防水吗", []string{"zq-series"}, nil},
		{"bare alone", "ZQ 防水吗", []string{"zq-series"}, nil},
		{"bare then text", "ZQ 怎么充电", []string{"zq-series"}, nil},
		{"bare then year", "ZQ 2024 款", []string{"zq-series"}, nil},
		{"nova k2 still matches", "Nova K2 保修", []string{"nova-k2"}, nil},
		{"bare nova alone", "Nova 保修", []string{"nova"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Extract(tc.in)
			if !slices.Equal(got.IDs, tc.ids) || len(got.IDs) != len(tc.ids) {
				t.Errorf("IDs = %v, want %v", got.IDs, tc.ids)
			}
			if !slices.Equal(got.UnknownModels, tc.unknown) || len(got.UnknownModels) != len(tc.unknown) {
				t.Errorf("UnknownModels = %v, want %v", got.UnknownModels, tc.unknown)
			}
		})
	}
}
