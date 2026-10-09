// SPDX-License-Identifier: Apache-2.0

// Package publish builds a versioned Meilisearch index from approved
// candidates, swaps it in atomically, and rolls back to an earlier publication.
//
// Invariants the code is organised around:
//
//   - Nothing unreviewed is ever indexed. A publication is built from the
//     snapshot rows in publication_items, which are read from
//     ListPublishableCandidates (APPROVED candidates of current, non-removed
//     file versions) in one REPEATABLE READ transaction.
//   - The live uid (faq_en, faq_zh) is only ever written by a swap of a fully
//     built, verified index. A failure before the swap deletes the staging
//     index and leaves the live index and the LIVE row untouched.
//   - The LIVE row flips only after the swap task succeeded, in one
//     transaction that also records, for both publications, which Meilisearch
//     uid now holds their content (publications.content_uid). If that
//     transaction fails, the swap is undone.
//   - One publish or rollback per language at a time (PostgreSQL advisory lock).
//   - Product tags. When the root products.yaml has a parsed catalog, the
//     publication records its catalog id, and every item records the products
//     its question names (else those its source file name names, else none:
//     generic). The tags are copied into the index documents and rebuilt from
//     publication_items on rollback. A catalog file that exists but did not
//     parse refuses the publish (CATALOG_UNAVAILABLE).
//
// A crash between the swap and the commit leaves a BUILDING row and a live
// index that is ahead of the database. The next publish marks the row FAILED
// (PUBLISH_ABANDONED), and rollback verifies the document ids of a retained
// index against publication_items before trusting content_uid, so a wrong
// claim leads to a rebuild, never to wrong content.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/products"
	"github.com/rasonyang/aicc-knowledge/internal/scan"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// Code is a coded failure. It is stored in publications.error_code.
type Code string

const (
	CodeInProgress         Code  = "PUBLISH_IN_PROGRESS"
	CodeEmptyRefused       Code  = "PUBLISH_EMPTY_REFUSED"
	CodeDimensionsMismatch Code  = "DIMENSIONS_MISMATCH"
	CodeEmbeddingFailed    Code  = "EMBEDDING_FAILED"
	CodeIndexBuildFailed   Code  = "INDEX_BUILD_FAILED"
	CodeIndexVerifyFailed  Code  = "INDEX_VERIFY_FAILED"
	CodeSwapFailed         Code  = "SWAP_FAILED"
	CodeStateCommitFailed  Code  = "STATE_COMMIT_FAILED"
	CodeAbandoned          Code  = "PUBLISH_ABANDONED"
	CodeNoLivePublication  Code  = "NO_LIVE_PUBLICATION"
	CodeNoRollbackTarget   Code  = "NO_ROLLBACK_TARGET"
	CodeRollbackTargetBad  Code  = "ROLLBACK_TARGET_INVALID"
	CodeDatabase           Code  = "DATABASE_ERROR"
	CodeHookFailed         Code  = "HOOK_FAILED"
	CodeCatalogUnavailable Code  = "CATALOG_UNAVAILABLE"
	defaultEmbedChunk            = 8
	indexAddBatch                = 200
	staleCleanupTimeout          = 30 * time.Second
	maxErrorCodeLen              = 64
	stagingIDChars               = 12
	defaultIndexPrefix           = "faq_"
	lockKeyBase            int64 = 0x50554200 // "PUB" + language offset
)

// Error is the only error type Publish and Rollback return.
type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// CodeOf returns the code of err, or "" when err is not an *Error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func fail(code Code, msg string, err error) *Error { return &Error{Code: code, Message: msg, Err: err} }

// Embedder embeds documents. *embed.Client satisfies it; tests wrap it.
type Embedder interface {
	// EmbedBatch returns one vector per text, in order, with retries.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
}

// Hooks let tests inject a failure at a precise point. They are nil in
// production.
type Hooks struct {
	// BeforeSwap runs after the staging index is built and verified.
	BeforeSwap func(ctx context.Context, stagingUID string) error
	// AfterSwap runs after the swap task succeeded, before the database commit.
	AfterSwap func(ctx context.Context) error
}

