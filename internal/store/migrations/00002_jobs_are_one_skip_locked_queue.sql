-- SPDX-License-Identifier: Apache-2.0
--
-- The job queue is one table, claimed with FOR UPDATE SKIP LOCKED. There is no
-- queue dependency to run or lose: a job is a row, and PostgreSQL is already
-- the source of truth.
--
-- A worker claims a QUEUED row whose run_after has passed, sets RUNNING and a
-- locked_until lease, and later sets SUCCEEDED or FAILED. A RUNNING row whose
-- lease expired is returned to QUEUED by the next claimer. attempts counts
-- claims; a job that reaches max_attempts is FAILED.
--
-- kind is deliberately not a CHECK enum. Job kinds are registered in code by
-- the milestone that introduces them (scan, parse, generate, publish), and an
-- unknown kind fails at claim time with last_error_code UNKNOWN_JOB_KIND
-- instead of needing a migration per kind.

-- +goose Up
CREATE TABLE jobs (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    kind               varchar(64)  NOT NULL,
    payload            jsonb        NOT NULL DEFAULT '{}'::jsonb,
    state              varchar(16)  NOT NULL DEFAULT 'QUEUED'
                       CHECK (state IN ('QUEUED', 'RUNNING', 'SUCCEEDED', 'FAILED')),
    attempts           integer      NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts       integer      NOT NULL DEFAULT 3 CHECK (max_attempts >= 1),
    run_after          timestamptz  NOT NULL DEFAULT now(),
    locked_until       timestamptz,
    locked_by          varchar(128),
    last_error_code    varchar(64),
    last_error_message text,
    created_at         timestamptz  NOT NULL DEFAULT now(),
    finished_at        timestamptz
);

-- The claim query scans only rows that can be claimed.
CREATE INDEX idx_jobs_claimable ON jobs (run_after) WHERE state IN ('QUEUED', 'RUNNING');

-- +goose Down
DROP TABLE jobs;
