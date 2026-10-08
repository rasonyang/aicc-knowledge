-- SPDX-License-Identifier: Apache-2.0
--
-- Source files and their versions.
--
-- source_files is one row per S3 object (bucket + key). file_versions is one
-- row per distinct content that object has held. A change never mutates a
-- version: the scan inserts a new row, and the old one gets superseded_at.
-- SHA-256 of the bytes is the identity of content; the S3 ETag is stored for
-- change detection only, because a multipart upload's ETag is not a content
-- hash.
--
-- Why there is no UNIQUE (source_file_id, sha256): content can come back. A
-- file edited from A to B and reverted to A holds content A twice, and the
-- revert must be a third version (A, B, A) so the history and every candidate
-- generated from the first A stay truthful. Uniqueness is instead enforced on
-- what must never be ambiguous: at most one *current* version per file
-- (uq_file_versions_current, a partial unique index on superseded_at IS NULL),
-- and a per-file version number (uq_file_versions_version_no). Scan
-- idempotence comes from comparing the object's hash with the current
-- version's, not from a constraint; the partial unique index guarantees two
-- concurrent scans cannot both insert a current version. idx_file_versions_sha256
-- serves "which versions hold this content".
--
-- superseded_at is set when a newer version replaces this one and also when the
-- source is deleted (state REMOVED), because in both cases the version stops
-- being current.

-- +goose Up
CREATE TABLE source_files (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    bucket        varchar(255) NOT NULL,
    object_key    text         NOT NULL,
    first_seen_at timestamptz  NOT NULL DEFAULT now(),
    last_seen_at  timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT uq_source_files_bucket_object_key UNIQUE (bucket, object_key)
);

CREATE TABLE file_versions (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    source_file_id   uuid         NOT NULL,
    version_no       integer      NOT NULL CHECK (version_no >= 1),
    sha256           bytea        NOT NULL CHECK (octet_length(sha256) = 32),
    size_bytes       bigint       NOT NULL CHECK (size_bytes >= 0),
    etag             varchar(128) NOT NULL,
    last_modified_at timestamptz  NOT NULL,
    state            varchar(16)  NOT NULL DEFAULT 'DISCOVERED'
                     CHECK (state IN ('DISCOVERED', 'PARSED', 'PARSE_FAILED', 'UNSUPPORTED', 'REMOVED')),
    parse_error_code varchar(64),
    parse_warnings   jsonb        NOT NULL DEFAULT '[]'::jsonb,
    discovered_at    timestamptz  NOT NULL DEFAULT now(),
    superseded_at    timestamptz,
    CONSTRAINT fk_file_versions_source_files FOREIGN KEY (source_file_id) REFERENCES source_files (id),
    CONSTRAINT uq_file_versions_version_no UNIQUE (source_file_id, version_no)
);

CREATE UNIQUE INDEX uq_file_versions_current ON file_versions (source_file_id) WHERE superseded_at IS NULL;
CREATE INDEX idx_file_versions_sha256 ON file_versions (sha256);
CREATE INDEX idx_file_versions_state ON file_versions (state);

-- +goose Down
DROP TABLE file_versions;
DROP TABLE source_files;
