# Tasks

The cursor is the first unchecked item. Check an item only after it is verified against live services (real PostgreSQL, real Meilisearch, and the compose stack where it applies).

## 0. Research

- [x] Validate every binding decision against docs, the AICC repo and live probes. Result: [docs/research/README.md](docs/research/README.md) (caveats C1 to C7 are binding).

## M1. Scaffold

- [x] Repo basics: go.mod, SPDX headers, NOTICE, README, CHANGELOG, .gitignore, CLAUDE.md and AGENTS.md.
- [x] Contract: `docs/openapi.json` (OpenAPI 3.1), `docs/embed.go`, redocly lint, oapi-codegen and oasdiff as `go tool`, `scripts/api-generate.sh`, `.oasdiff-breaking-ignore.txt`.
- [x] HTTP server: chi router implementing `api.ServerInterface`, bearer auth by SHA-256 digest, 404 and 405 in the envelope, validation, 422 `UNKNOWN_SCOPE_KEY`, 503 `INDEX_UNAVAILABLE`, 404 `FACT_TABLE_NOT_FOUND`.
- [x] Ops listener: `/metrics`, `/healthz`, `/readyz` (PostgreSQL, Meilisearch, TEI).
- [x] Observability: slog, OTel tracing when `KB_OTLP_ENDPOINT` is set, Prometheus exporter, per-stage latency histograms, HTTP metrics.
- [x] Config: `KB_*` keys, `.env.example` registry, gate test (registry equals the keys the code reads).
- [x] Database: goose migrations (api_keys, jobs, source_files, file_versions, candidates, publications, publication_items), sqlc queries, advisory-locked migrate at startup, up/down/up test.
- [x] Domain state machines with table-driven tests, and the gate test Go enums == DB CHECK sets.
- [x] CLI: `serve`, `version`, `create-api-key` work; the other subcommands exit non-zero naming their milestone.
- [x] Contract gate tests: ErrorCode enum == Go consts, lowerCamelCase properties, SCREAMING_SNAKE enums, every operation routed, bearer security, operationId verbs.
- [x] Makefile, Dockerfile, compose.yaml (app, postgres, meilisearch, tei, seaweedfs).
- [x] CI: `ci.yml` (PostgreSQL and Meilisearch services, sqlc check, race tests) and `api.yml` (api-check, api-breaking). Validated with actionlint only; it has not run on GitHub yet.
- [x] Verified end to end: `make generate api-check build lint`, `make test` with zero skips, full compose stack with curl checks.
- [x] Release chore: THIRD_PARTY_LICENSES generated with `go-licenses` v2.0.1 (`make licenses`; `make licenses-check` in CI fails when stale or when a license is outside Apache-2.0, MIT, BSD-2/3-Clause, ISC, MPL-2.0); LICENSE, NOTICE and THIRD_PARTY_LICENSES ship in the image under `/licenses`. Regenerate when dependencies change. See caveat C7.

## M2. Scan

- [x] S3 client with configurable endpoint and path-style (aws-sdk-go-v2); live check against the SeaweedFS in compose (research row 5).
- [x] Incremental ListObjectsV2 scan: compare size, LastModified and ETag with the current version; on change download and compute SHA-256.
- [x] Insert a new `file_versions` row on a new SHA-256 and supersede the old one; never mutate a version.
- [x] Deleted objects: version becomes `REMOVED`, candidates of that version become `STALE`.
- [x] Unsupported extensions (`.doc`, `.xls`, anything else) become `UNSUPPORTED` with `UNSUPPORTED_FORMAT`.
- [x] `scan` subcommand, and the jobs queue claim query (`FOR UPDATE SKIP LOCKED`).
- [x] Test: scan is idempotent (second run creates no rows; assert counts).
- [x] Test: a multipart-upload ETag never stands in for the content hash (same bytes, different ETag, one version; different bytes, same size, new version).
- [x] Test: a deleted object becomes `REMOVED` and its rows drop out at the next publish (store side: `ListPublishableCandidates` excludes them; end-to-end publish check is in M5).

