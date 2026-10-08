# Meilisearch probe report

Version: getmeili/meilisearch:v1.54.3 (GitHub latest release, 2026-10-01; digest sha256:e68913ab...41b3c9). Live-checked via GET /version (pkgVersion 1.54.3). Probe scripts: docs/research/probes/ (meili_lib.sh, meili_probe.py). Container removed.
Docs base: https://www.meilisearch.com/docs

| # | Item | Docs | Live evidence | Verdict |
|---|------|------|---------------|---------|
| 1 | Pin version | github.com/meilisearch/meilisearch/releases | v1.54.3 | CONFIRMED |
| 2 | userProvided embedder, no flag | /learn/ai_powered_search/search_with_user_provided_embeddings | GET /experimental-features all false (no vector flag exists); PATCH settings embedders.default {source:userProvided,dimensions:4} succeeded; dimensions 1024 succeeded and a 1024-dim doc `_vectors.default` indexed. (65536 and even 65537 accepted too: no observed cap.) Note: userProvided is incompatible with documentTemplate. | CONFIRMED |
| 3 | Hybrid / pure vector / keyword | /reference/api/search/search-with-post | `vector` + `hybrid:{embedder:"default",semanticRatio:0.5}` works; ratio 1.0 = pure vector; ratio 0 or no hybrid = keyword only. `q` keyword-matches `answer` when it is in searchableAttributes (q="zebra" hit doc 2 via answer, score 0.689). `vector` without `hybrid` -> 400 missing_search_hybrid. Wrong query dims -> 400 invalid_vector_dimensions (expected 4, found 3). | CONFIRMED |
| 4 | Ranking score | /reference/api/search/search-with-post (showRankingScore 0.0..1.0; rankingScoreThreshold "between 0.0 and 1.0", excluded hits not counted in totals) | `_rankingScore` in 0..1; rankingScoreThreshold works in 1.54.3 (threshold .9 returned only the near doc). showRankingScoreDetails for vector hits returns only `vectorSort:{order,similarity}`. | CAVEAT (see below) |
| 5 | Filters | /learn/filtering_and_sorting/search_with_facet_filters | filterableAttributes ["scope.brand","scope.channel"]; filter "scope.brand = a AND scope.channel = web" works on nested objects. Undeclared: HTTP 400 code `invalid_search_filter`, "Attribute `scope.region` is not filterable. Available ... `scope.brand`, `scope.channel`". | CONFIRMED |
| 6 | Swap | /reference/api/indexes/swap-indexes | See below | CONFIRMED (with notes) |
| 7 | Task failures | n/a | See below | CONFIRMED |
| 8 | Limits | /learn/resources/known_limitations | uid: alnum, - and _, docs say <=512 bytes but live limit was 400 (400 ok, 401 rejected, code invalid_index_uid). Payload default 100MB (docs; flag --http-payload-size-limit / MEILI_HTTP_PAYLOAD_SIZE_LIMIT). Max 10 query words. Dims: 1024 fine, no cap seen up to 65537. | CAVEAT (uid 400) |

## 4. Score behaviour (important for NO_MATCH)
- Vector similarity maps to (1+cos)/2, so the floor for an unrelated/orthogonal vector is 0.5, not 0. Pure vector, 4-dim:
  - near-identical [0.99,0.1,0,0] vs doc [1,0,0,0]: 0.9975
  - partial overlap: 0.550
  - orthogonal: 0.5000 (every doc returned at 0.5; query [0,0,0,1])
- Therefore a NO_MATCH threshold must be > 0.5 and is NOT a cosine threshold; calibrate on bge-m3 data. Note bge-m3 real-text cosines rarely go negative, so practical scores are in about 0.5..1.0 and thresholds should be tuned (e.g. 0.75+); resolution is compressed.
- Hybrid (q + vector, ratio .5): score is the max/merge of keyword and semantic hit scores, not a blend. q="password" + matching vector gave 1.0 (details show only vectorSort). q="zebra" with an orthogonal vector gave doc 2 = 0.689 purely from keyword match, i.e. a keyword hit can pass a threshold even when the vector says no match. Keyword scores are rank-rule-based (not semantic similarity), so hybrid scores are not strictly comparable to pure-vector scores.
- Recommendation: for threshold decisions either use semanticRatio 1.0 with empty q (clean, comparable cosine-derived score) or take the vector-only score for the gate and use hybrid just for ordering. Results with q="" and ratio 1.0 still return ALL docs (at >=0.5) unless rankingScoreThreshold is set, so always pass rankingScoreThreshold (empty hits = NO_MATCH).

## 6. Swap
- Docs: "Swap the documents, primary key, settings, and task history of two or more indexes ... The operation is atomic: either all swaps succeed or none do. ... Enqueued tasks are left unmodified."
- It is an async task (type indexSwap, 202 + taskUid). Duration ~1.2 ms on 2x20k-doc indexes.
- True swap: after swapping, faq_en served G2 content and faq_en_v2 held G1; repeated 5x flipping back and forth (rollback = swap again).
- Settings travel with content: sa (4d, searchable question) <-> sb (8d, searchable answer) swapped; afterwards sa had 8d/answer and sb 4d/question, docs likewise.
- Non-existent index: task is accepted (202) then FAILS: error index_not_found "Index `nope` not found." (no pre-validation at HTTP level, check task status).
- `rename:true` exists in 1.54.3 (docs: "rename the first index to the second instead of swapping"). With target existing it fails: index_already_exists "Cannot rename `faq_en` to `faq_en_v2` as the index already exists" (so rename needs target absent; not usable to replace a live index).
- Task queue is serial: a swap enqueued behind a 35s 20k-doc indexing task stayed `enqueued` and searches against faq_en kept returning old content (G2) the whole time, then flipped to G1 at completion. Swap waits behind other tasks, so build and swap in order; the build task must be finished (poll) before swapping.
- Concurrency probe (probe.py): 8 threads hammering search (pure vector, limit 20) on faq_en during 5 consecutive swaps with faq_en_v2: 9345 searches, 0 errors, 0 empty, 0 mixed-generation result sets (G1: 5167, G2: 4178).
- Design note: since tasks already enqueued against uid names are not rewritten, do not queue writes to the staging uid across a swap; stage-name must be reusable only after swap + delete of the old one.

