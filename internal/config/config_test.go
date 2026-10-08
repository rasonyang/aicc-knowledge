// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestEnvExampleMatchesConfigKeys fails when .env.example lists a key the
// package does not read, or the package reads a key .env.example omits.
var keyName = regexp.MustCompile(`^KB_[A-Z0-9_]+$`)

func TestEnvExampleMatchesConfigKeys(t *testing.T) {
	inExample := map[string]bool{}
	f, err := os.Open("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	re := regexp.MustCompile(`^#(KB_[A-Z0-9_]+)=`)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := re.FindStringSubmatch(sc.Text()); m != nil {
			if inExample[m[1]] {
				t.Errorf("%s appears twice in .env.example", m[1])
			}
			inExample[m[1]] = true
		}
	}

	inCode := map[string]bool{}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "config.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && keyName.MatchString(s) {
			inCode[s] = true
		}
		return true
	})

	if len(inCode) < 20 {
		t.Fatalf("found only %d keys in config.go, the AST scan is broken", len(inCode))
	}
	for k := range inCode {
		if !inExample[k] {
			t.Errorf("%s is read by config.go but missing from .env.example", k)
		}
	}
	for k := range inExample {
		if !inCode[k] {
			t.Errorf("%s is in .env.example but never read by config.go", k)
		}
	}
	if len(inCode) != len(inExample) {
		t.Errorf("key counts differ: code %d, .env.example %d", len(inCode), len(inExample))
	}
}

func TestRequiredKeysAreTheDocumentedOnes(t *testing.T) {
	// The example must mention every key the required-when-used sets name.
	b, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"KB_DATABASE_URL", "KB_MEILI_URL", "KB_TEI_URL"} {
		if !strings.Contains(string(b), "#"+k+"=") {
			t.Errorf("%s missing from .env.example", k)
		}
	}
}

func clearKB(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "KB_") && !strings.HasPrefix(k, "KB_TEST_") {
			t.Setenv(k, "")
		}
	}
}

