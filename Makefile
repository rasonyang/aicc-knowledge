# SPDX-License-Identifier: Apache-2.0
.DEFAULT_GOAL := help
SHELL := /bin/bash

VERSION ?= dev
IMAGE ?= aicc-knowledge:$(VERSION)
REDOCLY_VERSION := 2.60.0
REDOCLY := npx --yes @redocly/cli@$(REDOCLY_VERSION)

# Host ports of the dev stack (see compose.yaml); they avoid common local ports.
KB_PORT_POSTGRES ?= 15432
KB_PORT_MEILI ?= 17700
KB_PORT_TEI ?= 18088
KB_PORT_S3 ?= 18333
TEST_DATABASE_URL ?= postgres://kb:kb@127.0.0.1:$(KB_PORT_POSTGRES)/kb?sslmode=disable
TEST_MEILI_URL ?= http://127.0.0.1:$(KB_PORT_MEILI)
TEST_MEILI_API_KEY ?= dev-master-key-0123456789
TEST_S3_ENDPOINT ?= http://127.0.0.1:$(KB_PORT_S3)
TEST_S3_BUCKET ?= aicc-knowledge

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-16s %s\n", $$1, $$2}'

.PHONY: dev-up
dev-up: ## Start PostgreSQL, Meilisearch and SeaweedFS for the tests (docker compose)
	docker compose up -d --wait postgres meilisearch seaweedfs
	@echo
	@echo "  KB_TEST_DATABASE_URL='$(TEST_DATABASE_URL)'"
	@echo "  KB_TEST_MEILI_URL='$(TEST_MEILI_URL)'"
	@echo "  KB_TEST_MEILI_API_KEY='$(TEST_MEILI_API_KEY)'"
	@echo "  KB_TEST_S3_ENDPOINT='$(TEST_S3_ENDPOINT)'"
	@echo "  KB_TEST_S3_ACCESS_KEY_ID=dev KB_TEST_S3_SECRET_ACCESS_KEY=dev-secret"
	@echo "  KB_TEST_S3_BUCKET='$(TEST_S3_BUCKET)'"

.PHONY: dev-down
dev-down: ## Stop the dev stack (volumes are kept)
	docker compose down

.PHONY: generate
generate: ## Regenerate sqlc query code
	sqlc generate

.PHONY: api-lint
api-lint: ## Lint the API contract (docs/openapi.json)
	$(REDOCLY) lint docs/openapi.json

.PHONY: api-generate
api-generate: ## Regenerate Go API code from the contract
	scripts/api-generate.sh

.PHONY: api-check
api-check: api-lint ## CI gate: contract lints and committed generated code matches it
	@tmp=$$(mktemp) && cp internal/api/api.gen.go $$tmp \
		|| { echo 'internal/api/api.gen.go is missing; run make api-generate'; exit 1; }; \
	scripts/api-generate.sh && diff -u $$tmp internal/api/api.gen.go \
		|| { rm -f $$tmp; echo 'internal/api/api.gen.go is stale; run make api-generate and commit it'; exit 1; }; \
	rm -f $$tmp

.PHONY: sqlc-check
sqlc-check: ## CI gate: committed sqlc output matches the queries and migrations
	sqlc vet
	sqlc diff

BASE ?= main
API_BREAKING_IGNORE ?= .oasdiff-breaking-ignore.txt
.PHONY: api-breaking
api-breaking: ## Fail on undeclared breaking API changes vs BASE (default main)
	@base_spec=$$(mktemp); \
	if git show $(BASE):docs/openapi.json > $$base_spec 2>/dev/null; then \
		go tool oasdiff breaking --fail-on ERR --err-ignore $(API_BREAKING_IGNORE) $$base_spec docs/openapi.json; status=$$?; \
	else echo "no contract on $(BASE); nothing to compare"; status=0; fi; \
	rm -f $$base_spec; exit $$status

.PHONY: build
build: ## Build bin/aicc-knowledge
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/aicc-knowledge ./cmd/aicc-knowledge

.PHONY: test
test: ## go test -race ./... (needs KB_TEST_DATABASE_URL, KB_TEST_MEILI_URL, KB_TEST_S3_ENDPOINT, KB_TEST_S3_BUCKET)
	go test -race -count=1 ./...
	@if [ -z "$$KB_TEST_DATABASE_URL" ] || [ -z "$$KB_TEST_MEILI_URL" ] || [ -z "$$KB_TEST_S3_ENDPOINT" ] || [ -z "$$KB_TEST_S3_BUCKET" ]; then \
		echo; \
		echo '################################################################'; \
		echo '# WARNING: KB_TEST_DATABASE_URL, KB_TEST_MEILI_URL, KB_TEST_S3_ENDPOINT'; \
		echo '# and/or KB_TEST_S3_BUCKET is unset. Every test that needs PostgreSQL,'; \
		echo '# Meilisearch or S3 (SeaweedFS) was SKIPPED.'; \
		echo '# To run them here:  make dev-up   then'; \
		echo "#   KB_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' \\"; \
		echo "#   KB_TEST_MEILI_URL='$(TEST_MEILI_URL)' \\"; \
		echo "#   KB_TEST_MEILI_API_KEY='$(TEST_MEILI_API_KEY)' \\"; \
		echo "#   KB_TEST_S3_ENDPOINT='$(TEST_S3_ENDPOINT)' KB_TEST_S3_BUCKET='$(TEST_S3_BUCKET)' \\"; \
		echo "#   KB_TEST_S3_ACCESS_KEY_ID=dev KB_TEST_S3_SECRET_ACCESS_KEY=dev-secret make test"; \
		echo '################################################################'; \
	fi

.PHONY: lint
lint: ## Vet Go code and check formatting
	go vet ./...
	@unformatted=$$(gofmt -l cmd internal docs); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

.PHONY: licenses
licenses: ## Regenerate THIRD_PARTY_LICENSES (go-licenses, fails on a disallowed license)
	scripts/licenses.sh THIRD_PARTY_LICENSES

.PHONY: licenses-check
licenses-check: ## CI gate: committed THIRD_PARTY_LICENSES matches the dependencies
	@tmp=$$(mktemp) && scripts/licenses.sh $$tmp >/dev/null \
		&& { diff -u THIRD_PARTY_LICENSES $$tmp >/dev/null \
			|| { rm -f $$tmp; echo 'THIRD_PARTY_LICENSES is stale; run make licenses and commit it'; exit 1; }; } \
		|| { rm -f $$tmp; exit 1; }; \
	rm -f $$tmp

.PHONY: image
image: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

.PHONY: clean
clean: ## Remove build output
	rm -rf bin
