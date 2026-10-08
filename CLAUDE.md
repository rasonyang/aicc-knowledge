# aicc-knowledge: working agreement

CLAUDE.md and AGENTS.md carry the same text. Change them together.

`aicc-knowledge` is a single-tenant knowledge retrieval service for AICC (the AI Native Call Center). AICC calls it as an ordinary flow HTTP tool; AICC never depends on it. Source files live in S3, PostgreSQL is the source of truth, and Meilisearch is a derived index that can always be rebuilt. Licensed Apache-2.0.

Everything in this repo is English: code, comments, docs, commit messages. The research that backs the decisions below is in `docs/research/README.md`; read its caveats C1 to C7 before touching search, publish or the dev stack.

## Binding decisions (do not re-litigate)

- Go single binary; chi v5, pgx v5, sqlc, goose (migrations at startup), PostgreSQL 18, no pgvector.
- Contract-first: `docs/openapi.json` is the source of truth; oapi-codegen; oasdiff in CI.
- Naming and enums follow AICC: JSON lowerCamelCase, Go initialisms capitalized, DB snake_case, enums SCREAMING_SNAKE everywhere. Errors return codes, not strings.
- No multi-tenancy. No hardcoded endpoints. Config via `KB_*` env vars, `.env.example` is the registry.
- S3 via aws-sdk-go-v2 with configurable endpoint and path-style (AWS, MinIO, SeaweedFS).
- Change detection: ListObjectsV2 polling; size/LastModified/ETag change → download and compute SHA-256; SHA-256 is the identity of content. No S3 event notifications.
- Formats: `.docx` (stdlib zip + xml; headings from `styles.xml` `outlineLvl`, never style names) and `.xlsx` (excelize). `.doc`/`.xls` → `UNSUPPORTED_FORMAT`. No PDF, no OCR.
- Job queue: one PostgreSQL table with `FOR UPDATE SKIP LOCKED`. No queue dependency.
- Embeddings: TEI (CPU) serving bge-m3; Meilisearch uses `userProvided` vectors; this service embeds documents and queries itself so embedding and search latency are measured separately.
- One Meilisearch index per language (`faq_en`, `faq_zh`). Vectors built from question + alternate questions, one vector per distinct phrasing (Meilisearch scores a document by its best vector); `answer` stored and keyword-searchable. A document's `scope` comes from its S3 path (`KB_S3_SCOPE_PATH_TEMPLATE`); a document without a scope key is global for that key.
- Structured facts: per-sheet YAML mapping (columns, types, units, key columns, validity columns) → one generic table with typed JSONB rows validated at import; exact-match lookup only. No runtime DDL.
- Candidate Q&A: offline LLM via OpenAI-compatible chat completions with JSON schema output; answers are short and speakable; candidates containing figures carry a `CONTAINS_FIGURES` flag.
- Review: Excel round-trip. Export: locked id column, hidden content-hash column, action dropdown `APPROVE | REJECT | EDIT`. Import rejects rows whose source changed since export (`STALE`).
- Nothing unreviewed is ever published. Publish = build a new index from approved rows, then atomic swap; every publication is versioned; rollback swaps back.
- Admin operations are CLI subcommands of the same binary: `serve`, `scan`, `parse`, `generate`, `export-review`, `import-review`, `publish`, `rollback`, `eval`. The HTTP API serves AICC read paths and health only.
- Observability: slog, OTel, Prometheus `/metrics`, `/healthz`, `/readyz`; per-stage latency histograms (embedding, search, total).
- Dev stack: docker compose with app, postgres, meilisearch, tei, seaweedfs.

## HTTP contract v1

- `POST /v1/search`: `query`, `language` (`EN` | `ZH`, mapping to `faq_en` / `faq_zh`), `scope` (string-valued filter object), `topK` (1 to 3, default 3), `timeoutMs`. Response `status: HIT | NO_MATCH`, `items[]` (`id`, `question`, `answer`, `sourceRef`, `score`), `latencyMs` (`embedding`, `search`, `total`). NO_MATCH is decided by the per-index score threshold from config. Unknown `scope` key: 422 `UNKNOWN_SCOPE_KEY`. Timeout (embedding and search share the `timeoutMs` budget): 504 `UPSTREAM_TIMEOUT`. TEI or Meilisearch unreachable: 503 `UPSTREAM_UNAVAILABLE`. No live index for the language: 503 `INDEX_UNAVAILABLE`. Search never asks PostgreSQL which publication is LIVE: the live uid only ever holds published content.
- `POST /v1/facts/{table}/lookup`: `key` (object), optional `at` (date). Response `status: FOUND | NOT_FOUND`, `row`, `sourceRef`, `validFrom`, `validTo`. Unknown table: 404 `FACT_TABLE_NOT_FOUND`.
- Bearer API key, stored as a SHA-256 digest (AICC pattern). `create-api-key` prints the secret once.
- Ops listener (separate address, unauthenticated, not in the contract): `/metrics`, `/healthz`, `/readyz`.

## Naming

