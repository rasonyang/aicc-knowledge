# §0 Research — validation of binding decisions

Research was done on 2026-10-08, before any code was written. Each row maps a
binding decision to its evidence. The evidence is a doc URL, a `file:line` in
the AICC repo (github.com/rasonyang/ai-native-callcenter, read only), or the
output of a live probe. A row with no evidence says NOT FOUND, or says that no
§0 item asked for that check.

Detailed reports:

- [01-aicc-conventions.md](01-aicc-conventions.md): AICC conventions to mirror.
- [02-meilisearch.md](02-meilisearch.md): live probe of Meilisearch v1.54.3.
- [03-tei-bge-m3.md](03-tei-bge-m3.md): live probe of TEI v1.9.4 serving bge-m3.
- [04-licenses.md](04-licenses.md): dependency licenses.
- [probes/](probes/): the probe scripts, so the live checks can be re-run.

**Verdict: no finding contradicts a binding decision.** Caveats are listed
below the table. Each one changes how we implement a decision, not the
decision itself.

| # | Binding decision | Finding | Evidence | Verdict |
|---|---|---|---|---|
| 1 | Go single binary; chi v5, pgx v5, sqlc, goose at startup, PG 18 | AICC uses the same stack. goose v3 migrations are embedded and applied at startup under an advisory lock. sqlc uses `sql_package: pgx/v5`. CI uses a `postgres:18` service. | 01 §7, §10, §11 (AICC `internal/store/store.go:84`, `sqlc.yaml`, `.github/workflows/ci.yml`) | CONFIRMED |
| 2 | Contract-first `docs/openapi.json`, oapi-codegen, oasdiff in CI | AICC: OpenAPI 3.1 at `docs/openapi.json`. oapi-codegen v2 (`models` + `chi-server`) and oasdiff `breaking --fail-on ERR` are pinned as `go tool`s. Redocly `recommended` lint. | 01 §4, §6 | CONFIRMED |
| 3 | AICC naming: lowerCamel JSON, Go initialisms, snake DB, SCREAMING_SNAKE enums, error codes | AICC `docs/design/07-naming.md`. Error envelope is `{"error":{"code","message","params"}}` with an `ErrorCode` enum. DB enums are `varchar CHECK (… IN …)`. AICC has no spec-wide casing gate test, so we add one. | 01 §2, §5, §6, §8 | CONFIRMED (gate test added here) |
| 4 | No multi-tenancy; `KB_*` env; `.env.example` is the registry | AICC config loading is hand-rolled (`internal/config/config.go`). `.env.example` uses commented `#KEY=default` lines. AICC has no test that keeps the registry in sync, so we add one. | 01 §12 | CONFIRMED (gate test added here) |
| 5 | S3 via aws-sdk-go-v2, configurable endpoint and path-style | aws-sdk-go-v2 is Apache-2.0. The endpoint override needs a live check against SeaweedFS in M2. | 04 | NOT RESEARCHED (no §0 item); checked in M2 |
| 6 | ListObjectsV2 polling; SHA-256 is the content identity; multipart ETag never trusted | Design decision. No §0 item. | — | NOT RESEARCHED (no §0 item); tests in M2 |
| 7 | `.docx` via stdlib, `.xlsx` via excelize; `.doc`/`.xls` unsupported | excelize is BSD-3-Clause. That is compatible, but the license text must be reproduced. | 04 | CONFIRMED (license) |
| 8 | PG job queue with `FOR UPDATE SKIP LOCKED` | Design decision. No §0 item. | — | NOT RESEARCHED (no §0 item) |
| 9 | TEI (CPU) serving bge-m3; we embed docs and queries ourselves | TEI v1.9.4 serves BAAI/bge-m3. CLS pooling is read from the model's `1_Pooling/config.json`. Output is 1024-dim and L2-normalized. Single-query latency, native arm64, after warm-up, n=200: **p50 30.8 ms, p90 38.8 ms, p99 45.1 ms**. | 03; https://github.com/huggingface/text-embeddings-inference/releases/tag/v1.9.4; https://huggingface.co/BAAI/bge-m3 | CONFIRMED with caveats C1–C3 |
| 10 | Meilisearch `userProvided` vectors; one index per language | v1.54.3: the `userProvided` embedder needs no experimental flag. 1024 dims are accepted. `_vectors.default` is indexed. | 02 §1; https://www.meilisearch.com/docs/learn/ai_powered_search/search_with_user_provided_embeddings | CONFIRMED |
| 11 | Vector from question + alternates; `answer` keyword-searchable | Hybrid search takes a caller-supplied `vector`. `q` keyword-matches `answer` when it is in `searchableAttributes`. | 02 §2 | CONFIRMED |
| 12 | `NO_MATCH` by a per-index score threshold | `_rankingScore` is 0..1, and the `rankingScoreThreshold` search parameter exists. The vector score is (1+cos)/2, so an orthogonal vector scores 0.5. In hybrid mode a keyword hit can lift the score above the threshold. | 02 §3 | CONFIRMED with caveat C4 |
| 13 | `scope` filter object; unknown key → 422 | `filterableAttributes` works on nested keys. A key that is not declared returns Meilisearch 400 `invalid_search_filter`, so we must reject unknown keys before calling Meilisearch. | 02 §4 | CONFIRMED with caveat C5 |
| 14 | Publish = build a new index, then atomic swap; rollback swaps back | `/swap-indexes` is atomic. It swaps documents and settings, embedders included. Live probe: 8 threads, 9345 searches over 5 swaps, 0 errors, 0 empty, 0 mixed results. A second swap restores the previous state. | 02 §5; https://www.meilisearch.com/docs/reference/api/indexes/swap-indexes | CONFIRMED with caveat C6 |
| 15 | Publish failure leaves the live index untouched | A document batch that fails (dimension mismatch) fails the whole task and indexes 0 documents. The live index count did not change. | 02 §6 | CONFIRMED with caveat C6 |
| 16 | Offline LLM via OpenAI-compatible chat completions + JSON schema | Design decision. No §0 item. | — | NOT RESEARCHED (no §0 item); checked in M4 |
| 17 | Observability: slog, OTel, Prometheus, `/healthz`, `/readyz` | AICC `internal/obs/obs.go`: slog, OTLP traces, OTel Prometheus exporter. A separate ops listener serves `/metrics`, `/healthz` and `/readyz`. | 01 §13 | CONFIRMED |
| 18 | Bearer API key stored hashed | AICC: 32 random bytes, stored as a SHA-256 digest (bytea UNIQUE) and looked up by digest. | 01 §14 | CONFIRMED |
| 19 | Dev compose: app, postgres, meilisearch, tei, seaweedfs | Image tags are pinned below. SeaweedFS is Apache-2.0. | 02, 03, 04 | CONFIRMED with caveat C1 |
| 20 | Licenses of excelize, meilisearch-go, aws-sdk-go-v2, TEI, bge-m3 compatible with Apache-2.0 | excelize BSD-3-Clause; meilisearch-go MIT; aws-sdk-go-v2 Apache-2.0 (its NOTICE must be carried); TEI Apache-2.0; bge-m3 MIT. No GPL, LGPL, AGPL or MPL in anything checked. The Meilisearch server is `MIT AND BUSL-1.1`, and only Enterprise features (sharding, replication, S3 snapshots) are BUSL. Everything we use is MIT Community Edition. | 04 | CONFIRMED with caveat C7 |

