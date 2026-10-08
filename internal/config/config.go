// SPDX-License-Identifier: Apache-2.0

// Package config loads the service configuration from KB_* environment
// variables. It is hand-rolled on purpose: .env.example is the registry of
// every key, and config_test.go fails when the file and this package diverge.
//
// An empty value means "unset". No service URL has a default in code: a
// command declares what it needs (RequireServe, RequireDatabase) and fails
// fast naming the missing keys.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config is the whole configuration of the service.
type Config struct {
	Env      string
	LogLevel string

	HTTPAddr string
	OpsAddr  string

	DatabaseURL      string
	DatabaseMaxConns int

	MeiliURL    string
	MeiliAPIKey string
	// MeiliIndexPrefix starts the live index uids: faq_ gives faq_en and faq_zh.
	MeiliIndexPrefix string
	TEIURL           string
	// TEIBatchURL is the TEI that embeds documents for publish and rollback.
	// It defaults to TEIURL; a separate instance keeps long offline batches
	// from queueing in front of query embeddings.
	TEIBatchURL string
	// EmbeddingDimensions is the vector size of the embedder (bge-m3: 1024).
	EmbeddingDimensions int

	S3Endpoint        string
	S3Region          string
	S3Bucket          string
	S3Prefix          string
	S3UsePathStyle    bool
	S3AccessKeyID     string
	S3SecretAccessKey string
	// S3MaxObjectBytes is the largest object that is parsed; the scan versions
	// a larger one as UNSUPPORTED/OBJECT_TOO_LARGE.
	S3MaxObjectBytes int64
	// S3ScopePathTemplate maps leading directories of an object key (below
	// S3Prefix) to scope keys, e.g. "{brand}/{channel}"; empty means no scope.
	S3ScopePathTemplate string

	// PublishRetainIndexes is how many SUPERSEDED publications keep their
	// Meilisearch index for a fast rollback.
	PublishRetainIndexes int

	SearchScopeKeys        []string
	SearchThresholdEN      float64
	SearchThresholdZH      float64
	SearchDefaultTimeoutMS int
	SearchMaxTimeoutMS     int

	// Timezone is the IANA zone whose current date is the default `at` of a
	// facts lookup.
	Timezone string

	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string
	// LLMTimeout bounds one HTTP attempt to the LLM.
	LLMTimeout     time.Duration
	LLMTemperature float64
	// LLMSeed is sent to the LLM when non-nil.
	LLMSeed *int64

	// GenerateMaxAnswerCharsEN and ZH bound a candidate's spoken answer, in
	// characters.
	GenerateMaxAnswerCharsEN int
	GenerateMaxAnswerCharsZH int

	OTLPEndpoint string
	ServiceName  string

	ReadyzTimeout time.Duration
}

// MaxTimeoutCeilingMS is the largest timeoutMs the contract allows. The
// configured maximum may not exceed it.
const MaxTimeoutCeilingMS = 60000

