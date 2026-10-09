# AICC conventions research (read-only). Paths relative to the AICC repo root (github.com/rasonyang/ai-native-callcenter)

## 1. CLAUDE.md / AGENTS.md / CONTRIBUTING.md
- CLAUDE.md and AGENTS.md are identical except line 1; "carry the same text; change them together" (CLAUDE.md:3).
- SPDX: "new Go, SQL, Lua and script files start with an `SPDX-License-Identifier: Apache-2.0` line" (CLAUDE.md:7). Go: `// SPDX-License-Identifier: Apache-2.0`; SQL: `-- SPDX-...`; Makefile/yml/sh: `# SPDX-...`.
- Naming spec is mandatory: "Go `CallID` <-> JSON `callId` <-> TS `callId` <-> DB `call_id`; SCREAMING_SNAKE enum values byte-identical across JSON/TS/DB; `xxxAt`/`xxxMs`/`xxxSec`; `is_`/`has_` booleans; no upstream ... tokens outside boundary layers" (CLAUDE.md:9; full text docs/design/07-naming.md:1-60).
- API is the product: no route the contract does not declare; every op routed (CLAUDE.md:15); auth is scopes, not role guards (CLAUDE.md:17).
- Language: "Commit messages, code comments and documentation are in English" (CLAUDE.md:21).
- Tests: always `-race` (CLAUDE.md:27). DB tests skip unless `AICC_TEST_DATABASE_URL` set; CI sets it (CLAUDE.md:56). "A migration is not reviewed until it has run" - store tests apply all migrations from zero, roll back, re-apply, and migrate DBs with rows; a migration narrowing a CHECK must rewrite rows first; an enum-changing migration gets a fixture test in `migrate_test.go` / `migrate_*_test.go` (CLAUDE.md:62).
- Config: `AICC_*` env; `.env` loaded, real env wins; `.env.example` is the registry "kept in step with internal/config/config.go"; empty value = unset; no inline-comment syntax (CLAUDE.md:66).
- Spec-first (CLAUDE.md:68-85): edit contract -> generate -> implement -> test. Never code-first OpenAPI tooling; generated files committed, `DO NOT EDIT`; `scripts/api-generate.sh` is the only generation entry (79). `httpapi.Server` must implement generated `api.ServerInterface`, asserted in `internal/httpapi/api_server.go:19` (`var _ api.ServerInterface = (*Server)(nil)`) (80). Go initialisms via `name-normalizer` + `additional-initialisms`, not `x-go-name` (81). sqlc models / store types are never API types; handlers map to `api.*` at the boundary (83). Every route mounted via generated wrapper `s.apiWrapper()`; params arrive parsed; `api_server.go` holds only assertion, wrapper, `writeParamError` (85).
- Metric names live in one file (`internal/obs/callmetrics.go`) (CLAUDE.md:146). No perf claims in docs/commits (CLAUDE.md:142).
- "Related repositories ... read them for lineage, never import them" (CLAUDE.md:150).
- Commit/branch conventions: NOT FOUND in docs. Observed in git log: subject `pkg: lowercase statement` (e.g. `cdr: an agent's call to a bot number is the agent's call`, `make: warn when test skips the database tests`, `docs: drop stale ...`), merged via PR merge commits ("Merge pull request #108 from rasonyang/rasonyang/<long-slug>"); branches `rasonyang/<slug>` or `docs/<slug>`, `ci-...`.
- CONTRIBUTING.md:1-42 only covers "Integrations live outside the tree" (no vendor connectors in tree; integration = a service behind HTTP tools + REST API); build instructions point to README `#building` (CONTRIBUTING.md:3).

