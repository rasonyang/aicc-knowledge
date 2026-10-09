-- SPDX-License-Identifier: Apache-2.0
--
-- Structured facts: one generic pair of tables instead of a table per sheet.
-- There is no runtime DDL; a sheet mapping only decides what goes into rows.
--
-- fact_tables is the registry. A fact table is declared by a mapping file
-- (<name>.facts.yaml, next to <name>.xlsx in S3) and fed by the workbook, so
-- it remembers both source files. columns is the mapping's column list
-- (name, type, unit, required) and key_columns the lookup key; a lookup
-- validates and coerces the request against them.
--
-- The import pointer is what makes serving safe. workbook_version_id and
-- mapping_version_id name the exact pair of file versions whose rows are the
-- live data of the table. Lookups read only fact_rows that carry that pair, so
-- a superseded source is never served. The pointer is cleared (status
-- UNAVAILABLE, last_error_code says why) whenever either version is
-- superseded, removed or fails to import, and set again only by a complete
-- successful import, in the same transaction that inserts the rows. The
-- CHECKs make "AVAILABLE" and "has a pointer" the same fact.
--
-- workbook_source_file_id is NULL until the workbook has been seen: a mapping
-- can arrive before its workbook.
--
-- fact_rows holds one validated row each. key is the canonical lookup key: a
-- JSON object of the key columns with typed values (jsonb equality is
-- independent of key order and compares numbers by value, so 9.90 equals
-- 9.9). row is the full typed row. valid_from / valid_to are the half-open
-- validity period [valid_from, valid_to); NULL is unbounded. The index serves
-- the lookup: pointer pair plus key. Rows of a previous import are deleted
-- when a newer import succeeds; they are derived data and the source versions
-- are immutable.
--
-- status is a varchar CHECK enum; internal/store tests keep it equal to the
-- Go set.

-- +goose Up
CREATE TABLE fact_tables (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    name                    varchar(63)  NOT NULL CHECK (name ~ '^[a-z][a-z0-9]*(_[a-z0-9]+)*$'),
    workbook_source_file_id uuid,
    mapping_source_file_id  uuid         NOT NULL,
    columns                 jsonb        NOT NULL,
    key_columns             text[]       NOT NULL CHECK (cardinality(key_columns) >= 1),
    workbook_version_id     uuid,
    mapping_version_id      uuid,
    status                  varchar(16)  NOT NULL DEFAULT 'UNAVAILABLE' CHECK (status IN ('AVAILABLE', 'UNAVAILABLE')),
    last_error_code         varchar(64),
    updated_at              timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT uq_fact_tables_name UNIQUE (name),
    CONSTRAINT fk_fact_tables_workbook_source_files FOREIGN KEY (workbook_source_file_id) REFERENCES source_files (id),
    CONSTRAINT fk_fact_tables_mapping_source_files FOREIGN KEY (mapping_source_file_id) REFERENCES source_files (id),
    CONSTRAINT fk_fact_tables_workbook_versions FOREIGN KEY (workbook_version_id) REFERENCES file_versions (id),
    CONSTRAINT fk_fact_tables_mapping_versions FOREIGN KEY (mapping_version_id) REFERENCES file_versions (id),
    CONSTRAINT ck_fact_tables_pointer CHECK (
        (status = 'AVAILABLE' AND workbook_version_id IS NOT NULL AND mapping_version_id IS NOT NULL AND last_error_code IS NULL)
        OR (status = 'UNAVAILABLE' AND workbook_version_id IS NULL AND mapping_version_id IS NULL)
    )
);

CREATE INDEX idx_fact_tables_mapping_source_file_id ON fact_tables (mapping_source_file_id);
CREATE INDEX idx_fact_tables_workbook_source_file_id ON fact_tables (workbook_source_file_id);
CREATE INDEX idx_fact_tables_workbook_version_id ON fact_tables (workbook_version_id) WHERE workbook_version_id IS NOT NULL;
CREATE INDEX idx_fact_tables_mapping_version_id ON fact_tables (mapping_version_id) WHERE mapping_version_id IS NOT NULL;

CREATE TABLE fact_rows (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    fact_table_id       uuid  NOT NULL,
    workbook_version_id uuid  NOT NULL,
    mapping_version_id  uuid  NOT NULL,
    key                 jsonb NOT NULL,
    row                 jsonb NOT NULL,
    valid_from          date,
    valid_to            date,
    source_ref          text  NOT NULL,
    CONSTRAINT fk_fact_rows_fact_tables FOREIGN KEY (fact_table_id) REFERENCES fact_tables (id),
    CONSTRAINT fk_fact_rows_workbook_versions FOREIGN KEY (workbook_version_id) REFERENCES file_versions (id),
    CONSTRAINT fk_fact_rows_mapping_versions FOREIGN KEY (mapping_version_id) REFERENCES file_versions (id),
    CONSTRAINT ck_fact_rows_validity CHECK (valid_from IS NULL OR valid_to IS NULL OR valid_from < valid_to)
);

CREATE INDEX idx_fact_rows_lookup ON fact_rows (fact_table_id, workbook_version_id, mapping_version_id, key);

-- +goose Down
DROP TABLE fact_rows;
DROP TABLE fact_tables;
