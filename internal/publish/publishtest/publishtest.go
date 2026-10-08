// SPDX-License-Identifier: Apache-2.0

// Package publishtest wires a scratch PostgreSQL database, the real
// Meilisearch and the real TEI to a Publisher and a Searcher for tests, with
// an index namespace of its own that is emptied when the test ends. Nothing
// here is mocked.
package publishtest

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/embed"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/products"
	"github.com/rasonyang/aicc-knowledge/internal/publish"
	"github.com/rasonyang/aicc-knowledge/internal/search"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

// searchBudget is generous on purpose: the test packages run in parallel against
// one CPU TEI that serves requests in order, so a query can queue behind other
// packages' document batches (research caveat on TEI contention).
const searchBudget = 90 * time.Second

// ScopeKeys are the scope keys every test Env configures.
var ScopeKeys = []string{"brand", "channel"}

// Env is one test's world.
type Env struct {
	T         testing.TB
	Store     *store.Store
	Meili     *meili.Client
	Embed     *embed.Client
	Prefix    string
	Publisher *publish.Publisher
	Searcher  *search.Searcher
}

// NewStore returns a migrated scratch store.
func NewStore(t testing.TB) *store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.ScratchDSN(t, "publish"), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

// New builds an Env over a fresh scratch store. It skips the test when
// PostgreSQL, Meilisearch or TEI is not configured.
func New(t testing.TB) *Env { return NewOver(t, nil) }