## 7. Task failure
- Batch with one bad vector (3 dims vs 4) in a new index: task status `failed`, code invalid_vector_dimensions, message "Index `fb`: Invalid vector dimensions in document with id `2` in `._vectors.default`. embedding #0 has dimensions 3 / embedder `default` requires 4"; details indexedDocuments:0; the whole batch is rolled back (index stayed at 0 docs, even the valid doc 1 was not indexed).
- Same bad doc against live index faq_en: task failed, numberOfDocuments stayed 20000 (stats before/after identical). Live index untouched. The service must check task status == succeeded for every task (settings, each doc batch) before issuing the swap.

## Contradictions / flags for the design
- No contradictions found: userProvided vectors, hybrid with caller vector, rankingScoreThreshold and atomic swap all work on v1.54.3 without experimental flags.
- CAVEAT 1: score is (1+cos)/2 with floor 0.5 and in hybrid mode keyword matches inflate the score; calibrate the NO_MATCH threshold on real bge-m3 vectors, and prefer ratio 1.0 / empty q for the gate.
- CAVEAT 2: swap with a missing index fails asynchronously (task failed), not at request time.
- CAVEAT 3: live uid limit 400 chars though error text says 512.
- CAVEAT 4: swap is fast and atomic w.r.t. search, but it queues behind other tasks (serial queue); publication latency = build time + queue wait.

## 8. M5 probes (2026-10-08, v1.54.3)

Run while building publish and search. Index uids and ids are shortened.

**Several user-provided vectors per document.** `_vectors.default` may be an array of arrays; the document is scored by its best-matching vector, not by an average. Embedder dimensions 3, documents `a` (two vectors `[1,0,0]` and `[0,1,0]`), `b` (`[0,0,1]`), `c` (`[0.7071,0.7071,0]`):

```
POST /indexes/mv/documents  [{"id":"a","scope":{"brand":"x"},"_vectors":{"default":[[1,0,0],[0,1,0]]}}, ...]
POST /indexes/mv/search  {"q":"","vector":[1,0,0],"hybrid":{"embedder":"default","semanticRatio":1.0},"showRankingScore":true}
 -> a 1.0, c 0.8536, b 0.5          (query [0,1,0]: a 1.0, c 0.8536, b 0.5)
    query [0.7071,0.7071,0]: c 1.0, a 0.8536, b 0.5
    query [0,0,-1]: a 0.5, c 0.5, b 0.0
```

`a` scores 1.0 for both of its vectors, so the score is the maximum over a document's vectors. A mean vector would have scored 0.7071 against either phrasing. Publish therefore stores one vector per distinct phrasing (the question and each alternate question). The index counts documents (`numberOfDocuments` 3), not vectors (`numberOfEmbeddings` 4).

**`NOT EXISTS` on a nested filterable attribute** (`scope.brand`, a document with no `scope` at all, and one with `scope: {}`):

```
filter (scope.brand = "x" OR scope.brand NOT EXISTS), query [1,0,0] -> a 1.0, c 0.8536     (b has brand y: excluded)
filter (scope.brand = "y" OR scope.brand NOT EXISTS)                 -> c 0.8536, b 0.5   (a has brand x: excluded)
```

A document without a scope key is global for that key. `internal/meili` builds `(scope.k = "v" OR scope.k NOT EXISTS)` per key; `TestGlobalDocumentsMatchAnyScopeValue` covers `nil`, `{}` and partial scopes.

**`/swap-indexes` with `rename: true` onto an absent uid** succeeds atomically and moves the index:

```
POST /swap-indexes [{"indexes":["probe_mv","probe_ren"],"rename":true}]   -> task succeeded
GET /indexes/probe_ren/stats -> numberOfDocuments 3 ;  GET /indexes/probe_mv -> index_not_found
```

First publication of a language uses it: there is no live index to swap with, and creating an empty live index first would leave a window in which search answers NO_MATCH instead of INDEX_UNAVAILABLE. `rename` onto an existing uid fails the task with `index_already_exists` (section 6), which `TestSwapRenameMovesAnIndexOntoAnAbsentUID` pins.

**Vector search is approximate under a filter with a small limit.** A 5-document index (6 vectors, real bge-m3 embeddings) rebuilt 30 times, filters `brand=globex`, `channel=voice`, `brand=acme` (each with `OR NOT EXISTS`), every document queried with its own question (450 queries, the best answer scores 1.0 and is known). Counting queries whose first hit was not the best document:

| limit | wrong first hit |
|---|---|
| 1 | 4 of 450 (the returned hit was the document ranked third, 0.74 instead of 1.0) |
| 3, 10, 20 | 0 of 450 |

`internal/search` therefore asks Meilisearch for at least 10 hits and keeps the first `topK`. Not seen without a filter. The sample is small; treat the exact rate as indicative only.

**Reused HTTP connections stall about 40 ms** (compose stack on macOS, Meilisearch behind Docker's port forward). Over a kept-alive connection roughly every second search took 40+ ms (search stage p50 43 ms, p90 52 ms); with a new connection per search p50 3.6 ms, p90 4.2 ms, same 37 questions. The query-path Meilisearch client therefore disables keep-alive. TEI responses (single write) were not affected.
