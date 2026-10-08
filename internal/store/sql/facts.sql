-- SPDX-License-Identifier: Apache-2.0

-- name: GetFactTableByName :one
SELECT * FROM fact_tables WHERE name = $1;

-- name: GetFactTableByNameForUpdate :one
SELECT * FROM fact_tables WHERE name = $1 FOR UPDATE;

-- name: InsertFactTable :one
-- A new table starts UNAVAILABLE; only a complete import points it at rows.
INSERT INTO fact_tables (name, workbook_source_file_id, mapping_source_file_id, columns, key_columns, status, last_error_code)
VALUES ($1, $2, $3, $4, $5, 'UNAVAILABLE', $6)
RETURNING *;

-- name: UpdateFactTableDefinition :exec
-- Redefines the table from a newly parsed mapping and clears the pointer: the
-- caller sets it again when it imports rows in the same transaction.
UPDATE fact_tables
SET workbook_source_file_id = $2, mapping_source_file_id = $3, columns = $4, key_columns = $5,
    workbook_version_id = NULL, mapping_version_id = NULL, status = 'UNAVAILABLE', last_error_code = $6, updated_at = now()
WHERE id = $1;

-- name: PointFactTable :exec
UPDATE fact_tables
SET workbook_version_id = $2, mapping_version_id = $3, status = 'AVAILABLE', last_error_code = NULL, updated_at = now()
WHERE id = $1;

-- name: ListFactTablesByMappingSource :many
SELECT * FROM fact_tables WHERE mapping_source_file_id = $1 ORDER BY name FOR UPDATE;

-- name: ClearFactTablesForVersion :execrows
-- A file version stopped being the live source (superseded or removed): every
-- table whose pointer names it becomes UNAVAILABLE.
UPDATE fact_tables
SET workbook_version_id = NULL, mapping_version_id = NULL, status = 'UNAVAILABLE',
    last_error_code = sqlc.arg(error_code)::text, updated_at = now()
WHERE workbook_version_id = sqlc.arg(version_id) OR mapping_version_id = sqlc.arg(version_id);

-- name: MarkFactTablesUnavailableBySource :execrows
-- Clears the pointer of every table fed by this source file (as workbook or as
-- mapping) and records why.
UPDATE fact_tables
SET workbook_version_id = NULL, mapping_version_id = NULL, status = 'UNAVAILABLE',
    last_error_code = sqlc.arg(error_code)::text, updated_at = now()
WHERE workbook_source_file_id = sqlc.arg(source_file_id) OR mapping_source_file_id = sqlc.arg(source_file_id);

-- name: DeleteFactRows :execrows
DELETE FROM fact_rows WHERE fact_table_id = $1;

-- name: InsertFactRows :copyfrom
INSERT INTO fact_rows (fact_table_id, workbook_version_id, mapping_version_id, key, row, valid_from, valid_to, source_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: LookupFactRows :many
-- Exact match on the canonical key within the table's current import pointer,
-- valid at the given date (half-open: valid_from <= at < valid_to). LIMIT 2 so
-- the caller can tell "more than one" from "one"; import validation makes more
-- than one impossible.
SELECT r.row, r.source_ref, r.valid_from, r.valid_to
FROM fact_tables t
JOIN fact_rows r ON r.fact_table_id = t.id
                AND r.workbook_version_id = t.workbook_version_id
                AND r.mapping_version_id = t.mapping_version_id
WHERE t.id = sqlc.arg(table_id)
  AND t.status = 'AVAILABLE'
  AND r.key = sqlc.arg(key)::jsonb
  AND (r.valid_from IS NULL OR r.valid_from <= sqlc.arg(at)::date)
  AND (r.valid_to IS NULL OR sqlc.arg(at)::date < r.valid_to)
LIMIT 2;

-- name: ClearFactTable :exec
UPDATE fact_tables
SET workbook_version_id = NULL, mapping_version_id = NULL, status = 'UNAVAILABLE',
    last_error_code = sqlc.arg(error_code)::text, updated_at = now()
WHERE id = sqlc.arg(id);
