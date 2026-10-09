-- SPDX-License-Identifier: Apache-2.0

-- name: CreateAPIKey :one
INSERT INTO api_keys (name, key_prefix, key_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetActiveAPIKeyByHash :one
SELECT * FROM api_keys
WHERE key_hash = $1 AND revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;
