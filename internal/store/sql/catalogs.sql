-- SPDX-License-Identifier: Apache-2.0

-- name: InsertProductCatalog :one
-- Idempotent per file version: a retried parse job writes the same row.
INSERT INTO product_catalogs (file_version_id, products) VALUES ($1, $2)
ON CONFLICT (file_version_id) DO UPDATE SET products = EXCLUDED.products
RETURNING *;

-- name: ListCurrentCatalogSources :many
-- The current version of every object named products.yaml (any case, any
-- directory), with its stored catalog when it parsed. The caller picks the one
-- at the root of the S3 prefix (scan.IsCatalogRoot).
SELECT sf.object_key, v.id AS file_version_id, v.state, v.parse_error_code, c.id AS catalog_id, c.products
FROM source_files sf
JOIN file_versions v ON v.source_file_id = sf.id AND v.superseded_at IS NULL
LEFT JOIN product_catalogs c ON c.file_version_id = v.id
WHERE lower(sf.object_key) LIKE '%products.yaml'
ORDER BY v.last_modified_at DESC, v.id DESC;

-- name: GetLiveCatalog :one
-- The catalog the LIVE publication of a language was built with. No row: no
-- LIVE publication, or it was built without a catalog.
SELECT c.id, c.products
FROM publications p
JOIN product_catalogs c ON c.id = p.catalog_id
WHERE p.language = $1 AND p.state = 'LIVE';

-- name: GetPublicationCatalog :one
SELECT c.id, c.products
FROM publications p
JOIN product_catalogs c ON c.id = p.catalog_id
WHERE p.id = $1;