func TestLoadDefaultsHaveNoServiceEndpoints(t *testing.T) {
	clearKB(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"database": c.DatabaseURL, "meili": c.MeiliURL, "tei": c.TEIURL,
		"s3": c.S3Endpoint, "llm": c.LLMBaseURL, "otlp": c.OTLPEndpoint,
	} {
		if v != "" {
			t.Errorf("%s has an in-code default: %q", name, v)
		}
	}
	if err := c.RequireServe(); err == nil {
		t.Fatal("RequireServe passed with nothing configured")
	} else {
		for _, k := range []string{"KB_DATABASE_URL", "KB_MEILI_URL", "KB_TEI_URL"} {
			if !strings.Contains(err.Error(), k) {
				t.Errorf("RequireServe error does not name %s: %v", k, err)
			}
		}
	}
	if err := c.RequireDatabase(); err == nil || !strings.Contains(err.Error(), "KB_DATABASE_URL") {
		t.Fatalf("RequireDatabase: %v", err)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearKB(t)
	t.Setenv("KB_DATABASE_URL", "postgres://x")
	t.Setenv("KB_MEILI_URL", "http://meili:7700")
	t.Setenv("KB_TEI_URL", "http://tei:80")
	t.Setenv("KB_SEARCH_SCOPE_KEYS", "brand, channel,,")
	t.Setenv("KB_SEARCH_THRESHOLD_ZH", "0.8")
	t.Setenv("KB_S3_USE_PATH_STYLE", "false")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireServe(); err != nil {
		t.Fatal(err)
	}
	if got := c.SearchScopeKeys; len(got) != 2 || got[0] != "brand" || got[1] != "channel" {
		t.Errorf("scope keys = %v", got)
	}
	if c.SearchThresholdZH != 0.8 || c.SearchThresholdEN != 0.85 {
		t.Errorf("thresholds = %v %v", c.SearchThresholdEN, c.SearchThresholdZH)
	}
	if c.S3UsePathStyle {
		t.Error("S3UsePathStyle override ignored")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name, key, val, want string
	}{
		{"threshold at floor", "KB_SEARCH_THRESHOLD_EN", "0.5", "KB_SEARCH_THRESHOLD_EN"},
		{"threshold below floor", "KB_SEARCH_THRESHOLD_ZH", "0.3", "KB_SEARCH_THRESHOLD_ZH"},
		{"threshold above one", "KB_SEARCH_THRESHOLD_EN", "1.1", "KB_SEARCH_THRESHOLD_EN"},
		{"threshold not a number", "KB_SEARCH_THRESHOLD_EN", "high", "KB_SEARCH_THRESHOLD_EN"},
		{"bad env", "KB_ENV", "staging", "KB_ENV"},
		{"bad level", "KB_LOG_LEVEL", "loud", "KB_LOG_LEVEL"},
		{"zero conns", "KB_DATABASE_MAX_CONNS", "0", "KB_DATABASE_MAX_CONNS"},
		{"max timeout over ceiling", "KB_SEARCH_MAX_TIMEOUT_MS", "60001", "KB_SEARCH_MAX_TIMEOUT_MS"},
		{"max timeout under default", "KB_SEARCH_MAX_TIMEOUT_MS", "100", "KB_SEARCH_MAX_TIMEOUT_MS"},
		{"relative url", "KB_MEILI_URL", "meili:7700", "KB_MEILI_URL"},
		{"unknown time zone", "KB_TIMEZONE", "Mars/Olympus", "KB_TIMEZONE"},
		{"local is not an IANA name", "KB_TIMEZONE", "Local", "KB_TIMEZONE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearKB(t)
			t.Setenv(tc.key, tc.val)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load() error = %v, want it to name %s", err, tc.want)
			}
		})
	}
	t.Run("threshold of exactly one is valid", func(t *testing.T) {
		clearKB(t)
		t.Setenv("KB_SEARCH_THRESHOLD_EN", "1")
		if _, err := Load(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDotEnvDoesNotOverrideEnvironment(t *testing.T) {
	clearKB(t)
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/.env", []byte("KB_LOG_LEVEL=debug\nKB_ENV=\"prod\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("KB_LOG_LEVEL", "warn")
	t.Setenv("KB_ENV", "") // registered for restore, then truly unset
	if err := os.Unsetenv("KB_ENV"); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want the real environment to win", c.LogLevel)
	}
	if c.Env != "prod" {
		t.Errorf("Env = %q, want .env to fill an unset key", c.Env)
	}
}

func TestRequireScanNamesTheBucket(t *testing.T) {
	clearKB(t)
	t.Setenv("KB_DATABASE_URL", "postgres://x")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireScan(); err == nil || !strings.Contains(err.Error(), "KB_S3_BUCKET") || strings.Contains(err.Error(), "KB_DATABASE_URL") {
		t.Fatalf("RequireScan = %v, want it to name only KB_S3_BUCKET", err)
	}
	t.Setenv("KB_S3_BUCKET", "b")
	if c, err = Load(); err != nil || c.RequireScan() != nil {
		t.Fatalf("Load/RequireScan with a bucket: %v", err)
	}
	if c.S3MaxObjectBytes != 52428800 {
		t.Errorf("S3MaxObjectBytes default = %d", c.S3MaxObjectBytes)
	}
	t.Setenv("KB_S3_MAX_OBJECT_BYTES", "0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "KB_S3_MAX_OBJECT_BYTES") {
		t.Fatalf("zero cap accepted: %v", err)
	}
}

func TestTimezoneDefaultsToUTCAndResolves(t *testing.T) {
	clearKB(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Timezone != "UTC" || c.Location().String() != "UTC" {
		t.Errorf("default timezone = %q / %v", c.Timezone, c.Location())
	}
	t.Setenv("KB_TIMEZONE", "Asia/Shanghai")
	if c, err = Load(); err != nil {
		t.Fatal(err)
	}
	if _, off := time.Date(2026, 1, 1, 0, 0, 0, 0, c.Location()).Zone(); off != 8*3600 {
		t.Errorf("Asia/Shanghai offset = %d", off)
	}
}

func TestGenerateSettingsDefaultsOverridesAndRequirements(t *testing.T) {
	clearKB(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.LLMTimeout != 120*time.Second || c.LLMTemperature != 0 || c.LLMSeed != nil ||
		c.GenerateMaxAnswerCharsEN != 240 || c.GenerateMaxAnswerCharsZH != 90 {
		t.Errorf("defaults = %+v", c)
	}
	err = c.RequireGenerate()
	for _, k := range []string{"KB_DATABASE_URL", "KB_LLM_BASE_URL", "KB_LLM_MODEL"} {
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("RequireGenerate does not name %s: %v", k, err)
		}
	}
	if err != nil && (strings.Contains(err.Error(), "KB_LLM_API_KEY") || strings.Contains(err.Error(), "KB_S3_BUCKET")) {
		t.Errorf("RequireGenerate names optional or unrelated keys: %v", err)
	}

	t.Setenv("KB_DATABASE_URL", "postgres://x")
	t.Setenv("KB_LLM_BASE_URL", "http://llm:8080/v1")
	t.Setenv("KB_LLM_MODEL", "m")
	t.Setenv("KB_LLM_SEED", "42")
	t.Setenv("KB_LLM_TEMPERATURE", "0.2")
	t.Setenv("KB_LLM_TIMEOUT_MS", "5000")
	t.Setenv("KB_GENERATE_MAX_ANSWER_CHARS_ZH", "60")
	c, err = Load()
	if err != nil || c.RequireGenerate() != nil {
		t.Fatalf("Load = %v, require = %v", err, c.RequireGenerate())
	}
	if c.LLMSeed == nil || *c.LLMSeed != 42 || c.LLMTemperature != 0.2 || c.LLMTimeout != 5*time.Second || c.GenerateMaxAnswerCharsZH != 60 {
		t.Errorf("overrides = %+v seed %v", c, c.LLMSeed)
	}

	for key, val := range map[string]string{
		"KB_LLM_SEED": "abc", "KB_LLM_TEMPERATURE": "3", "KB_LLM_TIMEOUT_MS": "0", "KB_GENERATE_MAX_ANSWER_CHARS_EN": "0",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, val)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), key[:len("KB_LLM_")]) && !strings.Contains(err.Error(), "KB_GENERATE") {
				t.Errorf("%s=%s accepted: %v", key, val, err)
			}
		})
	}
}

