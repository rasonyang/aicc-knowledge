// SPDX-License-Identifier: Apache-2.0

// Package parsetest wires a scratch PostgreSQL database, the real S3 server
// and the scan and parse workers together for tests, and gives them the
// committed parser fixtures. Nothing here is mocked.
package parsetest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/parse"
	"github.com/rasonyang/aicc-knowledge/internal/s3store/s3test"
	"github.com/rasonyang/aicc-knowledge/internal/scan"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

// Env is a migrated scratch database plus an S3 prefix of its own.
type Env struct {
	T       testing.TB
	Store   *store.Store
	S3      *s3test.Env
	Scanner *scan.Scanner
	Worker  *parse.Worker
}

// New skips the test when PostgreSQL or S3 is not configured.
func New(t testing.TB) *Env {
	t.Helper()
	ctx := context.Background()
	s3 := s3test.New(t)
	st, err := store.Open(ctx, testdb.ScratchDSN(t, "parse"), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const maxBytes = 64 << 20
	return &Env{
		T: t, Store: st, S3: s3,
		Scanner: &scan.Scanner{Store: st, S3: s3.Client, MaxObjectBytes: maxBytes},
		Worker:  &parse.Worker{Store: st, S3: s3.Client, MaxObjectBytes: maxBytes},
	}
}

// Scan runs one scan and fails the test on any error.
func (e *Env) Scan() scan.Summary {
	e.T.Helper()
	sum, err := e.Scanner.Run(context.Background())
	if err != nil || sum.Errors != 0 {
		e.T.Fatalf("scan: %v %+v", err, sum)
	}
	return sum
}

// Parse drains the PARSE queue and fails the test on a Run error.
func (e *Env) Parse() parse.Summary {
	e.T.Helper()
	sum, err := e.Worker.Run(context.Background())
	if err != nil {
		e.T.Fatalf("parse: %v %+v", err, sum)
	}
	return sum
}

// ScanParse is a scan followed by a parse.
func (e *Env) ScanParse() parse.Summary {
	e.T.Helper()
	e.Scan()
	return e.Parse()
}

// Put uploads data under key (relative to the test's prefix).
func (e *Env) Put(key string, data []byte) {
	e.T.Helper()
	e.S3.Put(key, data)
}

// PutFixture uploads a committed parser fixture ("docx/headings.docx",
// "xlsx/pricing.xlsx") under key.
func (e *Env) PutFixture(key, fixture string) {
	e.T.Helper()
	e.S3.Put(key, Fixture(e.T, fixture))
}

// ObjectKey returns the full bucket key of a test key.
func (e *Env) ObjectKey(key string) string { return e.S3.Prefix + key }

// Version is the current version of one object.
type Version struct {
	ID       string
	No       int
	State    string
	ErrCode  string
	Warnings string
}

// Current returns the current (not superseded) version of key; ok is false
// when there is none.
func (e *Env) Current(key string) (Version, bool) {
	e.T.Helper()
	var v Version
	err := e.Store.Pool.QueryRow(context.Background(), `
		SELECT v.id::text, v.version_no, v.state, COALESCE(v.parse_error_code, ''), v.parse_warnings::text
		FROM file_versions v JOIN source_files sf ON sf.id = v.source_file_id
		WHERE sf.object_key = $1 AND v.superseded_at IS NULL`, e.ObjectKey(key)).Scan(&v.ID, &v.No, &v.State, &v.ErrCode, &v.Warnings)
	if err != nil {
		return Version{}, false
	}
	return v, true
}

// Fixture reads a committed fixture: "docx/<file>" is internal/docx/testdata,
// "xlsx/<file>" is internal/xlsx/testdata.
func Fixture(t testing.TB, rel string) []byte {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	internal := filepath.Join(filepath.Dir(here), "..", "..")
	pkg, file, _ := strings.Cut(rel, "/")
	b, err := os.ReadFile(filepath.Join(internal, pkg, "testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// WrapMapping turns single-table mapping YAML (the xlsx package's
// pricing.mapping.yaml) into the `tables:` wrapper of a .facts.yaml file.
func WrapMapping(single string) []byte {
	var b strings.Builder
	b.WriteString("tables:\n")
	first := true
	for _, line := range strings.Split(strings.TrimSpace(single), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if first {
			b.WriteString("  - " + line + "\n")
			first = false
		} else {
			b.WriteString("    " + line + "\n")
		}
	}
	return []byte(b.String())
}

// PricingMapping is pricing.mapping.yaml in .facts.yaml form (table
// plan_prices over the Pricing sheet of pricing.xlsx).
func PricingMapping(t testing.TB) []byte {
	t.Helper()
	return WrapMapping(string(Fixture(t, "xlsx/pricing.mapping.yaml")))
}

// EditPricing returns pricing.xlsx with edit applied to its workbook.
func EditPricing(t testing.TB, edit func(f *excelize.File)) []byte {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(Fixture(t, "xlsx/pricing.xlsx")))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	edit(f)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