## M3. Parse

- [x] `.docx` extraction with the stdlib: headings from `styles.xml` `outlineLvl`, never style names; Chinese heading styles; tracked changes; tables.
- [x] `.xlsx` extraction with excelize: merged cells, multi-row headers, hidden rows, formulas without cached values (a parse warning, never a silent empty).
- [x] Parsed sections table and migration (not created in M1).
- [x] Per-sheet YAML facts mapping (columns, types, units, key columns, validity columns) validated at import.
- [x] Generic facts table with typed JSONB rows; migration; no runtime DDL.
- [x] `parse` subcommand: `DISCOVERED → PARSED | PARSE_FAILED`, warnings in `parse_warnings`.
- [x] `parse --retry-failed`: operator-only flag that resets `PARSE_FAILED → DISCOVERED` (content stays immutable; only the processing state is reset, e.g. after a parser fix). `PARSED`, `UNSUPPORTED` and `REMOVED` are never retried.
- [x] `POST /v1/facts/{table}/lookup`: exact match on key columns, optional `at` against validity columns, `FOUND | NOT_FOUND`.
- [x] Golden tests for every fixture listed above; fixtures are committed.
- [x] Scan: an object over `KB_S3_MAX_OBJECT_BYTES` is versioned `UNSUPPORTED`/`OBJECT_TOO_LARGE` (no job) instead of being ignored.
- [x] Blame rule: an import problem caused by workbook data (any `xlsx.ImportFacts` issue, including `HEADER_NOT_FOUND` and `SHEET_NOT_FOUND`, since the mapping is the contract) fails the workbook version `FACTS_INVALID` and leaves the mapping `PARSED`; only `MAPPING_INVALID` and `FACT_TABLE_NAME_CONFLICT` fail the mapping. Limits: a mapping that arrives after an already-`PARSED` workbook cannot move it to `PARSE_FAILED` (no such edge), so the issues go into the workbook's `parse_warnings` and the table stays `UNAVAILABLE/FACTS_INVALID`; a removed or replaced mapping does not re-derive the workbook's sections until the workbook changes.

## M4. Generate and review

- [x] OpenAI-compatible chat-completions client with JSON schema output (`KB_LLM_*`): `internal/llm`, coded errors `UPSTREAM_TIMEOUT`, `UPSTREAM_UNAVAILABLE`, `LLM_OUTPUT_INVALID`, bounded retry on timeout, 429 and 5xx, output validated against the schema in Go.
- [x] `generate`: candidates per section; answers short and speakable; `CONTAINS_FIGURES` flag for candidates with figures. A parse that yields sections queues a `GENERATE` job (deduped per file version); validation, one retry with feedback, dedupe, idempotent transaction; candidates of withdrawn sections become `STALE` (`SECTION_WITHDRAWN`).
- [x] `export-review`: Excel workbook with locked id column, hidden content-hash column, action dropdown `APPROVE | REJECT | EDIT`, protected sheet, editable cells unlocked.
- [x] `import-review`: apply actions; reject rows whose source changed since export (`STALE`); `EDIT` is approve-with-edits; audit trail in `candidate_reviews`.
- [x] Test: import rejects stale edits (changed source, superseded version, tampered hash, `APPROVE` with edited text, double import).
- [x] Test: unreviewed candidates never reach any index (store side: `ListPublishableCandidates` returns 0 until approval, then exactly the approved ones; the end-to-end index check comes with `publish` in M5).
Known limits of M4: the deterministic rules cannot judge whether an answer is correct, so every candidate still needs a human review against the source excerpt; `import-review` writes no results workbook (the summary and per-row errors go to stdout); `generate` calls the LLM one section at a time (the job lease is extended after every section); the dedupe of questions is textual (case, width, punctuation), not semantic.

## M5. Publish and search

