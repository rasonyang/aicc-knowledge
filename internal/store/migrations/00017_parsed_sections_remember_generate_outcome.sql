-- SPDX-License-Identifier: Apache-2.0
--
-- A section the LLM could not answer (output that never conformed, a timeout,
-- an unreachable endpoint) ended with no candidate and looked exactly like a
-- section the model correctly left empty, so it was never retried and its
-- knowledge was lost until a new file version regenerated the whole document.
--
-- generate now records what happened to every section it sent to the model:
-- GENERATED (at least one candidate), EMPTY (the model deliberately returned
-- an empty list: nothing to retry) or FAILED (no usable output; the error
-- code says why). `generate --retry-failed` re-runs exactly the FAILED
-- sections of current versions. NULL means generate did not send the section
-- (a stub, a Q&A row, or a version generated before this migration).
--
-- The outcome lives on the parsed section, so a section parse re-derives
-- with different text starts with a clean slate (parse carries the outcome
-- over only when the text is unchanged).

-- +goose Up
ALTER TABLE parsed_sections
    ADD COLUMN generate_outcome    varchar(9)  CHECK (generate_outcome IN ('GENERATED', 'EMPTY', 'FAILED')),
    ADD COLUMN generate_error_code varchar(40),
    ADD CONSTRAINT ck_parsed_sections_failed_has_code CHECK ((generate_outcome = 'FAILED') = (generate_error_code IS NOT NULL));

CREATE INDEX parsed_sections_failed_idx ON parsed_sections (file_version_id) WHERE generate_outcome = 'FAILED';

-- +goose Down
DROP INDEX parsed_sections_failed_idx;
ALTER TABLE parsed_sections
    DROP CONSTRAINT ck_parsed_sections_failed_has_code,
    DROP COLUMN generate_error_code,
    DROP COLUMN generate_outcome;
