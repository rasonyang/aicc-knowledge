-- SPDX-License-Identifier: Apache-2.0
--
-- What the scan last saw of each object, kept on source_files.
--
-- file_versions are immutable, and their etag / last_modified_at / size_bytes
-- are the values at discovery. That is not enough for change detection: the
-- same bytes can be re-uploaded (a multipart upload changes the ETag, a plain
-- copy changes LastModified) without becoming a new version. If the scan only
-- compared the listing with the version's at-discovery values it would
-- download and hash that object on every run. So the scan records the newest
-- observed size, ETag and LastModified here, on the mutable parent row, and a
-- listing that matches them is "unchanged" without a download.
--
-- The columns are NULL until the first observation (a source_files row is
-- always created together with its first version, so in practice they are set
-- from the start); NULL is treated as "changed". last_seen_at keeps its
-- meaning from 00003 but is only touched when the scan processes the object
-- (new version, metadata update, removal): an unchanged object causes no
-- write at all, which is what makes a repeated scan a no-op.

-- +goose Up
ALTER TABLE source_files
    ADD COLUMN observed_size_bytes       bigint       CHECK (observed_size_bytes >= 0),
    ADD COLUMN observed_etag             varchar(128),
    ADD COLUMN observed_last_modified_at timestamptz;

-- +goose Down
ALTER TABLE source_files
    DROP COLUMN observed_last_modified_at,
    DROP COLUMN observed_etag,
    DROP COLUMN observed_size_bytes;