- [x] Embedding client for TEI (`internal/embed`); batch document embedding for publish.
- [x] Meilisearch client (`internal/meili`): userProvided embedder, task waiting, swap, pure-vector search with threshold, safe scope filter builder.
- [x] Publish must not starve query embeddings: TEI serves requests in order on CPU and `/health` itself queued 15 s behind long embeddings. Embed publish batches small and serially, and allow an optional separate TEI for offline work (e.g. `KB_TEI_BATCH_URL`, default `KB_TEI_URL`).
- [x] `publish`: build a new index from approved rows (settings, embedder, documents), check every task `succeeded` (caveat C6), then swap and wait for the swap task; mark `LIVE` only when it succeeded.
- [x] Track where each publication's content physically lives. `/swap-indexes` is a true swap: after publishing P2 into `faq_en`, the staging uid `faq_en_<P2>` holds P1's content, so `publications.index_uid` stops naming the publication's own index after a swap. Record per publication the Meilisearch uid that currently holds its content (new column, e.g. `content_uid`) and update it for both publications on every swap.
- [x] `rollback --to <publicationId>` (default: the previous publication) can target any `SUPERSEDED` publication. If its index no longer exists, rebuild it from `publication_items` plus the snapshotted question, alternates, answer and sourceRef in PostgreSQL (candidates may have changed state since; migration 00005 snapshots these columns), then swap. `SUPERSEDED → LIVE` and `LIVE → SUPERSEDED` in one transaction, after the swap task succeeded.
- [x] Retention: keep the N most recent non-live publication indexes (`KB_PUBLISH_RETAIN_INDEXES`, default 3) and delete older indexes; publication rows and items are kept forever. Add the config key and its `.env.example` entry when this is implemented.
- [x] `POST /v1/search`: embed the query, search with the pure-vector gate (`semanticRatio` 1.0, empty `q`, `rankingScoreThreshold`; caveat C4), scope filter, topK, timeout mapping to 504 `UPSTREAM_TIMEOUT`, per-stage latency in the response and in the histograms.
- [x] `eval`: input a question CSV with expected ids; output recall@3, NO_MATCH precision and per-stage latency p50 and p90.
- [ ] Calibrate `KB_SEARCH_THRESHOLD_EN` and `KB_SEARCH_THRESHOLD_ZH` with `eval` on real data. A first calibration on a small synthetic sample is done (defaults 0.85 EN and 0.85 ZH, see research caveat C4); the ZH default is now 0.875 from an offline calibration on a private real sample together with the product guard (M7); the box stays open until real questions and real documents have been run through `eval --sweep`.
- [x] Test: a publish failure leaves the live index untouched.
- [x] Test: rollback restores the previous publication, and a two-step rollback (to a publication older than the previous one) works.
- [x] Test: rollback to a publication whose index was pruned takes the rebuild path and serves the same content.
- [x] Test: after every swap, each publication's recorded uid holds exactly that publication's content.
- [x] Test: search never returns content from an unpublished version.
- [x] Test: NO_MATCH below the threshold.
- [x] Tests assert cardinality alongside equality.

Decisions taken in M5 (details in the CHANGELOG and docs/research/02-meilisearch.md section 8):

- One vector per distinct phrasing of a question (Meilisearch scores a document by its best vector). The first publication of a language renames the staging index onto the absent live uid; later ones swap. Search does not ask PostgreSQL for the LIVE publication. A document's scope comes from its S3 path (`KB_S3_SCOPE_PATH_TEMPLATE`); a document without a scope key is global for that key.
- Known limits: a crash between the swap and the database commit leaves a `BUILDING` row (the next publish marks it `FAILED`/`PUBLISH_ABANDONED`; a rollback verifies a retained index against `publication_items` before trusting `content_uid` and rebuilds when it disagrees). `eval -sweep` runs in process only. The Meilisearch query client opens a connection per search (reused connections stalled about 40 ms in the compose stack).

## M6. Validation findings and Q&A import

Found by a validation run on a private, desensitized corpus. Each fix has its own test.