## 2. Makefile (Makefile, 160 lines)
- Header `# SPDX-License-Identifier: Apache-2.0`, `.DEFAULT_GOAL := help`, `SHELL := /bin/bash` (1-3). Self-documenting `help` via `## ` comments (9-11):
```make
.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-16s %s\n", $$1, $$2}'
```
- Targets: dev-up/dev-down (docker compose -f deploy/dev/docker-compose.yml) (13-19); stack-up/stack-down/stack-logs/stack-config (21-62); installer-check (41); image (`docker build --build-arg VERSION=$(VERSION) -t aicc:...`) (64); fs-image/fs-push; `generate: sqlc generate` (77-79); api-lint/api-generate/api-check/api-breaking (81-110); build/run (112-118); test (125); lint (140); web-*; clean.
```make
generate: ## Regenerate sqlc query code
	sqlc generate
REDOCLY := $(WEB)/node_modules/.bin/redocly
api-lint: ## Lint the API contract (docs/openapi.json)
	$(REDOCLY) lint docs/openapi.json
api-generate: ## Regenerate Go + TS API code from the contract
	scripts/api-generate.sh
api-check: api-lint ## CI gate: contract lints and committed generated code matches it
	scripts/api-generate.sh
	git diff --exit-code -- internal/api web/src/generated
	@test -z "$$(git status --porcelain -- internal/api web/src/generated)" \
		|| { git status --short -- internal/api web/src/generated; echo 'untracked generated files'; exit 1; }
BASE ?= main
API_BREAKING_IGNORE ?= .oasdiff-breaking-ignore.txt
api-breaking: ## Fail on undeclared breaking API changes vs BASE (default main)
	@base_spec=$$(mktemp); \
	if git show $(BASE):docs/openapi.json > $$base_spec 2>/dev/null; then \
		go tool oasdiff breaking --fail-on ERR --err-ignore $(API_BREAKING_IGNORE) $$base_spec docs/openapi.json; status=$$?; \
	else echo "no contract on $(BASE); nothing to compare"; status=0; fi; \
	rm -f $$base_spec; exit $$status
lint: ## Vet Go code and lint the frontend
	go vet ./...
	gofmt -l . | grep -v node_modules && exit 1 || true
	cd $(WEB) && npx oxlint .
```
- `make test` (125-138): `go test -race ./...` (+ web), then prints a loud WARNING box if `AICC_TEST_DATABASE_URL` is unset ("every database test was SKIPPED ... To run them here: start PostgreSQL (make dev-up), then: AICC_TEST_DATABASE_URL='postgres://aicc:aicc@127.0.0.1:5432/aicc?sslmode=disable' make test"). `TEST_DATABASE_URL` var at :123.
- No golangci-lint anywhere (grep of Makefile/.github: NOT FOUND) despite 07-naming.md:"staticcheck ST1003 + golangci-lint in CI" (docs/design/07-naming.md, Enforcement para.) - the doc is aspirational; lint = `go vet` + `gofmt -l`. No `migrate` make target (migrations run at startup). `generate` runs only sqlc; there is NO CI check that sqlc output is current (NOT FOUND - sqlc not in ci.yml).

## 3. CI workflows
### .github/workflows/api.yml (42 lines) - name `api-contract`
- on: pull_request; push branches [main] (8-11). One job `contract`, ubuntu-latest (14-15).
- Steps: `actions/checkout@v7`; `actions/setup-go@v7` with `go-version-file: go.mod` (19-21); `actions/setup-node@v7` node 22, npm cache `web/package-lock.json` (23-27); `npm ci` in `web` (29-31, installs redocly + openapi-typescript); `make api-check` (33-34); then on PR only:
```yaml
      - name: No undeclared breaking changes
        if: github.event_name == 'pull_request'
        run: |
          git fetch origin "$BASE_REF" --depth=1
          make api-breaking BASE="origin/$BASE_REF"
        env:
          BASE_REF: ${{ github.base_ref }}
```
- Tools are pinned by go.mod `tool (...)` block (go.mod:113-116: `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`, `github.com/oasdiff/oasdiff`), invoked as `go tool oapi-codegen` / `go tool oasdiff`. Versions: oapi-codegen/v2 v2.8.0 (go.mod:59), oasdiff v1.28.0 (go.mod:60), oapi-codegen/runtime v1.6.0. Redocly version is in web/package.json (not read; NOT in Go). sqlc is NOT a go tool and not pinned in the repo (NOT FOUND) - Makefile just calls `sqlc generate`.
### .github/workflows/ci.yml (131 lines) - name `ci`
- Triggers ignore markdown-only changes: `paths: ['**', '!**/*.md']` on PR and push main (14-23); `concurrency: group: ci-${{ github.ref }}, cancel-in-progress: true` (25-27).
- Job `go` (30-101), Postgres as a GitHub service container (44-57):
```yaml
    services:
      postgres:
        image: postgres:18
        env: { POSTGRES_USER: aicc, POSTGRES_PASSWORD: aicc, POSTGRES_DB: aicc }
        ports: ['5432:5432']
        options: >-
          --health-cmd "pg_isready -U aicc"
          --health-interval 5s
          --health-timeout 5s
          --health-retries 10
    env:
      AICC_TEST_DATABASE_URL: postgres://aicc:aicc@127.0.0.1:5432/aicc?sslmode=disable
```
  Comment (34-43): same PG major as deploy/dev; DB tests silently skipped before this was added. Steps: checkout@v7, setup-go@v7 (`go-version-file: go.mod`, `cache: true`), `go build ./...`, `go vet ./...`, gofmt check (`unformatted=$(gofmt -l cmd internal web)` -> fail), `go test -race ./...`, allocation benchmarks (AICC-specific, skip).