// Load reads .env (never overriding a real environment variable), then the
// environment, and validates what it read.
func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	var p reader
	c := Config{
		Env:      p.str("KB_ENV", "dev"),
		LogLevel: p.str("KB_LOG_LEVEL", "info"),

		HTTPAddr: p.str("KB_HTTP_ADDR", ":8080"),
		OpsAddr:  p.str("KB_OPS_ADDR", "127.0.0.1:9090"),

		DatabaseURL:      p.str("KB_DATABASE_URL", ""),
		DatabaseMaxConns: p.integer("KB_DATABASE_MAX_CONNS", 10),

		MeiliURL:    p.str("KB_MEILI_URL", ""),
		MeiliAPIKey: p.str("KB_MEILI_API_KEY", ""),

		MeiliIndexPrefix: p.str("KB_MEILI_INDEX_PREFIX", "faq_"),
		TEIURL:           p.str("KB_TEI_URL", ""),

		EmbeddingDimensions: p.integer("KB_EMBEDDING_DIMENSIONS", 1024),

		S3Endpoint:        p.str("KB_S3_ENDPOINT", ""),
		S3Region:          p.str("KB_S3_REGION", "us-east-1"),
		S3Bucket:          p.str("KB_S3_BUCKET", ""),
		S3Prefix:          p.str("KB_S3_PREFIX", ""),
		S3UsePathStyle:    p.boolean("KB_S3_USE_PATH_STYLE", true),
		S3AccessKeyID:     p.str("KB_S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey: p.str("KB_S3_SECRET_ACCESS_KEY", ""),
		S3MaxObjectBytes:  int64(p.integer("KB_S3_MAX_OBJECT_BYTES", 52428800)),

		S3ScopePathTemplate:  p.str("KB_S3_SCOPE_PATH_TEMPLATE", ""),
		PublishRetainIndexes: p.integer("KB_PUBLISH_RETAIN_INDEXES", 3),

		SearchScopeKeys:        splitList(p.str("KB_SEARCH_SCOPE_KEYS", "")),
		SearchThresholdEN:      p.float("KB_SEARCH_THRESHOLD_EN", 0.85),
		SearchThresholdZH:      p.float("KB_SEARCH_THRESHOLD_ZH", 0.85),
		SearchDefaultTimeoutMS: p.integer("KB_SEARCH_DEFAULT_TIMEOUT_MS", 2000),
		SearchMaxTimeoutMS:     p.integer("KB_SEARCH_MAX_TIMEOUT_MS", 5000),

		Timezone: p.str("KB_TIMEZONE", "UTC"),

		LLMBaseURL: p.str("KB_LLM_BASE_URL", ""),
		LLMAPIKey:  p.str("KB_LLM_API_KEY", ""),
		LLMModel:   p.str("KB_LLM_MODEL", ""),

		LLMTimeout:     time.Duration(p.integer("KB_LLM_TIMEOUT_MS", 120000)) * time.Millisecond,
		LLMTemperature: p.float("KB_LLM_TEMPERATURE", 0),
		LLMSeed:        p.optionalInt64("KB_LLM_SEED"),

		GenerateMaxAnswerCharsEN: p.integer("KB_GENERATE_MAX_ANSWER_CHARS_EN", 240),
		GenerateMaxAnswerCharsZH: p.integer("KB_GENERATE_MAX_ANSWER_CHARS_ZH", 90),

		OTLPEndpoint: p.str("KB_OTLP_ENDPOINT", ""),
		ServiceName:  p.str("KB_SERVICE_NAME", "aicc-knowledge"),

		ReadyzTimeout: time.Duration(p.integer("KB_READYZ_TIMEOUT_MS", 2000)) * time.Millisecond,
	}
	// KB_TEI_BATCH_URL falls back to KB_TEI_URL.
	c.TEIBatchURL = p.str("KB_TEI_BATCH_URL", c.TEIURL)
	if err := errors.Join(append(p.errs, c.validate()...)...); err != nil {
		return Config{}, err
	}
	return c, nil
}

// IsDev reports whether the process runs in dev mode (text logs).
func (c Config) IsDev() bool { return c.Env == "dev" }

// Threshold returns the NO_MATCH score threshold of a language code (EN, ZH).
func (c Config) Threshold(language string) (float64, bool) {
	switch language {
	case "EN":
		return c.SearchThresholdEN, true
	case "ZH":
		return c.SearchThresholdZH, true
	}
	return 0, false
}

