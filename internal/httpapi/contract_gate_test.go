// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rasonyang/aicc-knowledge/docs"
)

type spec = map[string]any

func loadSpec(t *testing.T) spec {
	t.Helper()
	var s spec
	if err := json.Unmarshal(docs.Contract, &s); err != nil {
		t.Fatal(err)
	}
	if s["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v, want 3.1.0", s["openapi"])
	}
	return s
}

type operation struct {
	method, path string
	body         spec
}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

func operations(t *testing.T, s spec) []operation {
	t.Helper()
	var ops []operation
	for path, item := range s["paths"].(spec) {
		for _, m := range httpMethods {
			if body, ok := item.(spec)[m]; ok {
				ops = append(ops, operation{strings.ToUpper(m), path, body.(spec)})
			}
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].path+ops[i].method < ops[j].path+ops[j].method })
	if len(ops) == 0 {
		t.Fatal("the contract declares no operations")
	}
	return ops
}

// walk calls f for every map in the document with the key that led to it.
func walk(v any, key string, f func(key string, m spec)) {
	switch x := v.(type) {
	case spec:
		f(key, x)
		for k, c := range x {
			walk(c, k, f)
		}
	case []any:
		for _, c := range x {
			walk(c, key, f)
		}
	}
}

func goErrorCodes(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || !strings.HasPrefix(vs.Names[0].Name, "Code") || len(vs.Values) != 1 {
			return true
		}
		if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "ErrorCode" {
			return true
		}
		if lit, ok := vs.Values[0].(*ast.BasicLit); ok {
			v, _ := strconv.Unquote(lit.Value)
			out = append(out, v)
		}
		return true
	})
	sort.Strings(out)
	return out
}

func TestErrorCodeEnumEqualsGoConstants(t *testing.T) {
	s := loadSpec(t)
	raw := s["components"].(spec)["schemas"].(spec)["ErrorCode"].(spec)["enum"].([]any)
	var specCodes []string
	for _, v := range raw {
		specCodes = append(specCodes, v.(string))
	}
	sort.Strings(specCodes)
	got := goErrorCodes(t)
	if !slices.Equal(specCodes, got) {
		t.Fatalf("spec ErrorCode %v\nGo Code* consts %v", specCodes, got)
	}
	if len(got) < 11 {
		t.Fatalf("only %d error codes, the required set has 11", len(got))
	}
}

var lowerCamel = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

func TestEverySpecPropertyNameIsLowerCamelCase(t *testing.T) {
	s := loadSpec(t)
	n := 0
	walk(s, "", func(key string, m spec) {
		if props, ok := m["properties"].(spec); ok {
			for name := range props {
				n++
				if !lowerCamel.MatchString(name) {
					t.Errorf("property %q is not lowerCamelCase", name)
				}
			}
		}
		if _, isParam := m["in"]; isParam {
			if name, ok := m["name"].(string); ok {
				n++
				if !lowerCamel.MatchString(name) {
					t.Errorf("parameter %q is not lowerCamelCase", name)
				}
			}
		}
	})
	if n < 15 {
		t.Fatalf("checked only %d names, the walk is broken", n)
	}
}

