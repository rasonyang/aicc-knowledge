// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
	"github.com/rasonyang/aicc-knowledge/internal/s3store/s3test"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

func TestGenerateAndReviewFailFastNamingMissingConfigOrFlags(t *testing.T) {
	clearKB(t)
	code, _, stderr := invoke(t, "generate")
	for _, k := range []string{"KB_DATABASE_URL", "KB_LLM_BASE_URL", "KB_LLM_MODEL"} {
		if code != exitFailure || !strings.Contains(stderr, k) {
			t.Fatalf("generate = %d %q does not name %s", code, stderr, k)
		}
	}
	if strings.Contains(stderr, "KB_LLM_API_KEY") || strings.Contains(stderr, "KB_S3_BUCKET") {
		t.Errorf("generate names keys it does not need: %q", stderr)
	}
	if code, _, _ := invoke(t, "generate", "-version", "nope"); code != exitUsage {
		t.Errorf("generate -version nope = %d, want usage", code)
	}
	if code, _, _ := invoke(t, "generate", "stray"); code != exitUsage {
		t.Errorf("generate stray = %d, want usage", code)
	}
	for _, args := range [][]string{
		{"export-review"}, {"export-review", "-out", "x.csv"}, {"export-review", "-out", "x.xlsx", "-language", "FR"},
		{"import-review"}, {"import-review", "-in", "x.xlsx"}, {"import-review", "-reviewer", "a"},
	} {
		if code, _, _ := invoke(t, args...); code != exitUsage {
			t.Errorf("%v = %d, want usage", args, code)
		}
	}
	if code, _, stderr := invoke(t, "export-review", "-out", "x.xlsx"); code != exitFailure || !strings.Contains(stderr, "KB_DATABASE_URL") {
		t.Errorf("export-review without a database = %d %q", code, stderr)
	}
	if code, _, stderr := invoke(t, "import-review", "-in", "x.xlsx", "-reviewer", "a"); code != exitFailure || !strings.Contains(stderr, "KB_DATABASE_URL") {
		t.Errorf("import-review without a database = %d %q", code, stderr)
	}
}