// NewOver builds an Env over an existing migrated store (nil: a fresh one).
func NewOver(t testing.TB, st *store.Store) *Env {
	t.Helper()
	murl, mkey := testdb.MeiliURL(t)
	teiURL := testdb.TEIURL()
	if teiURL == "" {
		t.Skip("SKIPPED: set KB_TEST_TEI_URL to run this test against a real TEI")
	}
	if st == nil {
		st = NewStore(t)
	}
	mc, err := meili.New(meili.Config{BaseURL: murl, APIKey: mkey})
	if err != nil {
		t.Fatal(err)
	}
	ec, err := embed.New(embed.Config{BaseURL: teiURL, Dimensions: 1024, HTTPClient: &http.Client{Timeout: 3 * time.Minute}, Retry: embed.RetryPolicy{MaxAttempts: 3, Backoff: 200 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{T: t, Store: st, Meili: mc, Embed: ec, Prefix: fmt.Sprintf("t%d_faq_", time.Now().UnixNano())}
	e.Publisher = &publish.Publisher{
		Store: st, Meili: mc, Embedder: ec, Dimensions: 1024, ScopeKeys: ScopeKeys, ScopePathKeys: ScopeKeys,
		S3Prefix: "kb/", RetainIndexes: 3, IndexPrefix: e.Prefix,
	}
	e.Searcher = &search.Searcher{Meili: mc, Embedder: ec, ScopeKeys: ScopeKeys, ThresholdEN: 0.75, ThresholdZH: 0.75, IndexPrefix: e.Prefix}
	t.Cleanup(e.deleteIndexes)
	return e
}

func (e *Env) deleteIndexes() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, uid := range e.IndexUIDs() {
		if id, err := e.Meili.DeleteIndex(ctx, uid); err == nil {
			_, _ = e.Meili.WaitTask(ctx, id)
		}
	}
}

// IndexUIDs lists the indexes of this Env's namespace, sorted.
func (e *Env) IndexUIDs() []string {
	e.T.Helper()
	all, err := e.Meili.ListIndexUIDs(context.Background())
	if err != nil {
		e.T.Fatal(err)
	}
	var mine []string
	for _, u := range all {
		if strings.HasPrefix(u, e.Prefix) {
			mine = append(mine, u)
		}
	}
	slices.Sort(mine)
	return mine
}

// Live is the live uid of a language.
func (e *Env) Live(lang domain.Language) string { return e.Publisher.LiveUID(lang) }

func (e *Env) exec(sql string, args ...any) {
	e.T.Helper()
	if _, err := e.Store.Pool.Exec(context.Background(), sql, args...); err != nil {
		e.T.Fatalf("%v\n%s", err, sql)
	}
}

// Cand describes a candidate to insert.
type Cand struct {
	// Key is the S3 object key of the source file (default "kb/faq.docx").
	Key      string
	Language domain.Language
	Question string
	Alts     []string
	Answer   string
	// State defaults to APPROVED.
	State string
}

// EnsureVersion makes sure the source file has a current PARSED version and
// returns its id.
func (e *Env) EnsureVersion(key string) uuid.UUID {
	e.T.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := e.Store.Pool.QueryRow(ctx, `
		SELECT v.id FROM file_versions v JOIN source_files sf ON sf.id = v.source_file_id
		WHERE sf.object_key = $1 AND v.superseded_at IS NULL`, key).Scan(&id)
	if err == nil {
		return id
	}
	e.exec(`INSERT INTO source_files (bucket, object_key) VALUES ('b', $1) ON CONFLICT DO NOTHING`, key)
	return e.insertVersion(key, "PARSED")
}

func (e *Env) insertVersion(key, state string) uuid.UUID {
	e.T.Helper()
	var id uuid.UUID
	err := e.Store.Pool.QueryRow(context.Background(), `
		INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at, state)
		SELECT sf.id, COALESCE((SELECT max(version_no) FROM file_versions WHERE source_file_id = sf.id), 0) + 1,
		       sha256(convert_to(gen_random_uuid()::text, 'UTF8')), 1, 'e', now(), $2
		FROM source_files sf WHERE sf.object_key = $1 RETURNING id`, key, state).Scan(&id)
	if err != nil {
		e.T.Fatal(err)
	}
	return id
}

// AddCandidate inserts a candidate on the current version of c.Key and returns its id.
func (e *Env) AddCandidate(c Cand) uuid.UUID {
	e.T.Helper()
	if c.Key == "" {
		c.Key = "kb/faq.docx"
	}
	if c.State == "" {
		c.State = "APPROVED"
	}
	if c.Answer == "" {
		c.Answer = "Answer to: " + c.Question
	}
	if c.Alts == nil {
		c.Alts = []string{}
	}
	v := e.EnsureVersion(c.Key)
	var id uuid.UUID
	err := e.Store.Pool.QueryRow(context.Background(), `
		INSERT INTO candidates (file_version_id, language, question, alternate_questions, answer, source_ref, state, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, sha256(convert_to($3 || $5 || gen_random_uuid()::text, 'UTF8'))) RETURNING id`,
		v, string(c.Language), c.Question, c.Alts, c.Answer, c.Key+"#"+c.Question, c.State).Scan(&id)
	if err != nil {
		e.T.Fatal(err)
	}
	return id
}

// NewVersion supersedes the current version of key the way a scan does: the
// old version is superseded, its candidates become STALE, and a new PARSED
// version is current. Add its candidates with AddCandidate.
func (e *Env) NewVersion(key string) {
	e.T.Helper()
	old := e.EnsureVersion(key)
	e.exec(`UPDATE file_versions SET superseded_at = now() WHERE id = $1`, old)
	e.exec(`UPDATE candidates SET state = 'STALE' WHERE file_version_id = $1`, old)
	e.insertVersion(key, "PARSED")
}

// RemoveSource marks the current version REMOVED and its candidates STALE, as
// a scan does when the object disappears.
func (e *Env) RemoveSource(key string) {
	e.T.Helper()
	v := e.EnsureVersion(key)
	e.exec(`UPDATE file_versions SET state = 'REMOVED' WHERE id = $1`, v)
	e.exec(`UPDATE candidates SET state = 'STALE' WHERE file_version_id = $1`, v)
}

// Publish publishes a language and fails the test on an error.
func (e *Env) Publish(lang domain.Language) publish.Result {
	e.T.Helper()
	r, err := e.Publisher.Publish(context.Background(), lang, publish.PublishOptions{})
	if err != nil {
		e.T.Fatalf("publish %s: %v", lang, err)
	}
	return r
}

// Pub is a publication row.
type Pub struct {
	ID         uuid.UUID
	Language   string
	IndexUID   string
	State      string
	ItemCount  int
	ErrorCode  string
	ContentUID string // "" when NULL
}

// Pubs lists the publications of a language, oldest first.
func (e *Env) Pubs(lang domain.Language) []Pub {
	e.T.Helper()
	rows, err := e.Store.Pool.Query(context.Background(), `
		SELECT id, language, index_uid, state, item_count, COALESCE(error_code, ''), COALESCE(content_uid, '')
		FROM publications WHERE language = $1 ORDER BY created_at, id`, string(lang))
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var out []Pub
	for rows.Next() {
		var p Pub
		if err := rows.Scan(&p.ID, &p.Language, &p.IndexUID, &p.State, &p.ItemCount, &p.ErrorCode, &p.ContentUID); err != nil {
			e.T.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// Pub returns one publication by id.
func (e *Env) Pub(id uuid.UUID) Pub {
	e.T.Helper()
	for _, lang := range []domain.Language{domain.LanguageEN, domain.LanguageZH} {
		for _, p := range e.Pubs(lang) {
			if p.ID == id {
				return p
			}
		}
	}
	e.T.Fatalf("no publication %s", id)
	return Pub{}
}

// ItemIDs lists the candidate ids snapshotted into a publication, sorted.
func (e *Env) ItemIDs(pub uuid.UUID) []string {
	e.T.Helper()
	rows, err := e.Store.Pool.Query(context.Background(), `SELECT candidate_id::text FROM publication_items WHERE publication_id = $1 ORDER BY 1`, pub)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.T.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// DocIDs lists the document ids of an index, sorted.
func (e *Env) DocIDs(uid string) []string {
	e.T.Helper()
	ids, err := e.Meili.DocumentIDs(context.Background(), uid)
	if err != nil {
		e.T.Fatalf("document ids of %s: %v", uid, err)
	}
	slices.Sort(ids)
	return ids
}

// IDs turns uuids into sorted strings.
func IDs(ids ...uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	slices.Sort(out)
	return out
}

// AssertContentUIDs checks the invariant of the content_uid column for a
// language: every non-NULL content_uid names an index that holds exactly that
// publication's items, the LIVE publication's is the live uid, no two
// publications share one, and every index of the namespace (apart from the
// other language's live uid) is claimed by a publication.
func (e *Env) AssertContentUIDs(lang domain.Language) {
	e.T.Helper()
	claimed := map[string]uuid.UUID{}
	for _, p := range e.Pubs(lang) {
		if p.ContentUID == "" {
			if p.State == "LIVE" {
				e.T.Errorf("LIVE publication %s has no content_uid", p.ID)
			}
			continue
		}
		if other, dup := claimed[p.ContentUID]; dup {
			e.T.Errorf("publications %s and %s both claim %s", other, p.ID, p.ContentUID)
		}
		claimed[p.ContentUID] = p.ID
		if p.State == "LIVE" && p.ContentUID != e.Live(lang) {
			e.T.Errorf("LIVE publication %s content_uid = %s, want %s", p.ID, p.ContentUID, e.Live(lang))
		}
		if p.State != "LIVE" && p.State != "SUPERSEDED" {
			e.T.Errorf("%s publication %s has content_uid %s", p.State, p.ID, p.ContentUID)
		}
		got, want := e.DocIDs(p.ContentUID), e.ItemIDs(p.ID)
		if !slices.Equal(got, want) || len(got) != len(want) || len(got) != p.ItemCount {
			e.T.Errorf("index %s (publication %s %s) holds %v, want its items %v (item_count %d)", p.ContentUID, p.ID, p.State, got, want, p.ItemCount)
		}
	}
	prefix := e.Live(lang)
	for _, uid := range e.IndexUIDs() {
		if !strings.HasPrefix(uid, prefix) {
			continue
		}
		if _, ok := claimed[uid]; !ok {
			e.T.Errorf("index %s exists but no publication claims it", uid)
		}
	}
}

// Search runs a search through the Env's Searcher.
func (e *Env) Search(lang domain.Language, query string, scope map[string]string, topK int) (search.Response, error) {
	e.T.Helper()
	return e.Searcher.Search(context.Background(), search.Request{Query: query, Language: lang, Scope: scope, TopK: topK, Timeout: searchBudget})
}

// SearchIDs returns the ids of the HIT items of a search, in rank order.
func (e *Env) SearchIDs(lang domain.Language, query string, scope map[string]string, topK int) []string {
	e.T.Helper()
	r, err := e.Search(lang, query, scope, topK)
	if err != nil {
		e.T.Fatal(err)
	}
	out := []string{}
	for _, it := range r.Items {
		out = append(out, it.ID)
	}
	return out
}

// CatalogKey is the object key of the catalog the Env's publisher expects.
const CatalogKey = "kb/products.yaml"

// SetCatalog makes yaml the parsed catalog of a new current version of
// CatalogKey, as scan and parse would, and returns the catalog row id. It
// fails the test when yaml does not validate.
func (e *Env) SetCatalog(yaml string) uuid.UUID {
	e.T.Helper()
	cat, err := products.Parse([]byte(yaml))
	if err != nil {
		e.T.Fatal(err)
	}
	raw, err := cat.MarshalJSON()
	if err != nil {
		e.T.Fatal(err)
	}
	e.NewVersion(CatalogKey)
	v := e.EnsureVersion(CatalogKey)
	var id uuid.UUID
	if err := e.Store.Pool.QueryRow(context.Background(),
		`INSERT INTO product_catalogs (file_version_id, products) VALUES ($1, $2) RETURNING id`, v, raw).Scan(&id); err != nil {
		e.T.Fatal(err)
	}
	return id
}

// BreakCatalog makes the current catalog version PARSE_FAILED (CATALOG_INVALID),
// as a bad edit of the file would.
func (e *Env) BreakCatalog() {
	e.T.Helper()
	e.NewVersion(CatalogKey)
	e.exec(`UPDATE file_versions SET state = 'PARSE_FAILED', parse_error_code = 'CATALOG_INVALID' WHERE id = $1`, e.EnsureVersion(CatalogKey))
}

// UseCatalogs gives the Env's Searcher a catalog cache over the Env's store
// and loads it. Call Refresh on the returned cache after a publish or rollback.
func (e *Env) UseCatalogs() *search.CatalogCache {
	e.T.Helper()
	c := &search.CatalogCache{Queries: e.Store.Queries}
	if err := c.Refresh(context.Background()); err != nil {
		e.T.Fatal(err)
	}
	e.Searcher.Catalogs = c
	return c
}

// ItemProducts returns the products snapshotted per candidate of a publication.
func (e *Env) ItemProducts(pub uuid.UUID) map[string][]string {
	e.T.Helper()
	rows, err := e.Store.Pool.Query(context.Background(), `SELECT candidate_id::text, products FROM publication_items WHERE publication_id = $1`, pub)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id string
		var ps []string
		if err := rows.Scan(&id, &ps); err != nil {
			e.T.Fatal(err)
		}
		out[id] = ps
	}
	return out
}

// PubCatalog returns the catalog id recorded on a publication (uuid.Nil: none).
func (e *Env) PubCatalog(pub uuid.UUID) uuid.UUID {
	e.T.Helper()
	var id *uuid.UUID
	if err := e.Store.Pool.QueryRow(context.Background(), `SELECT catalog_id FROM publications WHERE id = $1`, pub).Scan(&id); err != nil {
		e.T.Fatal(err)
	}
	if id == nil {
		return uuid.Nil
	}
	return *id
}