- Job `web` (103-130): node 22, `npx oxlint .`, `npm run test`, `npm run build` (AICC-specific; skip for Go-only service).
- Integration tests use a real Postgres via the service container + `internal/testdb.ScratchDSN` (creates `aicc_<prefix>_<UnixNano>` DB per test, drops WITH (FORCE) on cleanup, skips if env unset): internal/testdb/testdb.go:22-57. NO testcontainers (go.mod has none).
### .github/workflows/release.yml (360 lines)
- Triggers: `push: tags: ["v*"]` + workflow_dispatch dry run (57-60). Jobs: deploy-tags (checks installer placeholders / deploy docs name the tag), image (docker/setup-buildx-action@v4, docker/login-action@v4, docker/metadata-action@v6, docker/build-push-action@v7; builds linux/amd64 first to check `--version` stamp, then multi-arch push), switch (matrix, AICC-specific), switch-manifest, bundle-dry-run, publish (`softprops/action-gh-release@v3`, notes from `scripts/release-notes.sh "${GITHUB_REF_NAME}"`: CHANGELOG section for the tag else commit subjects). Tags: exact git tag only, no `latest` (36-38). Pre-release iff tag has a hyphen (48-50). Secrets: DOCKERHUB_USERNAME, DOCKERHUB_TOKEN (40-43). actions/upload-artifact@v6, download-artifact@v7.
- No golangci-lint, no dependabot, no CodeQL: NOT FOUND (.github has only ISSUE_TEMPLATE/config.yml + 3 workflows).

## 4. OpenAPI
- Spec: `docs/openapi.json`, OpenAPI 3.1.0 (info.title "AI Native Call Center API", version 1.0.0, license Apache-2.0, servers `[{url: /api/v1}]`). Root keys: openapi, info, servers, x-scopes, tags, paths (71), components. Embedded for serving by `docs/embed.go` (`//go:embed openapi.json` -> `docs.Contract []byte`).
- `redocly.yaml` (7 lines):
```yaml
extends:
  - recommended
```
  Exceptions in `.redocly.lint-ignore.yaml:3-21` (keyed by `docs/openapi.json:` -> rule -> list of `#/...` pointers; used for `no-unused-components` and `operation-4xx-response`, each with a prose reason). Lint is `redocly lint docs/openapi.json` with zero errors/warnings (CLAUDE.md:73). No custom rules.
- `oapi-codegen.yaml` (29 lines), full:
```yaml
# SPDX-License-Identifier: Apache-2.0
package: api
generate:
  models: true
  chi-server: true
output: internal/api/api.gen.go
output-options:
  name-normalizer: ToCamelCaseWithInitialisms
  additional-initialisms: [AI, CDR, CDRs, DID, DIDs, DTMF, IDs, SSE]
compatibility:
  always-prefix-enum-values: true
```
  (no `strict-server`; handlers implement `api.ServerInterface` with `(w, r, params)` and write the envelope by hand.) Generation script `scripts/api-generate.sh`: `go tool oapi-codegen -config oapi-codegen.yaml docs/openapi.json`, then prepends `// SPDX-License-Identifier: Apache-2.0\n\n` to api.gen.go (scripts/api-generate.sh:17-24), `gofmt -w`, then TS (openapi-typescript) and two node scripts that generate scopes/opsecurity tables (web-specific, skip or replicate for scopes). `internal/api/generate.go:` `//go:generate ../../scripts/api-generate.sh`. Generated files: internal/api/{api.gen.go,scopes.gen.go,opsecurity.gen.go}.
