-- SPDX-License-Identifier: Apache-2.0

-- name: GetFileVersionWithSource :one
SELECT v.id, v.source_file_id, v.version_no, v.sha256, v.size_bytes, v.state, v.superseded_at,
       sf.bucket, sf.object_key
FROM file_versions v
JOIN source_files sf ON sf.id = v.source_file_id
WHERE v.id = $1;

-- name: LockFileVersion :one
SELECT * FROM file_versions WHERE id = $1 FOR UPDATE;

-- name: GetCurrentVersionByObjectKey :one
-- The current (not superseded, not removed) version of one object, if any.
SELECT v.id, v.source_file_id, v.version_no, v.sha256, v.size_bytes, v.state, v.superseded_at,
       sf.bucket, sf.object_key
FROM source_files sf
JOIN file_versions v ON v.source_file_id = sf.id AND v.superseded_at IS NULL
WHERE sf.bucket = $1 AND sf.object_key = $2;

-- name: GetSourceFileByObjectKey :one
SELECT * FROM source_files WHERE bucket = $1 AND object_key = $2;

-- name: TransitionFileVersion :execrows
-- The caller has checked the edge with domain.FileVersionState.Transition. The
-- guard on the old state and on superseded_at makes a lost race affect 0 rows.
UPDATE file_versions
SET state = sqlc.arg(to_state), parse_error_code = sqlc.narg(parse_error_code), parse_warnings = sqlc.arg(parse_warnings)
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state) AND superseded_at IS NULL;

-- name: DeleteParsedSections :execrows
DELETE FROM parsed_sections WHERE file_version_id = $1;

-- name: InsertParsedSections :copyfrom
INSERT INTO parsed_sections (file_version_id, ordinal, kind, heading_path, level, body, source_ref, qa_question, qa_alternates, qa_language)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListParsedSections :many
SELECT * FROM parsed_sections WHERE file_version_id = $1 ORDER BY ordinal;

-- name: SetSectionGenerateOutcome :execrows
-- Records what generate made of a section. Matches on (ordinal, source_ref) so
-- a section re-derived while generating is not marked.
UPDATE parsed_sections
SET generate_outcome = sqlc.narg(outcome), generate_error_code = sqlc.narg(error_code)
WHERE file_version_id = sqlc.arg(file_version_id) AND ordinal = sqlc.arg(ordinal) AND source_ref = sqlc.arg(source_ref);

-- name: ListVersionsWithFailedSections :many
-- Current PARSED versions that have a section generate recorded FAILED.
SELECT v.id FROM file_versions v
WHERE v.superseded_at IS NULL AND v.state = 'PARSED'
  AND EXISTS (SELECT 1 FROM parsed_sections s WHERE s.file_version_id = v.id AND s.generate_outcome = 'FAILED')
ORDER BY v.discovered_at, v.id;

-- name: ListCurrentFailedFileVersions :many
SELECT * FROM file_versions
WHERE state = 'PARSE_FAILED' AND superseded_at IS NULL
ORDER BY discovered_at, id
FOR UPDATE;

-- name: GetCurrentVersionOfSourceFile :one
-- Not locking: used to check whether another source file still has a live
-- version.
SELECT id, state FROM file_versions WHERE source_file_id = $1 AND superseded_at IS NULL;

-- name: SetParseWarnings :exec
-- Re-derived warnings of a PARSED version whose sections were re-derived.
UPDATE file_versions SET parse_warnings = $2 WHERE id = $1 AND state = 'PARSED';
