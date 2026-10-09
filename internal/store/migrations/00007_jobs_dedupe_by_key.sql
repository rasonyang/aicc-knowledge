-- SPDX-License-Identifier: Apache-2.0
--
-- Idempotent enqueue for the job queue.
--
-- A scan that is interrupted and re-run, or two code paths that both notice
-- the same new file version, must not queue the work twice. A job may carry a
-- dedupe_key (for PARSE it is the file_version_id); (kind, dedupe_key) is
-- unique across all states, so a job that already SUCCEEDED or FAILED is not
-- re-queued by a later enqueue. An operator retry (planned
-- `parse --retry-failed`) resets the existing row rather than inserting a
-- second one.
--
-- dedupe_key is nullable: jobs that are legitimately repeatable (a periodic
-- tick, say) leave it NULL and are never deduplicated. The index is partial,
-- so those rows cost nothing.

-- +goose Up
ALTER TABLE jobs ADD COLUMN dedupe_key varchar(255);

CREATE UNIQUE INDEX uq_jobs_kind_dedupe_key ON jobs (kind, dedupe_key) WHERE dedupe_key IS NOT NULL;

-- +goose Down
DROP INDEX uq_jobs_kind_dedupe_key;
ALTER TABLE jobs DROP COLUMN dedupe_key;
