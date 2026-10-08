-- SPDX-License-Identifier: Apache-2.0
--
-- Parsed sections: what the parsers extract from a file version, in the form
-- the offline LLM reads.
--
-- One row per .docx section (a heading and the body below it, or the
-- preamble) or per .xlsx chunk (a run of data rows of one sheet with its
-- header line). body is already the LLM-input rendering (plain text), so
-- generation does not need to re-open the file. heading_path and level keep
-- the document structure; source_ref is globally meaningful, because the
-- candidates generated from a section copy it: "<object key>#<parser source
-- ref>", the object key being the full key inside the bucket.
--
-- A section belongs to exactly one file version and (file_version_id, ordinal)
-- is unique, so re-parsing a version replaces its sections instead of adding
-- to them. Sections of superseded or removed versions are retained for audit;
-- candidate generation (M4) reads only the sections of the current PARSED
-- version of each file.
--
-- kind is a varchar CHECK enum like every other enum; internal/store tests
-- keep it equal to the Go set.

-- +goose Up
CREATE TABLE parsed_sections (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    file_version_id uuid        NOT NULL,
    ordinal         integer     NOT NULL CHECK (ordinal >= 0),
    kind            varchar(16) NOT NULL CHECK (kind IN ('DOCX_SECTION', 'XLSX_CHUNK')),
    heading_path    text[]      NOT NULL DEFAULT '{}',
    level           integer     NOT NULL DEFAULT 0 CHECK (level >= 0),
    body            text        NOT NULL,
    source_ref      text        NOT NULL,
    CONSTRAINT fk_parsed_sections_file_versions FOREIGN KEY (file_version_id) REFERENCES file_versions (id),
    CONSTRAINT uq_parsed_sections_file_version_ordinal UNIQUE (file_version_id, ordinal)
);

-- +goose Down
DROP TABLE parsed_sections;