// Publisher publishes and rolls back. Fields without a comment are required.
type Publisher struct {
	Store    *store.Store
	Meili    *meili.Client
	Embedder Embedder
	// Dimensions is the vector size of the indexes; the embedder must agree.
	Dimensions int
	// ScopeKeys are all configured scope keys; they become filterable.
	ScopeKeys []string
	// ScopePathKeys and S3Prefix derive a document's scope from its source key.
	ScopePathKeys []string
	S3Prefix      string
	// RetainIndexes: SUPERSEDED publications that keep their index.
	RetainIndexes int
	// IndexPrefix defaults to "faq_".
	IndexPrefix string
	// EmbedChunk is the number of texts per TEI request on the publish path
	// (default 8): small and serial, so a long publish does not starve queries.
	EmbedChunk int
	Metrics    *obs.Metrics
	Log        *slog.Logger
	Hooks      Hooks
}

func (p *Publisher) prefix() string {
	if p.IndexPrefix == "" {
		return defaultIndexPrefix
	}
	return p.IndexPrefix
}

func (p *Publisher) log() *slog.Logger {
	if p.Log == nil {
		return slog.Default()
	}
	return p.Log
}

// LiveUID is the live index uid of a language.
func (p *Publisher) LiveUID(lang domain.Language) string {
	return meili.LiveUID(p.prefix(), string(lang))
}

func (p *Publisher) stagingUID(lang domain.Language, id uuid.UUID, kind string) string {
	hex := strings.ReplaceAll(id.String(), "-", "")
	return p.LiveUID(lang) + "_" + kind + hex[len(hex)-stagingIDChars:]
}

// PublishOptions are the flags of `publish`.
type PublishOptions struct {
	// AllowEmpty permits publishing an empty index over a live one that has
	// documents, e.g. after every source file was deleted.
	AllowEmpty bool
}

// Outcome is the result of a publish run that did not fail.
type Outcome string

const (
	OutcomeLive         Outcome = "LIVE"
	OutcomeSkippedEmpty Outcome = "SKIPPED_EMPTY"
)

// Result describes a successful Publish.
type Result struct {
	Language      domain.Language
	Outcome       Outcome
	PublicationID uuid.UUID
	Items         int
	// Superseded is the publication that was LIVE before, when there was one.
	Superseded *uuid.UUID
	// Pruned lists publications whose retained index retention deleted.
	Pruned   []uuid.UUID
	Duration time.Duration
}

func langOffset(lang domain.Language) int64 {
	if lang == domain.LanguageZH {
		return 2
	}
	return 1
}

// lock takes the per-language advisory lock on a dedicated connection and
// returns the function that releases it. Publish and Rollback share it.
func (p *Publisher) lock(ctx context.Context, lang domain.Language) (func(), error) {
	conn, err := p.Store.Pool.Acquire(ctx)
	if err != nil {
		return nil, fail(CodeDatabase, "acquire a connection for the publish lock", err)
	}
	key := lockKeyBase + langOffset(lang)
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		conn.Release()
		return nil, fail(CodeDatabase, "take the publish lock", err)
	}
	if !ok {
		conn.Release()
		return nil, fail(CodeInProgress, "another publish or rollback of "+string(lang)+" is running", nil)
	}
	return func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}, nil
}

// ScopeFromObjectKey derives a document's scope from the S3 object key: the
// key is taken relative to prefix, and its leading directories are matched to
// keys in order. A key with fewer directories than the template leaves the
// remaining scope keys unset (global). A key outside the prefix has no scope.
func ScopeFromObjectKey(keys []string, prefix, objectKey string) map[string]string {
	scope := map[string]string{}
	if len(keys) == 0 || !strings.HasPrefix(objectKey, prefix) {
		return scope
	}
	rel := strings.TrimLeft(strings.TrimPrefix(objectKey, prefix), "/")
	segs := strings.Split(rel, "/")
	dirs := segs[:len(segs)-1]
	for i, k := range keys {
		if i >= len(dirs) {
			break
		}
		if dirs[i] != "" {
			scope[k] = dirs[i]
		}
	}
	return scope
}

