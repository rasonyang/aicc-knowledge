// SPDX-License-Identifier: Apache-2.0

package facts

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

func allTypes() []xlsx.Column {
	return []xlsx.Column{
		{Name: "s", Type: xlsx.TypeString}, {Name: "i", Type: xlsx.TypeInteger}, {Name: "d", Type: xlsx.TypeDecimal},
		{Name: "dt", Type: xlsx.TypeDate}, {Name: "b", Type: xlsx.TypeBoolean},
	}
}

func canon(t *testing.T, col string, v any) (string, *KeyError) {
	t.Helper()
	b, err := CanonicalKey(allTypes(), []string{col}, map[string]any{col: v})
	if err != nil {
		var ke *KeyError
		if !errors.As(err, &ke) {
			t.Fatalf("err = %v", err)
		}
		return "", ke
	}
	return string(b), nil
}

func TestKeyCoercionPerType(t *testing.T) {
	num := func(s string) json.Number { return json.Number(s) }
	cases := []struct {
		name string
		col  string
		in   any
		want string // canonical JSON; empty means a KeyError on that column
	}{
		{"string", "s", "Basic", `{"s":"Basic"}`},
		{"string keeps spaces and case", "s", " basic ", `{"s":" basic "}`},
		{"string rejects a number", "s", num("42"), ""},
		{"string rejects null", "s", nil, ""},
		{"string rejects an object", "s", map[string]any{"a": 1}, ""},

		{"integer number", "i", num("42"), `{"i":42}`},
		{"integer string", "i", "42", `{"i":42}`},
		{"integer negative string", "i", "-7", `{"i":-7}`},
		{"integer leading zeros", "i", "042", `{"i":42}`},
		{"integer float64 from a plain decoder", "i", float64(42), `{"i":42}`},
		{"integer with fraction", "i", num("42.5"), ""},
		{"integer 42.0 is not an integer literal", "i", num("42.0"), ""},
		{"integer with exponent", "i", num("4.2e1"), ""},
		{"integer string with a fraction", "i", "42.0", ""},
		{"integer string with spaces", "i", " 42", ""},
		{"integer plus sign", "i", "+42", ""},
		{"integer out of range", "i", "9223372036854775808", ""},
		{"integer bool", "i", true, ""},
		{"integer empty string", "i", "", ""},

		{"decimal number", "d", num("9.9"), `{"d":9.9}`},
		{"decimal number keeps its digits", "d", num("9.90"), `{"d":9.90}`},
		{"decimal string", "d", "9.90", `{"d":9.90}`},
		{"decimal string leading zeros", "d", "007.5", `{"d":7.5}`},
		{"decimal negative", "d", "-0.1", `{"d":-0.1}`},
		{"decimal integer string", "d", "10", `{"d":10}`},
		{"decimal exact beyond float64", "d", num("1234567890.123456789"), `{"d":1234567890.123456789}`},
		{"decimal string exponent", "d", "1e3", ""},
		{"decimal comma", "d", "9,9", ""},
		{"decimal bool", "d", false, ""},

		{"date", "dt", "2024-07-01", `{"dt":"2024-07-01"}`},
		{"date not padded", "dt", "2024-7-1", ""},
		{"date impossible", "dt", "2024-02-30", ""},
		{"date with time", "dt", "2024-07-01T00:00:00Z", ""},
		{"date number", "dt", num("20240701"), ""},

		{"bool", "b", true, `{"b":true}`},
		{"bool string true", "b", "true", `{"b":true}`},
		{"bool string false", "b", "false", `{"b":false}`},
		{"bool capitalised", "b", "True", ""},
		{"bool number", "b", num("1"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ke := canon(t, c.col, c.in)
			if c.want == "" {
				if ke == nil || len(ke.Invalid) != 1 || ke.Invalid[0].Column != c.col || len(ke.Missing) != 0 || len(ke.Unknown) != 0 {
					t.Fatalf("got %q / %+v, want one invalid entry for %s", got, ke, c.col)
				}
				return
			}
			if ke != nil {
				t.Fatalf("unexpected %+v", ke)
			}
			if got != c.want {
				t.Errorf("canonical = %s, want %s", got, c.want)
			}
		})
	}
}

func TestKeyMustCoverExactlyTheKeyColumns(t *testing.T) {
	cols := allTypes()
	_, err := CanonicalKey(cols, []string{"s", "i"}, map[string]any{"i": "1", "zeta": 1, "alpha": 2})
	var ke *KeyError
	if !errors.As(err, &ke) {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(ke.Missing, []string{"s"}) || !slices.Equal(ke.Unknown, []string{"alpha", "zeta"}) || len(ke.Invalid) != 0 ||
		!slices.Equal(ke.KeyColumns, []string{"s", "i"}) {
		t.Errorf("key error = %+v", ke)
	}
	// A non-key column of the table is unknown as a key.
	_, err = CanonicalKey(cols, []string{"s"}, map[string]any{"s": "x", "i": "1"})
	if !errors.As(err, &ke) || !slices.Equal(ke.Unknown, []string{"i"}) {
		t.Errorf("non-key column: %v", err)
	}
	// Problems are reported together.
	_, err = CanonicalKey(cols, []string{"s", "i"}, map[string]any{"i": "x", "q": 1})
	if !errors.As(err, &ke) || len(ke.Missing) != 1 || len(ke.Unknown) != 1 || len(ke.Invalid) != 1 {
		t.Errorf("combined: %+v", ke)
	}
	// A composite key produces one canonical object whatever the input order.
	a, err1 := CanonicalKey(cols, []string{"s", "i"}, map[string]any{"s": "x", "i": "1"})
	b, err2 := CanonicalKey(cols, []string{"i", "s"}, map[string]any{"i": json.Number("1"), "s": "x"})
	if err1 != nil || err2 != nil || string(a) != string(b) || string(a) != `{"i":1,"s":"x"}` {
		t.Errorf("composite = %s / %s (%v %v)", a, b, err1, err2)
	}
}