- Contract-level conventions (info.description of openapi.json): lowerCamelCase properties, SCREAMING_SNAKE enums, `xxxAt` RFC 3339, `xxxSec/xxxMs`, `is/has` booleans; errors always `{"error": {code, message, params}}`, including router 404/405; operationIds are lowerCamelCase verbs (`login`, `getMe`, `listCalls`, `createCall`, `updateAgent`, `agentReady`); paths kebab-case plural, query params camelCase (docs/design/07-naming.md URL paths row). Security schemes: `cookieSession` (apiKey cookie `aicc_session`), `csrfHeader` (apiKey header `X-AICC-Csrf`), `apiKeyBearer` (`type: http, scheme: bearer`). Scopes: root `x-scopes`, `resource:action[:range]` (`calls:read:own`, `keys:manage`). Reusable responses in components.responses: BadRequest, Unauthorized, Forbidden, NotFound, Conflict, UnprocessableEntity, InternalError, BadGateway, ServiceUnavailable - each `description: "... Codes X, Y."` with `content: application/json: schema: $ref ErrorResponse`.
- Gate tests (Go tests asserting the spec/contract):
  - `internal/httpapi/contract_gate_test.go`: `TestEveryMountedRouteDeclaresItsAuthorization` (:63), `TestOneErrorCodeIsSpelledTheSameEverywhere` (:141; contract ErrorCode enum == Go `Code*` constants parsed from errors.go via go/ast == web en/zh translation keys), `TestTheUntranslatableKeysAreStillThere` (:277), `TestASystemCanReachWhatAPersonCan` (:312), `TestEveryContractOperationHasASecurityRow` (:351). Helpers read `../../docs/openapi.json` with a minimal struct (`contractErrorCodes`, :~165-190) and parse errors.go with go/parser (`goErrorCodes`).
  - `internal/httpapi/routes_test.go:36` `TestEveryContractOperationIsRouted` (reflects on `api.ServerInterface`, ast-parses server.go for `op.X`/`s.X`; `mountedElsewhere = []string{"StreamEvents"}` registry).
  - `internal/store/naming_test.go:14` `TestLedgerTypesMarshalPerTheNamingSpec` - marshals zero values and fails if any JSON key is empty, starts uppercase, or contains `_`.
  - NOT FOUND: any test asserting enum casing across the whole spec, lowerCamelCase of every schema property, or operationId conventions. Those are by review only; the scaffold may ADD such gates (new, not mirrored).
- oasdiff breaking check: `make api-breaking BASE=origin/<base>`: `go tool oasdiff breaking --fail-on ERR --err-ignore .oasdiff-breaking-ignore.txt <base-spec> docs/openapi.json` (Makefile:102-110). Ignore file format (`.oasdiff-breaking-ignore.txt:1-8`): comment lines with date + reason, then one pinned line copied verbatim from oasdiff output, e.g. `api path removed without deprecation POST /calls/dial`. CI runs only on PRs, after `git fetch origin "$BASE_REF" --depth=1` (api.yml:36-42).

## 5. Error response shape
- Wire (docs/openapi.json components.schemas, verbatim):
```json
"ErrorResponse": {"type":"object","properties":{"error":{"$ref":"#/components/schemas/Error"}},"required":["error"]},
"Error": {"type":"object","description":"The single error envelope body: an error code plus interpolation params. Message is diagnostic English, never shown to end users.",
  "properties":{"code":{"$ref":"#/components/schemas/ErrorCode"},"message":{"type":"string"},
    "params":{"type":"object","description":"Interpolation parameters for the localized message, e.g. {\"field\": \"extensionNumber\"}.","additionalProperties":true}},
  "required":["code","message"]},
"ErrorCode": {"type":"string","description":"Machine-readable, translatable failure identifier. The frontend renders errors.<CODE>; the backend never localizes.","enum":[...]}
```
  Body example: `{"error":{"code":"VALIDATION_FAILED","message":"...","params":{"rule":"...","field":"..."}}}`.
- Current ErrorCode enum (28): INVALID_CREDENTIALS, SESSION_EXPIRED, FORBIDDEN, AGENT_REQUIRED, AGENT_IMPERSONATION_NOT_ALLOWED, INSUFFICIENT_SCOPE, VALIDATION_FAILED, TERMINAL_ANNOUNCE_REQUIRED, USER_DATA_TOO_LARGE, NOT_FOUND, METHOD_NOT_ALLOWED, CONFLICT, EXTENSION_IN_USE, EXTENSION_ASSIGNED_TO_AGENT, LAST_ADMIN, EXTENSION_POOL_EXHAUSTED, AGENT_ALREADY_LOGGED_IN, AGENT_NOT_LOGGED_IN, AGENT_NOT_IN_WRAP_UP, DEVICE_NOT_REGISTERED, CALL_NOT_FOUND, NOT_CALL_PARTY, OPERATION_NOT_ALLOWED_FOR_CALL_TYPE, USER_SUSPENDED, SWITCH_DOWN, STORAGE_DOWN, RATE_LIMITED, INTERNAL. Naming: SCREAMING_SNAKE, domain-prefixed (`CALL_NOT_FOUND`), generic ones for shared (`NOT_FOUND`, `CONFLICT`, `VALIDATION_FAILED`, `INTERNAL`) (07-naming.md API error codes row).
- Go side: `internal/httpapi/errors.go`: `type ErrorCode string` (:17), consts `CodeXxx ErrorCode = "XXX"` (:20-85), `type APIError struct{Code ErrorCode `json:"code"`; Message string `json:"message"`; Params map[string]any `json:"params,omitempty"`}` (:88-92), `errorEnvelope{Error APIError `json:"error"`}` (:94), `writeJSON` sets `Content-Type: application/json; charset=utf-8` (:99-108), `writeError(w, status, code, message, params)` (:111), `isUniqueViolation` = pgconn code 23505 (:117), `violatesConstraint(err, name)` matches 23001/23503 on a named constraint (:132-138).
- Status mapping is a per-handler `switch` on `errors.Is/As`, no central table. Example (internal/httpapi/catalog_handlers.go:~213-243): validation error -> 422 `VALIDATION_FAILED` with params {rule, field}; `catalog.ErrNotFound` -> 404 `NOT_FOUND`; unique violation -> 409 `CONFLICT`; default -> `slog.ErrorContext(...)` + 503 `STORAGE_DOWN`. Param parse errors from generated wrapper -> 400 `VALIDATION_FAILED` with params.field (`writeParamError`, api_server.go:39-63). Router 404/405 also use the envelope (openapi info.description). Auth codes: 401 INVALID_CREDENTIALS/SESSION_EXPIRED, 403 FORBIDDEN/INSUFFICIENT_SCOPE. Domain sentinel errors live in the domain packages (e.g. `catalog.ErrNotFound`, `*catalog.ValidationError{Rule, Field}`), handlers translate.

