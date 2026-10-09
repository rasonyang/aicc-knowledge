// SPDX-License-Identifier: Apache-2.0

// Package repocheck holds repository-wide gate tests that belong to no single
// package.
package repocheck

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const spdx = "SPDX-License-Identifier: Apache-2.0"

// generatedByTool reports output that carries a "generated" header instead:
// everything sqlc writes into internal/store/queries, which `sqlc diff`
// guards. oapi-codegen output is stamped by scripts/api-generate.sh, so it is
// not exempt.
func generatedByTool(rel string) bool {
	return strings.HasPrefix(rel, "internal/store/queries/")
}

func needsHeader(rel string) bool {
	base := filepath.Base(rel)
	switch {
	case base == "Makefile" || base == "Dockerfile":
		return true
	}
	switch filepath.Ext(rel) {
	case ".go", ".sql", ".sh", ".yml", ".yaml":
		return true
	}
	return false
}

func TestEverySourceFileCarriesTheSPDXHeader(t *testing.T) {
	root := "../.."
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case ".git", "bin", "docs/research", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !needsHeader(rel) || generatedByTool(rel) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		found := false
		for i := 0; i < 3 && sc.Scan(); i++ { // a shebang may come first
			if strings.Contains(sc.Text(), spdx) {
				found = true
			}
		}
		checked++
		if !found {
			t.Errorf("%s lacks %q in its first lines", rel, spdx)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 30 {
		t.Fatalf("checked only %d files, the walk is broken", checked)
	}
}