## Caveats

- **C1: TEI image arch and memory.** `cpu-1.9.4` is amd64 only. Apple silicon
  needs `cpu-arm64-1.9.4`, so compose takes the image from an env var. At the
  default `--max-batch-tokens` (16384) the container was OOM-killed in a VM
  with 11.6 GiB free; 4096 works. After warm-up it uses about 6.2 GiB, so plan
  for at least 8 GiB.
- **C2: The latency numbers are from Apple silicon.** They were measured on
  native arm64. The amd64 image `cpu-1.9.4` was also run under emulation on
  the same Mac: Rosetta single-query p50 58.8 / p99 244 ms, QEMU TCG p50 2395 /
  p99 6010 ms (details in `03-tei-bge-m3.md`). Emulation does not represent
  real x86 server latency; it only confirms the amd64 image works and gives an
  upper-bound-ish indication. The production x86 number still needs your own
  run of `docs/research/probes/tei_bench.py <url>`.
- **C3: `normalize:false` has no effect** on the ONNX backend; the output is
  always unit-norm. Do not rely on that flag.
- **C4: Score floor and keyword lift.** The threshold must sit above 0.5 and be
  calibrated on real bge-m3 vectors through `eval`. The NO_MATCH gate is
  computed on the pure-vector score (`semanticRatio: 1.0`, empty `q`) with
  `rankingScoreThreshold` set. A hybrid keyword hit on a mismatched vector
  scored 0.689 in the probe.

  First calibration (M5, 2026-10-08): one `eval --sweep` over 68 hand-written
  questions (42 answerable, 26 not covered; 24 + 18 answerable against 46
  LLM-generated FAQs about billing, refunds, plans, roaming and installation)
  chose `KB_SEARCH_THRESHOLD_EN=0.825` and `KB_SEARCH_THRESHOLD_ZH=0.85`: the
  highest threshold that keeps recall@3 at its maximum with NO_MATCH precision
  of at least 0.9. On that sample every threshold from 0.75 to 0.825 (EN) and
  0.725 to 0.85 (ZH) had precision 1.0 and full recall, so the rule picks the
  top of that plateau: one step higher recall falls to 0.958 (EN) and 0.944
  (ZH). The trade-off is NO_MATCH recall: at 0.825 five of the 13 uncovered EN
  questions still get a (wrong) HIT, while ZH answers NO_MATCH to all 13; a
  higher threshold would catch them at the price of missing correct answers
  (EN 0.875: recall 0.917, precision 0.857). Whether a wrong HIT or a missed
  answer costs more is a business decision. Decision (2026-10-08): EN is
  raised to 0.85, because in a call a wrong spoken answer costs more than a
  NO_MATCH handed to a person; on this sample that keeps EN NO_MATCH precision
  at 1.0 with recall@3 0.958. The two score distributions
  overlap, so no threshold separates them perfectly, and
  the plateau edge is close to a cliff. **The sample is small and synthetic: the questions were
  written by the same person who saw the FAQs, the content is three short
  documents per language, and scores of real callers' speech-to-text will
  differ. Treat 0.85/0.85 as a starting point and re-calibrate on real
  questions before relying on NO_MATCH.** The sweep is `aicc-knowledge eval
  --in questions.csv --sweep`.
- **C5: Scope keys are validated in the service** against the configured
  filterable keys, before Meilisearch is called.
- **C6: Swap tasks are asynchronous.** A swap returns 202 even when an index
  does not exist; the task fails later. Swaps also queue behind indexing tasks.
  Publish must check that every build task `succeeded` before it enqueues the
  swap. It then waits for the swap task, and marks the publication `LIVE` only
  when that task has `succeeded`. `rename:true` cannot replace an index that
  already exists; that is why the first publication of a language (no live
  index yet) uses `rename:true` onto the absent live uid (02 section 8).
- **C7: Third-party notices.** NOTICE must carry the notices of aws-sdk-go-v2,
  smithy-go and prometheus/client_golang. A THIRD_PARTY_LICENSES file, built
  with `go-licenses` before the first release, must reproduce the BSD and MIT
  texts.

## Pinned versions (2026-10-08)

| Component | Version |
|---|---|
| Meilisearch | `getmeili/meilisearch:v1.54.3` |
| TEI | `ghcr.io/huggingface/text-embeddings-inference:cpu-1.9.4` (amd64), `cpu-arm64-1.9.4` (arm64) |
| Embedding model | `BAAI/bge-m3` (MIT) |
| PostgreSQL | `postgres:18-alpine` |
