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
| `generate` | Draft candidate Q&A with the LLM (`--watch`, `--version <id>`) |
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
make lint        # go vet and gofmt
make generate    # sqlc
make api-check   # contract lint and generated code is current
```

## Status

Milestones M1 (scaffold), M2 (scan), M3 (parse and structured facts), M4 (generate and review) and M5 (publish, rollback, search and eval) are done; see [TASKS.md](TASKS.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