## 6. Enum conventions
- Rule (docs/design/07-naming.md section 2/4): Go = named string type, const name = TypeName+PascalState, value SCREAMING_SNAKE; no iota enums. Example: `type Role string` + `RoleAdmin Role = "ADMIN"` (internal/auth/auth.go:23,29); `type ErrorCode string` + `CodeNotFound ErrorCode = "NOT_FOUND"` (errors.go:17,50). Generated enums get uniform type-prefixed consts via `always-prefix-enum-values: true` (oapi-codegen.yaml:26-29).
- DB: varchar(N) + inline CHECK, NOT native enum, NOT text; values byte-identical to JSON (07-naming.md section 4). Example (internal/store/migrations/00001_foundation.sql:14-15): `role varchar(16) NOT NULL CHECK (role IN ('AGENT', 'SUPERVISOR', 'ADMIN'))`, `status varchar(16) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','SUSPENDED'))`. TS: string-literal unions, `enum` keyword forbidden.
- Changing allowed values: new migration that rewrites old rows first (Down narrows -> rewrite; e.g. 00032_device_lost_is_a_reason.sql) + fixture test (see section 7).
- DB naming: tables plural snake_case; PK `id` (uuid); FK `<singular>_id`; booleans `is_/has_`; `xxx_at timestamptz`; `xxx_ms/_sec`; indexes `idx_<table>_<cols>`, unique `uq_<table>_<cols>`, FK `fk_<table>_<referenced_table>` (07-naming.md section 4; 00001_foundation.sql:16-45 shows `uq_users_username`, `fk_sessions_users`, `idx_sessions_user_id`). JSONB keys lowerCamelCase.