The spec is AICC's `docs/design/07-naming.md` (sibling repo `ai-native-callcenter`, read-only, never imported). In short: Go `SourceRef` <-> JSON `sourceRef` <-> DB `source_ref`. Enum values are SCREAMING_SNAKE and byte-identical in JSON, Go and the database. Go enums are a named string type plus consts (`FileVersionParsed FileVersionState = "PARSED"`), never `iota`. Durations are `xxxMs` / `xxxSec`, timestamps `xxxAt`, booleans `is` / `has`. Tables are plural, constraints are named `uq_`, `fk_`, indexes `idx_`. Enum columns are `varchar(N)` with an inline CHECK, not native enums. Metric names are snake_case and live in `internal/obs/metrics.go`.

## State machines

Defined in `internal/domain`, with a table-driven test for every allowed and every disallowed pair.

- File version: `DISCOVERED → PARSED | PARSE_FAILED | UNSUPPORTED`; a deleted source moves any of those to `REMOVED` (terminal). `PARSE_FAILED → DISCOVERED` is the one retry edge, triggered only explicitly by an operator (`parse --retry-failed`); the version's content stays immutable and only its processing state is reset, e.g. after a parser fix. `PARSED` and `UNSUPPORTED` have no retry edge. A content change inserts a new version; it never mutates the old one.
- Candidate: `PENDING_REVIEW → APPROVED | REJECTED`; any state → `STALE` when its source version is superseded (terminal).
- Publication: `BUILDING → LIVE | FAILED`, `LIVE → SUPERSEDED`. Rollback is the explicit edge `SUPERSEDED → LIVE`, applied in one transaction together with `LIVE → SUPERSEDED` of the current publication, after the swap task succeeded. At most one LIVE publication per language (partial unique index). `/swap-indexes` is a true swap, so `publications.content_uid` records which Meilisearch uid currently holds each publication's documents; `index_uid` is only the name it was built under. One publish or rollback per language at a time (advisory lock). Going from a non-empty live index to an empty one needs `publish --allow-empty`.
- Job: `QUEUED → RUNNING → SUCCEEDED | FAILED`, and `RUNNING → QUEUED` for a retry or an expired lease.

## Conventions

- SPDX: every Go, SQL, shell, YAML and Makefile file starts with `SPDX-License-Identifier: Apache-2.0` in that file's comment syntax. Generated sqlc output is exempt (its header says it is generated and `sqlc diff` guards it); the oapi-codegen output gets the header from `scripts/api-generate.sh`.
- Migrations: goose, `internal/store/migrations/NNNNN_snake_sentence.sql` (5 digits), SPDX line, a prose header that says why, then `-- +goose Up` and a real `-- +goose Down`. Applied at `serve` startup and by every subcommand that touches the database, under an advisory lock. A migration is not reviewed until it has run: the store tests go up from zero, down to zero, and up again. A migration that narrows a CHECK rewrites rows first and gets a fixture test.
- Gate tests (they fail when two sources of truth drift): Go enum sets equal the database CHECK sets; the spec's `ErrorCode` enum equals the Go `Code*` consts; every spec property is lowerCamelCase and every spec enum value SCREAMING_SNAKE; every operation is routed and declares bearer security; the key set of `.env.example` equals the key set `internal/config` reads; every source file carries the SPDX header (`internal/repocheck`).
- Config: `KB_*` only, hand-rolled in `internal/config`. `.env.example` lists every key as a commented `#KEY=default` line with a comment above it. An empty value means unset. Service URLs have no default in code; a command declares what it requires and fails naming the missing keys.
- Errors: every non-2xx response is `{"error":{"code","message","params"}}`, including router 404 and 405. `message` is diagnostic English; clients branch on `code`. The code set is the `ErrorCode` enum in the spec.
- Contract: change `docs/openapi.json` first, run `make api-generate`, then implement `api.ServerInterface`. `make api-check` and `make api-breaking` must pass. Never add a route the contract does not declare.
- Tests assert cardinality next to equality: a list's length as well as its contents.
- Metrics: names are constants in `internal/obs/metrics.go`. Do not state performance figures in docs or commits unless `eval` measured them.

## Workflow

- Tests first. Write the failing test, then the code.
- Real PostgreSQL and real Meilisearch in tests. No mocks for either. `internal/testdb.ScratchDSN` makes a throwaway database per test from `KB_TEST_DATABASE_URL`; `testdb.MeiliURL` reads `KB_TEST_MEILI_URL`. A test that needs them skips loudly when they are unset, and `make test` prints a warning box. TEI may be faked with httptest only in unit tests of readiness wiring; prefer the real one.
- `TASKS.md` is the plan. The cursor is the first unchecked item. Check an item only after it is verified against live services.
- Done means verified against live services: `make dev-up`, set the `KB_TEST_*` variables, `make test` with zero skips, then exercise the running stack with curl.
- CI runs `scripts/test-gate.sh` (`make test-gate`): `-race`, real PostgreSQL, Meilisearch, SeaweedFS and TEI, and it fails on any skipped test not listed in `.ci-allowed-skips.txt`. Only the live-LLM tests (`KB_TEST_LLM_URL`) may be listed; never allowlist a test because a service is missing, add the service to `ci.yml`.
- Run Go tests with `-race`. Run `make generate api-check build lint sqlc-check` before saying a change is done.
- Do not add attribution lines to commits, PRs, issues or release notes.