func TestPublishSettingsDefaultsOverridesAndRequirements(t *testing.T) {
	clearKB(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SearchThresholdEN != 0.85 || c.SearchThresholdZH != 0.875 || c.SearchGenericMarginEN != 0 || c.SearchGenericMarginZH != 0.04 || c.ProductsRefreshSec != 30 || c.MeiliIndexPrefix != "faq_" || c.EmbeddingDimensions != 1024 || c.PublishRetainIndexes != 3 || c.S3ScopePathTemplate != "" || c.TEIBatchURL != "" || c.ScopePathKeys() != nil {
		t.Errorf("defaults = %+v", c)
	}
	err = c.RequirePublish()
	for _, k := range []string{"KB_DATABASE_URL", "KB_MEILI_URL", "KB_TEI_URL"} {
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("RequirePublish does not name %s: %v", k, err)
		}
	}
	if err := c.RequireEval(); err == nil || strings.Contains(err.Error(), "KB_DATABASE_URL") {
		t.Errorf("RequireEval = %v, want Meilisearch and TEI only", err)
	}

	t.Setenv("KB_TEI_URL", "http://tei:80")
	c, err = Load()
	if err != nil || c.TEIBatchURL != "http://tei:80" {
		t.Fatalf("batch URL does not default to KB_TEI_URL: %q, %v", c.TEIBatchURL, err)
	}
	t.Setenv("KB_TEI_BATCH_URL", "http://tei-batch:80")
	t.Setenv("KB_SEARCH_SCOPE_KEYS", "brand,channel")
	t.Setenv("KB_S3_SCOPE_PATH_TEMPLATE", "{brand}/{channel}")
	t.Setenv("KB_PUBLISH_RETAIN_INDEXES", "0")
	t.Setenv("KB_EMBEDDING_DIMENSIONS", "768")
	c, err = Load()
	if err != nil || c.TEIBatchURL != "http://tei-batch:80" || c.PublishRetainIndexes != 0 || c.EmbeddingDimensions != 768 ||
		strings.Join(c.ScopePathKeys(), ",") != "brand,channel" {
		t.Fatalf("overrides = %+v, %v", c, err)
	}
}

func TestScopePathTemplateIsValidatedAtStartup(t *testing.T) {
	allowed := []string{"brand", "channel"}
	for _, tc := range []struct {
		tmpl string
		want []string
		bad  bool
	}{
		{"", nil, false},
		{"{brand}", []string{"brand"}, false},
		{"/{brand}/{channel}/", []string{"brand", "channel"}, false},
		{"{channel}/{brand}", []string{"channel", "brand"}, false},
		{"{region}", nil, true},
		{"{brand}/{brand}", nil, true},
		{"{brand}/static", nil, true},
		{"prefix-{brand}", nil, true},
		{"{}", nil, true},
		{"{brand}//{channel}", nil, true},
	} {
		got, err := ParseScopePathTemplate(tc.tmpl, allowed)
		if (err != nil) != tc.bad || strings.Join(got, ",") != strings.Join(tc.want, ",") || len(got) != len(tc.want) {
			t.Errorf("%q: got %v, %v; want %v (bad=%v)", tc.tmpl, got, err, tc.want, tc.bad)
		}
	}
	clearKB(t)
	t.Setenv("KB_S3_SCOPE_PATH_TEMPLATE", "{brand}")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "KB_S3_SCOPE_PATH_TEMPLATE") {
		t.Errorf("a placeholder outside KB_SEARCH_SCOPE_KEYS was accepted: %v", err)
	}
	for k, v := range map[string]string{"KB_EMBEDDING_DIMENSIONS": "0", "KB_PUBLISH_RETAIN_INDEXES": "-1", "KB_MEILI_INDEX_PREFIX": "faq en"} {
		clearKB(t)
		t.Setenv(k, v)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s accepted: %v", k, v, err)
		}
	}
}

func TestProductGuardSettings(t *testing.T) {
	clearKB(t)
	t.Setenv("KB_SEARCH_GENERIC_MARGIN_ZH", "0.06")
	t.Setenv("KB_SEARCH_GENERIC_MARGIN_EN", "0.02")
	t.Setenv("KB_PRODUCTS_REFRESH_SEC", "5")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SearchGenericMarginZH != 0.06 || c.SearchGenericMarginEN != 0.02 || c.ProductsRefreshSec != 5 {
		t.Errorf("settings = %v %v %v", c.SearchGenericMarginZH, c.SearchGenericMarginEN, c.ProductsRefreshSec)
	}
	for key, bad := range map[string]string{
		"KB_SEARCH_GENERIC_MARGIN_ZH": "-0.01", "KB_SEARCH_GENERIC_MARGIN_EN": "0.5", "KB_PRODUCTS_REFRESH_SEC": "0",
	} {
		clearKB(t)
		t.Setenv(key, bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%s: err = %v, want it named", key, bad, err)
		}
	}
}
