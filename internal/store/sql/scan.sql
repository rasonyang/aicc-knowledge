-- SPDX-License-Identifier: Apache-2.0

-- name: ListScanState :many
-- Every source file of a bucket with its current version, if any. A removed
-- version is no longer current (superseded_at is set), so a removed file shows
-- up here with a NULL version_id.
SELECT sf.id AS source_file_id,
       sf.object_key,
       sf.observed_size_bytes,
       sf.observed_etag,
       sf.observed_last_modified_at,
       v.id        AS version_id,
       v.version_no,
       v.sha256,
       v.state     AS version_state
FROM source_files sf
LEFT JOIN file_versions v ON v.source_file_id = sf.id AND v.superseded_at IS NULL
WHERE sf.bucket = $1
ORDER BY sf.object_key;

-- name: UpsertSourceFile :one
-- Creates the row or locks the existing one for the rest of the transaction.
INSERT INTO source_files (bucket, object_key)
VALUES ($1, $2)
ON CONFLICT (bucket, object_key) DO UPDATE SET last_seen_at = now()
RETURNING *;

-- name: GetCurrentFileVersion :one
SELECT * FROM file_versions
WHERE source_file_id = $1 AND superseded_at IS NULL
FOR UPDATE;

-- name: NextVersionNo :one
SELECT (COALESCE(MAX(version_no), 0) + 1)::int AS version_no
FROM file_versions
WHERE source_file_id = $1;

-- name: InsertFileVersion :one
INSERT INTO file_versions (source_file_id, version_no, sha256, size_bytes, etag, last_modified_at, state, parse_error_code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: SupersedeFileVersion :execrows
UPDATE file_versions SET superseded_at = now()
WHERE id = $1 AND superseded_at IS NULL;

-- name: RemoveFileVersion :execrows
-- The caller has checked the edge with domain.FileVersionState.Transition.
UPDATE file_versions SET state = 'REMOVED', superseded_at = now()
WHERE id = $1 AND superseded_at IS NULL;

-- name: MarkCandidatesStale :execrows
-- Every candidate state has an edge to STALE (domain.CandidateStates; checked
-- by a test), so this is one statement for the whole version.
UPDATE candidates SET state = 'STALE'
WHERE file_version_id = $1 AND state <> 'STALE';

-- name: UpdateObservedMetadata :exec
UPDATE source_files
SET observed_size_bytes = $2, observed_etag = $3, observed_last_modified_at = $4, last_seen_at = now()
WHERE id = $1;

-- name: ListPublishableCandidates :many
-- The only candidates a publish may read: APPROVED, of a version that is still
-- current and not removed. A candidate whose version was superseded or removed
-- is STALE already; the join is a second guard so a missed transition can
-- never publish old content.
-- object_key is the source file's S3 key, from which publish derives the scope.
SELECT c.*, sf.object_key
FROM candidates c
JOIN file_versions v ON v.id = c.file_version_id
JOIN source_files sf ON sf.id = v.source_file_id
WHERE c.language = $1
  AND c.state = 'APPROVED'
  AND v.superseded_at IS NULL
  AND v.state <> 'REMOVED'
ORDER BY c.created_at, c.id;
