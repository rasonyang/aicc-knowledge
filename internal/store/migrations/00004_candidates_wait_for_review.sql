-- SPDX-License-Identifier: Apache-2.0
--
-- Candidate Q&A generated from a file version. Nothing here is published until
-- a human approves it: only rows in state APPROVED are ever read by publish.
--
-- content_hash is the SHA-256 of the candidate's question, alternate
-- questions, answer and source version. The review export carries it in a
-- hidden column and the import rejects a row whose hash no longer matches
-- (STALE), so an edit made against outdated content never lands.
--
-- A candidate belongs to exactly one file version. When that version is
-- superseded or removed, its candidates become STALE (state machine in
-- internal/domain); they are kept for audit, never deleted.
--
-- flags is an array so one candidate can carry several markers. The only
-- value today is CONTAINS_FIGURES: the answer states numbers a reviewer must
-- check against the source.

-- +goose Up
CREATE TABLE candidates (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    file_version_id     uuid         NOT NULL,
    language            varchar(2)   NOT NULL CHECK (language IN ('EN', 'ZH')),
    question            text         NOT NULL CHECK (question <> ''),
    alternate_questions text[]       NOT NULL DEFAULT '{}',
    answer              text         NOT NULL CHECK (answer <> ''),
    source_ref          text         NOT NULL DEFAULT '',
    flags               text[]       NOT NULL DEFAULT '{}' CHECK (flags <@ ARRAY['CONTAINS_FIGURES']::text[]),
    state               varchar(16)  NOT NULL DEFAULT 'PENDING_REVIEW'
                        CHECK (state IN ('PENDING_REVIEW', 'APPROVED', 'REJECTED', 'STALE')),
    content_hash        bytea        NOT NULL CHECK (octet_length(content_hash) = 32),
    review_note         text,
    reviewed_at         timestamptz,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT fk_candidates_file_versions FOREIGN KEY (file_version_id) REFERENCES file_versions (id)
);

CREATE INDEX idx_candidates_file_version_id ON candidates (file_version_id);
CREATE INDEX idx_candidates_state_language ON candidates (state, language);

-- +goose Down
DROP TABLE candidates;