var screamingSnake = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`)

func TestEverySpecEnumValueIsScreamingSnake(t *testing.T) {
	s := loadSpec(t)
	enums, values := 0, 0
	walk(s, "", func(_ string, m spec) {
		list, ok := m["enum"].([]any)
		if !ok {
			return
		}
		enums++
		for _, v := range list {
			values++
			str, isStr := v.(string)
			if !isStr || !screamingSnake.MatchString(str) {
				t.Errorf("enum value %v is not SCREAMING_SNAKE", v)
			}
		}
	})
	if enums != 4 { // Language, SearchStatus, FactsLookupStatus, ErrorCode
		t.Errorf("enums = %d, want 4 (update this when the contract gains one)", enums)
	}
	if values != 2+2+2+12 {
		t.Errorf("enum values = %d, want 18", values)
	}
}

func chiPath(p string) string { return p } // OpenAPI {name} and chi {name} agree

func TestEveryOperationIsRoutedAndNoRouteIsUndeclared(t *testing.T) {
	s := loadSpec(t)
	declared := map[string]bool{}
	for _, op := range operations(t, s) {
		declared[op.method+" "+chiPath(op.path)] = true
	}
	routed := map[string]bool{}
	err := chi.Walk(newRouter(&Server{}), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routed[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for k := range declared {
		if !routed[k] {
			t.Errorf("contract operation %s is not routed", k)
		}
	}
	for k := range routed {
		if !declared[k] {
			t.Errorf("route %s is mounted but the contract does not declare it", k)
		}
	}
	if len(routed) != len(declared) {
		t.Errorf("routed %d, declared %d", len(routed), len(declared))
	}
}

func TestEveryOperationDeclaresBearerSecurity(t *testing.T) {
	s := loadSpec(t)
	schemes := s["components"].(spec)["securitySchemes"].(spec)
	bearer := schemes["apiKeyBearer"].(spec)
	if bearer["type"] != "http" || bearer["scheme"] != "bearer" {
		t.Fatalf("apiKeyBearer = %v, want http bearer", bearer)
	}
	for _, op := range operations(t, s) {
		sec, ok := op.body["security"].([]any)
		if !ok || len(sec) != 1 {
			t.Errorf("%s %s: security = %v, want exactly one requirement", op.method, op.path, op.body["security"])
			continue
		}
		if _, ok := sec[0].(spec)["apiKeyBearer"]; !ok {
			t.Errorf("%s %s: security does not name apiKeyBearer", op.method, op.path)
		}
	}
}

var operationVerbs = []string{"search", "lookup", "get", "list", "create", "update", "delete"}

func TestOperationIDsAreLowerCamelVerbs(t *testing.T) {
	s := loadSpec(t)
	seen := map[string]bool{}
	for _, op := range operations(t, s) {
		id, _ := op.body["operationId"].(string)
		if !lowerCamel.MatchString(id) {
			t.Errorf("%s %s: operationId %q is not lowerCamelCase", op.method, op.path, id)
			continue
		}
		if seen[id] {
			t.Errorf("operationId %q is used twice", id)
		}
		seen[id] = true
		if !slices.ContainsFunc(operationVerbs, func(v string) bool {
			return strings.HasPrefix(id, v) && len(id) > len(v) && id[len(v)] >= 'A' && id[len(v)] <= 'Z'
		}) {
			t.Errorf("operationId %q does not start with a verb from %v", id, operationVerbs)
		}
	}
}

var codeToken = regexp.MustCompile(`\b[A-Z]+(?:_[A-Z]+)+\b|\bINTERNAL\b|\bUNAUTHORIZED\b`)

func TestErrorResponsesListTheirCodes(t *testing.T) {
	s := loadSpec(t)
	known := map[string]bool{}
	for _, c := range goErrorCodes(t) {
		known[c] = true
	}
	responses := s["components"].(spec)["responses"].(spec)
	used := map[string]bool{}
	for _, op := range operations(t, s) {
		for status, r := range op.body["responses"].(spec) {
			ref, _ := r.(spec)["$ref"].(string)
			if ref == "" {
				continue
			}
			name := strings.TrimPrefix(ref, "#/components/responses/")
			used[name] = true
			if status[0] == '2' {
				t.Errorf("%s %s: success response %s must not be an error component", op.method, op.path, status)
			}
		}
	}
	for name, r := range responses {
		desc := r.(spec)["description"].(string)
		codes := codeToken.FindAllString(desc, -1)
		if len(codes) == 0 {
			t.Errorf("response %s lists no error codes: %q", name, desc)
		}
		for _, c := range codes {
			if !known[c] {
				t.Errorf("response %s names %s which is not an ErrorCode", name, c)
			}
		}
		if !used[name] {
			t.Errorf("response %s is declared but no operation uses it", name)
		}
	}
	if len(responses) != 7 {
		t.Errorf("components.responses = %d, want 7", len(responses))
	}
}
