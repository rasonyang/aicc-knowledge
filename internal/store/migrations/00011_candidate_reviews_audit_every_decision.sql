-- SPDX-License-Identifier: Apache-2.0
--
-- Audit trail of review decisions.
--
-- One row per applied row of an imported review workbook: which candidate,
-- what the reviewer decided (action), who they say they are (reviewer, from the
-- import command line), the candidate's content and state before and after as
-- jsonb, and the workbook's file name. Rows that were skipped, stale or
-- rejected by validation change nothing and are not audited; they are reported
-- by the import itself.
--
-- The table is append-only by convention (the service only inserts). It has
-- no ON DELETE action: candidates are never deleted.
--
-- action is a varchar CHECK enum equal to the Go set domain.ReviewAction. EDIT
-- means approve-with-edits: after->state is APPROVED and after holds the
-- edited text.

-- +goose Up
CREATE TABLE candidate_reviews (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    candidate_id       uuid         NOT NULL,
    action             varchar(8)   NOT NULL CHECK (action IN ('APPROVE', 'REJECT', 'EDIT')),
    reviewer           text         NOT NULL CHECK (reviewer <> ''),
    before             jsonb        NOT NULL,
    after              jsonb        NOT NULL,
    source_file_name   text         NOT NULL,
    reviewed_at        timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT fk_candidate_reviews_candidates FOREIGN KEY (candidate_id) REFERENCES candidates (id)
);

CREATE INDEX idx_candidate_reviews_candidate_id ON candidate_reviews (candidate_id, reviewed_at);

-- +goose Down
DROP TABLE candidate_reviews;