// TestGenerateExportReviewImportCLI walks the whole M4 path through the
// subcommands: scan, parse, generate (scripted LLM), export-review, an edited
// workbook, import-review, then a source change and a stale re-import.
func TestGenerateExportReviewImportCLI(t *testing.T) {
	clearKB(t)
	env := s3test.New(t)
	srv := llmtest.New(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		heading := strings.TrimPrefix(strings.SplitN(c.User(), "\n", 2)[0], "Section heading: ")
		return llmtest.Reply(
			llmtest.Cand{Question: "What is the " + heading + " policy?", Answer: "The " + heading + " policy is in the handbook.", Language: "EN"},
			llmtest.Cand{Question: "How long is the " + heading + " period?", Answer: "It is 30 days.", Language: "EN"},
		), nil
	})
	t.Setenv("KB_DATABASE_URL", testdb.ScratchDSN(t, "cmdreview"))
	t.Setenv("KB_S3_ENDPOINT", env.Config.Endpoint)
	t.Setenv("KB_S3_BUCKET", env.Config.Bucket)
	t.Setenv("KB_S3_PREFIX", env.Config.Prefix)
	t.Setenv("KB_S3_ACCESS_KEY_ID", env.Config.AccessKeyID)
	t.Setenv("KB_S3_SECRET_ACCESS_KEY", env.Config.SecretAccessKey)
	t.Setenv("KB_LLM_BASE_URL", srv.URL+"/v1")
	t.Setenv("KB_LLM_MODEL", "fake-model")
	t.Setenv("KB_LLM_SEED", "1")

	env.Put("faq_en.docx", parsetest.Fixture(t, "docx/faq_en.docx"))
	if code, _, stderr := invoke(t, "scan"); code != exitOK {
		t.Fatalf("scan = %d %s", code, stderr)
	}
	if code, _, stderr := invoke(t, "parse"); code != exitOK {
		t.Fatalf("parse = %d %s", code, stderr)
	}
	code, out, stderr := invoke(t, "generate")
	if code != exitOK || !strings.HasPrefix(out, "claimed=1 generated=1 skipped=0 errors=0 sections=3 candidates=6 warnings=0 duplicates=0 ") ||
		!strings.Contains(out, "prompt_version=faq-v1 model=fake-model") {
		t.Fatalf("generate = %d %q %s", code, out, stderr)
	}
	// Nothing left to do; re-arming a done version skips it.
	if code, out, _ := invoke(t, "generate"); code != exitOK || !strings.HasPrefix(out, "claimed=0 generated=0 ") {
		t.Fatalf("second generate = %d %q", code, out)
	}

	dir := t.TempDir()
	book := filepath.Join(dir, "review.xlsx")
	if code, out, stderr := invoke(t, "export-review", "-out", book); code != exitOK || out != "exported=6 out="+book+"\n" {
		t.Fatalf("export-review = %d %q %s", code, out, stderr)
	}
	x, err := excelize.OpenFile(book)
	if err != nil {
		t.Fatal(err)
	}
	for r, action := range map[int]string{2: "APPROVE", 3: "REJECT", 4: "EDIT", 5: "APPROVE"} {
		_ = x.SetCellStr("Review", fmt.Sprintf("C%d", r), action)
	}
	_ = x.SetCellStr("Review", "G4", "It is thirty days.") // the edit
	_ = x.SetCellStr("Review", "E5", "A quietly changed question?")
	if err := x.SaveAs(book); err != nil {
		t.Fatal(err)
	}
	x.Close()

	code, out, _ = invoke(t, "import-review", "-in", book, "-reviewer", "alice")
	wantErr := "error row=5 id="
	if code != exitFailure || !strings.HasPrefix(out, "rows=6 approved=1 rejected=1 edited=1 skipped=2 errors=1\n") || !strings.Contains(out, wantErr) ||
		!strings.Contains(out, "code=EDIT_REQUIRES_EDIT_ACTION") {
		t.Fatalf("import-review = %d %q", code, out)
	}
	// The same file again: the applied rows are NOT_PENDING.
	code, out, _ = invoke(t, "import-review", "-in", book, "-reviewer", "alice")
	if code != exitFailure || !strings.HasPrefix(out, "rows=6 approved=0 rejected=0 edited=0 skipped=2 errors=4\n") || strings.Count(out, "code=NOT_PENDING") != 3 {
		t.Fatalf("second import-review = %d %q", code, out)
	}

	// A fresh export holds the 3 candidates still pending; the source then changes.
	book2 := filepath.Join(dir, "review2.xlsx")
	if code, out, _ := invoke(t, "export-review", "-out", book2, "-language", "en"); code != exitOK || !strings.HasPrefix(out, "exported=3 ") {
		t.Fatalf("export-review 2 = %d %q", code, out)
	}
	y, _ := excelize.OpenFile(book2)
	for r := 2; r <= 4; r++ {
		_ = y.SetCellStr("Review", fmt.Sprintf("C%d", r), "APPROVE")
	}
	if err := y.Save(); err != nil {
		t.Fatal(err)
	}
	y.Close()
	env.Put("faq_en.docx", parsetest.Fixture(t, "docx/faq_zh.docx"))
	if code, _, stderr := invoke(t, "scan"); code != exitOK {
		t.Fatalf("scan = %d %s", code, stderr)
	}
	code, out, _ = invoke(t, "import-review", "-in", book2, "-reviewer", "alice")
	if code != exitFailure || !strings.HasPrefix(out, "rows=3 approved=0 rejected=0 edited=0 skipped=0 errors=3\n") || strings.Count(out, "code=STALE") != 3 {
		t.Fatalf("stale import-review = %d %q", code, out)
	}
	// A file that is not a review export fails without touching anything.
	bad := filepath.Join(dir, "bad.xlsx")
	z := excelize.NewFile()
	_ = z.SaveAs(bad)
	z.Close()
	if code, _, stderr := invoke(t, "import-review", "-in", bad, "-reviewer", "alice"); code != exitFailure || !strings.Contains(stderr, "REVIEW_FILE_INVALID") {
		t.Errorf("import of a foreign workbook = %d %q", code, stderr)
	}
}
