-- SPDX-License-Identifier: Apache-2.0
--
-- Publishing swaps indexes, and a swap is a true swap: after publication P2 is
-- swapped into the live uid (faq_en), the staging uid it was built under holds
-- P1's content. publications.index_uid is the name a publication was built
-- under and never changes, so it stops saying where the content is.
-- content_uid is that missing fact: the Meilisearch uid that currently holds
-- exactly this publication's documents, or NULL when no index holds them (the
-- index was pruned by retention, or the publication FAILED). Rollback uses it
-- to swap a retained index back in; when it is NULL, rollback rebuilds the
-- index from publication_items. The partial unique index keeps two
-- publications from claiming the same uid. A swap updates both rows inside the
-- transaction that flips their states, clearing one claim before it sets the
-- other.
--
-- publication_items gets the scope the document carried. Scope values come from
-- the S3 object path of the candidate's source file (KB_S3_SCOPE_PATH_TEMPLATE)
-- and are snapshotted next to the question and answer, so a rebuild reproduces
-- the index even after the source file moved or the template changed. An
-- empty object means the document is global.

-- +goose Up
ALTER TABLE publications ADD COLUMN content_uid varchar(128);
CREATE UNIQUE INDEX uq_publications_content_uid ON publications (content_uid) WHERE content_uid IS NOT NULL;
-- Publications that existed before this migration were never swapped by this
-- code; the best available fact is the name they were built under.
UPDATE publications SET content_uid = index_uid WHERE state IN ('LIVE', 'SUPERSEDED');

ALTER TABLE publication_items ADD COLUMN scope jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(scope) = 'object');

-- +goose Down
ALTER TABLE publication_items DROP COLUMN scope;
DROP INDEX uq_publications_content_uid;
ALTER TABLE publications DROP COLUMN content_uid;
