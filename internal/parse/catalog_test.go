// SPDX-License-Identifier: Apache-2.0

package parse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
)

const catalogYAML = `products:
  - id: zq-3
    names: ["ZQ 3", "ZQ三"]
    compatibleWith: [zq-3s]
  - id: zq-3s
    names: ["ZQ 3S"]
  - id: zq-ultra
    names: ["ZQ Ultra"]
`

func rows(t *testing.T, e *parsetest.Env, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.Store.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCatalogAtTheRootIsParsedAndStored(t *testing.T) {
	e := parsetest.New(t)
	e.Put("products.yaml", []byte(catalogYAML))
	ps := e.ScanParse()
	if ps.Claimed != 1 || ps.Parsed != 1 || ps.Failed != 0 {
		t.Fatalf("parse = %+v", ps)
	}
	v, ok := e.Current("products.yaml")
	if !ok || v.State != "PARSED" || v.ErrCode != "" {
		t.Fatalf("catalog version = %+v", v)
	}
	if n := count(t, e, `SELECT count(*) FROM product_catalogs WHERE file_version_id = $1`, v.ID); n != 1 {
		t.Fatalf("stored catalogs = %d, want 1", n)
	}
	var raw []byte
	if err := e.Store.Pool.QueryRow(context.Background(), `SELECT products FROM product_catalogs WHERE file_version_id = $1`, v.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Products []struct {
			ID    string
			Names []string
		}
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Products) != 3 || doc.Products[0].ID != "zq-3" || len(doc.Products[0].Names) != 2 {
		t.Fatalf("stored catalog = %s (%v)", raw, err)
	}
	// A catalog is not content: no sections, no candidates to generate.
	if n := count(t, e, `SELECT count(*) FROM parsed_sections`); n != 0 {
		t.Errorf("sections = %d, want 0", n)
	}
	if n := count(t, e, `SELECT count(*) FROM jobs WHERE kind = 'GENERATE'`); n != 0 {
		t.Errorf("GENERATE jobs = %d, want 0", n)
	}
}

func TestInvalidCatalogFailsWithItsCodeAndEveryIssue(t *testing.T) {
	e := parsetest.New(t)
	e.Put("products.yaml", []byte(`products:
  - id: a
    names: [Alpha]
    compatibleWith: [nope]
  - id: a
    names: [Alpha]
`))
	ps := e.ScanParse()
	if ps.Claimed != 1 || ps.Failed != 1 || ps.Parsed != 0 {
		t.Fatalf("parse = %+v", ps)
	}
	v, _ := e.Current("products.yaml")
	if v.State != "PARSE_FAILED" || v.ErrCode != "CATALOG_INVALID" {
		t.Fatalf("version = %+v", v)
	}
	var warns []struct{ Code, Detail string }
	if err := json.Unmarshal([]byte(v.Warnings), &warns); err != nil || len(warns) != 3 {
		t.Fatalf("warnings = %s (%v), want the 3 issues (duplicate id, duplicate name, unknown product)", v.Warnings, err)
	}
	for _, w := range warns {
		if w.Code != "CATALOG_INVALID" || w.Detail == "" {
			t.Errorf("warning = %+v", w)
		}
	}
	if n := count(t, e, `SELECT count(*) FROM product_catalogs`); n != 0 {
		t.Errorf("stored catalogs = %d, want 0", n)
	}
}

func TestSecondCatalogIsAFailureAndTheRootStaysTheCatalog(t *testing.T) {
	e := parsetest.New(t)
	e.Put("products.yaml", []byte(catalogYAML))
	e.Put("brand/products.yaml", []byte(catalogYAML))
	e.Put("brand/Products.YAML", []byte(catalogYAML))
	ps := e.ScanParse()
	if ps.Claimed != 3 || ps.Parsed != 1 || ps.Failed != 2 {
		t.Fatalf("parse = %+v", ps)
	}
	if v, _ := e.Current("products.yaml"); v.State != "PARSED" {
		t.Errorf("root catalog = %+v", v)
	}
	for _, k := range []string{"brand/products.yaml", "brand/Products.YAML"} {
		v, ok := e.Current(k)
		if !ok || v.State != "PARSE_FAILED" || v.ErrCode != "CATALOG_MISPLACED" || !strings.Contains(v.Warnings, "root") {
			t.Errorf("%s = %+v", k, v)
		}
	}
	if n := count(t, e, `SELECT count(*) FROM product_catalogs`); n != 1 {
		t.Errorf("stored catalogs = %d, want only the root one", n)
	}
}

func TestEditedCatalogIsANewVersionWithItsOwnRow(t *testing.T) {
	e := parsetest.New(t)
	e.Put("products.yaml", []byte(catalogYAML))
	e.ScanParse()
	first, _ := e.Current("products.yaml")
	e.Put("products.yaml", []byte(strings.Replace(catalogYAML, "ZQ Ultra", "ZQ Ultra Max", 1)))
	ps := e.ScanParse()
	if ps.Parsed != 1 {
		t.Fatalf("parse = %+v", ps)
	}
	second, _ := e.Current("products.yaml")
	if second.ID == first.ID || second.No != first.No+1 || second.State != "PARSED" {
		t.Fatalf("versions = %+v then %+v", first, second)
	}
	if n := count(t, e, `SELECT count(*) FROM product_catalogs`); n != 2 {
		t.Errorf("stored catalogs = %d, want one per version (2)", n)
	}
	// An edit that breaks the file fails the new version; the old row stays,
	// but the current version has no catalog.
	e.Put("products.yaml", []byte("products: ["))
	e.ScanParse()
	third, _ := e.Current("products.yaml")
	if third.State != "PARSE_FAILED" || third.ErrCode != "CATALOG_INVALID" {
		t.Fatalf("broken edit = %+v", third)
	}
	if n := count(t, e, `SELECT count(*) FROM product_catalogs WHERE file_version_id = $1`, third.ID); n != 0 {
		t.Errorf("a broken version stored a catalog")
	}
}