// Publish builds the language's approved candidates into a new index and
// swaps it in. On any failure the live index and the LIVE row are untouched
// and the publication (when one was created) is FAILED with its error code.
func (p *Publisher) Publish(ctx context.Context, lang domain.Language, opts PublishOptions) (Result, error) {
	start := time.Now()
	res := Result{Language: lang}
	unlock, err := p.lock(ctx, lang)
	if err != nil {
		return res, err
	}
	defer unlock()
	res, err = p.publish(ctx, lang, opts)
	res.Duration = time.Since(start)
	if p.Metrics != nil {
		outcome := string(res.Outcome)
		switch {
		case CodeOf(err) == CodeEmptyRefused:
			outcome = "REFUSED_EMPTY"
		case err != nil:
			outcome = "FAILED"
		}
		p.Metrics.ObservePublish(ctx, string(lang), outcome, res.Duration)
	}
	return res, err
}

func (p *Publisher) publish(ctx context.Context, lang domain.Language, opts PublishOptions) (Result, error) {
	res := Result{Language: lang}
	if got := p.Embedder.Dimensions(); got != p.Dimensions {
		return res, fail(CodeDimensionsMismatch, fmt.Sprintf("the embedder produces %d dimensions, the index would be built for %d", got, p.Dimensions), nil)
	}
	if err := p.abandonStale(ctx, lang); err != nil {
		return res, err
	}

	pub, items, prev, empty, err := p.snapshot(ctx, lang, opts)
	if err != nil {
		return res, err
	}
	if empty {
		res.Outcome = OutcomeSkippedEmpty
		return res, nil
	}
	res.PublicationID, res.Items = pub.ID, len(items)
	staging := pub.IndexUid
	live := p.LiveUID(lang)

	failWith := func(code Code, msg string, cause error) (Result, error) {
		e := fail(code, msg, cause)
		p.markFailed(pub.ID, staging, e)
		return res, e
	}

	if err := p.build(ctx, staging, pub.ID, items); err != nil {
		var pe *Error
		if errors.As(err, &pe) {
			return failWith(pe.Code, pe.Message, pe.Err)
		}
		return failWith(CodeIndexBuildFailed, "build the index", err)
	}
	if h := p.Hooks.BeforeSwap; h != nil {
		if err := h(ctx, staging); err != nil {
			return failWith(CodeHookFailed, "BeforeSwap hook", err)
		}
	}

	displaced, err := p.swapIn(ctx, staging, live)
	if err != nil {
		return failWith(CodeSwapFailed, "swap the staging index with the live index", err)
	}
	if h := p.Hooks.AfterSwap; h != nil {
		if err := h(ctx); err != nil {
			p.undoSwap(staging, live, displaced)
			return failWith(CodeStateCommitFailed, "AfterSwap hook", err)
		}
	}

	superseded, err := p.commitPublish(ctx, lang, pub.ID, prev, displaced)
	if err != nil {
		p.undoSwap(staging, live, displaced)
		return failWith(CodeStateCommitFailed, "record the publication as LIVE", err)
	}
	res.Outcome, res.Superseded = OutcomeLive, superseded
	if prev == nil && displaced != "" {
		// The live uid held content no publication owns. It sits in the staging
		// uid now; nothing refers to it.
		p.dropIndex(displaced)
	}
	res.Pruned = p.retain(ctx, lang)
	p.log().Info("published", "language", lang, "publication", pub.ID, "items", len(items), "liveUid", live)
	return res, nil
}

