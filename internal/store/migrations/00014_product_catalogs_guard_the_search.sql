-- SPDX-License-Identifier: Apache-2.0
--
-- The product catalog (`products.yaml` at the root of the S3 prefix) lets the
-- searcher refuse a hit about another product than the one the caller asked
-- about. See internal/products and internal/search.
--
-- product_catalogs holds one row per parsed catalog file version: the
-- validated products as jsonb, in the shape internal/products reads back. The
-- row is immutable, like the file version it belongs to. The current catalog is
-- the row of the root `products.yaml`'s current version; there is no separate
-- "current" pointer to keep in step.
--
-- publications.catalog_id snapshots the catalog a publication was built with
-- (NULL: no catalog, the guard is off for that publication). publication_items
-- .products snapshots the product ids each document is about (empty: generic).
-- Both are copied into the index documents, and a rollback rebuilds from them,
-- so a rolled-back publication is served with the product sets and the catalog
-- it was published with, even after the catalog file changed.
--
-- No ON DELETE action on catalog_id: catalogs are never deleted, and a
-- publication keeps its row forever.

-- +goose Up
CREATE TABLE product_catalogs (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    file_version_id uuid        NOT NULL,
    products        jsonb       NOT NULL CHECK (jsonb_typeof(products) = 'object'),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_product_catalogs_file_version UNIQUE (file_version_id),
    CONSTRAINT fk_product_catalogs_file_versions FOREIGN KEY (file_version_id) REFERENCES file_versions (id)
);

ALTER TABLE publications
    ADD COLUMN catalog_id uuid,
    ADD CONSTRAINT fk_publications_product_catalogs FOREIGN KEY (catalog_id) REFERENCES product_catalogs (id);

ALTER TABLE publication_items ADD COLUMN products text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE publication_items DROP COLUMN products;
ALTER TABLE publications DROP CONSTRAINT fk_publications_product_catalogs, DROP COLUMN catalog_id;
DROP TABLE product_catalogs;
