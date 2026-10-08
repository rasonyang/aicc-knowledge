#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Regenerates every artifact derived from docs/openapi.json (the API contract):
#   internal/api/api.gen.go   Go types + chi ServerInterface (oapi-codegen)
#
# This script is the single generation entry point: `make api-generate` and
# `go generate ./internal/api` both land here, so the committed output is
# byte-identical no matter which door was used.
set -euo pipefail
cd "$(dirname "$0")/.."

go tool oapi-codegen -config oapi-codegen.yaml docs/openapi.json

# Every Go source file in this repo carries the SPDX header; oapi-codegen
# cannot emit one itself, so it is stamped here, above the generated marker.
tmp="$(mktemp)"
printf '// SPDX-License-Identifier: Apache-2.0\n\n' | cat - internal/api/api.gen.go > "$tmp"
mv "$tmp" internal/api/api.gen.go
gofmt -w internal/api/api.gen.go