// snapshot creates the BUILDING publication and copies the approved candidates
// into publication_items in one REPEATABLE READ transaction. empty is true when
// there is nothing to publish and nothing live to empty.
func (p *Publisher) snapshot(ctx context.Context, lang domain.Language, opts PublishOptions) (pub queries.Publication, items []queries.PublicationItem, prev *queries.Publication, empty bool, err error) {
	tx, err := p.Store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return pub, nil, nil, false, fail(CodeDatabase, "begin the snapshot transaction", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	q := p.Store.Queries.WithTx(tx)

	cands, err := q.ListPublishableCandidates(ctx, string(lang))
	if err != nil {
		return pub, nil, nil, false, fail(CodeDatabase, "read the approved candidates", err)
	}
	if lv, err := q.GetLivePublication(ctx, string(lang)); err == nil {
		prev = &lv
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return pub, nil, nil, false, fail(CodeDatabase, "read the live publication", err)
	}
	cat, catalogID, err := p.currentCatalog(ctx, q)
	if err != nil {
		return pub, nil, nil, false, err
	}
	if len(cands) == 0 && !opts.AllowEmpty {
		if prev != nil && prev.ItemCount > 0 {
			return pub, nil, prev, false, fail(CodeEmptyRefused, fmt.Sprintf(
				"no approved candidate is left for %s but %d are live; pass --allow-empty to publish an empty index", lang, prev.ItemCount), nil)
		}
		return pub, nil, prev, true, nil
	}

	id, err := uuid.NewV7()
	if err != nil {
		return pub, nil, nil, false, fail(CodeDatabase, "generate a publication id", err)
	}
	staging := p.stagingUID(lang, id, "")
	if err := q.InsertPublication(ctx, queries.InsertPublicationParams{ID: id, Language: string(lang), IndexUid: staging, CatalogID: catalogID}); err != nil {
		return pub, nil, nil, false, fail(CodeDatabase, "insert the publication", err)
	}
	items = make([]queries.PublicationItem, 0, len(cands))
	for _, c := range cands {
		scopeJSON, err := json.Marshal(ScopeFromObjectKey(p.ScopePathKeys, p.S3Prefix, c.ObjectKey))
		if err != nil {
			return pub, nil, nil, false, fail(CodeDatabase, "encode a scope", err)
		}
		prods := []string{}
		if cat != nil {
			prods = append(prods, cat.DocumentProducts(c.Question, c.AlternateQuestions, c.ObjectKey)...)
		}
		it := queries.PublicationItem{PublicationID: id, CandidateID: c.ID, ContentHash: c.ContentHash, Question: c.Question,
			AlternateQuestions: c.AlternateQuestions, Answer: c.Answer, SourceRef: c.SourceRef, Scope: scopeJSON, Products: prods}
		if err := q.InsertPublicationItem(ctx, queries.InsertPublicationItemParams{PublicationID: id, CandidateID: c.ID,
			ContentHash: c.ContentHash, Question: c.Question, AlternateQuestions: c.AlternateQuestions, Answer: c.Answer,
			SourceRef: c.SourceRef, Scope: scopeJSON, Products: prods}); err != nil {
			return pub, nil, nil, false, fail(CodeDatabase, "insert a publication item", err)
		}
		items = append(items, it)
	}
	if err := q.SetPublicationItemCount(ctx, queries.SetPublicationItemCountParams{ID: id, ItemCount: int32(len(items))}); err != nil {
		return pub, nil, nil, false, fail(CodeDatabase, "set the item count", err)
	}
	if pub, err = q.GetPublication(ctx, id); err != nil {
		return pub, nil, nil, false, fail(CodeDatabase, "read the publication back", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return pub, nil, nil, false, fail(CodeDatabase, "commit the snapshot", err)
	}
	return pub, items, prev, false, nil
}

// currentCatalog returns the catalog a publication built now is tagged with:
// the stored catalog of the current version of the root products.yaml. No such
// object means no catalog (nil, nil: the search guard is off). An object whose
// current version is not PARSED (still DISCOVERED, PARSE_FAILED, too large) is
// CATALOG_UNAVAILABLE: publishing then would silently publish without product
// tags, so the operator must fix the file or remove it first.
func (p *Publisher) currentCatalog(ctx context.Context, q *queries.Queries) (*products.Catalog, *uuid.UUID, error) {
	rows, err := q.ListCurrentCatalogSources(ctx)
	if err != nil {
		return nil, nil, fail(CodeDatabase, "read the product catalog", err)
	}
	for _, r := range rows {
		if !scan.IsCatalogRoot(p.S3Prefix, r.ObjectKey) {
			continue
		}
		if r.CatalogID == nil || r.State != string(domain.FileVersionParsed) {
			code := ""
			if r.ParseErrorCode != nil {
				code = *r.ParseErrorCode
			}
			return nil, nil, fail(CodeCatalogUnavailable, fmt.Sprintf(
				"%s is %s %s; fix it (and parse), or remove it, before publishing", r.ObjectKey, r.State, code), nil)
		}
		cat, err := products.FromJSON(r.Products)
		if err != nil {
			return nil, nil, fail(CodeCatalogUnavailable, "the stored catalog of "+r.ObjectKey+" does not validate", err)
		}
		return cat, r.CatalogID, nil
	}
	return nil, nil, nil
}

// phrasings returns the distinct, non-empty question texts of an item: the
// question first, then its alternates.
func phrasings(it queries.PublicationItem) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append([]string{it.Question}, it.AlternateQuestions...) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// build creates uid, applies the settings, indexes the items (one vector per
// phrasing of each question), waits for EVERY task (research caveat C6) and
// verifies the index holds exactly the items.
func (p *Publisher) build(ctx context.Context, uid string, pubID uuid.UUID, items []queries.PublicationItem) error {
	// Embed first: a TEI problem should not leave an index behind.
	var flat []string
	spans := make([][2]int, len(items))
	for i, it := range items {
		ph := phrasings(it)
		spans[i] = [2]int{len(flat), len(flat) + len(ph)}
		flat = append(flat, ph...)
	}
	vecs, err := p.embedAll(ctx, flat)
	if err != nil {
		return fail(CodeEmbeddingFailed, "embed the questions", err)
	}

	docs := make([]meili.Document, len(items))
	for i, it := range items {
		var scope map[string]string
		if err := json.Unmarshal(it.Scope, &scope); err != nil {
			return fail(CodeIndexBuildFailed, "decode the scope of "+it.CandidateID.String(), err)
		}
		alts := it.AlternateQuestions
		if alts == nil {
			alts = []string{}
		}
		d := meili.Document{ID: it.CandidateID.String(), Question: it.Question, AlternateQuestions: alts,
			Answer: it.Answer, SourceRef: it.SourceRef, Scope: scope, PublicationID: pubID.String(), Products: prodsOrEmpty(it.Products)}
		d.SetVectors(vecs[spans[i][0]:spans[i][1]])
		docs[i] = d
	}

	settings, err := meili.DefaultSettings(p.Dimensions, p.ScopeKeys)
	if err != nil {
		return fail(CodeIndexBuildFailed, "index settings", err)
	}
	var tasks []int64
	t, err := p.Meili.CreateIndex(ctx, uid, "id")
	if err != nil {
		return fail(CodeIndexBuildFailed, "create the index", err)
	}
	tasks = append(tasks, t)
	if t, err = p.Meili.ConfigureIndex(ctx, uid, settings); err != nil {
		return fail(CodeIndexBuildFailed, "configure the index", err)
	}
	tasks = append(tasks, t)
	if len(docs) > 0 {
		ts, err := p.Meili.AddDocuments(ctx, uid, docs, indexAddBatch)
		tasks = append(tasks, ts...)
		if err != nil {
			_ = p.Meili.WaitTasks(ctx, tasks) // let queued tasks settle before the caller deletes the index
			return fail(CodeIndexBuildFailed, "add documents", err)
		}
	}
	if err := p.Meili.WaitTasks(ctx, tasks); err != nil {
		return fail(CodeIndexBuildFailed, "index task failed", err)
	}
	return p.verify(ctx, uid, items)
}

func prodsOrEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

func (p *Publisher) verify(ctx context.Context, uid string, items []queries.PublicationItem) error {
	ok, detail, err := p.holds(ctx, uid, items)
	if err != nil {
		return fail(CodeIndexVerifyFailed, "read back the index", err)
	}
	if !ok {
		return fail(CodeIndexVerifyFailed, detail, nil)
	}
	return nil
}

// holds reports whether index uid contains exactly the items' candidate ids.
func (p *Publisher) holds(ctx context.Context, uid string, items []queries.PublicationItem) (bool, string, error) {
	n, err := p.Meili.DocumentCount(ctx, uid)
	if err != nil {
		return false, "", err
	}
	if int(n) != len(items) {
		return false, fmt.Sprintf("index %s has %d documents, expected %d", uid, n, len(items)), nil
	}
	ids, err := p.Meili.DocumentIDs(ctx, uid)
	if err != nil {
		return false, "", err
	}
	want := make(map[string]bool, len(items))
	for _, it := range items {
		want[it.CandidateID.String()] = true
	}
	if len(ids) != len(want) {
		return false, fmt.Sprintf("index %s lists %d documents, expected %d", uid, len(ids), len(want)), nil
	}
	for _, id := range ids {
		if !want[id] {
			return false, fmt.Sprintf("index %s holds unexpected document %s", uid, id), nil
		}
	}
	return true, "", nil
}

func (p *Publisher) embedAll(ctx context.Context, texts []string) ([][]float32, error) {
	chunk := p.EmbedChunk
	if chunk <= 0 {
		chunk = defaultEmbedChunk
	}
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += chunk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := p.Embedder.EmbedBatch(ctx, texts[i:min(i+chunk, len(texts))])
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

// swapIn puts the content of staging under the live uid. It returns the uid
// that now holds the content that was live: staging after a true swap, or ""
// when the live uid did not exist and staging was renamed onto it. It waits
// for the swap task and returns an error unless it succeeded.
func (p *Publisher) swapIn(ctx context.Context, staging, live string) (displaced string, err error) {
	exists, err := p.Meili.IndexExists(ctx, live)
	if err != nil {
		return "", err
	}
	var task int64
	if exists {
		task, err = p.Meili.Swap(ctx, staging, live)
		displaced = staging
	} else {
		task, err = p.Meili.SwapRename(ctx, staging, live)
	}
	if err != nil {
		return "", err
	}
	if _, err := p.Meili.WaitTask(ctx, task); err != nil {
		return "", err
	}
	return displaced, nil
}

// undoSwap reverses a successful swapIn with a fresh context (the caller's may
// be the reason it failed). Best effort: an error is logged, not returned.
func (p *Publisher) undoSwap(staging, live, displaced string) {
	ctx, cancel := context.WithTimeout(context.Background(), staleCleanupTimeout)
	defer cancel()
	var task int64
	var err error
	if displaced != "" {
		task, err = p.Meili.Swap(ctx, staging, live)
	} else {
		task, err = p.Meili.SwapRename(ctx, live, staging)
	}
	if err == nil {
		_, err = p.Meili.WaitTask(ctx, task)
	}
	if err != nil {
		p.log().Error("could not undo the swap; the live index may be ahead of the database, run publish again", "live", live, "error", err)
	}
}

// commitPublish flips the states in one transaction: previous LIVE ->
// SUPERSEDED (its content now lives in displaced), new BUILDING -> LIVE (its
// content lives in the live uid).
func (p *Publisher) commitPublish(ctx context.Context, lang domain.Language, id uuid.UUID, prevSeen *queries.Publication, displaced string) (*uuid.UUID, error) {
	tx, err := p.Store.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	q := p.Store.Queries.WithTx(tx)

	var superseded *uuid.UUID
	cur, err := q.LockLivePublication(ctx, string(lang))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		cur = queries.Publication{}
	case err != nil:
		return nil, err
	}
	if (prevSeen == nil) != (cur.ID == uuid.Nil) || prevSeen != nil && prevSeen.ID != cur.ID {
		return nil, errors.New("the LIVE publication changed while publishing")
	}
	if prevSeen != nil {
		if _, err := domain.PublicationState(cur.State).Transition(domain.PublicationSuperseded); err != nil {
			return nil, err
		}
		var uidPtr *string
		if displaced != "" {
			uidPtr = &displaced
		}
		if n, err := q.SupersedePublication(ctx, queries.SupersedePublicationParams{ID: cur.ID, ContentUid: uidPtr}); err != nil || n != 1 {
			return nil, fmt.Errorf("supersede the previous publication: rows=%d err=%v", n, err)
		}
		superseded = &cur.ID
	}
	if _, err := domain.PublicationBuilding.Transition(domain.PublicationLive); err != nil {
		return nil, err
	}
	live := p.LiveUID(lang)
	if n, err := q.PromotePublication(ctx, queries.PromotePublicationParams{ID: id, FromState: string(domain.PublicationBuilding), ContentUid: &live}); err != nil || n != 1 {
		return nil, fmt.Errorf("promote the publication: rows=%d err=%v", n, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return superseded, nil
}

// markFailed records BUILDING -> FAILED and deletes the staging index, with a
// fresh context so a cancelled run still cleans up.
func (p *Publisher) markFailed(id uuid.UUID, staging string, cause *Error) {
	ctx, cancel := context.WithTimeout(context.Background(), staleCleanupTimeout)
	defer cancel()
	code := string(cause.Code)
	if len(code) > maxErrorCodeLen {
		code = code[:maxErrorCodeLen]
	}
	if _, err := domain.PublicationBuilding.Transition(domain.PublicationFailed); err != nil {
		p.log().Error("publication state machine", "error", err)
	}
	if _, err := p.Store.Queries.FailPublication(ctx, queries.FailPublicationParams{ID: id, ErrorCode: &code}); err != nil {
		p.log().Error("could not mark the publication FAILED", "publication", id, "error", err)
	}
	p.dropIndex(staging)
	p.log().Error("publication failed", "publication", id, "code", cause.Code, "message", cause.Message, "cause", cause.Err)
}

// dropIndex deletes an index and waits; a missing index is fine. Errors are
// logged: a leftover index is waste, not corruption.
func (p *Publisher) dropIndex(uid string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), staleCleanupTimeout)
	defer cancel()
	task, err := p.Meili.DeleteIndex(ctx, uid)
	if err == nil {
		_, err = p.Meili.WaitTask(ctx, task)
	}
	var me *meili.Error
	if err != nil && !(errors.As(err, &me) && me.MeiliCode == "index_not_found") {
		p.log().Warn("could not delete an index", "uid", uid, "error", err)
		return false
	}
	return true
}

// abandonStale marks BUILDING publications of the language FAILED. The caller
// holds the language lock, so any BUILDING row belongs to a run that died.
func (p *Publisher) abandonStale(ctx context.Context, lang domain.Language) error {
	stale, err := p.Store.Queries.ListBuildingPublications(ctx, string(lang))
	if err != nil {
		return fail(CodeDatabase, "list stale publications", err)
	}
	for _, s := range stale {
		p.markFailed(s.ID, s.IndexUid, fail(CodeAbandoned, "a previous run died before finishing", nil))
	}
	return nil
}

// retain deletes the indexes of SUPERSEDED publications beyond the
// RetainIndexes newest and clears their content_uid. It never fails the
// caller: a publication that is already live must stay live.
func (p *Publisher) retain(ctx context.Context, lang domain.Language) []uuid.UUID {
	sup, err := p.Store.Queries.ListSupersededPublications(ctx, string(lang))
	if err != nil {
		p.log().Warn("retention: list superseded publications", "error", err)
		return nil
	}
	live := p.LiveUID(lang)
	var pruned []uuid.UUID
	kept := 0
	for _, s := range sup {
		if s.ContentUid == nil {
			continue
		}
		if kept++; kept <= p.RetainIndexes {
			continue
		}
		if *s.ContentUid != live && !p.dropIndex(*s.ContentUid) {
			continue
		}
		if err := p.Store.Queries.SetPublicationContentUID(ctx, queries.SetPublicationContentUIDParams{ID: s.ID}); err != nil {
			p.log().Warn("retention: clear content_uid", "publication", s.ID, "error", err)
			continue
		}
		pruned = append(pruned, s.ID)
	}
	return pruned
}
