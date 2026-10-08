-- SPDX-License-Identifier: Apache-2.0
--
-- Where a candidate came from, and what made it.
--
-- section_ordinal is the ordinal of the parsed section the candidate was
-- generated from. The identity of a section is (file_version_id, ordinal) plus
-- its source_ref, never parsed_sections.id: a re-parse deletes and re-inserts
-- the rows of a version, so row ids do not survive it. When a version's
-- sections are derived again (a facts mapping arrives and a sheet stops being
-- Q&A content) a candidate whose section is gone becomes STALE with
-- review_note SECTION_WITHDRAWN.
--
-- section_ordinal is nullable only so that rows inserted without a section
-- (tests, future importers) stay valid; generate always sets it. source_ref on
-- the candidate stays a snapshot of the section's source_ref.
--
-- prompt_version and model record which prompt and which LLM produced the
-- text, so a quality problem can be traced to a prompt revision. generated_at
-- is when the LLM answered.
--
-- (file_version_id, content_hash) is unique: generating a version twice, or a
-- reviewer's edit that lands on text another candidate of the version already
-- has, cannot create a duplicate. content_hash covers the file version, so the
-- same text from two versions never collides.

-- +goose Up
ALTER TABLE candidates
    ADD COLUMN section_ordinal integer CHECK (section_ordinal >= 0),
    ADD COLUMN prompt_version  varchar(32)  NOT NULL DEFAULT '',
    ADD COLUMN model           varchar(128) NOT NULL DEFAULT '',
    ADD COLUMN generated_at    timestamptz  NOT NULL DEFAULT now();

CREATE INDEX idx_candidates_file_version_section ON candidates (file_version_id, section_ordinal);
CREATE UNIQUE INDEX uq_candidates_file_version_content_hash ON candidates (file_version_id, content_hash);

-- +goose Down
DROP INDEX uq_candidates_file_version_content_hash;
DROP INDEX idx_candidates_file_version_section;
ALTER TABLE candidates
    DROP COLUMN generated_at,
    DROP COLUMN model,
    DROP COLUMN prompt_version,
    DROP COLUMN section_ordinal;