## 7. Migrations and sqlc
- goose v3 (`github.com/pressly/goose/v3 v3.27.3`, go.mod:14), SQL migrations in `internal/store/migrations/`, named `NNNNN_snake_case_sentence.sql`, 5-digit zero-padded (`00001_foundation.sql` ... `00034_a_claim_with_no_tool_behind_it.sql`). File format: SPDX comment line, prose header explaining WHY, then `-- +goose Up` ... `-- +goose Down` (00001_foundation.sql:1-8, 55+). Down is always written and must be reversible (tests roll back all).
- Embedded: `//go:embed migrations/*.sql` + `var migrationFS embed.FS` (internal/store/store.go:24-25). Run AT SERVER STARTUP: `st.Migrate(ctx)` (cmd/aicc/main.go:113; store.go:84-96) using `goose.SetBaseFS(migrationFS); goose.SetLogger(goose.NopLogger()); goose.SetDialect("postgres"); goose.UpContext(ctx, stdlib.OpenDBFromPool(s.Pool), "migrations")`. `MigrationState{DBVersion, LatestVersion, HasPending}` via `MigrationStatus` (goose.NewProvider + WithDisableGlobalRegistry) feeds /readyz. Single-instance guard: advisory lock `0x41494343` on a dedicated connection (store.go:~28-37). Separate advisory key for allocators.
- Migration tests: `internal/store/migrate_test.go` + `migrate_*_test.go` (up from zero, down all, up again, fixture rows through a version boundary); use `testdb.ScratchDSN`.
- `sqlc.yaml` (verbatim, 30 lines):
```yaml
version: "2"
sql:
  - engine: postgresql
    schema: internal/store/migrations      # schema read straight from goose files
    queries: internal/store/sql
    gen:
      go:
        package: queries
        out: internal/store/queries
        sql_package: pgx/v5
        emit_json_tags: true
        emit_pointers_for_null_types: true
        emit_empty_slices: true
        json_tags_case_style: camel
        rename: {ip: "IP", url: "URL", sip_url: "SIPURL"}
        overrides:
          - db_type: "uuid"
            go_type: "github.com/google/uuid.UUID"
          - db_type: "uuid"
            nullable: true
            go_type: {type: "UUID", import: "github.com/google/uuid", pointer: true}
```
- Query files: `internal/store/sql/<area>.sql` (agents.sql, api_keys.sql, sessions.sql, users.sql ...), each begins `-- SPDX-License-Identifier: Apache-2.0`, then `-- name: CreateAgent :one` / `:many` / `:execrows`; plain `RETURNING *`. Store wraps queries: `type Store struct{Pool *pgxpool.Pool; Queries *queries.Queries}`; per-area stores (`APIKeyStore`, ...) in internal/store/*store.go convert rows -> domain types (e.g. `apiKeyFrom(row)`). pgxpool config: MaxConns from config, MaxConnLifetime 1h, HealthCheckPeriod 30s, Ping on Open (store.go:59-79).

## 8. Config registry
- Hand-rolled, no library: `internal/config/config.go` (590 lines). `Load()` (:272) calls `loadDotEnv(".env")` (:556: reads KEY=VALUE, skips blank/`#`, `strings.Cut` on first `=`, trims quotes, never overrides existing env) then builds `Config{...}` with helpers `env(key, def)` (:505; empty value = unset -> default), `envInt`, `envBool`, `envDuration`, `envDurationIfSet` (unset vs zero). Then `c.validate()` (:357) returns errors (fail-fast at startup). Struct fields Go-style (`HTTPAddr`, `DatabaseURL`, `IsBotEnabled`); booleans prefixed `Is`; `func (c Config) IsDev() bool { return c.Env == "dev" }`.
```go
func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" { return v }
	return def
}
```
- Prefix `AICC_` (service-specific: `AICC_ENV` dev|prod, `AICC_HTTP_ADDR=:8080`, `AICC_METRICS_ADDR=127.0.0.1:9090`, `AICC_DATABASE_URL=postgres://aicc:aicc@127.0.0.1:5432/aicc?sslmode=disable`, `AICC_DATABASE_MAX_CONNS=10`, `AICC_LOG_LEVEL=info`, `AICC_LOG_DIR`, `AICC_OTLP_ENDPOINT=` empty = tracing off, `AICC_SERVICE_NAME=aicc`) (config.go:~273-329; .env.example:27-39,363-369). Third-party provider keys are NOT AICC_-prefixed (CLAUDE.md:66).
- `.env.example` (426 lines) header (verbatim, :1-32), followed by sections with `#\n# Section name\n#` banner and every setting commented out with its real default:
```
# SPDX-License-Identifier: Apache-2.0
#
# Every runtime setting of the aicc server, with its built-in default.
#
#   cp .env.example .env   # then uncomment only what this deployment changes
#
# The server reads .env from its working directory at startup and never
# overrides a real environment variable with it (internal/config/config.go).
# Lines beginning with # are ignored; surrounding single or double quotes are
# stripped from values. There is no inline-comment syntax - everything after
# the first = is the value, so keep notes on their own line.
#
# Every line below is commented out and shows the value the server already
# uses, so a copied file changes nothing until something is uncommented - and
# a default that changes in a later release still reaches this deployment.
#
# **An empty value means "unset"**: the server falls back to the default, it
# does not read it as "blank". ...

#
# Process
#
# AICC_ENV must be dev or prod. dev logs as text and is more verbose; prod logs
# JSON. ...
#AICC_ENV=dev
# The API, the event stream and the embedded SPA.
#AICC_HTTP_ADDR=:8080
#
# Database - PostgreSQL 18. Migrations run at startup.
#
#AICC_DATABASE_URL=postgres://aicc:aicc@127.0.0.1:5432/aicc?sslmode=disable
# Pool size. Must be >= 1 or the server refuses to start.
#AICC_DATABASE_MAX_CONNS=10
```
  Note the commented-out `#KEY=default` style (a comment line above each setting, then `#KEY=value`).
- Test asserting .env.example matches the config struct: NOT FOUND (no Go test references `.env.example`; CLAUDE.md:66 only asserts it by convention). `internal/config/config_test.go` tests: TestLoadDefaults (:12), TestLoadOverrides (:44), TestBotMediaTimeouts, TestParsePeers (:132), TestValidate (:177). Also separate `deploy/.env.example` (compose-level settings; checked by release.yml deploy-tags job, :106).

## 9. Project layout, logging, OTel, metrics, health, auth
- `cmd/aicc/` (main.go, wiring.go, doctor.go, version.go, useradd.go, passwd.go, flowadd.go, storewait.go + tests, composition_test.go) plus `cmd/aicc-loadgen`, `cmd/aicc-mockbackend`, `cmd/aicc-mockprovider`. Subcommand-style single binary (useradd, passwd, flowadd, doctor, version). `main.version` stamped via `-ldflags "-X main.version=${VERSION}"` (Dockerfile:50).
- `internal/` packages (single lowercase word, no underscores): agents, aicall, api (generated), auth, catalog, config, esl, events, flow, httpapi, loadgen, media, mockprovider, obs, outbound, provider, recording, seed, sipsession, store (+ store/migrations, store/sql, store/queries), streamin, telephony, testdb, transcribe, transcript, voice, webhook. `docs/` is a Go package too (docs/embed.go). File names snake_case (07-naming.md section 2).
- Logging/OTel/metrics: `internal/obs/obs.go`. `obs.Setup(ctx, serviceName, logLevel, otlpEndpoint, logDir string, dev bool) (*Providers, error)` (:50): installs `slog.SetDefault(newLogger(...))` (:114: TextHandler in dev, JSONHandler in prod, wrapped in `traceHandler` that adds traceId/spanId); logs to stderr + optional per-start file `aicc-YYYYMMDD-HHMMSS.log` in logDir (:126); OTel resource via `resource.Merge(resource.Default(), resource.NewSchemaless(semconv.ServiceName(...)))` with semconv v1.26.0; OTLP/HTTP trace exporter only if endpoint non-empty (`otlptracehttp.WithEndpointURL`); propagators TraceContext+Baggage; metrics via `go.opentelemetry.io/otel/exporters/prometheus` reader -> `promhttp.Handler()`; `Providers.Shutdown(ctx)`. Called from cmd/aicc/main.go:84. Metric instrument names in one file `internal/obs/callmetrics.go`; names/labels follow Prometheus convention (`aicc_turn_latency_ms`, snake labels) (07-naming.md row "Metrics/log keys"). HTTP instrumented with `otelhttp v0.70.0`.
- Ops listener (separate, unauthenticated, default 127.0.0.1:9090): `httpapi.MetricsHandler(metrics http.Handler, ready Readiness)` (internal/httpapi/server.go:440), a plain `http.NewServeMux`: `GET /metrics`; `GET /healthz` -> 200 `ok\n` (:443); `GET /readyz` (:447) -> ctx timeout 3s (`readyzTimeout`), `ready.Ping` (pgxpool Ping) decides 200 vs 503; text/plain body of stable lines `ready|<err>`, `database: ok|<err>`, `switch: up|down`, `migrations: <db>/<latest>`; headers `Content-Type: text/plain; charset=utf-8`, `X-Content-Type-Options: nosniff`. `Readiness{Ping func(ctx) error; IsSwitchUp func() bool; Migrations func(ctx)(store.MigrationState, error)}`. Not in the OpenAPI contract by design (info.description). Main server: `http.Server{ReadHeaderTimeout:10s, IdleTimeout:120s}` no WriteTimeout (SSE), metrics server `go serve(metricsSrv,"metrics")`, graceful `Shutdown(shutdownCtx)` (main.go:~355-411).
- Router: chi v5 + generated wrapper `s.apiWrapper()`; `r.NotFound` serves SPA (server.go). Middleware in `internal/httpapi/middleware.go` (`bearerPrefix = "Bearer "` :42); per-op scope enforcement from generated `api.OperationSecurityByRoute`.
- Auth hashing: API keys and session tokens are 32 random bytes, base64 RawURL-encoded, stored as SHA-256 digest (bytea `key_hash` / `token_hash`, UNIQUE), looked up by digest (internal/store/apikeystore.go:58 `keySecretBytes = 32`, `sha256.Sum256([]byte(secret))` in Issue (:~78) and Authenticate (:~110); `keyPrefixLen = 8` clear prefix kept for display; secret returned once; internal/auth/auth.go:186 `hashToken` = sha256). Passwords: argon2id PHC string (internal/auth/password.go:19-41: time=1, mem=64MiB, keyLen=32, salt=16; `golang.org/x/crypto v0.55.0`), with a `dummyHash` to equalize unknown-user timing (auth.go). Stream tokens: HMAC-SHA256 (internal/streamin/streamin.go).

## 10. go.mod / dependencies
- `module github.com/rasonyang/ai-native-callcenter` (go.mod:1) -> sibling should be `github.com/rasonyang/aicc-knowledge`. `go 1.27.1` (go.mod:3); CI uses `go-version-file: go.mod`; Dockerfile `golang:1.27.1-alpine`.
- Direct deps (go.mod:5-25): chi/v5 v5.3.1; google/uuid v1.6.0; jackc/pgx/v5 v5.10.0; oapi-codegen/runtime v1.6.0; pressly/goose/v3 v3.27.3; prometheus/client_golang v1.24.1; otelhttp v0.70.0; otel v1.45.0 (+ otlptracehttp v1.45.0, exporters/prometheus v0.67.0, sdk, sdk/metric, metric, trace); golang.org/x/crypto v0.55.0. (AICC-only: gorilla/websocket, minio-go, pion/rtp, pion/rtcp.) Tool block go.mod:113-116 (oapi-codegen v2.8.0, oasdiff v1.28.0). sqlc: external binary, version not pinned. testcontainers: NOT used; integration tests use CI service container / local `make dev-up` Postgres.

## 11. Dockerfile and deploy
- Dockerfile (74 lines): header comment; `ARG BASE_IMAGE=gcr.io/distroless/static-debian12:nonroot`; build stages run on `--platform=$BUILDPLATFORM` (no QEMU): node:22-alpine web stage, `golang:1.27.1-alpine AS build` with `ARG GOPROXY=https://proxy.golang.org,direct`, `COPY go.mod go.sum` + `go mod download` (layer cache) then `COPY . .`:
```dockerfile
ARG VERSION=""
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/aicc ./cmd/aicc
...
FROM ${BASE_IMAGE}
COPY --from=build /out/aicc /usr/local/bin/aicc
ENV AICC_LOG_DIR=/var/log/aicc AICC_HTTP_ADDR=:8080
EXPOSE 8080 9090 ...
USER nonroot
ENTRYPOINT ["/usr/local/bin/aicc"]
```
  Writable dirs pre-created and `chown 65532:65532` in the build stage because static base has no shell (Dockerfile:52-55, 61-62). Metrics port 9090 exposed but "stays shut until AICC_METRICS_ADDR is opened deliberately" (:67-71). `.dockerignore` exists.
- Dev compose `deploy/dev/docker-compose.yml`: service `postgres`, `image: postgres:18-alpine`, `container_name: aicc-postgres`, ports `127.0.0.1:5432:5432`, env POSTGRES_USER/PASSWORD/DB = aicc, volume `postgres-data:/var/lib/postgresql` (comment: PG18 images mount at /var/lib/postgresql), healthcheck `pg_isready -U aicc -d aicc` interval 5s timeout 3s retries 20.
- `deploy/docker-compose.yml` = whole stack (postgres + switch + app), overlays `compose.linux.yml`/`compose.macos.yml`, `compose.release.yml.in` (tag placeholder `@TAG@` stamped by scripts/release-bundle.sh), `deploy/.env.example`, `deploy/install.sh` (one-line installer), `deploy/README.md`, `deploy/production-checklist.md`. Most of it is switch-specific; mirror only the postgres + app service shape.

## 12. NOTICE / LICENSE / README / CHANGELOG
- LICENSE: Apache-2.0 (README.md:165-167 "Apache-2.0. See [LICENSE](LICENSE)."). NOTICE (5 lines, verbatim):
```
AI Native Call Center
Copyright 2026 Rason Yang

This product includes software developed as part of the AI Native Call Center
project, licensed under the Apache License, Version 2.0.
```
- README.md sections (H2): Try it, What it does, How it fits together, Building, Extending it, Status, License (title H1 `# AI Native Call Center`). Building block lists `make dev-up`, `make build`, `go test -race ./...  # always -race`, `make lint`, `make api-check`. Companion `README.zh-CN.md` (only Chinese file allowed besides product content).
- CHANGELOG.md (319 lines): H1 `# Changelog`, intro paragraph, then `## vX.Y.Z - YYYY-MM-DD` newest first, with `### Upgrade notes` (when needed; mentions migration numbers and contract changes e.g. "API contract: the CDR gains ... Nothing was removed."), `### Added`, and presumably `### Changed`/`### Fixed`. Not Keep-a-Changelog "[Unreleased]". `scripts/release-notes.sh <tag>` extracts the section for the tag.
- .gitignore: Go standard template (+ .env, logs, bin). `.github/ISSUE_TEMPLATE/config.yml` only.