func (c Config) validate() []error {
	var errs []error
	if c.Env != "dev" && c.Env != "prod" {
		errs = append(errs, fmt.Errorf("KB_ENV must be dev or prod, got %q", c.Env))
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("KB_LOG_LEVEL must be debug, info, warn or error, got %q", c.LogLevel))
	}
	if c.DatabaseMaxConns < 1 {
		errs = append(errs, errors.New("KB_DATABASE_MAX_CONNS must be >= 1"))
	}
	// A pure-vector score is (1+cosine)/2, so unrelated content scores 0.5.
	// A threshold at or below 0.5 would let everything through.
	for key, v := range map[string]float64{
		"KB_SEARCH_THRESHOLD_EN": c.SearchThresholdEN,
		"KB_SEARCH_THRESHOLD_ZH": c.SearchThresholdZH,
	} {
		if v <= 0.5 || v > 1 {
			errs = append(errs, fmt.Errorf("%s must be in (0.5, 1], got %v", key, v))
		}
	}
	if c.SearchDefaultTimeoutMS < 1 {
		errs = append(errs, errors.New("KB_SEARCH_DEFAULT_TIMEOUT_MS must be >= 1"))
	}
	if c.SearchMaxTimeoutMS < c.SearchDefaultTimeoutMS || c.SearchMaxTimeoutMS > MaxTimeoutCeilingMS {
		errs = append(errs, fmt.Errorf("KB_SEARCH_MAX_TIMEOUT_MS must be between KB_SEARCH_DEFAULT_TIMEOUT_MS and %d", MaxTimeoutCeilingMS))
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil || c.Timezone == "Local" {
		errs = append(errs, fmt.Errorf("KB_TIMEZONE must be an IANA time zone name such as UTC or Asia/Shanghai, got %q", c.Timezone))
	}
	if c.S3MaxObjectBytes < 1 {
		errs = append(errs, errors.New("KB_S3_MAX_OBJECT_BYTES must be >= 1"))
	}
	if c.LLMTimeout <= 0 {
		errs = append(errs, errors.New("KB_LLM_TIMEOUT_MS must be >= 1"))
	}
	if c.LLMTemperature < 0 || c.LLMTemperature > 2 {
		errs = append(errs, fmt.Errorf("KB_LLM_TEMPERATURE must be in [0, 2], got %v", c.LLMTemperature))
	}
	if c.GenerateMaxAnswerCharsEN < 1 || c.GenerateMaxAnswerCharsZH < 1 {
		errs = append(errs, errors.New("KB_GENERATE_MAX_ANSWER_CHARS_EN and KB_GENERATE_MAX_ANSWER_CHARS_ZH must be >= 1"))
	}
	if !indexPrefixRe.MatchString(c.MeiliIndexPrefix) {
		errs = append(errs, fmt.Errorf("KB_MEILI_INDEX_PREFIX must be 1 to 100 letters, digits, - or _, got %q", c.MeiliIndexPrefix))
	}
	if c.EmbeddingDimensions < 1 {
		errs = append(errs, errors.New("KB_EMBEDDING_DIMENSIONS must be >= 1"))
	}
	if c.PublishRetainIndexes < 0 {
		errs = append(errs, errors.New("KB_PUBLISH_RETAIN_INDEXES must be >= 0"))
	}
	if _, err := ParseScopePathTemplate(c.S3ScopePathTemplate, c.SearchScopeKeys); err != nil {
		errs = append(errs, fmt.Errorf("KB_S3_SCOPE_PATH_TEMPLATE: %w", err))
	}
	if c.ReadyzTimeout <= 0 {
		errs = append(errs, errors.New("KB_READYZ_TIMEOUT_MS must be >= 1"))
	}
	for key, v := range map[string]string{
		"KB_MEILI_URL": c.MeiliURL, "KB_TEI_URL": c.TEIURL, "KB_TEI_BATCH_URL": c.TEIBatchURL,
		"KB_S3_ENDPOINT": c.S3Endpoint, "KB_LLM_BASE_URL": c.LLMBaseURL,
	} {
		if v == "" {
			continue
		}
		if u, err := url.Parse(v); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s must be an absolute URL, got %q", key, v))
		}
	}
	return errs
}

var indexPrefixRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// ScopePathKeys returns the scope keys of KB_S3_SCOPE_PATH_TEMPLATE in path
// order (nil for an empty template). Load has already validated it.
func (c Config) ScopePathKeys() []string {
	keys, _ := ParseScopePathTemplate(c.S3ScopePathTemplate, c.SearchScopeKeys)
	return keys
}

