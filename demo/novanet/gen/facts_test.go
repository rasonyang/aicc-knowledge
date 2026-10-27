// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

func genFacts(t *testing.T, today string) string {
	t.Helper()
	d, err := time.Parse(time.DateOnly, today)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeFacts(&env{out: dir, today: d}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "facts")
}

func importTable(t *testing.T, dir, name string) *xlsx.FactsResult {
	t.Helper()
	y, err := os.ReadFile(filepath.Join(dir, name+".facts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	mf, err := xlsx.ParseMappingFile(y)
	if err != nil {
		t.Fatal(err)
	}
	if len(mf.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(mf.Tables))
	}
	b, err := os.ReadFile(filepath.Join(dir, name+".xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := xlsx.ImportFactsBytes(b, &mf.Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPlansImport(t *testing.T) {
	dir := genFacts(t, "2026-10-27")
	res := importTable(t, dir, "plans")
	if len(res.Issues) != 0 {
		t.Fatalf("issues: %+v", res.Issues)
	}
	if want := len(plans) + 1; len(res.Rows) != want {
		t.Fatalf("rows = %d, want %d", len(res.Rows), want)
	}
	var fiber []xlsx.FactRow
	for _, r := range res.Rows {
		if r.Key["plan"] == "Fiber 500" {
			fiber = append(fiber, r)
		}
	}
	if len(fiber) != 2 {
		t.Fatalf("Fiber 500 rows = %d, want 2", len(fiber))
	}
	old, cur := fiber[0], fiber[1]
	if old.ValidFrom == nil || *old.ValidFrom != "2025-01-01" || old.ValidTo == nil || *old.ValidTo != "2026-10-27" {
		t.Errorf("old row validity = %v..%v", old.ValidFrom, old.ValidTo)
	}
	if cur.ValidFrom == nil || *cur.ValidFrom != "2026-10-27" || cur.ValidTo != nil {
		t.Errorf("new row validity = %v..%v", cur.ValidFrom, cur.ValidTo)
	}
	if old.Values["monthly_fee"].(interface{ String() string }).String() != "139" || cur.Values["monthly_fee"].(interface{ String() string }).String() != "129" {
		t.Errorf("fees = %v, %v", old.Values["monthly_fee"], cur.Values["monthly_fee"])
	}
}

func TestTopupsImport(t *testing.T) {
	res := importTable(t, genFacts(t, "2026-10-27"), "topups")
	if len(res.Issues) != 0 || len(res.Rows) != 3 {
		t.Fatalf("issues=%+v rows=%d, want none and 3", res.Issues, len(res.Rows))
	}
}

func TestRoamingMissingKey(t *testing.T) {
	res := importTable(t, genFacts(t, "2026-10-27"), "roaming_packs")
	if len(res.Issues) != 1 || res.Issues[0].Code != xlsx.CodeRequiredMissing {
		t.Fatalf("issues = %+v, want exactly one REQUIRED_MISSING", res.Issues)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("rows = %d, want 0 on a failed import", len(res.Rows))
	}
}

func TestFactsDeterministic(t *testing.T) {
	a, b := genFacts(t, "2026-10-27"), genFacts(t, "2026-10-27")
	ents, err := os.ReadDir(a)
	if err != nil || len(ents) != 6 {
		t.Fatalf("files = %d err=%v, want 6", len(ents), err)
	}
	for _, e := range ents {
		x, _ := os.ReadFile(filepath.Join(a, e.Name()))
		y, _ := os.ReadFile(filepath.Join(b, e.Name()))
		if string(x) != string(y) {
			t.Errorf("%s differs between runs", e.Name())
		}
	}
}
