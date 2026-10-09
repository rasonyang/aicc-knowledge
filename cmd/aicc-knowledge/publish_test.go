// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/llm/llmtest"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
	"github.com/rasonyang/aicc-knowledge/internal/s3store/s3test"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

func TestPublishRollbackEvalFailFastNamingMissingConfigOrFlags(t *testing.T) {
	clearKB(t)
	for _, args := range [][]string{
		{"publish"}, {"publish", "-language", "FR"}, {"publish", "-language", "EN", "stray"},
		{"rollback"}, {"rollback", "-language", "ALL"}, {"rollback", "-language", "EN", "-to", "not-a-uuid"},
		{"eval"}, {"eval", "-in", "q.csv", "-sweep", "-url", "http://x"}, {"eval", "-in", "q.csv", "-url", "http://x"},
		{"eval", "-in", "q.csv", "-language", "FR"},
	} {
		if code, _, stderr := invoke(t, args...); code != exitUsage || stderr == "" {
			t.Errorf("%v = %d %q, want usage", args, code, stderr)
		}
	}
	for _, name := range []string{"publish", "rollback"} {
		code, _, stderr := invoke(t, name, "-language", "EN")
		for _, k := range []string{"KB_DATABASE_URL", "KB_MEILI_URL", "KB_TEI_URL"} {
			if code != exitFailure || !strings.Contains(stderr, k) {
				t.Errorf("%s = %d %q does not name %s", name, code, stderr, k)
			}
		}
	}
	csv := filepath.Join(t.TempDir(), "q.csv")
	if err := os.WriteFile(csv, []byte("question,language,expectedIds\nq,EN,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := invoke(t, "eval", "-in", csv)
	if code != exitFailure || !strings.Contains(stderr, "KB_MEILI_URL") || !strings.Contains(stderr, "KB_TEI_URL") || strings.Contains(stderr, "KB_DATABASE_URL") {
		t.Errorf("eval = %d %q", code, stderr)
	}
	if code, _, stderr := invoke(t, "eval", "-in", filepath.Join(t.TempDir(), "missing.csv")); code != exitFailure || stderr == "" {
		t.Errorf("eval with a missing file = %d %q", code, stderr)
	}
}

var pubIDRe = regexp.MustCompile(`publication=([0-9a-f-]{36})`)

// TestOperatorWorkflowThroughTheCLI runs scan, parse, generate, export-review,
// import-review, publish, search (eval), a source change, publish, rollback and
// a roll forward through the subcommands, against the real services.
func TestOperatorWorkflowThroughTheCLI(t *testing.T) {
	clearKB(t)
	murl, mkey := testdb.MeiliURL(t)
	tei := testdb.TEIURL()
	if tei == "" {
		t.Skip("SKIPPED: set KB_TEST_TEI_URL to run this test against a real TEI")
	}
	env := s3test.New(t)
	srv := llmtest.New(t, func(c llmtest.Call) (string, *llmtest.Fail) {
		heading := strings.TrimPrefix(strings.SplitN(c.User(), "\n", 2)[0], "Section heading: ")
		if strings.ContainsAny(heading, "账单退款套餐") {
			return llmtest.Reply(llmtest.Cand{Question: "关于" + heading + "有什么规定？", Answer: "请参阅" + heading + "的规定。", Language: "ZH"}), nil
		}
		return llmtest.Reply(llmtest.Cand{Question: "What is the " + heading + " policy?", Answer: "The policy is in the handbook.", Language: "EN"}), nil
	})
	prefix := fmt.Sprintf("cli%d_faq_", time.Now().UnixNano())
	t.Setenv("KB_DATABASE_URL", testdb.ScratchDSN(t, "cmdpublish"))
	t.Setenv("KB_S3_ENDPOINT", env.Config.Endpoint)
	t.Setenv("KB_S3_BUCKET", env.Config.Bucket)
	t.Setenv("KB_S3_PREFIX", env.Config.Prefix)
	t.Setenv("KB_S3_ACCESS_KEY_ID", env.Config.AccessKeyID)
	t.Setenv("KB_S3_SECRET_ACCESS_KEY", env.Config.SecretAccessKey)
	t.Setenv("KB_LLM_BASE_URL", srv.URL+"/v1")
	t.Setenv("KB_LLM_MODEL", "fake-model")
	t.Setenv("KB_MEILI_URL", murl)
	t.Setenv("KB_MEILI_API_KEY", mkey)
	t.Setenv("KB_MEILI_INDEX_PREFIX", prefix)
	t.Setenv("KB_TEI_URL", tei)
	t.Setenv("KB_SEARCH_SCOPE_KEYS", "brand")
	t.Setenv("KB_S3_SCOPE_PATH_TEMPLATE", "{brand}")
	t.Cleanup(func() {
		mc, _ := meili.New(meili.Config{BaseURL: murl, APIKey: mkey})
		ctx := context.Background()
		uids, _ := mc.ListIndexUIDs(ctx)
		for _, u := range uids {
			if strings.HasPrefix(u, prefix) {
				if id, err := mc.DeleteIndex(ctx, u); err == nil {
					_, _ = mc.WaitTask(ctx, id)
				}
			}
		}
	})

	step := func(want int, args ...string) string {
		t.Helper()
		code, out, stderr := invoke(t, args...)
		if code != want {
			t.Fatalf("%v = %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, stderr)
		}
		return out
	}
	approveAll := func(name string) {
		t.Helper()
		book := filepath.Join(t.TempDir(), name)
		out := step(exitOK, "export-review", "-out", book)
		if strings.HasPrefix(out, "exported=0") {
			return
		}
		x, err := excelize.OpenFile(book)
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := x.GetRows("Review")
		for r := 2; r <= len(rows); r++ {
			_ = x.SetCellStr("Review", fmt.Sprintf("C%d", r), "APPROVE")
		}
		if err := x.SaveAs(book); err != nil {
			t.Fatal(err)
		}
		x.Close()
		step(exitOK, "import-review", "-in", book, "-reviewer", "alice")
	}

	// Two languages, the English one in a brand directory.
	env.Put("acme/faq_en.docx", parsetest.Fixture(t, "docx/faq_en.docx"))
	env.Put("faq_zh.docx", parsetest.Fixture(t, "docx/faq_zh.docx"))
	step(exitOK, "scan")
	step(exitOK, "parse")
	step(exitOK, "generate")

	// Nothing is approved yet: publish has nothing to do and creates nothing.
	if out := step(exitOK, "publish", "-language", "ALL"); strings.Count(out, "outcome=SKIPPED_EMPTY") != 2 {
		t.Fatalf("publish before approval = %q", out)
	}
	approveAll("review1.xlsx")

	out := step(exitOK, "publish", "-language", "ALL")
	if strings.Count(out, "outcome=LIVE") != 2 || !strings.Contains(out, "language=EN outcome=LIVE") || !strings.Contains(out, "items=3") {
		t.Fatalf("publish = %q", out)
	}
	pub1 := pubIDRe.FindStringSubmatch(strings.SplitN(out, "\n", 2)[0])[1]

	// eval over the live index, in process: the stored questions are found.
	csv := filepath.Join(t.TempDir(), "q.csv")
	_ = os.WriteFile(csv, []byte("question,language,expectedIds,scope\n"+
		"What is the Billing policy?,EN,,\n"+
		"What is the capital of France?,EN,,\n"), 0o600)
	js := step(exitOK, "eval", "-in", csv, "-json")
	var rep struct {
		Reports []struct {
			Language   string  `json:"language"`
			Unanswered int     `json:"unanswerable"`
			Precision  float64 `json:"noMatchPrecision"`
		} `json:"reports"`
	}
	if err := json.Unmarshal([]byte(js), &rep); err != nil || len(rep.Reports) != 1 || rep.Reports[0].Language != "EN" || rep.Reports[0].Unanswered != 2 {
		t.Fatalf("eval -json = %v\n%s", err, js)
	}
	if txt := step(exitOK, "eval", "-in", csv, "-language", "EN", "-sweep"); !strings.Contains(txt, "threshold sweep") || !strings.Contains(txt, "0.950") {
		t.Fatalf("eval -sweep = %s", txt)
	}

	// The English source changes; the new version is approved and published.
	env.Put("acme/faq_en.docx", parsetest.Fixture(t, "docx/stubs.docx"))
	step(exitOK, "scan")
	step(exitOK, "parse")
	step(exitOK, "generate")
	approveAll("review2.xlsx")
	out = step(exitOK, "publish", "-language", "EN")
	pub2 := pubIDRe.FindStringSubmatch(out)[1]
	if !strings.Contains(out, "superseded="+pub1) {
		t.Fatalf("publish 2 = %q", out)
	}

	// Rollback, then roll forward to the newest by id.
	if out := step(exitOK, "rollback", "-language", "EN"); !strings.Contains(out, "publication="+pub1) || !strings.Contains(out, "superseded="+pub2) || !strings.Contains(out, "rebuilt=false") {
		t.Fatalf("rollback = %q", out)
	}
	if out := step(exitOK, "rollback", "-language", "EN", "-to", pub2); !strings.Contains(out, "publication="+pub2) {
		t.Fatalf("roll forward = %q", out)
	}
	// Refusals exit 1 with a code.
	if out := step(exitFailure, "rollback", "-language", "EN", "-to", pub2); !strings.Contains(out, "code=ROLLBACK_TARGET_INVALID") {
		t.Fatalf("rollback to the live publication = %q", out)
	}
	if out := step(exitFailure, "rollback", "-language", "ZH"); !strings.Contains(out, "code=NO_ROLLBACK_TARGET") {
		t.Fatalf("rollback with one publication = %q", out)
	}
}
