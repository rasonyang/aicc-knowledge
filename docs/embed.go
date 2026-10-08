// SPDX-License-Identifier: Apache-2.0

// Package docs embeds the API contract so the binary and its tests read the
// same bytes the generators read.
package docs

import _ "embed"

// Contract is docs/openapi.json, the source of truth for the HTTP API.
//
//go:embed openapi.json
var Contract []byte