- [x] Search: the searcher no longer sends `rankingScoreThreshold` (Meilisearch needs a slow path when fewer than `limit` hits clear it); it fetches at least 10 hits and applies the threshold itself. Test: a recording transport around the real client sees no threshold on a NO_MATCH search.
- [x] Generate counts heading plus body for its length gate. A question heading (ends with `？` or `?`, or starts with `Q` and a digit) with a short body is sent. Every skipped section is logged and counted (`SKIPPED_STUB`, `SKIPPED_NO_LANGUAGE`, summary fields).
- [x] Stub sections no longer reach the LLM: after removing boilerplate lines a body under 20 characters is context for the next section. Every prompt carries the document title and heading path; prompt `faq-v2` returns nothing when the section states no answer; an answer figure (digits, full-width digits, model numbers) absent from the source is dropped as `UNGROUNDED_FIGURE`.
- [x] `candidate.DetectLanguage`: Chinese with many Latin product names is ZH (6 or more Han characters, or 4 or more and 15% of the letters).
- [x] `.xlsx` content is chunked by size (6000 characters, header repeated); a single row over the limit is cut with `SECTION_TRUNCATED`. Generate warns whenever it cuts input.
- [x] `CONTAINS_FIGURES` ignores letter-first model tokens (`K5`, `ZQ 3S`, `A2`); `4K`, `60fps`, `1999元` and plain numbers still count.
- [x] `generate --version <id>` claims only that version's job.
- [x] An outline-level paragraph over 40 characters or ending in `。！.!` is body text (`HEADING_DEMOTED`); a short question heading stays a heading.
- [x] Consecutive identical heading entries collapse in a heading path.
- [x] Q&A workbook direct import: `<name>.qa.yaml` mapping, `XLSX_QA_ROW` sections (migration 00013), generate without the LLM (`qa-import-v1`) or with one condense call (`qa-condense-v1`), review/export/publish unchanged.
Known limits of M6: the verbatim import trusts the sheet, so every row still needs a reviewer; spelled-out numbers (`五十`, `fifty`) are not compared by the grounding check, so a condensed or generated answer that rewrites `50` as `五十` is dropped; a stub's context goes to the next section that is sent only; the ordinals of Q&A rows follow the content chunks, so a facts mapping that removes a content sheet shifts them and stales their candidates (regenerate by re-arming the version).

## M7. Product catalog and search guard

Motivated by an offline experiment on a private real sample (no data in this repository): most wrong HITs were product-model confusion and generic FAQs matching off-topic questions. Synthetic fixtures only.

- [x] `internal/products`: normalization (NFKC, case, hyphen and whitespace, Chinese numerals after a Latin word), longest-match extraction with boundaries, lists, symmetric compatibility, model-token detector; table tests.
- [x] `products.yaml` at the root of `KB_S3_PREFIX`: scan classifies it, parse validates and stores it (migration 00014 `product_catalogs`), `CATALOG_INVALID`, a second one `CATALOG_MISPLACED`.
- [x] Publish tags documents with products and snapshots them with the catalog id; rollback (swap and rebuild) restores the old product sets; a catalog file that is not `PARSED` refuses the publish (`CATALOG_UNAVAILABLE`).
- [x] Search guard R1 (drop other products, compatibility), R2 (unknown model is NO_MATCH), R3 (generic margin); no catalog means unchanged behavior; `serve` refreshes live catalogs in memory (`KB_PRODUCTS_REFRESH_SEC`); counter `kb_product_guard_total`.
- [x] `KB_SEARCH_THRESHOLD_ZH` 0.875, `KB_SEARCH_GENERIC_MARGIN_ZH` 0.04, `KB_SEARCH_GENERIC_MARGIN_EN` 0; `.env.example`, research caveat C4, CHANGELOG, README.
- [x] `eval` reports the guard counts and `--sweep` sweeps the generic margin when a catalog is live.
- [x] Tests: catalog lifecycle through S3 (valid, invalid, second catalog, change, rollback); search with a synthetic corpus (other product never returned, compatible allowed, unknown model, generic margin, no catalog unchanged); guard latency under 1 ms.
- Deferred: a lazy catalog refresh when a hit carries a newer publication id than the cached catalog; a per-product `family:` override in `products.yaml` (families are the leading Latin word of each alias; add the override if a prefix turns out ambiguous).