// ParseScopePathTemplate splits a template such as "{brand}/{channel}" into
// its scope keys. Every segment must be exactly one {key}, every key must be
// in allowed (KB_SEARCH_SCOPE_KEYS) and appear once. An empty template yields
// no keys.
func ParseScopePathTemplate(tmpl string, allowed []string) ([]string, error) {
	tmpl = strings.Trim(strings.TrimSpace(tmpl), "/")
	if tmpl == "" {
		return nil, nil
	}
	var keys []string
	for _, seg := range strings.Split(tmpl, "/") {
		if len(seg) < 3 || seg[0] != '{' || seg[len(seg)-1] != '}' {
			return nil, fmt.Errorf("segment %q must be exactly one placeholder such as {brand}", seg)
		}
		k := seg[1 : len(seg)-1]
		if strings.ContainsAny(k, "{}") {
			return nil, fmt.Errorf("segment %q must be exactly one placeholder such as {brand}", seg)
		}
		if !slices.Contains(allowed, k) {
			return nil, fmt.Errorf("placeholder {%s} is not in KB_SEARCH_SCOPE_KEYS", k)
		}
		if slices.Contains(keys, k) {
			return nil, fmt.Errorf("placeholder {%s} appears twice", k)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// Location returns the zone named by KB_TIMEZONE (UTC if it cannot be loaded,
// which Load has already rejected).
func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// RequireDatabase fails when the database is not configured.
func (c Config) RequireDatabase() error {
	return requireKeys(map[string]string{"KB_DATABASE_URL": c.DatabaseURL})
}

// RequireServe fails when a dependency of `serve` is not configured.
func (c Config) RequireServe() error {
	return requireKeys(map[string]string{
		"KB_DATABASE_URL": c.DatabaseURL,
		"KB_MEILI_URL":    c.MeiliURL,
		"KB_TEI_URL":      c.TEIURL,
	})
}

// RequireScan fails when a dependency of `scan` is not configured. The S3
// endpoint is optional (empty means AWS) and so are the static credentials
// (empty means the AWS default credential chain).
func (c Config) RequireScan() error {
	return requireKeys(map[string]string{
		"KB_DATABASE_URL": c.DatabaseURL,
		"KB_S3_BUCKET":    c.S3Bucket,
	})
}

// RequireParse fails when a dependency of `parse` is not configured: the
// database and the bucket (the parser reads the objects the scan versioned).
func (c Config) RequireParse() error { return c.RequireScan() }

// RequireGenerate fails when a dependency of `generate` is not configured:
// the database (the sections are there) and the LLM endpoint and model. The
// API key is optional.
func (c Config) RequireGenerate() error {
	return requireKeys(map[string]string{
		"KB_DATABASE_URL": c.DatabaseURL,
		"KB_LLM_BASE_URL": c.LLMBaseURL,
		"KB_LLM_MODEL":    c.LLMModel,
	})
}

// RequirePublish fails when a dependency of `publish` and `rollback` is not
// configured: the database, Meilisearch and a TEI for documents.
func (c Config) RequirePublish() error {
	return requireKeys(map[string]string{
		"KB_DATABASE_URL": c.DatabaseURL,
		"KB_MEILI_URL":    c.MeiliURL,
		"KB_TEI_URL":      c.TEIBatchURL,
	})
}

// RequireEval fails when a dependency of an in-process `eval` is not
// configured: Meilisearch and TEI. The database is not needed.
func (c Config) RequireEval() error {
	return requireKeys(map[string]string{"KB_MEILI_URL": c.MeiliURL, "KB_TEI_URL": c.TEIURL})
}

func requireKeys(kv map[string]string) error {
	var missing []string
	for _, k := range []string{"KB_DATABASE_URL", "KB_MEILI_URL", "KB_TEI_URL", "KB_S3_BUCKET", "KB_LLM_BASE_URL", "KB_LLM_MODEL"} {
		if v, ok := kv[k]; ok && v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

// reader reads typed values and collects parse errors. Every KB_ key the
// package reads goes through one of its methods with a string literal, which
// is what the registry gate test looks for.
type reader struct{ errs []error }

func (p *reader) str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func (p *reader) integer(key string, def int) int {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be an integer, got %q", key, v))
		return def
	}
	return n
}

func (p *reader) float(key string, def float64) float64 {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be a number, got %q", key, v))
		return def
	}
	return f
}

// optionalInt64 returns nil when the key is unset.
func (p *reader) optionalInt64(key string) *int64 {
	v := p.str(key, "")
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be an integer, got %q", key, v))
		return nil
	}
	return &n
}

func (p *reader) boolean(key string, def bool) bool {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be true or false, got %q", key, v))
		return def
	}
	return b
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// loadDotEnv reads KEY=VALUE lines from path. A missing file is fine. A value
// already present in the environment wins.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
