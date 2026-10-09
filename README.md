# AICC Knowledge

Knowledge retrieval for the [AI Native Call Center](https://github.com/rasonyang/ai-native-callcenter) (AICC). It answers a caller's question from reviewed FAQ content and looks up exact facts (prices, limits, dates) from spreadsheets. AICC calls it as an ordinary flow HTTP tool and never depends on it.

## Try it

You need Docker. On Apple silicon set the TEI image first (see [compose.yaml](compose.yaml)).

```sh
export KB_TEI_IMAGE=ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-1.9.4   # Apple silicon only
docker compose up -d --build        # the first start downloads about 2.2 GB of model weights
docker compose run --rm app create-api-key -name demo      # prints the secret once
curl -s localhost:18490/readyz
curl -s -X POST localhost:18480/v1/search \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"query":"How do I reset my password?","language":"EN"}'
```

Until content is published, search answers 503 `INDEX_UNAVAILABLE` and fact lookup answers 404 `FACT_TABLE_NOT_FOUND`. [Operating it](#operating-it) shows how content gets there.

## What it does

- Reads `.docx` and `.xlsx` files from S3 (AWS, MinIO or SeaweedFS), tracks every version by SHA-256, and extracts text and tables.
- Generates candidate question-and-answer pairs offline with an LLM. A person reviews them in an Excel round-trip. Nothing unreviewed is ever published.
- Publishes approved Q&A to one Meilisearch index per language (`faq_en`, `faq_zh`) with bge-m3 vectors from TEI, swapping a freshly built index in atomically. Every publication is versioned and can be rolled back.
- Serves two read paths over HTTP: semantic FAQ search with a NO_MATCH threshold, and exact-match lookup in typed fact tables.

## How it fits together

PostgreSQL is the source of truth. Meilisearch is a derived index that can be rebuilt from it at any time. One binary holds the HTTP server and the admin commands:

| Command | Purpose |
|---|---|
| `serve` | HTTP API (`:8080`) and ops listener (`/metrics`, `/healthz`, `/readyz`) |
| `create-api-key` | Issue a bearer key; the database stores only its SHA-256 digest |
| `scan` | One full S3 scan (`--watch --interval 5m` to loop): new, changed and deleted files become immutable versions; prints a summary |
| `parse` | Parse discovered versions into sections and fact rows (`--retry-failed` resets `PARSE_FAILED` versions, `--watch` keeps polling); exits 1 if any version failed |
| `generate` | Draft candidate Q&A with the LLM (`--watch`; `--version <id>` works on that version's job only) |
| `export-review`, `import-review` | The Excel review round-trip; nothing is published until a person approves it |
| `publish -language EN\|ZH\|ALL [-allow-empty]` | Build a new index from the approved candidates and swap it in |
| `rollback -language EN\|ZH [-to <publicationId>]` | Make an earlier publication live again |
| `eval -in questions.csv [-language] [-url -api-key] [-json] [-sweep]` | recall@3, NO_MATCH precision and per-stage latency; `-sweep` calibrates the thresholds |
| `version` | Print the version |

The API contract is [docs/openapi.json](docs/openapi.json). Configuration is `KB_*` environment variables; [.env.example](.env.example) lists every key. The design decisions and their evidence are in [docs/research](docs/research/README.md), and the working rules are in [CLAUDE.md](CLAUDE.md).

## Operating it

The content path, in order. Every step is a subcommand of the same binary; run the ones before `publish` whenever sources change.

```sh
aicc-knowledge scan                                   # S3 -> immutable file versions
aicc-knowledge parse                                  # versions -> sections and fact rows
aicc-knowledge generate                               # sections -> candidate Q&A (LLM)
aicc-knowledge export-review -out review.xlsx         # a person sets action APPROVE / REJECT / EDIT per row
aicc-knowledge import-review -in review.xlsx -reviewer alice
aicc-knowledge publish -language ALL                  # build new indexes from the approved rows, then swap
aicc-knowledge rollback -language EN                  # swap the previous publication back in
aicc-knowledge eval -in questions.csv --sweep         # measure, and calibrate KB_SEARCH_THRESHOLD_EN/ZH
```

- **Publish** reads the approved candidates of current, non-deleted file versions in one snapshot, embeds the question and each alternate question (one vector per phrasing), builds a staging index `faq_<lang>_<id>`, waits for every Meilisearch task, checks it holds exactly the snapshot, swaps it with `faq_<lang>` and waits for the swap before marking the publication `LIVE`. Any failure marks the publication `FAILED`, deletes the staging index and leaves the live index and the `LIVE` row as they were. The first publication of a language renames the staging index onto the live uid. When a deleted source empties an index, `publish` refuses unless you pass `-allow-empty`.
- **Rollback** picks the most recently superseded publication, or the one named by `-to`. If the Meilisearch index of that publication still exists (`KB_PUBLISH_RETAIN_INDEXES` newest, default 3) and holds exactly its documents, it is swapped in; otherwise the index is rebuilt from the question, answer and scope snapshotted in `publication_items`. Run `rollback` again to go forward again.
- **Scope.** Set `KB_SEARCH_SCOPE_KEYS=brand,channel` and `KB_S3_SCOPE_PATH_TEMPLATE={brand}/{channel}`; the object `acme/web/faq.docx` (below `KB_S3_PREFIX`) is published with scope `brand=acme, channel=web`. A document without a value for a key is global for it, so a search for `brand=globex` also returns documents that name no brand. The scope is snapshotted at publish time.
- **Eval.** The question CSV has the columns `question,language,expectedIds,scope`. `expectedIds` are candidate ids separated by `;` (empty means the right answer is NO_MATCH); `scope` is optional (`brand=acme;channel=web`). recall@3 counts answerable questions with an expected id among the first three results (a NO_MATCH is a miss); NO_MATCH precision is the share of NO_MATCH answers that were right. By default `eval` searches in process with the same code as the HTTP handler; `-url` goes through a running service. `-sweep` re-runs with the threshold disabled and prints thresholds 0.50 to 0.95. The shipped thresholds come from a small synthetic sample; calibrate on real questions ([research caveat C4](docs/research/README.md)).
- **What `generate` does with a section.** A section whose text, once its boilerplate lines (short `label: value` lines such as an applicable-products line) are removed, has fewer than 20 characters is a stub. It is not sent to the LLM, because the model would invent an answer from a title; its heading and text become `Context` of the next section that is sent. A section whose heading is itself a question (ends with `？` or `?`, or starts with `Q` and a digit) is sent even with a one-word answer. Every prompt carries the document title and the heading path. An answer that states a number or a model name (`K5`, `ZQ 3S`) that the section does not contain is dropped with `UNGROUNDED_FIGURE`. Text over 6000 characters is cut with a `SECTION_TRUNCATED` warning, and `.xlsx` content is chunked by size (6000 characters, header repeated) so long rows are not cut; a single row over the limit is cut with the same warning in `parse_warnings`. A skipped, truncated or dropped section is never silent: it is logged, counted in `kb_generate_sections_total` (`SKIPPED_STUB`, `SKIPPED_NO_LANGUAGE`, `TRUNCATED`) and printed in the `generate` summary line.
- **Q&A workbooks (direct import).** A workbook whose sheets already hold curated question and answer columns can skip the LLM. Put `<name>.qa.yaml` next to `<name>.xlsx` (strict YAML; unknown fields are rejected):

  ```yaml
  sheets:
    - sheet: Support FAQ          # sheet name
      headerStartRow: 2           # optional, default 1
      headerRows: 1               # header rows; data starts below them
      question: Customer question # header text (multi-row headers: "Group / Question")
      answer: Reply
      alternates: [Other phrasings] # optional columns of alternate questions
      language: AUTO              # EN, ZH or AUTO (default: detected per row)
  ```

  Each row becomes one section (`XLSX_QA_ROW`, source reference `<objectKey>#<Sheet>!A<row>`) and, in `generate`, one `PENDING_REVIEW` candidate that is reviewed like any other. The question is taken as written (white space collapsed). The alternate questions are the cells of the `alternates` columns, one variant per line or several separated by `；` (the full-width semicolon; an ASCII `;` stays inside a variant). The answer is taken verbatim (`prompt_version` `qa-import-v1`, no model) when it passes the usual rules. When it fails only because it is too long or has line breaks or Markdown, one LLM call shortens it using only that row's answer (`qa-condense-v1`); the shortened answer may not state a number or model name the original does not. The review workbook's `sourceExcerpt` shows the original answer. A row that cannot become a candidate (empty question or answer, a URL, a language that is not the row's, is skipped with a warning (`QA_ROW_INCOMPLETE` at parse, a `Q&A row dropped` log line and `QA_DROPPED` at generate). A row whose condensation fails (still too long, a figure the original does not state, a cut-off or invalid model answer) is never dropped: it becomes a `PENDING_REVIEW` candidate with the original answer verbatim, the flag `NEEDS_SHORTENING` (migration 00015), `prompt_version` `qa-import-v1` and no model (the text is the sheet's own), a `Q&A answer kept for shortening` log line and `QA_NEEDS_SHORTENING` in `kb_generate_sections_total` and the summary. In the review workbook the `flags` column shows `NEEDS_SHORTENING` and the answer is the original. The reviewer must set `EDIT` and shorten the answer to the usual limits: `APPROVE` is refused with `SHORTENING_REQUIRED` (nothing is applied), an `EDIT` that is still too long is refused with `INVALID_EDIT`, and a valid `EDIT` approves the row with the flag removed (`CONTAINS_FIGURES` is recomputed). `REJECT` works as usual. Publish takes only `APPROVED` candidates, so a flagged row never reaches an index unedited; hidden rows are skipped with `HIDDEN_ROW_SKIPPED`. The sheets a `.qa.yaml` covers are not content sections; other sheets still are. One workbook may have both a `.facts.yaml` and a `.qa.yaml` for different sheets; a sheet named in both fails the workbook with `QA_SHEET_CONFLICT`, and a sheet or header that is not in the workbook fails it with `QA_INVALID` (the mapping stays `PARSED`). Whichever of workbook and mapping is parsed last does the import. Editing the workbook makes a new version (its candidates become `STALE`); editing the mapping stales the candidates of the rows whose text changed (`QA_ROW_CHANGED`) and `generate` tops up only those rows.
- **Product catalog (`products.yaml`).** A vector search happily answers a question about one product model with the FAQ of another, and generic FAQs match off-topic questions. A product catalog lets the searcher refuse both. Put exactly one file named `products.yaml` at the root of `KB_S3_PREFIX` (strict YAML; unknown fields are rejected):

  ```yaml
  products:
    - id: zq-3                  # stable id, [a-z0-9-]
      names: ["ZQ 3", "ZQ三"]   # aliases; full-width, case, hyphens and spaces do not matter
      compatibleWith: [zq-3s]   # optional; symmetric: a ZQ 3 question may be answered by a ZQ 3S FAQ and the other way round
    - id: zq-3s
      names: ["ZQ 3S"]
  ```

  Scan versions it like any source file and `parse` validates it (`PARSED`, or `PARSE_FAILED` with `CATALOG_INVALID` and one warning per problem: duplicate ids, a name that normalizes the same for two products, `compatibleWith` naming an unknown product, an empty file). A `products.yaml` anywhere but the root fails `CATALOG_MISPLACED` and is ignored: there is exactly one catalog. `publish` tags every FAQ with the products its question (or alternate questions) names, else those named by its source file's name (directories do not count), else none (a generic FAQ), and records the catalog version on the publication, so `rollback` serves the product tags the publication was published with. With no `products.yaml` the tags are empty and search behaves as it does without a catalog. If `products.yaml` exists but did not parse, `publish` refuses (`CATALOG_UNAVAILABLE`) instead of silently publishing untagged; fix the file or remove it. A catalog change takes effect with the next publish.

  When the live publication of a language has a catalog, `/v1/search` applies three rules to the over-fetched hits before the threshold and `topK`: **R1** if the question names a catalog product, a hit about other products only is dropped (a compatible product counts as named); **R2** if the question contains a model-like token (a letter-first token with digits: `K5`, `ZQ 9`) of a product family the catalog knows that no alias covers, the answer is `NO_MATCH` (unknown model). A family is the leading Latin word of an alias (`zq` in `ZQ 3`, `nova` in `Nova K2`); the token's own letters (`ZQ` in `ZQ 9`) or the Latin word right before it (`Nova` in `Nova K9`) must be one. Tokens of other families (`iOS17`, `USB3`, `wifi6`, `mp4`, `Android 14`) are never unknown models; **R3** a generic hit needs `threshold + KB_SEARCH_GENERIC_MARGIN_<lang>` (default 0.04 ZH, 0 EN). Names are matched longest first on alphanumeric boundaries, after NFKC normalization and with Chinese numerals after a Latin word read as digits (`ZQ三S` is `ZQ 3S`), so `ZQ 3` is not found in `ZQ 3S`; lists such as `ZQ 3/3S`, `A、B` and `A和B` name several products. A lower-case model name with a space (`zq 9`) counts as model-like when its letters are a known family. A bare family alias (an alias that is only the family word, such as `ZQ` or `Nova`, for a series product) does not hide an unlisted model: when it is directly followed by a model suffix (digits with up to two letters or a word such as `Pro`, `Air`: `ZQ 5`, `ZQ五`, `ZQ-5S`, `Nova 7 Pro`) that no longer alias covers, that span is an unknown model (R2, `NO_MATCH`) and not the series. `ZQ 3`, `ZQ 3S` and `ZQ Ultra` still match as listed, `ZQ 系列` and a lone `ZQ` name the series, and `ZQ 怎么充电` is the series too. `serve` keeps the catalog of each language's live publication in memory (loaded at startup and every `KB_PRODUCTS_REFRESH_SEC`, default 30); a search never queries PostgreSQL. After a publish or rollback that changes the catalog, questions are read with the previous catalog's names for up to that long; the product tags travel with the documents and switch with the index swap. `kb_product_guard_total{language,outcome}` counts `dropped_disjoint`, `unknown_model` and `generic_below_margin`. `eval` (with `KB_DATABASE_URL` set) uses the live catalog, reports the guard counts, and `--sweep` also sweeps the generic margin over 0, 0.02, 0.04 and 0.06.
- **Local development.** Needs TEI (`KB_TEI_URL`) and Meilisearch (`KB_MEILI_URL`) on top of PostgreSQL. `KB_TEI_BATCH_URL` points publish at a second TEI so a long publish does not queue in front of query embeddings.

## Building

```sh
make dev-up      # PostgreSQL, Meilisearch and SeaweedFS (S3) for the tests
export KB_TEST_DATABASE_URL='postgres://kb:kb@127.0.0.1:15432/kb?sslmode=disable'
export KB_TEST_MEILI_URL=http://127.0.0.1:17700 KB_TEST_MEILI_API_KEY=dev-master-key-0123456789
export KB_TEST_S3_ENDPOINT=http://127.0.0.1:18333 KB_TEST_S3_BUCKET=aicc-knowledge
export KB_TEST_S3_ACCESS_KEY_ID=dev KB_TEST_S3_SECRET_ACCESS_KEY=dev-secret   # the dev SeaweedFS has no identities and accepts any key
make build       # bin/aicc-knowledge
make test        # go test -race ./...
make test-gate   # what CI runs: also fails on any skipped test outside .ci-allowed-skips.txt (needs jq, KB_TEST_TEI_URL)
make lint        # go vet and gofmt
make generate    # sqlc
make api-check   # contract lint and generated code is current
```

## Benchmarking on target hardware

Latency figures from a development machine (Apple M3, arm64 TEI image) are not production numbers: TEI's CPU kernels, the model backend and the memory bandwidth all differ on an Intel server. Run the benchmark on the host that will serve traffic.

```sh
make bench                      # needs Docker (and Go, or a prebuilt kb-bench; see below)
BENCH_RERANKERS="BAAI/bge-reranker-v2-m3 BAAI/bge-reranker-base" make bench   # also the small reranker
make bench-compare A=bench-results/<host-a> B=bench-results/<host-b>          # numeric drift between two hosts
```

`scripts/bench/bench.sh` starts one TEI container at a time (`cpu-1.9.4` on x86_64, `cpu-arm64-1.9.4` on arm64; override with `BENCH_IMAGE`), waits on `/info`, and drives it with `cmd/kb-bench`. The first run downloads the model weights into `BENCH_CACHE` (default `~/.cache/kb-bench`). It records:

- host facts: architecture, CPU model and flags (avx2, avx512, amx, fma, ...), cores, RAM, Docker version, image digests;
- startup and warm-up time per model;
- client-side p50/p90/p99/max of a single-query embedding (n=200), a batch of 8, and a rerank of 10 and 20 passages for question-only and question+answer passages (n=100 each), plus throughput at concurrency 2 and 4;
- a numeric fingerprint: embeddings of 50 fixed sentences and raw rerank scores of 4 fixed sets, so `compare` can report max and mean absolute difference, minimum cosine, and whether the rerank top-1 and top-3 order match across architectures.

Results go to `bench-results/<host>-<timestamp>/` (`host.json`, one JSON per model, `summary.md`); the directory is git-ignored. The workload is synthetic: call-center-shaped Chinese and English questions and answers about invented products, generated deterministically by the program. No real data is involved.

On a server without Go, build a static binary elsewhere and copy it with the script:

```sh
make bench-dist                 # bin/kb-bench-linux-amd64
scp bin/kb-bench-linux-amd64 server:kb-bench
scp scripts/bench/bench.sh server:bench.sh
ssh server 'BENCH_BIN=./kb-bench bash bench.sh'
```

## Status

Milestones M1 (scaffold), M2 (scan), M3 (parse and structured facts), M4 (generate and review) and M5 (publish, rollback, search and eval) are done; see [TASKS.md](TASKS.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
